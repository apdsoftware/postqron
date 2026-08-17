package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/apdsoftware/postqron/services/api/internal/dispatch"
	"github.com/apdsoftware/postqron/services/api/internal/httpexec"
	"github.com/apdsoftware/postqron/services/api/internal/jobs"
	"github.com/apdsoftware/postqron/services/api/internal/scheduler"
)

// Il motore cron dentro il processo dell'API (issue #486).
//
// # Perché sta qui e non in un package
//
// I pezzi esistono tutti e sono verificati singolarmente: internal/schedule sa
// *quando*, internal/scheduler calcola le occorrenze dovute e le accoda,
// internal/dispatch le esegue con un worker pool isolato per job,
// internal/httpexec fa la chiamata, internal/netguard decide dove non si può
// andare. Quello che mancava è il punto in cui si conoscono — e quel punto è
// `main`, deliberatamente, perché nessuno di loro deve conoscere gli altri più
// di quanto già faccia.
//
// Il caso che lo rende evidente è il trigger manuale. `jobs.Dispatcher`
// (issue #395) parla di [jobs.Execution]; `scheduler.Dispatcher` (issue #388)
// parla di [scheduler.Occurrence]. **Non combaciano ed è corretto**: il primo è
// il confine a monte, dall'API verso chi esegue; il secondo è quello a valle,
// dal motore verso chi esegue. Unificarli per simmetria significherebbe far
// importare internal/scheduler a internal/jobs, cioè legare l'API al motore per
// risparmiare l'adattatore che sta qui sotto — vedi [manualDispatcher].

// targetChecker anticipa il rifiuto di una destinazione non pubblica (R38).
// L'implementazione d'esercizio è `*netguard.Guard`.
type targetChecker interface {
	CheckTarget(ctx context.Context, target *url.URL) error
}

// engineOptions sono le dipendenze del motore.
type engineOptions struct {
	// Pool è la connessione a PostgreSQL, la stessa dell'API: motore, API e
	// database stanno sulla stessa macchina (SPEC §2).
	Pool   *pgxpool.Pool
	Logger *slog.Logger

	// Clients è la sorgente del client HTTP dell'esecutore, ed è obbligatoria:
	// in esercizio è il guard di netguard, l'unico client protetto (R38). Vedi
	// la documentazione di internal/httpexec.
	Clients httpexec.Guard
	// Targets è il controllo sulla destinazione, ed è obbligatorio per la stessa
	// ragione per cui non è opzionale in jobs.NewService: un controllo di
	// sicurezza che si disattiva dimenticando una riga di composizione è un
	// controllo che prima o poi qualcuno dimentica.
	Targets targetChecker
}

// engine tiene insieme scheduler e worker pool per il ciclo di vita del
// processo.
type engine struct {
	workers *dispatch.Pool
	sched   *scheduler.Engine
	manual  *manualDispatcher
	log     *slog.Logger

	// stopped si chiude quando il ciclo dello scheduler è uscito.
	stopped chan struct{}
}

// newEngine costruisce il motore. Non avvia niente: vedi [engine.Start].
func newEngine(opts engineOptions) (*engine, error) {
	if opts.Pool == nil {
		return nil, errors.New("motore: il pool di connessioni è obbligatorio")
	}
	if opts.Clients == nil {
		return nil, errors.New("motore: la sorgente del client HTTP è obbligatoria (R38)")
	}
	if opts.Targets == nil {
		return nil, errors.New("motore: il controllo sulla destinazione è obbligatorio (R38)")
	}
	log := opts.Logger
	if log == nil {
		log = slog.Default()
	}

	executor, err := httpexec.New(httpexec.Options{Guard: opts.Clients, Logger: log})
	if err != nil {
		return nil, err
	}

	workers, err := dispatch.New(dispatch.Options{
		Store:    dispatch.NewPostgresStore(opts.Pool),
		Executor: executor,
		Guard:    targetGuard{check: opts.Targets},
		Logger:   log,
	})
	if err != nil {
		return nil, err
	}

	sched, err := scheduler.New(scheduler.Options{
		Pool:       opts.Pool,
		Dispatcher: workers,
		Logger:     log,
	})
	if err != nil {
		return nil, err
	}

	return &engine{
		workers: workers,
		sched:   sched,
		manual:  &manualDispatcher{pool: opts.Pool, sink: workers, now: time.Now},
		log:     log,
		stopped: make(chan struct{}),
	}, nil
}

// Manual è l'adattatore da passare a [jobs.Options.Dispatcher].
func (e *engine) Manual() jobs.Dispatcher { return e.manual }

// Start avvia il motore e torna subito.
//
// Il contesto è quello del processo: alla sua chiusura lo scheduler esce dal
// proprio ciclo. Il worker pool **non** si ferma da lì — l'arresto ordinato ha
// bisogno di un contesto ancora vivo per drenare le esecuzioni in volo, ed è
// [engine.Shutdown] a dargliene uno.
func (e *engine) Start(ctx context.Context) {
	e.workers.Start()
	go func() {
		defer close(e.stopped)
		if err := e.sched.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
			e.log.Error("motore: lo scheduler si è fermato", slog.Any("error", err))
		}
	}()
	e.log.Info("motore avviato",
		slog.Duration("passata", scheduler.DefaultInterval),
		slog.Duration("tolleranza_dispatch", scheduler.DefaultTolerance),
		slog.Int("worker", dispatch.DefaultWorkers))
}

// Shutdown ferma il motore: prima smette di accodare, poi drena ciò che è in
// volo.
//
// L'ordine è l'unico che non produce lavoro perso. Fermare prima i worker
// lascerebbe lo scheduler ad accodare occorrenze a un pool che le rifiuta: le
// righe resterebbero `pending` — verrebbero riprese al riavvio, quindi nulla di
// grave — ma ogni rifiuto passerebbe dai log come un errore, e un arresto pulito
// che si stampa come un guasto è un arresto che nessuno legge più.
//
// Ciò che resta in volo oltre il drenaggio viene rilasciato a `pending` dal pool
// e ripreso dal recupero dello scheduler al riavvio: l'errore che [dispatch.Pool.Shutdown]
// restituisce in quel caso non è un guasto, è la traccia da lasciare nei log.
func (e *engine) Shutdown(ctx context.Context) error {
	select {
	case <-e.stopped:
	case <-ctx.Done():
		e.log.Warn("motore: lo scheduler non si è fermato entro il tempo dell'arresto")
	}
	if err := e.workers.Shutdown(ctx); err != nil {
		return err
	}
	e.log.Info("motore arrestato")
	return nil
}

// ------------------------------------------------------- guard sulla destinazione

// targetGuard adatta il guard di netguard a [dispatch.Guard].
//
// # Cosa aggiunge, e cosa no
//
// **Non è il blocco.** Il blocco è nel `DialContext` del client che l'esecutore
// usa, si ripete su ogni connessione e quindi su ogni redirect, e vale
// nell'istante in cui la connessione si apre. Questo controllo sta prima, ed è
// l'anticipo: un bersaglio che sappiamo già di non voler chiamare non arriva a
// consumare un worker né una connessione in uscita, e l'esecuzione si chiude
// come `failed` con scritto il motivo invece che con un errore di rete.
//
// Il costo dichiarato è una risoluzione DNS in più per occorrenza, perché il
// dial ne farà comunque una propria — quella è la sola che conta, ed è per
// questo che non si può riusare il risultato di questa. Il beneficio è che le
// destinazioni rifiutate finiscono in [dispatch.Stats.Blocked], cioè diventano
// una grandezza osservabile (R7) invece che un errore di rete indistinguibile
// da un bersaglio spento.
type targetGuard struct {
	check targetChecker
}

var _ dispatch.Guard = targetGuard{}

func (g targetGuard) Allow(ctx context.Context, occ scheduler.Occurrence) error {
	target, err := url.Parse(occ.Job.URL)
	if err != nil {
		// Il valore non si ripete nell'errore: finisce nella colonna `error`, che
		// l'utente rilegge dall'API, e da #458 in poi un URL può contenere segreti
		// del workspace risolti in esecuzione (R43).
		return errors.New("URL del job illeggibile")
	}
	return g.check.CheckTarget(ctx, target)
}

// ---------------------------------------------------------- trigger manuale

// manualDispatcher collega il trigger manuale dell'API (R8) all'esecuzione.
//
// È l'adattatore fra i due confini che non combaciano: prende la
// [jobs.Execution] che l'API ha appena registrato e la consegna al worker pool
// nella forma che il motore usa, la [scheduler.Occurrence].
//
// # Perché serve una lettura del database
//
// La riga di `job_executions` identifica l'occorrenza — la quaterna naturale
// `(job_id, scheduled_for, environment, attempt)` — ma non dice **cosa
// chiamare**: URL, metodo, header, corpo e timeout stanno su `jobs`. Lo
// scheduler li ha già in mano perché li ha letti nella stessa passata che ha
// deciso che il job era dovuto; qui l'occorrenza nasce da una richiesta HTTP,
// e il bersaglio va letto adesso.
//
// Leggerlo adesso è anche la cosa giusta nel merito: fra la creazione della
// riga e la sua esecuzione il job può essere stato modificato, e ciò che va
// eseguito è il bersaglio corrente — lo stesso che il motore userebbe per
// un'occorrenza schedulata nello stesso istante.
//
// # Chi possiede la riga
//
// Questa, e nessun altro. Lo scheduler filtra `triggered_by = 'schedule'` sia
// nel recupero sia nella scadenza, per una scelta dichiarata: riproporre un
// «esegui adesso» mezz'ora dopo un riavvio sarebbe la cosa sbagliata. La
// conseguenza è che un trigger manuale rifiutato dalla coda **non viene ripreso
// da nessuno**: la sua riga resta `pending`, l'API lo scrive nei log e risponde
// comunque 202, perché ciò che ha promesso è la registrazione. Ed è giusto
// che il confine si veda.
type manualDispatcher struct {
	pool *pgxpool.Pool
	sink scheduler.Dispatcher
	now  func() time.Time
}

var _ jobs.Dispatcher = (*manualDispatcher)(nil)

// loadTimeout è il tetto della lettura del bersaglio. È una query su chiave
// primaria: se non torna in cinque secondi il database ha un problema che non si
// risolve aspettando ancora. Coincide con [dispatch.DefaultStoreTimeout], che
// misura la stessa cosa.
const loadTimeout = 5 * time.Second

// Dispatch implementa [jobs.Dispatcher]. Non blocca: la lettura del bersaglio è
// una query su chiave primaria, e la consegna al pool è un accodamento.
func (d *manualDispatcher) Dispatch(ctx context.Context, exec jobs.Execution) error {
	// Il contesto della richiesta HTTP viene lasciato indietro. La riga esiste
	// già — l'API l'ha appena creata — e da questo momento **nessun altro la
	// riprenderà**: un client che chiude la connessione un istante dopo aver
	// ricevuto il 202 lascerebbe altrimenti un'occorrenza `pending` per sempre.
	// I valori del contesto restano, così il log conserva la richiesta d'origine.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), loadTimeout)
	defer cancel()

	job, err := loadJobTarget(ctx, d.pool, exec.JobID)
	if err != nil {
		return err
	}
	return d.sink.Dispatch(ctx, scheduler.Occurrence{
		Job: job,
		// La chiave naturale va ricopiata esatta: è su di essa che si regge
		// l'aggiornamento condizionato con cui il pool prende la riga (R4).
		ScheduledFor: exec.ScheduledFor,
		Environment:  string(exec.Environment),
		Attempt:      int16(exec.Attempt),
		EnqueuedAt:   d.now(),
	})
}

// jobTargetSQL legge il bersaglio di un job.
//
// Le colonne e le conversioni sono quelle che internal/scheduler usa per la
// propria lettura, e la duplicazione è voluta: esportare quella query
// significherebbe rendere pubblica una parte interna del motore per l'unico
// beneficio di risparmiare quindici righe qui. Il disallineamento possibile è
// una colonna aggiunta a `jobs` e usata dall'esecutore: la prova end-to-end del
// trigger manuale la scoprirebbe, perché passa dall'esecutore vero.
const jobTargetSQL = `
	SELECT id::text, user_id::text, name,
	       coalesce(schedule, ''), coalesce(every_seconds, 0), timezone,
	       environments::text[], url, method::text, headers, body,
	       timeout_seconds, max_retries, retry_backoff::text, enabled
	  FROM jobs
	 WHERE id = $1::uuid`

func loadJobTarget(ctx context.Context, pool *pgxpool.Pool, jobID string) (scheduler.Job, error) {
	var (
		job     scheduler.Job
		every   int32
		timeout int32
		retries int16
	)
	err := pool.QueryRow(ctx, jobTargetSQL, jobID).Scan(
		&job.ID, &job.UserID, &job.Name,
		&job.Expression, &every, &job.Timezone,
		&job.Environments, &job.URL, &job.Method, &job.Headers, &job.Body,
		&timeout, &retries, &job.RetryBackoff, &job.Enabled,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		// Il job è stato cancellato fra la registrazione della riga e la sua
		// consegna. La riga se n'è andata con lui (`ON DELETE CASCADE`, 0006):
		// non c'è niente da eseguire e niente da chiudere.
		return scheduler.Job{}, fmt.Errorf("motore: job %s non trovato", jobID)
	}
	if err != nil {
		return scheduler.Job{}, fmt.Errorf("motore: lettura del bersaglio del job %s: %w", jobID, err)
	}

	job.Every = time.Duration(every) * time.Second
	job.Timeout = time.Duration(timeout) * time.Second
	job.MaxRetries = int(retries)
	return job, nil
}
