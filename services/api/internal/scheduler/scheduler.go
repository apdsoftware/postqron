// Package scheduler calcola le occorrenze dovute e le accoda per il dispatch
// (R2, R4).
//
// È il cuore del motore: sta fra internal/schedule, che sa *quando* tocca a un
// job, e il dispatch vero e proprio — worker pool (#389) ed esecutore HTTP
// (#390) — che sta dietro l'interfaccia [Dispatcher] e qui non è implementato.
//
// # Che cosa deve garantire
//
// Una cosa sola, e vale la pena scriverla per esteso perché tutto il resto ne
// discende: **ogni occorrenza schedulata è eseguita una volta sola, anche se il
// processo si riavvia, anche se ne girano due insieme** (R4).
//
// # Come la garantisce
//
// Non con un lock applicativo, ma con la chiave primaria di `job_executions`.
// La migrazione 0006 la definisce naturale — `(job_id, scheduled_for,
// environment, attempt)` — proprio per questo: l'occorrenza è identificabile
// *prima* di essere eseguita, quindi l'inserimento della riga può fare da lock.
// Il motore **inserisce prima di dispatchare**; chi arriva secondo trova un
// conflitto e si ferma. Non c'è finestra fra «verifico» e «inserisco», e non
// serve una tabella di lock.
//
// Il conflitto si riconosce dal solo SQLSTATE `23505`. Il *nome* del vincolo
// violato non si guarda mai: su una tabella partizionata è il nome della
// partizione (`job_executions_20260817_pkey`), e un confronto con una costante
// funzionerebbe il giorno in cui è stato scritto e fallirebbe tutti gli altri.
//
// # Il riavvio
//
// Il riavvio non è un caso limite, è il requisito. Tre meccanismi lo coprono, e
// ognuno risponde a un momento diverso in cui il processo può morire:
//
//   - **Prima del commit.** Il calcolo delle occorrenze, il loro inserimento e
//     l'avanzamento di `jobs.next_run_at` stanno nella **stessa transazione**.
//     Un processo che muore lì dentro non lascia niente: al riavvio le stesse
//     occorrenze vengono ricalcolate identiche, perché dipendono solo da
//     `next_run_at`, e nulla è andato perso.
//   - **Fra il commit e il dispatch.** Le righe esistono, stato `pending`, ma
//     nessuno le ha prese. Le ritrova [Engine.Recover], che è il motivo per cui
//     0006 tiene un indice parziale sulle sole esecuzioni in volo.
//   - **Dopo il dispatch.** Non è più affare di questo package: chi esegue
//     porta la riga a `running` con un aggiornamento condizionato su
//     `status = 'pending'`, ed è quel `WHERE` a impedire che una ripresa la
//     faccia partire due volte. Vedi [Dispatcher].
//
// # La finestra di recupero
//
// Dopo un fermo lungo qualcuno deve decidere che fare dell'arretrato, e non c'è
// una risposta indolore. Un job a un secondo produce 86.400 occorrenze al
// giorno: eseguirle tutte al ritorno significa scaricarle sul bersaglio
// dell'utente tutte insieme, cioè trasformare un disservizio in un attacco.
//
// La scelta è dichiarata invece che implicita. Le occorrenze antecedenti a
// `now - CatchUp` **non si eseguono**: quelle mai accodate vengono contate e
// segnalate ([Dropped]), quelle già accodate e mai partite vengono chiuse come
// `skipped`, che è lo stato che 0001 riserva a «l'occorrenza non è mai partita
// per decisione del motore». Dentro la finestra invece non si perde niente: si
// recupera a ondate, al massimo [Options.MaxPerJob] occorrenze per job a
// passata, e `next_run_at` avanza solo fino a dove si è arrivati davvero.
//
// # La tolleranza (R47)
//
// «Risoluzione 1 secondo» senza un numero accanto è una frase di marketing. Il
// numero è [DefaultTolerance] e si misura su [Occurrence.Lag]: lo scarto fra
// l'orario dovuto e il momento della consegna al dispatch. Ogni occorrenza
// passa da lì, [Observer] la vede, e [Stats] ne riporta il massimo e quante
// hanno superato la tolleranza. La misura in esercizio è la issue #462; qui c'è
// il punto in cui si osserva.
package scheduler

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/apdsoftware/postqron/services/api/internal/schedule"
)

// I valori di partenza. Sono legati fra loro più di quanto sembri: vedi
// [DefaultTolerance], che è un impegno reso possibile da [DefaultInterval].
const (
	// DefaultInterval è ogni quanto il motore guarda se c'è qualcosa da fare.
	//
	// Deve essere una frazione della risoluzione più fine venduta, che è 1
	// secondo (piano Team, SPEC §8): con una passata al secondo un job a un
	// secondo partirebbe sistematicamente in ritardo di quasi un intero
	// periodo. Un quarto di secondo lascia margine per il tempo della passata
	// stessa e costa quattro query indicizzate al secondo su un indice parziale
	// che a riposo è vuoto.
	DefaultInterval = 250 * time.Millisecond

	// DefaultTolerance è la tolleranza di dispatch dichiarata (R47): lo scarto
	// massimo atteso fra l'orario dovuto di un'occorrenza e il momento in cui
	// viene consegnata al dispatch, a coda scarica.
	//
	// Un secondo, e il conto è questo: fino a [DefaultInterval] di attesa
	// prima che la passata cominci, più il tempo della passata — una SELECT su
	// indice, un INSERT per lotto, un UPDATE — che su PostgreSQL in locale
	// (SPEC §2) sta nell'ordine dei millisecondi. Il margine è ampio di
	// proposito: una tolleranza dichiarata deve reggere anche le giornate
	// storte, non solo il caso migliore. Superarla non è un errore ma un fatto
	// da contare, ed è contato in [Stats.Late].
	DefaultTolerance = time.Second

	// DefaultCatchUp è la finestra di recupero: quanto indietro si va a
	// riprendere occorrenze mancate. Cinque minuti sono abbastanza per
	// assorbire un riavvio, un deploy o una pausa del database senza saltare
	// nulla, e abbastanza poco perché il recupero di un job a un secondo resti
	// nell'ordine delle centinaia di occorrenze e non delle decine di migliaia.
	DefaultCatchUp = 5 * time.Minute

	// DefaultMaxPerJob è il tetto di occorrenze accodate per job a ogni
	// passata. Serve a smaltire un arretrato a ondate invece che in un colpo
	// solo: 120 occorrenze quattro volte al secondo drenano un job a un secondo
	// molto più in fretta di quanto si riempia, senza che una singola passata
	// si trasformi in una scrittura di migliaia di righe.
	DefaultMaxPerJob = 120

	// DefaultBatch è quanti job dovuti si prendono per passata.
	DefaultBatch = 200

	// DefaultReclaimAfter è da quanto una riga dev'essere ferma in `pending`
	// prima di essere riofferta al dispatch. È generoso rispetto al tempo di
	// presa dell'esecutore: riofferta troppo presto significherebbe consegnare
	// due volte un'occorrenza che sta semplicemente aspettando il suo turno in
	// coda.
	DefaultReclaimAfter = 30 * time.Second

	// DefaultReclaimHorizon è quanto indietro il recupero si spinge a cercare
	// occorrenze in sospeso. Delimita quali partizioni giornaliere vengono
	// aperte: oltre l'orizzonte le righe restano `pending` e le porta via la
	// retention (R6). Due giorni coprono qualunque fermo dopo il quale abbia
	// ancora senso guardare l'arretrato.
	DefaultReclaimHorizon = 48 * time.Hour

	// DefaultQuarantineFor è per quanto un job con una schedulazione
	// illeggibile viene messo da parte prima di riprovare a interpretarla.
	// Senza, il motore ritenterebbe lo stesso errore a ogni passata e un
	// pugno di job rotti terrebbe occupato lo spazio dei job nuovi.
	DefaultQuarantineFor = 5 * time.Minute
)

// Policy sono le due decisioni sul recupero, quelle che [planOccurrences]
// applica senza sapere niente del database.
type Policy struct {
	// CatchUp è la finestra di recupero: le occorrenze più vecchie di così non
	// si eseguono.
	CatchUp time.Duration
	// MaxPerJob è il tetto di occorrenze accodate per job a ogni passata.
	MaxPerJob int
}

// Stats è il resoconto di una passata. Serve alle metriche (R7) e a decidere se
// c'è ancora arretrato da smaltire.
type Stats struct {
	// Jobs sono i job dovuti esaminati.
	Jobs int
	// Seeded sono i job che hanno ricevuto la loro prima occorrenza.
	Seeded int
	// Enqueued sono le occorrenze nuove consegnate al dispatch.
	Enqueued int
	// Recovered sono le occorrenze riprese perché rimaste in sospeso.
	Recovered int
	// Conflicts sono le occorrenze che qualcun altro aveva già preso: il
	// conflitto sulla chiave primaria che è la prova di R4 al lavoro.
	Conflicts int
	// Rejected sono le occorrenze che il dispatch ha rifiutato. Le loro righe
	// restano `pending` e il recupero le riprende.
	Rejected int
	// Dropped sono le occorrenze scartate perché fuori dalla finestra di
	// recupero, ed Expired quelle già accodate e chiuse come `skipped` per lo
	// stesso motivo.
	Dropped int
	Expired int
	// Invalid sono i job la cui schedulazione non è interpretabile.
	Invalid int
	// Late sono le occorrenze consegnate oltre la tolleranza dichiarata, e
	// MaxLag il ritardo massimo osservato: le due grandezze di R47.
	Late   int
	MaxLag time.Duration
	// Backlog è vero quando la passata ha lasciato indietro del lavoro già
	// dovuto: [Engine.Run] riparte subito invece di aspettare il proprio turno.
	Backlog bool
	// Duration è quanto è durata la passata.
	Duration time.Duration
}

// Options configura il motore. Pool e Dispatcher sono obbligatori; tutto il
// resto ha un default.
type Options struct {
	Pool       *pgxpool.Pool
	Dispatcher Dispatcher
	Observer   Observer
	Logger     *slog.Logger

	// Now sostituisce l'orologio. Serve ai test, che devono poter mettere un
	// job in ritardo di tre ore senza aspettarle.
	Now func() time.Time

	Interval  time.Duration
	Tolerance time.Duration
	CatchUp   time.Duration
	MaxPerJob int

	Batch          int
	ReclaimAfter   time.Duration
	ReclaimHorizon time.Duration
	QuarantineFor  time.Duration
}

// Engine è lo scheduler. Va costruito con [New].
type Engine struct {
	pool     *pgxpool.Pool
	dispatch Dispatcher
	obs      Observer
	log      *slog.Logger
	now      func() time.Time

	policy         Policy
	interval       time.Duration
	tolerance      time.Duration
	batch          int
	reclaimAfter   time.Duration
	reclaimHorizon time.Duration
	quarantineFor  time.Duration

	mu sync.Mutex
	// quarantine tiene da parte i job con una schedulazione illeggibile, in
	// memoria e non sul database: è uno stato del processo, non un fatto del
	// job, e un riavvio deve rimetterlo in discussione.
	quarantine map[string]time.Time
	// partitionDay è l'ultimo giorno per cui le partizioni sono state
	// preparate, in forma AAAAMMGG.
	partitionDay string
}

// New costruisce il motore.
func New(opts Options) (*Engine, error) {
	if opts.Pool == nil {
		return nil, errors.New("scheduler: Pool è obbligatorio")
	}
	if opts.Dispatcher == nil {
		return nil, errors.New("scheduler: Dispatcher è obbligatorio; il worker pool è la issue #389")
	}

	e := &Engine{
		pool:     opts.Pool,
		dispatch: opts.Dispatcher,
		obs:      opts.Observer,
		log:      opts.Logger,
		now:      opts.Now,
		policy: Policy{
			CatchUp:   durationOr(opts.CatchUp, DefaultCatchUp),
			MaxPerJob: intOr(opts.MaxPerJob, DefaultMaxPerJob),
		},
		interval:       durationOr(opts.Interval, DefaultInterval),
		tolerance:      durationOr(opts.Tolerance, DefaultTolerance),
		batch:          intOr(opts.Batch, DefaultBatch),
		reclaimAfter:   durationOr(opts.ReclaimAfter, DefaultReclaimAfter),
		reclaimHorizon: durationOr(opts.ReclaimHorizon, DefaultReclaimHorizon),
		quarantineFor:  durationOr(opts.QuarantineFor, DefaultQuarantineFor),
		quarantine:     map[string]time.Time{},
	}
	if e.obs == nil {
		e.obs = nopObserver{}
	}
	if e.log == nil {
		e.log = slog.Default()
	}
	if e.now == nil {
		e.now = time.Now
	}
	if e.policy.CatchUp <= 0 {
		return nil, fmt.Errorf("scheduler: CatchUp (%s) dev'essere positivo", e.policy.CatchUp)
	}
	if e.reclaimHorizon < e.policy.CatchUp {
		// Un orizzonte più corto della finestra di recupero renderebbe
		// irrecuperabili occorrenze che la finestra dichiara ancora buone.
		return nil, fmt.Errorf("scheduler: ReclaimHorizon (%s) più corto di CatchUp (%s)",
			e.reclaimHorizon, e.policy.CatchUp)
	}
	return e, nil
}

func durationOr(v, fallback time.Duration) time.Duration {
	if v == 0 {
		return fallback
	}
	return v
}

func intOr(v, fallback int) int {
	if v <= 0 {
		return fallback
	}
	return v
}

// Run fa girare il motore finché il contesto non viene chiuso.
//
// La prima cosa che fa è [Engine.Recover]: se il processo precedente è morto
// dopo aver preso delle occorrenze ma prima di consegnarle, quelle occorrenze
// devono ripartire *subito*, non dopo il tempo di abbandono.
//
// Un errore di una passata non ferma il motore. PostgreSQL sta sulla stessa
// macchina (SPEC §2) ma può comunque essere in riavvio o momentaneamente
// irraggiungibile, e un motore che esce al primo errore trasforma un intoppo di
// due secondi in un fermo che dura finché qualcuno non se ne accorge. L'errore
// si registra e si riprova alla passata successiva.
func (e *Engine) Run(ctx context.Context) error {
	if err := e.Recover(ctx); err != nil {
		// Nemmeno questo è fatale: le occorrenze in sospeso verranno riprese
		// dalle passate ordinarie, solo un po' più tardi.
		e.log.Error("scheduler: recupero iniziale fallito", slog.Any("err", err))
	}

	ticker := time.NewTicker(e.interval)
	defer ticker.Stop()

	for {
		st, err := e.Tick(ctx)
		switch {
		case ctx.Err() != nil:
			return ctx.Err()
		case err != nil:
			e.log.Error("scheduler: passata fallita", slog.Any("err", err))
		case st.Backlog:
			// C'è ancora arretrato: ripartire subito invece di aspettare il
			// turno è ciò che fa smaltire un fermo in secondi invece che in
			// minuti. Ogni giro fa comunque lavoro vero, quindi non è un ciclo
			// a vuoto.
			continue
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

// Tick è una singola passata: chiude le occorrenze scadute, riprende quelle
// rimaste in sospeso, dà una prima occorrenza ai job che non ce l'hanno e accoda
// quelle dovute.
//
// L'ordine non è casuale. Chiudere le scadute prima di recuperare evita di
// consegnare al dispatch qualcosa che di lì a poco verrebbe dichiarato troppo
// vecchio; dare la prima occorrenza prima di accodare fa sì che un job appena
// creato entri nel giro dalla passata successiva, non da quella dopo ancora.
func (e *Engine) Tick(ctx context.Context) (Stats, error) {
	var st Stats
	started := e.now()
	now := started.UTC()

	defer func() {
		st.Duration = e.now().Sub(started)
		e.obs.Tick(st)
	}()

	if err := e.ensurePartitions(ctx, now); err != nil {
		return st, err
	}
	if err := e.expire(ctx, now, &st); err != nil {
		return st, err
	}
	if err := e.reclaim(ctx, now, now.Add(-e.reclaimAfter), &st); err != nil {
		return st, err
	}
	if err := e.seed(ctx, now, &st); err != nil {
		return st, err
	}
	if err := e.claim(ctx, now, &st); err != nil {
		return st, err
	}
	return st, nil
}

// Recover riconsegna al dispatch le occorrenze prese da un processo che non è
// arrivato a consegnarle.
//
// Il taglio è l'istante della chiamata: si riprende ciò che è stato *creato
// prima*, cioè da qualcun altro o da una vita precedente di questo processo. È
// la ragione per cui la finestra di abbandono qui non si applica — dopo un
// riavvio non c'è niente da aspettare, quelle righe sono ferme per certo.
//
// Chiamarla su due processi che partono insieme può consegnare la stessa
// occorrenza a entrambi: è il caso previsto dalla terza clausola di
// [Dispatcher], dove l'aggiornamento condizionato dell'esecutore fa da arbitro.
func (e *Engine) Recover(ctx context.Context) error {
	var st Stats
	now := e.now().UTC()
	if err := e.reclaim(ctx, now, now, &st); err != nil {
		return err
	}
	if st.Recovered > 0 {
		e.log.Info("scheduler: occorrenze riprese dopo il riavvio", slog.Int("occorrenze", st.Recovered))
	}
	return nil
}

// ---------------------------------------------------------------- partizioni

// ensurePartitions prepara la finestra di partizioni giornaliere di
// `job_executions`, una volta per giorno di calendario.
//
// Senza partizione l'inserimento fallisce, deliberatamente (0006). La
// manutenzione periodica c'è per questo, ma il motore è l'unico che scrive su
// quella tabella e non ha motivo di dipendere da un cron esterno per restare in
// piedi: la chiamata è idempotente e costa un giro di CREATE TABLE IF NOT
// EXISTS.
func (e *Engine) ensurePartitions(ctx context.Context, now time.Time) error {
	day := now.Format("20060102")

	e.mu.Lock()
	done := e.partitionDay == day
	e.mu.Unlock()
	if done {
		return nil
	}

	if err := ensurePartitions(ctx, e.pool); err != nil {
		return err
	}

	e.mu.Lock()
	e.partitionDay = day
	e.mu.Unlock()
	return nil
}

// ------------------------------------------------------------------ scadenza

func (e *Engine) expire(ctx context.Context, now time.Time, st *Stats) error {
	from := now.Add(-e.reclaimHorizon)
	before := now.Add(-e.policy.CatchUp)

	n, err := expireOccurrences(ctx, e.pool, from, before, e.batch)
	if err != nil {
		return err
	}
	st.Expired = n
	if n > 0 {
		e.log.Warn("scheduler: occorrenze chiuse perché fuori dalla finestra di recupero",
			slog.Int("occorrenze", n), slog.String("finestra", e.policy.CatchUp.String()))
	}
	return nil
}

// ------------------------------------------------------------------ recupero

func (e *Engine) reclaim(ctx context.Context, now, createdBefore time.Time, st *Stats) error {
	from := now.Add(-e.reclaimHorizon)
	// Il bordo superiore è la finestra di recupero: più indietro di così
	// l'occorrenza è comunque destinata a essere chiusa da expire.
	to := now
	occs, err := selectPendingOccurrences(ctx, e.pool, from, to, createdBefore, e.batch)
	if err != nil {
		return err
	}
	for _, occ := range occs {
		e.handoff(ctx, occ, st)
	}
	return nil
}

// ------------------------------------------------- prima occorrenza dei job nuovi

func (e *Engine) seed(ctx context.Context, now time.Time, st *Stats) error {
	tx, err := e.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("scheduler: apertura della transazione di avvio: %w", err)
	}
	// Il rollback dopo un commit riuscito è un no-op: è la rete per tutti i
	// ritorni anticipati che stanno più sotto.
	defer func() { _ = tx.Rollback(ctx) }()

	jobs, err := selectUnscheduledJobs(ctx, tx, e.batch)
	if err != nil {
		return err
	}

	ids := make([]string, 0, len(jobs))
	next := make([]*time.Time, 0, len(jobs))
	for _, job := range jobs {
		if e.quarantined(job.ID, now) {
			continue
		}
		sch, err := parseSchedule(job)
		if err != nil {
			e.markInvalid(job, now, err, st)
			continue
		}
		first, ok := firstOccurrence(sch, now)
		if !ok {
			// Un'espressione legittima che non ha occorrenze future (`0 0 30 2 *`).
			// Resta senza prossima occorrenza, ma non va riesaminata a ogni
			// passata: è un fatto stabile quanto un errore di sintassi.
			e.quarantineJob(job.ID, now)
			continue
		}
		ids = append(ids, job.ID)
		next = append(next, &first)
	}

	if err := updateNextRuns(ctx, tx, ids, next); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("scheduler: commit della transazione di avvio: %w", err)
	}
	st.Seeded = len(ids)
	return nil
}

// ------------------------------------------------------------------ accodamento

// claim è il cuore della passata: legge i job dovuti, calcola le occorrenze,
// le inserisce e avanza `next_run_at`.
//
// Inserimento e avanzamento stanno nella stessa transazione, ed è la scelta che
// rende il riavvio innocuo. Se stessero separati ci sarebbe un istante in cui
// `next_run_at` è già avanzato e le righe non ci sono: un processo che muore lì
// perderebbe quelle occorrenze per sempre, senza che nulla ne conservi traccia.
// Insieme, o si vede tutto o non si vede niente — e «niente» significa
// semplicemente che la passata successiva rifà lo stesso calcolo.
//
// Il dispatch invece sta **fuori** dalla transazione: chiamare codice altrui
// tenendo aperta una transazione significa tenere occupata una connessione per
// tutto il tempo che quel codice decide di prendersi.
func (e *Engine) claim(ctx context.Context, now time.Time, st *Stats) error {
	tx, err := e.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("scheduler: apertura della transazione di accodamento: %w", err)
	}
	// Il rollback dopo un commit riuscito è un no-op: è la rete per tutti i
	// ritorni anticipati che stanno più sotto.
	defer func() { _ = tx.Rollback(ctx) }()

	due, err := selectDueJobs(ctx, tx, now, e.batch)
	if err != nil {
		return err
	}
	st.Jobs = len(due)
	// Un lotto pieno vuol dire che di job dovuti ce n'erano almeno tanti quanti
	// se ne potevano prendere: quasi certamente ce ne sono altri in attesa.
	st.Backlog = len(due) == e.batch

	var (
		claimed []Occurrence
		ids     = make([]string, 0, len(due))
		nexts   = make([]*time.Time, 0, len(due))
		drops   []Dropped
	)

	for _, item := range due {
		sch, err := parseSchedule(item.Job)
		if err != nil {
			// Il job esce dall'indice del dispatch: lasciarcelo significherebbe
			// riesaminarlo — e rifallire — a ogni passata, occupando lo spazio
			// dei job sani. Lo riprende [Engine.seed] a quarantena scaduta.
			e.markInvalid(item.Job, now, err, st)
			ids = append(ids, item.Job.ID)
			nexts = append(nexts, nil)
			continue
		}

		p := planOccurrences(sch, item.Cursor, now, e.policy)

		if p.droppedCount > 0 {
			st.Dropped += p.droppedCount
			drops = append(drops, Dropped{
				Job:       item.Job,
				From:      p.droppedFrom,
				To:        p.droppedTo,
				Count:     p.droppedCount,
				Truncated: p.droppedTruncated,
			})
		}
		if p.capped {
			st.Backlog = true
		}

		occs := expand(item.Job, p.due)
		inserted, conflicts, err := e.insert(ctx, tx, occs)
		if err != nil {
			return err
		}
		claimed = append(claimed, inserted...)
		st.Conflicts += conflicts

		ids = append(ids, item.Job.ID)
		if p.hasNext {
			next := p.next
			nexts = append(nexts, &next)
		} else {
			nexts = append(nexts, nil)
		}
	}

	if err := updateNextRuns(ctx, tx, ids, nexts); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("scheduler: commit della transazione di accodamento: %w", err)
	}

	// Da qui in poi le occorrenze esistono su database: qualunque cosa succeda,
	// il recupero le ritrova.
	for _, d := range drops {
		e.log.Warn("scheduler: occorrenze scartate perché fuori dalla finestra di recupero",
			slog.String("job", d.Job.Name), slog.Int("occorrenze", d.Count),
			slog.Bool("troncato", d.Truncated), slog.Time("da", d.From), slog.Time("a", d.To))
		e.obs.Dropped(d)
	}
	for _, occ := range claimed {
		e.handoff(ctx, occ, st)
	}
	return nil
}

// expand moltiplica le occorrenze per gli ambienti del job: un job in due
// ambienti produce due esecuzioni per occorrenza, con esito e alert separati
// (R23).
func expand(job Job, instants []time.Time) []Occurrence {
	if len(instants) == 0 || len(job.Environments) == 0 {
		return nil
	}
	out := make([]Occurrence, 0, len(instants)*len(job.Environments))
	for _, at := range instants {
		for _, env := range job.Environments {
			out = append(out, Occurrence{
				Job:          job,
				ScheduledFor: at,
				Environment:  env,
				Attempt:      1,
			})
		}
	}
	return out
}

// insert scrive le occorrenze e restituisce quelle di cui questo motore ha
// vinto l'inserimento.
//
// Due percorsi, e la differenza è solo di costo. Il **percorso veloce** inserisce
// l'intero lotto con un solo statement: è il caso normale, perché due motori non
// lavorano mai sugli stessi job insieme — il `FOR UPDATE SKIP LOCKED` della
// query calda li tiene separati. Il **percorso lento** si apre solo quando quel
// singolo statement trova un conflitto, e reinserisce una per una per sapere
// *quali* occorrenze erano già prese: senza, un conflitto su una sola occorrenza
// farebbe scartare tutto il lotto.
//
// Ogni inserimento sta dentro un savepoint perché in PostgreSQL un errore
// abortisce la transazione: senza, il primo conflitto porterebbe con sé anche il
// lavoro dei job già processati in questa passata.
func (e *Engine) insert(ctx context.Context, tx pgx.Tx, occs []Occurrence) ([]Occurrence, int, error) {
	if len(occs) == 0 {
		return nil, 0, nil
	}

	sp, err := tx.Begin(ctx)
	if err != nil {
		return nil, 0, fmt.Errorf("scheduler: savepoint: %w", err)
	}
	err = insertOccurrenceBatch(ctx, sp, occs)
	switch {
	case err == nil:
		if err := sp.Commit(ctx); err != nil {
			return nil, 0, fmt.Errorf("scheduler: inserimento delle occorrenze: %w", err)
		}
		return occs, 0, nil
	case isUniqueViolation(err):
		// Qualcuna di queste occorrenze c'era già. Quale, lo dice solo il
		// percorso lento.
		if err := sp.Rollback(ctx); err != nil {
			return nil, 0, fmt.Errorf("scheduler: rollback al savepoint: %w", err)
		}
	default:
		_ = sp.Rollback(ctx)
		// Se manca una partizione è quella dell'occorrenza più recente: le
		// vecchie ce l'hanno per definizione, ci hanno già scritto.
		return nil, 0, fmt.Errorf("scheduler: inserimento delle occorrenze: %w",
			missingPartitionError(err, occs[len(occs)-1].ScheduledFor))
	}

	var (
		inserted  []Occurrence
		conflicts int
	)
	for _, occ := range occs {
		sp, err := tx.Begin(ctx)
		if err != nil {
			return nil, 0, fmt.Errorf("scheduler: savepoint: %w", err)
		}
		err = insertOccurrence(ctx, sp, occ)
		switch {
		case err == nil:
			if err := sp.Commit(ctx); err != nil {
				return nil, 0, fmt.Errorf("scheduler: inserimento di %s: %w", occ, err)
			}
			inserted = append(inserted, occ)
		case isUniqueViolation(err):
			// L'occorrenza esiste già: l'ha presa qualcun altro, o l'abbiamo
			// presa noi in una vita precedente. In entrambi i casi non è nostra
			// e non va dispatchata — se è rimasta in sospeso la riprende il
			// recupero, che è l'unico posto in cui quella decisione si prende.
			if err := sp.Rollback(ctx); err != nil {
				return nil, 0, fmt.Errorf("scheduler: rollback al savepoint: %w", err)
			}
			conflicts++
		default:
			_ = sp.Rollback(ctx)
			return nil, 0, fmt.Errorf("scheduler: inserimento di %s: %w", occ,
				missingPartitionError(err, occ.ScheduledFor))
		}
	}
	return inserted, conflicts, nil
}

// handoff consegna un'occorrenza al dispatch e ne misura il ritardo (R47).
func (e *Engine) handoff(ctx context.Context, occ Occurrence, st *Stats) {
	occ.EnqueuedAt = e.now()

	// Il ritardo di un'occorrenza ripresa non dice niente sulla precisione
	// dello scheduler — dice per quanto il processo è stato fermo — e mescolarlo
	// alle altre falserebbe la misura che R47 chiede.
	if !occ.Recovered {
		if lag := occ.Lag(); lag > st.MaxLag {
			st.MaxLag = lag
		}
		if occ.Lag() > e.tolerance {
			st.Late++
		}
	}

	e.obs.Enqueued(occ)

	if err := e.dispatch.Dispatch(ctx, occ); err != nil {
		// La riga resta `pending`: il recupero la riproporrà. È il motivo per
		// cui un dispatch che non può accettare deve restituire un errore
		// invece di far finta di niente.
		st.Rejected++
		e.log.Error("scheduler: occorrenza rifiutata dal dispatch",
			slog.String("occorrenza", occ.String()), slog.Any("err", err))
		return
	}

	if occ.Recovered {
		st.Recovered++
	} else {
		st.Enqueued++
	}
}

// --------------------------------------------------------------- schedulazione

// parseSchedule costruisce la schedulazione del job. Il vincolo XOR fra le due
// modalità è già garantito dal database (`jobs_schedule_xor_every_check`), ma
// [schedule.Parse] lo riapplica: è la stessa regola letta dalla stessa parte,
// non un secondo controllo che può divergere.
func parseSchedule(job Job) (schedule.Schedule, error) {
	return schedule.Parse(schedule.Spec{
		Expression: job.Expression,
		Every:      job.Every,
		Timezone:   job.Timezone,
	})
}

// markInvalid registra un job la cui schedulazione non si riesce a interpretare.
//
// È un'anomalia, non un caso d'uso: il database garantisce la forma
// dell'espressione e chi scrive il job ne valida la semantica. Se arriva qui,
// qualcosa è passato da una porta di servizio — e il motore non deve né
// fermarsi né riprovare all'infinito.
func (e *Engine) markInvalid(job Job, now time.Time, cause error, st *Stats) {
	st.Invalid++
	e.quarantineJob(job.ID, now)
	e.log.Error("scheduler: schedulazione illeggibile",
		slog.String("job", job.Name), slog.String("job_id", job.ID), slog.Any("err", cause))
}

func (e *Engine) quarantineJob(id string, now time.Time) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.quarantine[id] = now.Add(e.quarantineFor)
}

func (e *Engine) quarantined(id string, now time.Time) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	until, ok := e.quarantine[id]
	if !ok {
		return false
	}
	if now.Before(until) {
		return true
	}
	delete(e.quarantine, id)
	return false
}
