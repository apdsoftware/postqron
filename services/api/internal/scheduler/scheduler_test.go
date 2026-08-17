package scheduler_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/apdsoftware/postqron/services/api/internal/schedule"
	"github.com/apdsoftware/postqron/services/api/internal/scheduler"
)

// Le proprietà provate qui sono quelle che vivono nel database e che nessun
// finto archivio potrebbe garantire: la chiave primaria come arbitro
// dell'idempotenza, il conflitto che porta il nome della partizione, il riavvio
// che non perde e non duplica, due motori che lavorano insieme senza pestarsi i
// piedi. Il calcolo delle occorrenze si prova senza database, in
// occurrences_test.go.

// testClock è l'orologio dei test. Parte dal tempo reale — e non da una data
// inventata — perché due colonne scritte da PostgreSQL, `created_at` e le
// partizioni giornaliere di `job_executions`, vivono nel presente: un motore che
// crede di essere nel 2030 scriverebbe in una partizione che non esiste.
//
// Il troncamento al millisecondo evita un falso negativo che non c'entra niente
// con lo scheduler: `timestamptz` ha risoluzione al microsecondo, e un istante
// preso con i nanosecondi non torna mai uguale a se stesso dopo un giro sul
// database.
func testClock() time.Time { return time.Now().UTC().Truncate(time.Millisecond) }

// gridNext è l'occorrenza successiva di un intervallo. Serve alle attese dei
// test, perché un intervallo non cade «un'ora dopo l'ultima volta» ma sulla
// griglia ancorata all'epoch: `every: 1h` scocca all'ora piena UTC.
func gridNext(t *testing.T, every time.Duration, after time.Time) time.Time {
	t.Helper()
	s, err := schedule.NewInterval(every)
	if err != nil {
		t.Fatalf("schedule.NewInterval(%s): %v", every, err)
	}
	next, ok := s.Next(after)
	if !ok {
		t.Fatalf("nessuna occorrenza dopo %s", after)
	}
	return next
}

// ------------------------------------------------------- accodamento di base

func TestUnJobDovutoVieneAccodatoEConsegnato(t *testing.T) {
	pool := newTestDatabase(t)
	user := createUser(t, pool)

	now := testClock()
	due := now.Add(-time.Second)
	jobID := createJob(t, pool, user, jobSpec{Every: time.Hour, NextRunAt: &due})

	rec := &recorder{}
	engine := newEngine(t, pool, rec, &now, nil)

	st, err := engine.Tick(t.Context())
	if err != nil {
		t.Fatalf("Tick: %v", err)
	}

	if st.Enqueued != 1 || st.Jobs != 1 {
		t.Fatalf("stats = %+v, attesa una sola occorrenza accodata", st)
	}
	rows := executions(t, pool)
	if len(rows) != 1 {
		t.Fatalf("esecuzioni registrate = %d, attesa 1", len(rows))
	}
	if rows[0].Status != "pending" {
		t.Fatalf("stato = %q, atteso pending: la riga è presa, non ancora partita", rows[0].Status)
	}
	if !rows[0].ScheduledFor.Equal(due) {
		t.Fatalf("scheduled_for = %s, atteso l'istante teorico %s", rows[0].ScheduledFor, due)
	}

	got := rec.all()
	if len(got) != 1 {
		t.Fatalf("occorrenze consegnate = %d, attesa 1", len(got))
	}
	if got[0].Job.ID != jobID || !got[0].ScheduledFor.Equal(due) || got[0].Attempt != 1 {
		t.Fatalf("occorrenza consegnata = %+v, non corrisponde alla riga", got[0])
	}
	if got[0].Job.URL == "" || got[0].Job.Method == "" {
		t.Fatal("l'occorrenza non porta con sé il bersaglio: chi esegue dovrebbe rileggerlo")
	}

	next := nextRunAt(t, pool, jobID)
	if want := gridNext(t, time.Hour, due); next == nil || !next.Equal(want) {
		t.Fatalf("prossima occorrenza = %v, attesa %s", next, want)
	}
}

func TestUnJobInDueAmbientiProduceUnEsecuzionePerAmbiente(t *testing.T) {
	pool := newTestDatabase(t)
	user := createUser(t, pool)

	now := testClock()
	due := now.Add(-time.Second)
	createJob(t, pool, user, jobSpec{
		Every:        time.Hour,
		NextRunAt:    &due,
		Environments: []string{"staging", "production"},
	})

	rec := &recorder{}
	engine := newEngine(t, pool, rec, &now, nil)
	if _, err := engine.Tick(t.Context()); err != nil {
		t.Fatalf("Tick: %v", err)
	}

	rows := executions(t, pool)
	if len(rows) != 2 {
		t.Fatalf("esecuzioni = %d, attese 2 (una per ambiente, R23)", len(rows))
	}
	envs := map[string]bool{}
	for _, r := range rows {
		envs[r.Environment] = true
	}
	if !envs["staging"] || !envs["production"] {
		t.Fatalf("ambienti registrati = %v, attesi staging e production", envs)
	}
	if rec.count() != 2 {
		t.Fatalf("occorrenze consegnate = %d, attese 2", rec.count())
	}
}

func TestUnaSecondaPassataNonRiaccodaLaStessaOccorrenza(t *testing.T) {
	pool := newTestDatabase(t)
	user := createUser(t, pool)

	now := testClock()
	due := now.Add(-time.Second)
	createJob(t, pool, user, jobSpec{Every: time.Hour, NextRunAt: &due})

	rec := &recorder{}
	engine := newEngine(t, pool, rec, &now, nil)

	for i := range 3 {
		if _, err := engine.Tick(t.Context()); err != nil {
			t.Fatalf("Tick %d: %v", i, err)
		}
	}

	if rows := executions(t, pool); len(rows) != 1 {
		t.Fatalf("esecuzioni = %d dopo tre passate, attesa 1", len(rows))
	}
	if dup := duplicates(rec.keys()); len(dup) > 0 {
		t.Fatalf("occorrenze consegnate più di una volta: %v", dup)
	}
}

// ---------------------------------------------------------- la trappola di 0006

func TestConflittoDiIdempotenzaPortaIlNomeDellaPartizione(t *testing.T) {
	// Questo test non prova lo scheduler: prova l'assunto su cui lo scheduler è
	// costruito. Se un giorno il conflitto smettesse di arrivare come 23505, o
	// il nome del vincolo diventasse stabile, si saprebbe da qui.
	pool := newTestDatabase(t)
	user := createUser(t, pool)

	now := testClock()
	jobID := createJob(t, pool, user, jobSpec{Every: time.Hour})
	at := now.Truncate(time.Second)

	insert := func() error {
		_, err := pool.Exec(t.Context(),
			`INSERT INTO job_executions (job_id, scheduled_for, environment)
			 VALUES ($1::uuid, $2, 'production')`, jobID, at)
		return err
	}

	if err := insert(); err != nil {
		t.Fatalf("primo inserimento: %v", err)
	}
	err := insert()
	if err == nil {
		t.Fatal("il secondo inserimento è riuscito: la chiave primaria non sta facendo da lock (R4)")
	}

	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		t.Fatalf("errore = %v, atteso un errore del driver PostgreSQL", err)
	}
	if pgErr.Code != "23505" {
		t.Fatalf("SQLSTATE = %q, atteso 23505: è l'unica cosa su cui il motore può contare", pgErr.Code)
	}

	// Il punto della trappola: il nome che arriva è quello della partizione del
	// giorno. Un motore che confrontasse questo nome con `job_executions_pkey`
	// passerebbe i test e sbaglierebbe in esercizio, ogni giorno.
	partition := "job_executions_" + at.Format("20060102") + "_pkey"
	if pgErr.ConstraintName != partition {
		t.Fatalf("vincolo violato = %q, atteso %q", pgErr.ConstraintName, partition)
	}
	if pgErr.ConstraintName == "job_executions_pkey" {
		t.Fatal("il nome del vincolo coincide con quello della tabella padre: la trappola non esisterebbe")
	}
}

// -------------------------------------------------------------------- riavvio

func TestIlRiavvioNonPerdeLOccorrenzaGiaPresaMaNonConsegnata(t *testing.T) {
	// Il momento peggiore per morire: la riga è scritta e il dispatch non l'ha
	// mai vista. Nessuno la eseguirà, se non c'è un recupero.
	pool := newTestDatabase(t)
	user := createUser(t, pool)

	now := testClock()
	due := now.Add(-time.Second)
	createJob(t, pool, user, jobSpec{Every: time.Hour, NextRunAt: &due})

	// Primo processo: prende l'occorrenza e muore prima di consegnarla. Un
	// dispatch che rifiuta è indistinguibile, dal lato del database, da un
	// processo che si spegne fra il commit e la consegna.
	morto := &recorder{fail: func(scheduler.Occurrence) error { return errors.New("processo terminato") }}
	primo := newEngine(t, pool, morto, &now, nil)
	st, err := primo.Tick(t.Context())
	if err != nil {
		t.Fatalf("Tick del primo processo: %v", err)
	}
	if st.Rejected != 1 {
		t.Fatalf("stats = %+v, attesa un'occorrenza rifiutata", st)
	}
	if morto.count() != 0 {
		t.Fatal("il dispatch morto ha registrato qualcosa")
	}
	if rows := executions(t, pool); len(rows) != 1 || rows[0].Status != "pending" {
		t.Fatalf("esecuzioni = %+v, attesa una sola riga pending", rows)
	}

	// Secondo processo: riparte e recupera. L'orologio avanza di qualche
	// secondo, come farebbe un riavvio vero.
	now = now.Add(5 * time.Second)
	vivo := &recorder{}
	secondo := newEngine(t, pool, vivo, &now, nil)
	if err := secondo.Recover(t.Context()); err != nil {
		t.Fatalf("Recover: %v", err)
	}

	got := vivo.all()
	if len(got) != 1 {
		t.Fatalf("occorrenze recuperate = %d, attesa 1: l'occorrenza è andata persa", len(got))
	}
	if !got[0].Recovered {
		t.Fatal("l'occorrenza recuperata non è marcata come tale: la misura di R47 ne resterebbe falsata")
	}
	if !got[0].ScheduledFor.Equal(due) {
		t.Fatalf("occorrenza recuperata a %s, attesa %s", got[0].ScheduledFor, due)
	}

	// E non la si recupera due volte: la finestra di abbandono tiene ferma la
	// riga appena riconsegnata.
	if _, err := secondo.Tick(t.Context()); err != nil {
		t.Fatalf("Tick del secondo processo: %v", err)
	}
	if dup := duplicates(vivo.keys()); len(dup) > 0 {
		t.Fatalf("occorrenze consegnate più di una volta: %v", dup)
	}
	if rows := executions(t, pool); len(rows) != 1 {
		t.Fatalf("esecuzioni = %d, attesa sempre 1", len(rows))
	}
}

func TestIlRiavvioNonDuplicaUnOccorrenzaGiaScritta(t *testing.T) {
	// L'altro momento in cui si può morire: le righe ci sono ma `next_run_at`
	// non è avanzato. Il motore ricalcola le stesse occorrenze e le ritrova già
	// prese — è il conflitto 23505 a dirglielo, ed è l'unica cosa che glielo
	// dice.
	pool := newTestDatabase(t)
	user := createUser(t, pool)

	now := testClock()
	due := now.Add(-time.Second).Truncate(time.Millisecond)
	jobID := createJob(t, pool, user, jobSpec{Every: time.Hour, NextRunAt: &due})

	// Lo stato lasciato dal processo morto: la riga esiste, la prossima
	// occorrenza è ancora quella vecchia.
	insertExecution(t, pool, jobID, due, "production")

	rec := &recorder{}
	engine := newEngine(t, pool, rec, &now, nil)
	st, err := engine.Tick(t.Context())
	if err != nil {
		t.Fatalf("Tick: %v", err)
	}

	if st.Conflicts != 1 {
		t.Fatalf("stats = %+v, atteso un conflitto di idempotenza", st)
	}
	if st.Enqueued != 0 {
		t.Fatalf("stats = %+v, nessuna occorrenza doveva essere consegnata: non era nostra", st)
	}
	if rows := executions(t, pool); len(rows) != 1 {
		t.Fatalf("esecuzioni = %d, attesa 1: l'occorrenza è stata duplicata", len(rows))
	}
	if want, next := gridNext(t, time.Hour, due), nextRunAt(t, pool, jobID); next == nil || !next.Equal(want) {
		t.Fatalf("prossima occorrenza = %v, attesa %s: il conflitto non deve bloccare l'avanzamento",
			next, want)
	}

	// Nessuno l'ha eseguita, però: a raccoglierla è il recupero, che è l'unico
	// posto in cui quella decisione si prende.
	now = now.Add(scheduler.DefaultReclaimAfter + time.Second)
	if _, err := engine.Tick(t.Context()); err != nil {
		t.Fatalf("Tick di recupero: %v", err)
	}
	if got := rec.all(); len(got) != 1 || !got[0].Recovered {
		t.Fatalf("occorrenze consegnate = %+v, attesa una sola ripresa", got)
	}
	if dup := duplicates(rec.keys()); len(dup) > 0 {
		t.Fatalf("occorrenze consegnate più di una volta: %v", dup)
	}
}

func TestUnConflittoSuUnAmbienteNonTravolgeLAltro(t *testing.T) {
	// Il percorso lento: metà lotto è già preso. Senza savepoint per
	// occorrenza, il conflitto del primo ambiente farebbe cadere anche il
	// secondo — che invece non è di nessuno e va eseguito.
	pool := newTestDatabase(t)
	user := createUser(t, pool)

	now := testClock()
	due := now.Add(-time.Second).Truncate(time.Millisecond)
	jobID := createJob(t, pool, user, jobSpec{
		Every:        time.Hour,
		NextRunAt:    &due,
		Environments: []string{"staging", "production"},
	})
	insertExecution(t, pool, jobID, due, "staging")

	rec := &recorder{}
	engine := newEngine(t, pool, rec, &now, nil)
	st, err := engine.Tick(t.Context())
	if err != nil {
		t.Fatalf("Tick: %v", err)
	}

	if st.Conflicts != 1 || st.Enqueued != 1 {
		t.Fatalf("stats = %+v, atteso un conflitto e un'occorrenza accodata", st)
	}
	got := rec.all()
	if len(got) != 1 || got[0].Environment != "production" {
		t.Fatalf("occorrenze consegnate = %+v, attesa la sola production", got)
	}
	if rows := executions(t, pool); len(rows) != 2 {
		t.Fatalf("esecuzioni = %d, attese 2", len(rows))
	}
}

// ---------------------------------------------------------------- concorrenza

func TestDueMotoriSulloStessoDatabaseNonDuplicano(t *testing.T) {
	pool := newTestDatabase(t)
	user := createUser(t, pool)

	now := testClock()
	due := now.Add(-30 * time.Second)
	const jobs = 12
	for i := range jobs {
		at := due.Add(time.Duration(i) * time.Millisecond)
		createJob(t, pool, user, jobSpec{
			Name:      "concorrente-" + string(rune('a'+i)),
			Every:     10 * time.Second,
			NextRunAt: &at,
		})
	}

	// Un solo registratore per entrambi i motori: se la stessa occorrenza viene
	// consegnata due volte, si vede qui.
	rec := &recorder{}
	motoreA := newEngine(t, pool, rec, &now, nil)
	motoreB := newEngine(t, pool, rec, &now, nil)

	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for _, engine := range []*scheduler.Engine{motoreA, motoreB} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 4 {
				if _, err := engine.Tick(t.Context()); err != nil {
					errs <- err
					return
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("Tick concorrente: %v", err)
	}

	if dup := duplicates(rec.keys()); len(dup) > 0 {
		t.Fatalf("occorrenze consegnate più di una volta: %v", dup)
	}
	rows := executions(t, pool)
	if len(rows) != rec.count() {
		t.Fatalf("righe su job_executions = %d, occorrenze consegnate = %d: devono coincidere",
			len(rows), rec.count())
	}
	// Ogni job aveva quattro occorrenze dovute (30 secondi di arretrato a 10
	// secondi di intervallo, più quella corrente): il lavoro dev'essere stato
	// fatto, non solo evitato.
	if len(rows) < jobs {
		t.Fatalf("righe su job_executions = %d, attese almeno %d", len(rows), jobs)
	}
	seen := map[string]bool{}
	for _, r := range rows {
		if seen[r.key()] {
			t.Fatalf("riga duplicata su job_executions: %s", r.key())
		}
		seen[r.key()] = true
	}
}

// ------------------------------------------------------ prima occorrenza e stato

func TestUnJobNuovoRiceveLaPrimaOccorrenza(t *testing.T) {
	pool := newTestDatabase(t)
	user := createUser(t, pool)

	now := testClock()
	jobID := createJob(t, pool, user, jobSpec{Expression: "*/5 * * * *"})
	if next := nextRunAt(t, pool, jobID); next != nil {
		t.Fatalf("il job nasce con next_run_at = %v, atteso NULL", next)
	}

	rec := &recorder{}
	engine := newEngine(t, pool, rec, &now, nil)
	st, err := engine.Tick(t.Context())
	if err != nil {
		t.Fatalf("Tick: %v", err)
	}

	if st.Seeded != 1 {
		t.Fatalf("stats = %+v, atteso un job avviato", st)
	}
	next := nextRunAt(t, pool, jobID)
	if next == nil {
		t.Fatal("il job è rimasto senza prossima occorrenza: non partirà mai")
	}
	if !next.After(now) {
		t.Fatalf("prima occorrenza = %s, non successiva a %s: un job appena creato non deve partire subito", next, now)
	}
	if rec.count() != 0 {
		t.Fatalf("occorrenze consegnate = %d, attesa nessuna", rec.count())
	}
}

func TestUnJobInPausaONonAncoraDovutoRestaFermo(t *testing.T) {
	pool := newTestDatabase(t)
	user := createUser(t, pool)

	now := testClock()
	passato := now.Add(-time.Second)
	futuro := now.Add(time.Hour)
	spento := false
	createJob(t, pool, user, jobSpec{Name: "in-pausa", Every: time.Hour, NextRunAt: &passato, Enabled: &spento})
	createJob(t, pool, user, jobSpec{Name: "non-dovuto", Every: time.Hour, NextRunAt: &futuro})

	rec := &recorder{}
	engine := newEngine(t, pool, rec, &now, nil)
	st, err := engine.Tick(t.Context())
	if err != nil {
		t.Fatalf("Tick: %v", err)
	}

	if st.Jobs != 0 || rec.count() != 0 {
		t.Fatalf("stats = %+v, consegnate = %d: nessuno dei due job era dovuto", st, rec.count())
	}
	if rows := executions(t, pool); len(rows) != 0 {
		t.Fatalf("esecuzioni = %d, attesa nessuna", len(rows))
	}
}

func TestUnaSchedulazioneIllegibileNonBloccaGliAltriJob(t *testing.T) {
	pool := newTestDatabase(t)
	user := createUser(t, pool)

	now := testClock()
	due := now.Add(-time.Second)
	// Cinque campi, quindi il vincolo di forma dello schema lo accetta; il minuto
	// 99 no. È il caso che può arrivare solo da una porta di servizio.
	rotto := createJob(t, pool, user, jobSpec{Name: "rotto", Expression: "99 * * * *", NextRunAt: &due})
	createJob(t, pool, user, jobSpec{Name: "sano", Every: time.Hour, NextRunAt: &due})

	rec := &recorder{}
	engine := newEngine(t, pool, rec, &now, nil)
	st, err := engine.Tick(t.Context())
	if err != nil {
		t.Fatalf("Tick: %v", err)
	}

	if st.Invalid != 1 {
		t.Fatalf("stats = %+v, atteso un job illeggibile", st)
	}
	if st.Enqueued != 1 {
		t.Fatalf("stats = %+v, il job sano doveva essere accodato lo stesso", st)
	}
	if next := nextRunAt(t, pool, rotto); next != nil {
		t.Fatalf("il job rotto ha next_run_at = %v: resterebbe nell'indice del dispatch per sempre", next)
	}
}

// ------------------------------------------------------- finestra di recupero

func TestLeOccorrenzeTroppoVecchieNonVengonoEseguite(t *testing.T) {
	pool := newTestDatabase(t)
	user := createUser(t, pool)

	now := testClock()
	// Un job al minuto fermo da mezz'ora, con finestra di recupero di cinque
	// minuti: 25 occorrenze non si eseguono, le ultime sì.
	due := now.Add(-30 * time.Minute).Truncate(time.Minute)
	createJob(t, pool, user, jobSpec{Every: time.Minute, NextRunAt: &due})

	rec := &recorder{}
	obs := &collector{}
	engine := newEngine(t, pool, rec, &now, func(o *scheduler.Options) {
		o.CatchUp = 5 * time.Minute
		o.Observer = obs
	})

	st, err := engine.Tick(t.Context())
	if err != nil {
		t.Fatalf("Tick: %v", err)
	}

	if st.Dropped == 0 {
		t.Fatal("nessuna occorrenza dichiarata scartata: il salto sarebbe silenzioso")
	}
	if st.Enqueued == 0 {
		t.Fatal("nessuna occorrenza accodata: la finestra doveva contenerne")
	}
	if st.Dropped+st.Enqueued < 30 {
		t.Fatalf("scartate %d + accodate %d, attese almeno 30 occorrenze in tutto", st.Dropped, st.Enqueued)
	}
	for _, occ := range rec.all() {
		if occ.ScheduledFor.Before(now.Add(-5 * time.Minute)) {
			t.Fatalf("occorrenza %s consegnata pur essendo fuori dalla finestra", occ.ScheduledFor)
		}
	}
	drops := obs.dropped()
	if len(drops) != 1 || drops[0].Count != st.Dropped {
		t.Fatalf("segnalazioni di scarto = %+v, attesa una da %d occorrenze", drops, st.Dropped)
	}
	if drops[0].Job.Name == "" {
		t.Fatal("la segnalazione non dice di quale job si tratta")
	}
}

func TestUnOccorrenzaInSospesoTroppoVecchiaVieneChiusa(t *testing.T) {
	pool := newTestDatabase(t)
	user := createUser(t, pool)

	now := testClock()
	jobID := createJob(t, pool, user, jobSpec{Every: time.Hour})
	vecchia := now.Add(-time.Hour).Truncate(time.Second)
	insertExecution(t, pool, jobID, vecchia, "production")

	rec := &recorder{}
	engine := newEngine(t, pool, rec, &now, func(o *scheduler.Options) { o.CatchUp = 5 * time.Minute })
	st, err := engine.Tick(t.Context())
	if err != nil {
		t.Fatalf("Tick: %v", err)
	}

	if st.Expired != 1 {
		t.Fatalf("stats = %+v, attesa un'occorrenza chiusa", st)
	}
	if rec.count() != 0 {
		t.Fatalf("occorrenze consegnate = %d: una riga di un'ora fa non va eseguita adesso", rec.count())
	}
	rows := executions(t, pool)
	if len(rows) != 1 || rows[0].Status != "skipped" {
		t.Fatalf("esecuzioni = %+v, attesa una riga skipped", rows)
	}
}

// ------------------------------------------------------------- tolleranza R47

func TestIlRitardoDiOgniOccorrenzaEMisurabile(t *testing.T) {
	pool := newTestDatabase(t)
	user := createUser(t, pool)

	now := testClock()
	// Un'occorrenza in ritardo di due secondi: oltre la tolleranza dichiarata.
	due := now.Add(-2 * time.Second)
	createJob(t, pool, user, jobSpec{Every: time.Hour, NextRunAt: &due})

	rec := &recorder{}
	obs := &collector{}
	engine := newEngine(t, pool, rec, &now, func(o *scheduler.Options) { o.Observer = obs })

	st, err := engine.Tick(t.Context())
	if err != nil {
		t.Fatalf("Tick: %v", err)
	}

	if st.MaxLag < 2*time.Second {
		t.Fatalf("ritardo massimo = %s, atteso almeno 2s", st.MaxLag)
	}
	if st.Late != 1 {
		t.Fatalf("occorrenze oltre tolleranza = %d, attesa 1 (tolleranza %s)", st.Late, scheduler.DefaultTolerance)
	}
	enqueued := obs.enqueued()
	if len(enqueued) != 1 {
		t.Fatalf("occorrenze osservate = %d, attesa 1", len(enqueued))
	}
	if enqueued[0].Lag() < 2*time.Second {
		t.Fatalf("ritardo osservato = %s, atteso almeno 2s", enqueued[0].Lag())
	}
	if got := rec.all()[0]; got.EnqueuedAt.IsZero() {
		t.Fatal("l'occorrenza consegnata non porta l'istante di consegna: R47 non sarebbe misurabile")
	}
}

func TestUnaOccorrenzaPuntualeRestaDentroLaTolleranza(t *testing.T) {
	pool := newTestDatabase(t)
	user := createUser(t, pool)

	// L'orologio del motore è quello vero: il ritardo misurato è quello vero,
	// comprensivo del tempo della passata.
	due := time.Now().UTC()
	createJob(t, pool, user, jobSpec{Every: time.Hour, NextRunAt: &due})

	rec := &recorder{}
	engine, err := scheduler.New(scheduler.Options{
		Pool:       pool,
		Dispatcher: rec,
		Logger:     testLogger(t),
	})
	if err != nil {
		t.Fatalf("scheduler.New: %v", err)
	}

	st, err := engine.Tick(t.Context())
	if err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if st.Enqueued != 1 {
		t.Fatalf("stats = %+v, attesa un'occorrenza accodata", st)
	}
	if st.Late != 0 {
		t.Fatalf("occorrenza dichiarata in ritardo: %s oltre la tolleranza di %s",
			st.MaxLag, scheduler.DefaultTolerance)
	}
}

// ---------------------------------------------------------------- ciclo di vita

func TestRunAccodaFinoAllaChiusuraDelContesto(t *testing.T) {
	pool := newTestDatabase(t)
	user := createUser(t, pool)

	// Tre occorrenze già dovute, così la prima passata ha subito qualcosa da
	// fare: il test non deve aspettare lo scoccare di un secondo.
	due := time.Now().UTC().Add(-3 * time.Second).Truncate(time.Second)
	createJob(t, pool, user, jobSpec{Every: time.Second, NextRunAt: &due})

	rec := &recorder{}
	engine, err := scheduler.New(scheduler.Options{
		Pool:       pool,
		Dispatcher: rec,
		Interval:   20 * time.Millisecond,
		Logger:     testLogger(t),
	})
	if err != nil {
		t.Fatalf("scheduler.New: %v", err)
	}

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- engine.Run(ctx) }()

	deadline := time.Now().Add(3 * time.Second)
	for rec.count() == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	cancel()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Run è uscito con %v, atteso context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run non si è fermato alla chiusura del contesto")
	}

	if rec.count() == 0 {
		t.Fatal("nessuna occorrenza consegnata dal ciclo")
	}
	if dup := duplicates(rec.keys()); len(dup) > 0 {
		t.Fatalf("occorrenze consegnate più di una volta: %v", dup)
	}
	rows := executions(t, pool)
	if len(rows) != rec.count() {
		t.Fatalf("righe su job_executions = %d, occorrenze consegnate = %d", len(rows), rec.count())
	}
}

// ------------------------------------------------------------------ osservatore

// collector raccoglie ciò che il motore osserva.
type collector struct {
	mu    sync.Mutex
	enq   []scheduler.Occurrence
	drops []scheduler.Dropped
	ticks []scheduler.Stats
}

func (c *collector) Enqueued(occ scheduler.Occurrence) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.enq = append(c.enq, occ)
}

func (c *collector) Dropped(d scheduler.Dropped) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.drops = append(c.drops, d)
}

func (c *collector) Tick(st scheduler.Stats) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.ticks = append(c.ticks, st)
}

func (c *collector) enqueued() []scheduler.Occurrence {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]scheduler.Occurrence(nil), c.enq...)
}

func (c *collector) dropped() []scheduler.Dropped {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]scheduler.Dropped(nil), c.drops...)
}

// insertExecution scrive a mano una riga di `job_executions`: è il modo di
// mettere il database nello stato in cui lo lascerebbe un processo morto a metà.
func insertExecution(t *testing.T, pool *pgxpool.Pool, jobID string, at time.Time, env string) {
	t.Helper()
	_, err := pool.Exec(t.Context(),
		`INSERT INTO job_executions (job_id, scheduled_for, environment, status, triggered_by)
		 VALUES ($1::uuid, $2, $3::environment, 'pending', 'schedule')`, jobID, at, env)
	if err != nil {
		t.Fatalf("inserimento dell'esecuzione: %v", err)
	}
}
