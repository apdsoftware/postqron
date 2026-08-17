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

	queued   int
	inFlight int
	closed   bool

	maxQueued         int
	maxQueuedPerJob   int
	maxInFlightPerJob int
}

// jobQueue è la coda di un singolo job.
type jobQueue struct {
	items    []scheduler.Occurrence
	inFlight int
	inRing   bool
}

func newQueue(maxQueued, maxQueuedPerJob, maxInFlightPerJob int) *queue {
	q := &queue{
		jobs:              map[string]*jobQueue{},
		keys:              map[string]struct{}{},
		maxQueued:         maxQueued,
		maxQueuedPerJob:   maxQueuedPerJob,
		maxInFlightPerJob: maxInFlightPerJob,
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
	if q.queued >= q.maxQueued {
		return fmt.Errorf("%w: %d occorrenze in attesa", ErrQueueFull, q.queued)
	}

	jq := q.jobs[occ.Job.ID]
	if jq == nil {
		jq = &jobQueue{}
		q.jobs[occ.Job.ID] = jq
	}
	if len(jq.items) >= q.maxQueuedPerJob {
		// Il tetto per job protegge la coda comune: senza, un job con ore di
		// arretrato la riempirebbe da solo e i job sani si vedrebbero rifiutare
		// occorrenze che non c'entrano niente con il suo problema.
		return fmt.Errorf("%w: il job %s ha già %d occorrenze in attesa",
			ErrQueueFull, occ.Job.Name, len(jq.items))
	}

	jq.items = append(jq.items, occ)
	q.keys[key] = struct{}{}
	q.queued++
	if !jq.inRing {
		jq.inRing = true
		q.ring = append(q.ring, occ.Job.ID)
	}
	q.cond.Signal()
	return nil
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
// rapidi, cioè di nuovo il problema che R3 vieta.
func (q *queue) pick() (scheduler.Occurrence, bool) {
	for i := 0; i < len(q.ring); i++ {
		idx := (q.cursor + i) % len(q.ring)
		id := q.ring[idx]
		jq := q.jobs[id]
		if jq.inFlight >= q.maxInFlightPerJob {
			continue
		}

		occ := jq.items[0]
		jq.items = jq.items[1:]
		jq.inFlight++
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

// done segnala che l'esecuzione è finita e libera il posto del job.
func (q *queue) done(occ scheduler.Occurrence) {
	q.mu.Lock()
	defer q.mu.Unlock()

	delete(q.keys, occurrenceKey(occ))
	q.inFlight--
	if jq := q.jobs[occ.Job.ID]; jq != nil {
		jq.inFlight--
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
