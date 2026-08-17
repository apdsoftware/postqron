package dispatch

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/apdsoftware/postqron/services/api/internal/scheduler"
	"github.com/apdsoftware/postqron/services/api/internal/secrets"
)

// Options configura il pool. Store ed Executor sono obbligatori; tutto il resto
// ha un default motivato, e i default stanno fra le costanti di dispatch.go.
type Options struct {
	Store    Store
	Executor Executor
	// Guard è il controllo sugli indirizzi di destinazione (R38, issue #455).
	// Se manca non blocca niente: vedi [Guard].
	Guard  Guard
	Logger *slog.Logger

	// Workers è quanti worker girano insieme.
	Workers int
	// MaxInFlightPerJob è quante occorrenze dello stesso job possono essere in
	// esecuzione insieme. È il tetto che rende l'isolamento una garanzia.
	MaxInFlightPerJob int
	// QueueDepth e QueueDepthPerJob sono i due tetti della coda in attesa.
	QueueDepth       int
	QueueDepthPerJob int

	// DrainTimeout è quanto l'arresto aspetta le esecuzioni in volo prima di
	// rilasciarle.
	DrainTimeout time.Duration
	// StoreTimeout è il tetto di una singola transizione di stato.
	StoreTimeout time.Duration
	// MaxTimeout è il tetto di durata di un'esecuzione, quale che sia il timeout
	// del job (R40).
	MaxTimeout time.Duration
}

// Pool è il worker pool. Va costruito con [New], avviato con [Pool.Start] e
// chiuso con [Pool.Shutdown].
//
// Implementa [scheduler.Dispatcher]: è il valore da passare a
// [scheduler.Options.Dispatcher].
type Pool struct {
	store Store
	exec  Executor
	guard Guard
	log   *slog.Logger

	workers      int
	drainTimeout time.Duration
	storeTimeout time.Duration
	maxTimeout   time.Duration

	q *queue

	// ctx è la vita delle esecuzioni: viene annullato solo quando il drenaggio è
	// scaduto, ed è il segnale con cui gli esecutori sanno di dover mollare.
	ctx    context.Context
	cancel context.CancelFunc
	// stopping distingue l'annullamento dell'arresto da un errore qualunque:
	// un'esecuzione interrotta perché il processo sta uscendo va **rilasciata**,
	// non scritta come fallita.
	stopping atomic.Bool

	wg        sync.WaitGroup
	startOnce sync.Once
	stopOnce  sync.Once
	stopErr   error

	// mu protegge live, che sono le occorrenze prese e non ancora chiuse. È
	// l'elenco che l'arresto rilascia quando il drenaggio non basta.
	mu   sync.Mutex
	live map[string]scheduler.Occurrence

	counters counters
}

// counters sono i contatori cumulativi, in atomiche perché li scrivono tutti i
// worker e li legge chiunque chieda [Pool.Stats].
type counters struct {
	accepted, duplicated, refused atomic.Int64
	claimed, lost                 atomic.Int64
	succeeded, failed, timedOut   atomic.Int64
	skipped, blocked              atomic.Int64
	released, errs                atomic.Int64
}

// Il pool è il dispatch che lo scheduler si aspettava a valle.
var _ scheduler.Dispatcher = (*Pool)(nil)

// New costruisce il pool.
func New(opts Options) (*Pool, error) {
	if opts.Store == nil {
		return nil, errors.New("dispatch: Store è obbligatorio")
	}
	if opts.Executor == nil {
		return nil, errors.New("dispatch: Executor è obbligatorio; l'esecutore HTTP è la issue #390")
	}

	p := &Pool{
		store:        opts.Store,
		exec:         opts.Executor,
		guard:        opts.Guard,
		log:          opts.Logger,
		workers:      intOr(opts.Workers, DefaultWorkers),
		drainTimeout: durationOr(opts.DrainTimeout, DefaultDrainTimeout),
		storeTimeout: durationOr(opts.StoreTimeout, DefaultStoreTimeout),
		maxTimeout:   durationOr(opts.MaxTimeout, DefaultMaxTimeout),
		live:         map[string]scheduler.Occurrence{},
	}
	if p.guard == nil {
		p.guard = openGuard{}
	}
	if p.log == nil {
		p.log = slog.Default()
	}

	// I due tetti per job non possono superare quelli complessivi: sopra di
	// quelli non sarebbero più tetti, e la protezione degli altri job
	// sparirebbe senza che nessuno se ne accorga. Si abbassano invece di
	// rifiutare la configurazione — «al massimo tutto» è una richiesta
	// legittima, e su un pool da due worker il tetto di default vale
	// esattamente questo.
	queued := intOr(opts.QueueDepth, DefaultQueueDepth)
	perJob := min(intOr(opts.QueueDepthPerJob, DefaultQueueDepthPerJob), queued)
	inFlightPerJob := min(intOr(opts.MaxInFlightPerJob, DefaultMaxInFlightPerJob), p.workers)
	p.q = newQueue(queued, perJob, inFlightPerJob)
	p.ctx, p.cancel = context.WithCancel(context.Background())
	return p, nil
}

func durationOr(v, fallback time.Duration) time.Duration {
	if v <= 0 {
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

// Start avvia i worker. Chiamarla due volte non fa niente.
func (p *Pool) Start() {
	p.startOnce.Do(func() {
		p.wg.Add(p.workers)
		for range p.workers {
			go p.work()
		}
		p.log.Info("dispatch: worker pool avviato", slog.Int("worker", p.workers))
	})
}

// Run avvia il pool e lo tiene in piedi finché il contesto non viene chiuso,
// poi lo arresta. È la forma comoda per chi lo fa girare accanto allo
// scheduler.
//
// L'arresto **non** usa il contesto appena chiuso — servirebbe a poco un
// drenaggio con un contesto già scaduto — ma ne conserva i valori.
func (p *Pool) Run(ctx context.Context) error {
	p.Start()
	<-ctx.Done()
	return p.Shutdown(context.WithoutCancel(ctx))
}

// Dispatch implementa [scheduler.Dispatcher]: accoda l'occorrenza e torna
// subito.
//
// Non blocca mai — è la seconda clausola del contratto, e un dispatch che
// aspettasse la fine della chiamata HTTP ritarderebbe ogni occorrenza
// successiva. Il contesto è nella firma perché ce l'ha l'interfaccia; qui non
// c'è niente su cui attendere.
//
// La riofferta di un'occorrenza già in carico (terza clausola: succede per
// costruzione, quando una resta in coda più di
// [scheduler.DefaultReclaimAfter]) non è un errore e non va segnalata come tale:
// viene scartata e contata in [Stats.Duplicated]. Un errore farebbe scrivere
// allo scheduler che il dispatch ha rifiutato del lavoro, che è falso.
func (p *Pool) Dispatch(_ context.Context, occ scheduler.Occurrence) error {
	switch err := p.q.push(occ); {
	case err == nil:
		p.counters.accepted.Add(1)
		return nil
	case errors.Is(err, errDuplicate):
		p.counters.duplicated.Add(1)
		return nil
	default:
		p.counters.refused.Add(1)
		return err
	}
}

// Stats è la fotografia del pool.
func (p *Pool) Stats() Stats {
	queued, inFlight := p.q.depth()
	return Stats{
		Queued:     queued,
		InFlight:   inFlight,
		Accepted:   p.counters.accepted.Load(),
		Duplicated: p.counters.duplicated.Load(),
		Refused:    p.counters.refused.Load(),
		Claimed:    p.counters.claimed.Load(),
		Lost:       p.counters.lost.Load(),
		Succeeded:  p.counters.succeeded.Load(),
		Failed:     p.counters.failed.Load(),
		TimedOut:   p.counters.timedOut.Load(),
		Skipped:    p.counters.skipped.Load(),
		Blocked:    p.counters.blocked.Load(),
		Released:   p.counters.released.Load(),
		Errors:     p.counters.errs.Load(),
	}
}

// ------------------------------------------------------------------- worker

func (p *Pool) work() {
	defer p.wg.Done()
	for {
		occ, ok := p.q.take()
		if !ok {
			return
		}
		p.run(occ)
		p.q.done(occ)
	}
}

// run porta una singola occorrenza dall'attesa a uno stato terminale.
func (p *Pool) run(occ scheduler.Occurrence) {
	// Un panico dell'esecutore o del guard è un bug loro, e senza questa rete
	// sarebbe un fermo del motore: la goroutine morirebbe portandosi dietro il
	// processo, cioè tutti gli altri job. Il worker invece sopravvive,
	// l'occorrenza si chiude come fallita e il motivo resta scritto sulla riga.
	claimed := false
	defer func() {
		r := recover()
		if r == nil {
			return
		}
		p.log.Error("dispatch: esecuzione andata in panico",
			slog.String("occorrenza", occ.String()), slog.Any("panico", r))
		if claimed {
			// Il valore del panico è l'unico testo di questa funzione che non
			// nasce qui, e nessuno può redigerlo: chi è andato in panico non ha
			// restituito niente, tantomeno un redattore. Ci si scrive lo stesso,
			// perché l'alternativa è un'esecuzione fallita senza un motivo visibile
			// — e un panico porta con sé un errore del runtime, non il contenuto
			// della richiesta.
			p.finish(occ, Record{
				Outcome: Failed,
				Error:   ownText(fmt.Sprintf("esecuzione interrotta da un errore interno: %v", r)),
			})
		}
	}()

	// Un job messo in pausa fra l'accodamento e adesso: capita solo alle
	// occorrenze riprese, ed è il caso che [scheduler.Job.Enabled] documenta.
	// L'occorrenza si chiude senza mai partire — che è la definizione di
	// `skipped` (migrazione 0001) — e senza toccare `started_at`.
	if !occ.Job.Enabled {
		p.skip(occ, "job in pausa: occorrenza non eseguita")
		return
	}

	ctx, cancel := p.storeCtx()
	got, err := p.store.Claim(ctx, occ)
	cancel()
	switch {
	case err != nil:
		// La riga resta `pending`: la ritrova il recupero. Non c'è niente da
		// scrivere, e insistere su un database che ha appena rifiutato un UPDATE
		// su chiave primaria non aiuterebbe.
		p.counters.errs.Add(1)
		p.log.Error("dispatch: presa dell'occorrenza fallita",
			slog.String("occorrenza", occ.String()), slog.Any("err", err))
		return
	case !got:
		// Il secondo cancello di R4 ha fatto il suo lavoro: l'occorrenza è di
		// qualcun altro, o lo scheduler l'ha già dichiarata scaduta. In nessuno
		// dei due casi è nostra da eseguire.
		p.counters.lost.Add(1)
		p.log.Debug("dispatch: occorrenza già presa da qualcun altro",
			slog.String("occorrenza", occ.String()))
		return
	}
	claimed = true
	p.counters.claimed.Add(1)

	p.track(occ)
	defer p.untrack(occ)

	// R38: il controllo sugli indirizzi di destinazione (issue #455). Sta dopo
	// la presa perché un rifiuto deve lasciare un'esecuzione visibile con
	// scritto il motivo.
	if err := p.guard.Allow(p.ctx, occ); err != nil {
		p.counters.blocked.Add(1)
		// Il testo è composto qui a partire dall'URL del job **a riposo**, dove i
		// riferimenti ai segreti non sono ancora stati espansi: il guard viene
		// chiamato prima dell'esecuzione, e chi risolve è l'esecutore.
		p.finish(occ, Record{Outcome: Failed, Error: ownText(fmt.Sprintf("destinazione rifiutata: %v", err))})
		return
	}

	res, execErr := p.execute(occ)

	// L'arresto ha annullato l'esecuzione a metà: la riga non va chiusa con un
	// esito che non c'è stato, va rilasciata perché il recupero la ritrovi.
	if execErr != nil && p.stopping.Load() && p.ctx.Err() != nil {
		p.release(occ)
		return
	}
	p.finish(occ, record(res, execErr))
}

// execute chiama l'esecutore con il timeout del job, tagliato al tetto del
// servizio (R40).
func (p *Pool) execute(occ scheduler.Occurrence) (Result, error) {
	timeout := occ.Job.Timeout
	if timeout <= 0 || timeout > p.maxTimeout {
		timeout = p.maxTimeout
	}
	ctx, cancel := context.WithTimeout(p.ctx, timeout)
	defer cancel()
	return p.exec.Execute(ctx, occ)
}

// record traduce ciò che l'esecutore riporta nell'esito da scrivere.
//
// La regola sta qui e non nell'esecutore perché è una regola sugli stati di
// `job_executions`, e chi fa la chiamata HTTP non deve conoscerli.
//
// `err.Error()` non compare in questa funzione, e non è una dimenticanza: è la
// quarta clausola di [Executor]. L'errore dice **quale** esito scrivere, il testo
// da scrivere arriva già redatto in [Result.ErrorText]. Prima della issue #496
// il messaggio finiva dritto nella colonna `error`, che l'utente rilegge
// dall'API — e da quando i segreti vengono risolti in esecuzione quel messaggio
// può contenerli.
func record(res Result, err error) Record {
	rec := Record{ResponseStatus: res.ResponseStatus, ResponseExcerpt: res.ResponseExcerpt}
	switch {
	case err == nil && res.ResponseStatus >= 400:
		rec.Outcome = Failed
		rec.Error = ownText(fmt.Sprintf("risposta HTTP %d", res.ResponseStatus))
	case err == nil:
		rec.Outcome = Succeeded
	case errors.Is(err, context.DeadlineExceeded):
		rec.Outcome = TimedOut
		rec.Error = executorText(res)
	default:
		rec.Outcome = Failed
		rec.Error = executorText(res)
	}
	return rec
}

// executorText è il testo che l'esecutore ha redatto per il proprio errore.
//
// Il ripiego serve al caso in cui un esecutore restituisca un errore senza il
// testo: la riga si chiude lo stesso, con scritto che è andata male e che il
// dettaglio non è arrivato. Non ci finisce `err.Error()` nemmeno lì — un
// esecutore che non rispetta la quarta clausola del contratto è esattamente
// quello di cui non ci si può fidare per la redazione.
func executorText(res Result) secrets.Excerpt {
	if !res.ErrorText.Empty() {
		return res.ErrorText
	}
	return ownText("esecuzione non riuscita: chi l'ha eseguita non ne ha riportato il testo")
}

// ownText è l'estratto di un testo composto da questo package.
//
// Passa da un [secrets.Redactor] vuoto perché [secrets.Excerpt] non è
// costruibile in nessun altro modo, ed è precisamente il punto: non esiste una
// scorciatoia per scrivere del testo grezzo sulla riga, nemmeno qui dentro.
//
// Il redattore vuoto non toglie niente, e qui è la scelta giusta invece che una
// deroga: questi testi nascono dagli stati di questo package e dai campi
// dell'occorrenza **a riposo**, dove i `${VAR}` non sono ancora stati espansi.
// Nessun valore di segreto ci può transitare — chi li conosce è [Executor], e
// per l'appunto redige il proprio.
func ownText(s string) secrets.Excerpt { return secrets.Redactor{}.Excerpt([]byte(s), 0) }

func (p *Pool) finish(occ scheduler.Occurrence, rec Record) {
	ctx, cancel := p.storeCtx()
	defer cancel()
	updated, err := p.store.Finish(ctx, occ, rec)

	// I contatori si muovono **dopo** la scrittura, non prima: chi guarda
	// [Pool.Stats] deve poter dedurre che la riga è già a posto. Contarli prima
	// renderebbe osservabile uno stato che sul database non c'è ancora.
	switch rec.Outcome {
	case Succeeded:
		p.counters.succeeded.Add(1)
	case TimedOut:
		p.counters.timedOut.Add(1)
	default:
		p.counters.failed.Add(1)
	}

	switch {
	case err != nil:
		p.counters.errs.Add(1)
		p.log.Error("dispatch: scrittura dell'esito fallita",
			slog.String("occorrenza", occ.String()),
			slog.String("esito", string(rec.Outcome)), slog.Any("err", err))
	case !updated:
		// La riga non è più `running`: qualcuno l'ha cambiata sotto di noi, di
		// norma il rilascio dell'arresto. L'esecuzione è avvenuta comunque, e
		// dirlo è l'unico modo per spiegare un'eventuale seconda esecuzione.
		p.log.Warn("dispatch: esito non registrato, la riga non era più in esecuzione",
			slog.String("occorrenza", occ.String()), slog.String("esito", string(rec.Outcome)))
	}
}

func (p *Pool) skip(occ scheduler.Occurrence, reason string) {
	ctx, cancel := p.storeCtx()
	defer cancel()
	_, err := p.store.Skip(ctx, occ, reason)
	p.counters.skipped.Add(1)
	if err != nil {
		p.counters.errs.Add(1)
		p.log.Error("dispatch: chiusura dell'occorrenza saltata fallita",
			slog.String("occorrenza", occ.String()), slog.Any("err", err))
	}
}

// release riporta l'occorrenza a `pending`. Vedi la documentazione del package
// per il compromesso che comporta.
func (p *Pool) release(occ scheduler.Occurrence) {
	ctx, cancel := p.storeCtx()
	defer cancel()

	released, err := p.store.Release(ctx, occ)
	switch {
	case err != nil:
		p.counters.errs.Add(1)
		p.log.Error("dispatch: rilascio dell'occorrenza fallito",
			slog.String("occorrenza", occ.String()), slog.Any("err", err))
	case released:
		p.counters.released.Add(1)
		p.log.Warn("dispatch: occorrenza rilasciata dall'arresto, il recupero la riprenderà",
			slog.String("occorrenza", occ.String()))
	}
}

// storeCtx dà a una transizione di stato il proprio tempo, scollegato dalla
// vita del pool.
//
// Lo scollegamento è il punto: la riga va chiusa **anche** mentre il processo
// sta uscendo. Se la scrittura dell'esito ereditasse il contesto annullato
// dall'arresto, ogni esecuzione interrotta resterebbe `running` per sempre —
// cioè esattamente il guasto che l'arresto pulito deve evitare.
func (p *Pool) storeCtx() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(p.ctx), p.storeTimeout)
}

func (p *Pool) track(occ scheduler.Occurrence) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.live[occurrenceKey(occ)] = occ
}

func (p *Pool) untrack(occ scheduler.Occurrence) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.live, occurrenceKey(occ))
}

// ------------------------------------------------------------------ arresto

// Shutdown ferma il pool: smette di accettare lavoro, aspetta le esecuzioni in
// volo fino a [Options.DrainTimeout] e rilascia quelle che non fanno in tempo.
//
// Restituisce un errore se qualcosa è stato rilasciato. Non è un guasto del
// pool — la riga è al sicuro e il recupero la riprenderà — ma è un fatto che
// chi arresta il processo deve poter vedere nei log invece di doverlo dedurre
// dalle esecuzioni che ricompaiono dopo il riavvio.
//
// Chiamarla due volte restituisce lo stesso esito della prima.
func (p *Pool) Shutdown(ctx context.Context) error {
	p.stopOnce.Do(func() { p.stopErr = p.shutdown(ctx) })
	return p.stopErr
}

func (p *Pool) shutdown(ctx context.Context) error {
	p.stopping.Store(true)

	// Da qui [Pool.Dispatch] rifiuta, e i worker in attesa escono. Le occorrenze
	// che erano in coda non sono mai state prese: le loro righe sono `pending` e
	// il recupero le ritrova senza che si scriva niente.
	abandoned := p.q.close()
	if len(abandoned) > 0 {
		p.log.Info("dispatch: occorrenze lasciate in attesa, restano da riprendere",
			slog.Int("occorrenze", len(abandoned)))
	}

	drainCtx, cancelDrain := context.WithTimeout(ctx, p.drainTimeout)
	defer cancelDrain()
	if p.wait(drainCtx) {
		p.cancel()
		p.log.Info("dispatch: worker pool fermato", slog.Int("in_attesa_lasciate", len(abandoned)))
		return nil
	}

	// Il drenaggio non è bastato: si annulla il contesto delle esecuzioni. Un
	// esecutore che lo rispetta torna con un errore e il suo worker rilascia la
	// riga da sé; il secondo giro d'attesa serve proprio a dargliene il tempo.
	p.log.Warn("dispatch: drenaggio scaduto, le esecuzioni in volo vengono interrotte",
		slog.String("attesa", p.drainTimeout.String()))
	p.cancel()

	hardCtx, cancelHard := context.WithTimeout(ctx, p.storeTimeout)
	defer cancelHard()
	p.wait(hardCtx)

	// Ciò che resta è di chi non è tornato affatto: lo rilascia l'arresto. Il
	// rilascio è condizionato su `status = 'running'`, quindi un worker che
	// dovesse chiudere la propria riga un istante dopo non la vede cambiata da
	// qui — la trova già `pending` e lo dice nei log.
	for _, occ := range p.snapshotLive() {
		p.release(occ)
	}

	released := p.counters.released.Load()
	if released == 0 {
		p.log.Info("dispatch: worker pool fermato", slog.Int("in_attesa_lasciate", len(abandoned)))
		return nil
	}
	return fmt.Errorf("dispatch: %d esecuzioni interrotte dall'arresto e rilasciate come `pending`", released)
}

// wait aspetta che tutti i worker siano usciti. Torna false se il contesto
// scade prima.
func (p *Pool) wait(ctx context.Context) bool {
	done := make(chan struct{})
	go func() {
		p.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return true
	case <-ctx.Done():
		return false
	}
}

func (p *Pool) snapshotLive() []scheduler.Occurrence {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]scheduler.Occurrence, 0, len(p.live))
	for _, occ := range p.live {
		out = append(out, occ)
	}
	return out
}
