package retention_test

import (
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/apdsoftware/postqron/services/api/internal/retention"
)

// day è la giornata, unità in cui SPEC §8 scrive la retention.
const day = 24 * time.Hour

// newService costruisce il servizio con opzioni da test: lotti piccoli, nessuna
// pausa apprezzabile, un lock_timeout corto.
func newService(t *testing.T, pool *pgxpool.Pool, opts retention.Options) *retention.Service {
	t.Helper()

	opts.Pool = pool
	if opts.Batch == 0 {
		opts.Batch = 100
	}
	if opts.Pause == 0 {
		opts.Pause = time.Millisecond
	}
	if opts.LockTimeout == 0 {
		opts.LockTimeout = 500 * time.Millisecond
	}
	svc, err := retention.New(opts)
	if err != nil {
		t.Fatalf("costruzione del servizio: %v", err)
	}
	return svc
}

// ------------------------------------------------------- le partizioni future

// TestSenzaChiamanteLeScrittureFalliscono documenta il confine da cui nasce metà
// di questa issue, invece di lasciarlo implicito.
//
// La 0006 prepara quattordici giorni in avanti **una volta sola**, al momento
// della migrazione. Da lì in poi non li prepara nessuno: l'inserimento oltre
// l'ultima partizione non finisce in un contenitore generico, fallisce. È la
// scelta della 0006 ed è quella giusta — un log che sparisce in una partizione
// di default si scopre settimane dopo — ma è una scelta che regge solo finché
// qualcuno crea le partizioni.
func TestSenzaChiamanteLeScrittureFalliscono(t *testing.T) {
	t.Parallel()
	pool := newTestDatabase(t)

	jobID := newJob(t, pool, newUser(t, pool, ""))
	oltre := time.Now().UTC().Add(30 * day)

	_, err := pool.Exec(t.Context(),
		`INSERT INTO job_executions (job_id, scheduled_for, environment)
		 VALUES ($1::uuid, $2, 'production')`, jobID, oltre)
	if err == nil {
		t.Fatal("l'inserimento oltre la finestra è riuscito: la 0006 non prepara più di quattordici giorni")
	}

	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23514" {
		t.Fatalf("errore = %v, atteso 23514 (nessuna partizione per la riga)", err)
	}
}

// TestLaPassataPreparaLePartizioniFuture è l'altra faccia del test precedente:
// con un chiamante, la stessa scrittura passa.
func TestLaPassataPreparaLePartizioniFuture(t *testing.T) {
	t.Parallel()
	pool := newTestDatabase(t)

	jobID := newJob(t, pool, newUser(t, pool, ""))
	oltre := time.Now().UTC().Add(30 * day)

	svc := newService(t, pool, retention.Options{DaysAhead: 40})
	stats, err := svc.Sweep(t.Context())
	if err != nil {
		t.Fatalf("passata fallita: %v", err)
	}
	if stats.PartitionsEnsured == 0 {
		t.Fatal("nessuna partizione preparata")
	}
	if stats.EnsureDeferred {
		t.Fatal("creazione rimandata senza nessuno che tenesse il lock")
	}

	if _, err := pool.Exec(t.Context(),
		`INSERT INTO job_executions (job_id, scheduled_for, environment)
		 VALUES ($1::uuid, $2, 'production')`, jobID, oltre); err != nil {
		t.Fatalf("inserimento a trenta giorni ancora rifiutato dopo la passata: %v", err)
	}
}

// TestLaPassataRicostruisceLaFinestraDaZero copre il caso peggiore: un database
// in cui le partizioni non ci sono più affatto. La passata deve renderlo
// scrivibile, non limitarsi a cancellare.
func TestLaPassataRicostruisceLaFinestraDaZero(t *testing.T) {
	t.Parallel()
	pool := newTestDatabase(t)

	jobID := newJob(t, pool, newUser(t, pool, ""))
	dropAllPartitions(t, pool)
	if names := partitionNames(t, pool); len(names) != 0 {
		t.Fatalf("partizioni residue: %v", names)
	}

	svc := newService(t, pool, retention.Options{})
	if _, err := svc.Sweep(t.Context()); err != nil {
		t.Fatalf("passata fallita: %v", err)
	}

	if !hasPartition(t, pool, time.Now().UTC()) {
		t.Fatal("la partizione di oggi non è stata ricreata")
	}
	if _, err := pool.Exec(t.Context(),
		`INSERT INTO job_executions (job_id, scheduled_for, environment)
		 VALUES ($1::uuid, now(), 'production')`, jobID); err != nil {
		t.Fatalf("scrittura di oggi ancora impossibile dopo la passata: %v", err)
	}
}

// --------------------------------------------------- retention lunga: il DROP

// TestLePartizioniInteramenteScaduteSeNeVanno: quando tutti i piani
// rappresentati hanno superato la propria finestra, la giornata si elimina
// intera. È il meccanismo veloce, quello che non lascia bloat.
func TestLePartizioniInteramenteScaduteSeNeVanno(t *testing.T) {
	t.Parallel()
	pool := newTestDatabase(t)

	// Nessuna sottoscrizione: tutti su `free`, tre giorni.
	jobID := newJob(t, pool, newUser(t, pool, ""))
	vecchio := time.Now().UTC().Add(-10 * day)
	insertExecution(t, pool, jobID, vecchio, 1)

	if !hasPartition(t, pool, vecchio) {
		t.Fatal("la partizione di dieci giorni fa doveva esistere")
	}

	svc := newService(t, pool, retention.Options{})
	stats, err := svc.Sweep(t.Context())
	if err != nil {
		t.Fatalf("passata fallita: %v", err)
	}

	if stats.LongestRetention != 3 {
		t.Errorf("retention più lunga = %d giorni, attesi 3 (solo `free` rappresentato)", stats.LongestRetention)
	}
	if stats.PartitionsDropped != 1 {
		t.Errorf("partizioni eliminate = %d, attesa 1", stats.PartitionsDropped)
	}
	if hasPartition(t, pool, vecchio) {
		t.Error("la partizione scaduta esiste ancora")
	}
	if n := countExecutions(t, pool, jobID); n != 0 {
		t.Errorf("righe residue = %d, attese 0", n)
	}
	// Il DROP deve portare via la giornata scaduta e nient'altro: la partizione
	// di oggi è quella su cui il motore sta scrivendo.
	if !hasPartition(t, pool, time.Now().UTC()) {
		t.Error("il DROP ha portato via anche la partizione di oggi")
	}
}

// TestUnSoloClienteAgencyTieneInVitaLaGiornataDiTutti è il caso che rende la
// retention un dato del piano e non una costante.
//
// Una partizione contiene le esecuzioni di tutti. Eliminarla appena il piano più
// corto ha superato la propria finestra cancellerebbe i log di un cliente Agency
// perché un cliente Free stava nella stessa giornata.
func TestUnSoloClienteAgencyTieneInVitaLaGiornataDiTutti(t *testing.T) {
	t.Parallel()
	pool := newTestDatabase(t)

	agency := newJob(t, pool, newUser(t, pool, "agency"))
	vecchio := time.Now().UTC().Add(-40 * day)
	insertExecution(t, pool, agency, vecchio, 1)

	svc := newService(t, pool, retention.Options{})
	stats, err := svc.Sweep(t.Context())
	if err != nil {
		t.Fatalf("passata fallita: %v", err)
	}

	if stats.LongestRetention != 90 {
		t.Errorf("retention più lunga = %d giorni, attesi 90", stats.LongestRetention)
	}
	if stats.PartitionsDropped != 0 {
		t.Errorf("partizioni eliminate = %d, attese 0: quaranta giorni stanno dentro i novanta di Agency",
			stats.PartitionsDropped)
	}
	if n := countExecutions(t, pool, agency); n != 1 {
		t.Errorf("righe di Agency = %d, attesa 1", n)
	}
}

// --------------------------------------- piani misti nella stessa partizione

// TestPianiMistiNellaStessaPartizione è il caso in cui i due meccanismi si
// incontrano, ed è quello in cui sbagliare costa i log di un cliente.
//
// Stessa giornata, dieci giorni fa. Dentro ci sono le esecuzioni di un utente
// Free (tre giorni: scadute) e quelle di un utente Agency (novanta giorni:
// vive). L'esito corretto è l'unico che rispetta entrambi i piani: **la
// partizione resta**, le righe Free se ne vanno una a una, quelle Agency non si
// toccano.
func TestPianiMistiNellaStessaPartizione(t *testing.T) {
	t.Parallel()
	pool := newTestDatabase(t)

	free := newJob(t, pool, newUser(t, pool, "free"))
	// Nessuna sottoscrizione: `free` per ricaduta, come ogni account appena
	// registrato. Deve essere trattato come quello con la riga esplicita.
	senzaPiano := newJob(t, pool, newUser(t, pool, ""))
	agency := newJob(t, pool, newUser(t, pool, "agency"))
	team := newJob(t, pool, newUser(t, pool, "team"))

	giornata := time.Now().UTC().Add(-10 * day)
	for attempt := 1; attempt <= 3; attempt++ {
		insertExecution(t, pool, free, giornata, attempt)
		insertExecution(t, pool, senzaPiano, giornata, attempt)
		insertExecution(t, pool, agency, giornata, attempt)
		insertExecution(t, pool, team, giornata, attempt)
	}

	svc := newService(t, pool, retention.Options{})
	stats, err := svc.Sweep(t.Context())
	if err != nil {
		t.Fatalf("passata fallita: %v", err)
	}

	if stats.PartitionsDropped != 0 {
		t.Fatalf("partizioni eliminate = %d: la giornata contiene righe ancora dentro la retention di Agency",
			stats.PartitionsDropped)
	}
	if !hasPartition(t, pool, giornata) {
		t.Fatal("la partizione è stata eliminata pur contenendo righe vive")
	}
	if stats.RowsDeleted != 6 {
		t.Errorf("righe cancellate = %d, attese 6 (i due utenti a tre giorni, tre righe ciascuno)",
			stats.RowsDeleted)
	}

	for _, tc := range []struct {
		nome   string
		jobID  string
		atteso int
	}{
		{"free con sottoscrizione", free, 0},
		{"free per ricaduta", senzaPiano, 0},
		{"team (30 giorni)", team, 3},
		{"agency (90 giorni)", agency, 3},
	} {
		if n := countExecutions(t, pool, tc.jobID); n != tc.atteso {
			t.Errorf("%s: righe = %d, attese %d", tc.nome, n, tc.atteso)
		}
	}
}

// TestIlConfineDelleRigheCoincideConQuelloDellaLettura: la cancellazione toglie
// esattamente ciò che R10-bis nasconde, né una riga in più né una in meno.
//
// Se i due confini divergessero avremmo una delle due cose sbagliate: righe che
// spariscono mentre l'API le sta ancora mostrando, oppure righe che nessuno può
// più leggere e che la privacy policy dichiara cancellate.
func TestIlConfineDelleRigheCoincideConQuelloDellaLettura(t *testing.T) {
	t.Parallel()
	pool := newTestDatabase(t)

	// Un cliente Agency vivo alza il taglio delle partizioni a novanta giorni:
	// così il DROP resta fuori dai piedi e si osserva solo il confine per riga.
	insertExecution(t, pool, newJob(t, pool, newUser(t, pool, "agency")),
		time.Now().UTC().Add(-1*day), 1)

	free := newJob(t, pool, newUser(t, pool, "free"))
	adesso := time.Now().UTC()
	dentro := adesso.Add(-3*day + time.Hour)
	fuori := adesso.Add(-3*day - time.Hour)
	insertExecution(t, pool, free, dentro, 1)
	insertExecution(t, pool, free, fuori, 1)

	svc := newService(t, pool, retention.Options{Now: func() time.Time { return adesso }})
	if _, err := svc.Sweep(t.Context()); err != nil {
		t.Fatalf("passata fallita: %v", err)
	}

	var rimaste []time.Time
	rows, err := pool.Query(t.Context(),
		`SELECT scheduled_for FROM job_executions WHERE job_id = $1::uuid`, free)
	if err != nil {
		t.Fatalf("lettura delle righe rimaste: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var when time.Time
		if err := rows.Scan(&when); err != nil {
			t.Fatalf("lettura di scheduled_for: %v", err)
		}
		rimaste = append(rimaste, when.UTC())
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("lettura delle righe rimaste: %v", err)
	}

	if len(rimaste) != 1 {
		t.Fatalf("righe rimaste = %d, attesa 1 (quella dentro la finestra)", len(rimaste))
	}
	if !rimaste[0].Equal(dentro.Truncate(time.Microsecond)) &&
		rimaste[0].Sub(dentro).Abs() > time.Millisecond {
		t.Errorf("è rimasta la riga di %s, attesa quella di %s",
			rimaste[0].Format(time.RFC3339), dentro.Format(time.RFC3339))
	}
}

// ------------------------------------------------------------------- i lotti

// TestLaCancellazioneProcedeALotti: nessun DELETE unico. È ciò che tiene corte
// le transazioni e limitato il WAL mentre il motore scrive sulla stessa tabella.
func TestLaCancellazioneProcedeALotti(t *testing.T) {
	t.Parallel()
	pool := newTestDatabase(t)

	// Agency vivo: il DROP non deve poter portare via la giornata in blocco,
	// altrimenti il conto dei lotti non misurerebbe niente.
	insertExecution(t, pool, newJob(t, pool, newUser(t, pool, "agency")),
		time.Now().UTC().Add(-1*day), 1)

	free := newJob(t, pool, newUser(t, pool, "free"))
	const righe = 250
	vecchio := time.Now().UTC().Add(-10 * day)
	for i := 1; i <= righe; i++ {
		insertExecution(t, pool, free, vecchio, i)
	}

	svc := newService(t, pool, retention.Options{Batch: 100})
	stats, err := svc.Sweep(t.Context())
	if err != nil {
		t.Fatalf("passata fallita: %v", err)
	}

	if stats.RowsDeleted != righe {
		t.Errorf("righe cancellate = %d, attese %d", stats.RowsDeleted, righe)
	}
	if stats.Batches < 3 {
		t.Errorf("lotti = %d: con lotti da 100 e %d righe ne servono almeno 3", stats.Batches, righe)
	}
	if stats.Truncated {
		t.Error("passata dichiarata incompleta pur avendo cancellato tutto")
	}
	if n := countExecutions(t, pool, free); n != 0 {
		t.Errorf("righe residue = %d, attese 0", n)
	}
}

// TestIlTettoDeiLottiSiDichiara: una passata che si ferma al tetto lo dice. Un
// tetto silenzioso si legge come «ho finito» quando non è vero, ed è il modo in
// cui una promessa di cancellazione smette di essere mantenuta senza che se ne
// accorga nessuno.
func TestIlTettoDeiLottiSiDichiara(t *testing.T) {
	t.Parallel()
	pool := newTestDatabase(t)

	insertExecution(t, pool, newJob(t, pool, newUser(t, pool, "agency")),
		time.Now().UTC().Add(-1*day), 1)

	free := newJob(t, pool, newUser(t, pool, "free"))
	vecchio := time.Now().UTC().Add(-10 * day)
	for i := 1; i <= 30; i++ {
		insertExecution(t, pool, free, vecchio, i)
	}

	svc := newService(t, pool, retention.Options{Batch: 10, MaxBatches: 2})
	stats, err := svc.Sweep(t.Context())
	if err != nil {
		t.Fatalf("passata fallita: %v", err)
	}

	if !stats.Truncated {
		t.Error("il tetto è stato raggiunto ma la passata non lo dichiara")
	}
	if stats.RowsDeleted != 20 {
		t.Errorf("righe cancellate = %d, attese 20 (due lotti da dieci)", stats.RowsDeleted)
	}
	if n := countExecutions(t, pool, free); n != 10 {
		t.Errorf("righe residue = %d, attese 10", n)
	}

	// Ciò che avanza lo prende la passata successiva: il tetto rimanda, non
	// rinuncia.
	if _, err := svc.Sweep(t.Context()); err != nil {
		t.Fatalf("seconda passata fallita: %v", err)
	}
	if n := countExecutions(t, pool, free); n != 0 {
		t.Errorf("righe residue dopo la seconda passata = %d, attese 0", n)
	}
}

// ------------------------------------------------------------- le opzioni

func TestNewRifiutaCiòCheNonPuòApplicare(t *testing.T) {
	t.Parallel()

	pool := &pgxpool.Pool{}
	casi := map[string]retention.Options{
		"senza pool":       {},
		"pausa negativa":   {Pool: pool, Pause: -time.Second},
		"lotto negativo":   {Pool: pool, Batch: -1},
		"margine negativo": {Pool: pool, DaysAhead: -1},
	}
	for nome, opts := range casi {
		t.Run(nome, func(t *testing.T) {
			t.Parallel()
			if _, err := retention.New(opts); err == nil {
				t.Fatal("opzioni incoerenti accettate")
			}
		})
	}
}
