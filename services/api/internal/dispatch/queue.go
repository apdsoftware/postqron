package dispatch

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/apdsoftware/postqron/services/api/internal/scheduler"
)

// Gli errori che [Pool.Dispatch] restituisce allo scheduler. Sono la seconda
// clausola di [scheduler.Dispatcher]: chi non può accodare lo dice, e la riga
// resta `pending` per il recupero.
var (
	// ErrQueueFull dice che la coda — quella comune o quella del singolo job —
	// non ha più posto.
	ErrQueueFull = errors.New("dispatch: coda piena")
	// ErrClosed dice che il pool è in arresto e non accetta più lavoro.
	ErrClosed = errors.New("dispatch: pool in arresto")
	// ErrOverlapSkipped dice che l'occorrenza **non va eseguita affatto**: la
	// precedente dello stesso job, nello stesso ambiente, è ancora in carico e il
	// job dichiara `on_overlap: skip` (R41).
	//
	// È diverso dagli altri due e la differenza è il destino della riga. Là non
	// c'è posto adesso e la riga resta `pending` perché qualcuno la riprenda; qui
	// nessuno la riprenderà mai, perché l'utente ha chiesto che non venga
	// eseguita. Avvolge [scheduler.ErrOverlap], che è la forma in cui lo
	// scheduler lo riconosce e chiude la riga come `skipped`.
	ErrOverlapSkipped = fmt.Errorf("dispatch: occorrenza saltata, la precedente è ancora in corso: %w",
		scheduler.ErrOverlap)
	// errDuplicate è interno: l'occorrenza è già in coda o in volo. Non è un
	// rifiuto — la riga *è* stata presa in carico — quindi non esce mai da
	// questo package. Vedi [Pool.Dispatch].
	errDuplicate = errors.New("dispatch: occorrenza già in carico")
)

// queue è la coda equa: una FIFO per job, servite a turno.
//
// È qui che vive l'isolamento di R3, e vale la pena dire perché non è una
// `chan scheduler.Occurrence`. Un canale è una FIFO sola: l'ordine di uscita è
// quello di ingresso, quindi l'arretrato di un job lento si mette davanti al
// lavoro di tutti gli altri, e i worker se lo prendono in blocco perché è ciò
// che trovano in testa. Un canale non sa nemmeno rifiutare un elemento
// duplicato né limitare quanto un singolo job può occupare.
//
// La struttura è deliberatamente banale: una mappa di code per job, un anello
// dei job che hanno lavoro pronto, un cursore che avanza di un posto a ogni
// prelievo. Il costo di un prelievo è O(job con lavoro pronto) nel caso
// peggiore — quello in cui sono tutti al tetto di esecuzioni in volo — e O(1)
// nel caso normale.
//
// # Tre limiti, e non sono la stessa cosa
//
// Sulla stessa coda convivono tre tetti, e confonderli è il modo di sbagliarne
// due su tre:
//
//   - **maxInFlightPerJob** è un tetto di risorse del processo: quanti worker un
//     singolo job può tenere occupati. Protegge gli altri job, vale uguale per
//     tutti e non è configurabile da nessuno.
//   - **La politica di sovrapposizione** ([scheduler.OverlapPolicy], R41) è una
//     scelta del job, e si applica alla **corsia** — la coppia (job, ambiente) —
//     perché è quella la cosa che «si sovrappone a sé stessa»: una prova in
//     staging e un'esecuzione in produzione sono due esecuzioni diverse, con
//     esiti e alert separati (R23), e farle aspettare a vicenda sarebbe un
//     ritardo che nessuno ha chiesto.
//   - **maxInFlightPerWorkspace** è il tetto tecnico del servizio (R10): quante
//     esecuzioni un singolo workspace può tenere in volo, tutti i suoi job messi
//     insieme. Protegge la macchina, non un job. Vedi
//     [DefaultMaxInFlightPerWorkspace] per il numero e per come è stato scelto.
type queue struct {
	mu   sync.Mutex
	cond *sync.Cond

	jobs map[string]*jobQueue
	// ring sono i job con almeno un'occorrenza pronta, nell'ordine in cui
	// vengono serviti; cursor è il prossimo da guardare.
	ring   []string
	cursor int

	// keys sono le occorrenze in carico — in coda o in volo — nella forma della
	// chiave naturale. Serve a riconoscere la riofferta di un'occorrenza che
	// abbiamo già: succede per costruzione, perché lo scheduler rioffre ciò che
	// resta `pending` oltre [scheduler.DefaultReclaimAfter] e un'occorrenza può
	// legittimamente aspettare in coda più a lungo.
	keys map[string]struct{}

	// workspaces sono le esecuzioni in volo per workspace, che è la grandezza su
	// cui il tetto tecnico del servizio conta. Ci stanno solo i workspace che
	// hanno qualcosa in volo adesso: la voce nasce alla presa e sparisce
	// all'ultima esecuzione chiusa.
	workspaces map[string]int

	queued   int
	inFlight int
	closed   bool

	maxQueued               int
	maxQueuedPerJob         int
	maxInFlightPerJob       int
	maxInFlightPerWorkspace int
}

// jobQueue è la coda di un singolo job.
type jobQueue struct {
	items    []scheduler.Occurrence
	inFlight int
	inRing   bool

	// workspace è il proprietario del job. Sta qui e non si rilegge
	// dall'occorrenza a ogni prelievo perché non cambia: tutte le occorrenze di
	// un job appartengono allo stesso workspace, e averlo sul job rende il
	// controllo del tetto un accesso a mappa invece di una scansione.
	workspace string

	// lanes è l'occupazione per ambiente, cioè la corsia su cui la politica di
	// sovrapposizione decide. Un job che vive in staging e in produzione ne ha
	// due, indipendenti.
	lanes map[string]*lane
}

// lane è quanto una singola coppia (job, ambiente) tiene in carico.
type lane struct{ queued, inFlight int }

func newQueue(maxQueued, maxQueuedPerJob, maxInFlightPerJob, maxInFlightPerWorkspace int) *queue {
	q := &queue{
		jobs:                    map[string]*jobQueue{},
		keys:                    map[string]struct{}{},
		workspaces:              map[string]int{},
		maxQueued:               maxQueued,
		maxQueuedPerJob:         maxQueuedPerJob,
		maxInFlightPerJob:       maxInFlightPerJob,
		maxInFlightPerWorkspace: maxInFlightPerWorkspace,
	}
	q.cond = sync.NewCond(&q.mu)
	return q
}

// occurrenceKey è la chiave naturale dell'occorrenza in forma testuale, la
// stessa quaterna che è chiave primaria su `job_executions`.
func occurrenceKey(occ scheduler.Occurrence) string {
	return fmt.Sprintf("%s|%s|%s|%d",
		occ.Job.ID, occ.ScheduledFor.UTC().Format(time.RFC3339Nano), occ.Environment, occ.Attempt)
}

// overlapOf è la politica di sovrapposizione che vale per questa occorrenza
// (R41).
//
// Due occorrenze non la subiscono, e in entrambi i casi la ragione è che non
// sono «un'occorrenza che scatta»:
//
//   - **I tentativi successivi al primo** (R5). Un retry è la continuazione di
//     un'occorrenza che la politica ha già ammesso, e il tentativo precedente per
//     definizione è finito — è per questo che se ne fa un altro. Fermarlo qui
//     significherebbe che un job `skip` che fallisce non ritenta mai.
//   - **I trigger manuali** (R8). «Esegui adesso» è un atto esplicito di una
//     persona, non un'occorrenza che scavalca la precedente perché il job è
//     lento; e ha già i propri due tetti — la chiave naturale ancorata alla
//     griglia del piano e il budget aggregato del piano — che stanno in
//     internal/jobs, dove nasce la riga.
//
// Restano comunque **occupanti**: un retry o un trigger in volo tengono la
// corsia, e un'occorrenza schedulata che gli arriva addosso la trova occupata.
// L'esenzione riguarda chi la subisce, non chi la produce.
func overlapOf(occ scheduler.Occurrence) scheduler.OverlapPolicy {
	if occ.Attempt > 1 || occ.Manual {
		return scheduler.OverlapAllow
	}
	return occ.Job.Overlap.Or()
}

// push accoda un'occorrenza. Non blocca mai: se non c'è posto lo dice.
func (q *queue) push(occ scheduler.Occurrence) error {
	q.mu.Lock()
	defer q.mu.Unlock()

	if q.closed {
		return ErrClosed
	}
	key := occurrenceKey(occ)
	if _, dup := q.keys[key]; dup {
		return errDuplicate
	}

	jq := q.jobs[occ.Job.ID]
	if jq == nil {
		jq = &jobQueue{workspace: occ.Job.UserID, lanes: map[string]*lane{}}
		q.jobs[occ.Job.ID] = jq
	}
	ln := jq.lanes[occ.Environment]

	// R41, politica `skip`: la corsia è già occupata da un'occorrenza in carico
	// — in coda o in volo, che per chi la subisce è lo stesso — e questa non va
	// eseguita. Il rifiuto arriva **prima** dei tetti di capienza perché è più
	// preciso: dire «coda piena» a un'occorrenza che comunque non si sarebbe
	// eseguita manderebbe a cercare un problema che non c'è.
	if overlapOf(occ) == scheduler.OverlapSkip && ln != nil && ln.queued+ln.inFlight > 0 {
		q.dropIfEmpty(occ.Job.ID, jq)
		return fmt.Errorf("%w: %s in %s", ErrOverlapSkipped, occ.Job.Name, occ.Environment)
	}

	if q.queued >= q.maxQueued {
		q.dropIfEmpty(occ.Job.ID, jq)
		return fmt.Errorf("%w: %d occorrenze in attesa", ErrQueueFull, q.queued)
	}
	if len(jq.items) >= q.maxQueuedPerJob {
		// Il tetto per job protegge la coda comune: senza, un job con ore di
		// arretrato la riempirebbe da solo e i job sani si vedrebbero rifiutare
		// occorrenze che non c'entrano niente con il suo problema.
		return fmt.Errorf("%w: il job %s ha già %d occorrenze in attesa",
			ErrQueueFull, occ.Job.Name, len(jq.items))
	}

	if ln == nil {
		ln = &lane{}
		jq.lanes[occ.Environment] = ln
	}
	jq.items = append(jq.items, occ)
	ln.queued++
	q.keys[key] = struct{}{}
	q.queued++
	if !jq.inRing {
		jq.inRing = true
		q.ring = append(q.ring, occ.Job.ID)
	}
	q.cond.Signal()
	return nil
}

// dropIfEmpty toglie dalla mappa una coda creata da un push che poi è stato
// rifiutato. Senza, un job che riceve solo rifiuti lascerebbe dietro di sé una
// voce vuota per sempre, e con migliaia di job la mappa crescerebbe da sola.
func (q *queue) dropIfEmpty(id string, jq *jobQueue) {
	if len(jq.items) == 0 && jq.inFlight == 0 && len(jq.lanes) == 0 {
		delete(q.jobs, id)
	}
}

// take restituisce la prossima occorrenza da eseguire, aspettando finché non ce
// n'è una. Torna false quando la coda è chiusa e non c'è più niente da fare.
//
// Chi la chiama **deve** chiamare [queue.done] a esecuzione finita: è quella a
// liberare il posto in volo del job e a togliere l'occorrenza dalle chiavi in
// carico.
func (q *queue) take() (scheduler.Occurrence, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()

	for {
		if occ, ok := q.pick(); ok {
			return occ, true
		}
		if q.closed {
			return scheduler.Occurrence{}, false
		}
		q.cond.Wait()
	}
}

// pick sceglie il prossimo job da servire scorrendo l'anello dal cursore. Va
// chiamata con il lock preso.
//
// Saltare i job già al tetto invece di aspettarli è tutto il punto: un worker
// che si mettesse in attesa del job lento sarebbe un worker in meno per i job
// rapidi, cioè di nuovo il problema che R3 vieta. Vale per entrambi i tetti
// documentati su [queue] — quello per job e quello della corsia — ed è ciò che
// rende ciascuno una difesa invece di un rallentamento esteso a tutti.
func (q *queue) pick() (scheduler.Occurrence, bool) {
	for i := 0; i < len(q.ring); i++ {
		idx := (q.cursor + i) % len(q.ring)
		id := q.ring[idx]
		jq := q.jobs[id]
		if jq.inFlight >= q.maxInFlightPerJob {
			continue
		}
		// Il tetto tecnico per workspace (R10). Si salta il job, non si aspetta:
		// è quel `continue` a fare la differenza fra «un workspace che sbatte
		// contro il tetto rallenta sé stesso» e «rallenta tutti».
		if q.workspaces[jq.workspace] >= q.maxInFlightPerWorkspace {
			continue
		}
		pos, ok := jq.ready(q.maxInFlightPerJob)
		if !ok {
			continue
		}

		occ := jq.items[pos]
		jq.items = append(jq.items[:pos], jq.items[pos+1:]...)
		ln := jq.lanes[occ.Environment]
		ln.queued--
		ln.inFlight++
		jq.inFlight++
		q.workspaces[jq.workspace]++
		q.queued--
		q.inFlight++

		if len(jq.items) == 0 {
			jq.inRing = false
			q.ring = append(q.ring[:idx], q.ring[idx+1:]...)
			// Chi stava dopo è scalato al posto di chi è uscito: il cursore
			// resta dov'era per servire proprio lui al giro successivo.
			q.cursor = idx
		} else {
			q.cursor = idx + 1
		}
		if len(q.ring) == 0 {
			q.cursor = 0
		} else {
			q.cursor %= len(q.ring)
		}
		return occ, true
	}
	return scheduler.Occurrence{}, false
}

// ready trova la prima occorrenza in coda la cui corsia ha ancora posto, e dice
// dove sta. Va chiamata con il lock della coda preso.
//
// Non è sempre la prima della fila, ed è deliberato: un job in due ambienti con
// `on_overlap: queue` può avere la produzione ferma su un'esecuzione lenta e
// staging libero, e servire staging invece di aspettare è la stessa scelta che
// [queue.pick] fa fra job diversi. Dentro una corsia l'ordine resta quello di
// arrivo, che è ciò che «accodare» significa.
//
// Il costo è O(occorrenze in coda del job) nel caso peggiore — tutte nella
// stessa corsia bloccata — e O(1) nel caso normale, in cui la prima va bene.
// Il caso peggiore è comunque limitato da [queue.maxQueuedPerJob].
func (jq *jobQueue) ready(maxInFlightPerJob int) (int, bool) {
	for i, occ := range jq.items {
		limit := 1
		if overlapOf(occ) == scheduler.OverlapAllow {
			// `allow` non ha un limite proprio: si ferma dove si ferma il tetto di
			// risorse del job, che è l'altra cosa e vale comunque.
			limit = maxInFlightPerJob
		}
		if jq.lanes[occ.Environment].inFlight < limit {
			return i, true
		}
	}
	return 0, false
}

// done segnala che l'esecuzione è finita e libera il posto del job.
func (q *queue) done(occ scheduler.Occurrence) {
	q.mu.Lock()
	defer q.mu.Unlock()

	delete(q.keys, occurrenceKey(occ))
	q.inFlight--
	if jq := q.jobs[occ.Job.ID]; jq != nil {
		jq.inFlight--
		if ln := jq.lanes[occ.Environment]; ln != nil {
			ln.inFlight--
			if ln.queued == 0 && ln.inFlight == 0 {
				delete(jq.lanes, occ.Environment)
			}
		}
		// Il conteggio del workspace si azzera scomparendo: la mappa contiene solo
		// chi ha qualcosa in volo adesso, e non cresce con il numero di clienti.
		if q.workspaces[jq.workspace]--; q.workspaces[jq.workspace] <= 0 {
			delete(q.workspaces, jq.workspace)
		}
		if jq.inFlight == 0 && len(jq.items) == 0 {
			// La coda di un job che non ha né lavoro né esecuzioni sparisce: con
			// migliaia di job la mappa non deve crescere per sempre.
			delete(q.jobs, occ.Job.ID)
		}
	}
	// Il posto liberato può sbloccare un worker fermo perché quel job era al
	// tetto. Signal e non Broadcast: i worker sono intercambiabili, quindi
	// svegliarne uno basta — se non trova niente di eleggibile non lo troverebbe
	// nessun altro.
	q.cond.Signal()
}

// close chiude la coda e restituisce le occorrenze che erano in attesa.
//
// Le occorrenze restituite non vanno toccate sul database: le loro righe sono
// ancora `pending`, che è esattamente lo stato in cui il recupero dello
// scheduler se le aspetta. Vedi la documentazione del package.
func (q *queue) close() []scheduler.Occurrence {
	q.mu.Lock()
	defer q.mu.Unlock()

	if q.closed {
		return nil
	}
	q.closed = true

	var abandoned []scheduler.Occurrence
	for id, jq := range q.jobs {
		abandoned = append(abandoned, jq.items...)
		for _, occ := range jq.items {
			delete(q.keys, occurrenceKey(occ))
		}
		jq.items = nil
		jq.inRing = false
		// Le corsie perdono la parte in attesa; quella in volo resta, perché i
		// worker che la stanno eseguendo chiameranno comunque [queue.done].
		for env, ln := range jq.lanes {
			ln.queued = 0
			if ln.inFlight == 0 {
				delete(jq.lanes, env)
			}
		}
		if jq.inFlight == 0 {
			delete(q.jobs, id)
		}
	}
	q.ring = nil
	q.cursor = 0
	q.queued = 0

	// Qui Broadcast: tutti i worker in attesa devono uscire, non uno solo.
	q.cond.Broadcast()
	return abandoned
}

// depth sono le occorrenze in attesa e quelle in volo.
func (q *queue) depth() (queued, inFlight int) {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.queued, q.inFlight
}

// workspaceInFlight è quanto un workspace sta occupando adesso del proprio
// tetto tecnico.
func (q *queue) workspaceInFlight(workspaceID string) (used, limit int) {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.workspaces[workspaceID], q.maxInFlightPerWorkspace
}
