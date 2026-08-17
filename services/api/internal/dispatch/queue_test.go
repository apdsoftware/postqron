package dispatch

import (
	"errors"
	"testing"
	"time"

	"github.com/apdsoftware/postqron/services/api/internal/scheduler"
)

// I test della coda stanno dentro il package perché la coda è dove vive
// l'isolamento di R3: attraverso [Pool] se ne vedrebbe solo l'effetto, e
// l'effetto è mediato da trentadue goroutine e da un orologio. Qui l'ordine di
// uscita è una cosa che si guarda.

var epoch = time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)

func fakeOccurrence(jobID string, seconds int) scheduler.Occurrence {
	return scheduler.Occurrence{
		Job:          scheduler.Job{ID: jobID, Name: jobID, Enabled: true},
		ScheduledFor: epoch.Add(time.Duration(seconds) * time.Second),
		Environment:  "production",
		Attempt:      1,
	}
}

func mustPush(t *testing.T, q *queue, occ scheduler.Occurrence) {
	t.Helper()
	if err := q.push(occ); err != nil {
		t.Fatalf("accodamento di %s: %v", occ, err)
	}
}

func mustTake(t *testing.T, q *queue) scheduler.Occurrence {
	t.Helper()
	occ, ok := q.take()
	if !ok {
		t.Fatal("la coda si è chiusa mentre c'era ancora lavoro")
	}
	return occ
}

// L'arretrato di un job non si mette davanti al lavoro degli altri: è la prima
// metà dell'isolamento di R3, e con una FIFO condivisa l'ordine atteso sarebbe
// a, a, a, b, c.
func TestLaCodaServeIJobATurno(t *testing.T) {
	q := newQueue(64, 64, 8)

	for i := range 3 {
		mustPush(t, q, fakeOccurrence("a", i))
	}
	mustPush(t, q, fakeOccurrence("b", 0))
	mustPush(t, q, fakeOccurrence("c", 0))

	want := []string{"a", "b", "c", "a", "a"}
	for i, expected := range want {
		got := mustTake(t, q)
		if got.Job.ID != expected {
			t.Fatalf("prelievo %d: atteso il job %q, ottenuto %q", i, expected, got.Job.ID)
		}
	}
}

// Dentro un singolo job l'ordine resta quello di arrivo: fra due occorrenze
// dello stesso job la più vecchia è anche la più urgente.
func TestDentroUnJobLOrdineEQuelloDiArrivo(t *testing.T) {
	q := newQueue(64, 64, 8)
	for i := range 4 {
		mustPush(t, q, fakeOccurrence("a", i))
	}
	for i := range 4 {
		got := mustTake(t, q)
		if want := epoch.Add(time.Duration(i) * time.Second); !got.ScheduledFor.Equal(want) {
			t.Fatalf("prelievo %d: attesa l'occorrenza di %s, ottenuta quella di %s", i, want, got.ScheduledFor)
		}
	}
}

// La seconda metà dell'isolamento: un job al tetto di esecuzioni in volo viene
// saltato, non aspettato. Un worker che si mettesse in coda dietro al job lento
// sarebbe un worker in meno per i job rapidi.
func TestUnJobAlTettoVieneSaltatoNonAspettato(t *testing.T) {
	q := newQueue(64, 64, 1)

	mustPush(t, q, fakeOccurrence("lento", 0))
	mustPush(t, q, fakeOccurrence("lento", 1))
	mustPush(t, q, fakeOccurrence("rapido", 0))

	slow := mustTake(t, q)
	if slow.Job.ID != "lento" {
		t.Fatalf("primo prelievo: atteso il job lento, ottenuto %q", slow.Job.ID)
	}
	// Il secondo prelievo salta la seconda occorrenza del job lento, che è in
	// testa alla sua coda ma il cui job è già al tetto.
	if got := mustTake(t, q); got.Job.ID != "rapido" {
		t.Fatalf("secondo prelievo: atteso il job rapido, ottenuto %q", got.Job.ID)
	}

	// Con il solo job lento rimasto e il suo posto ancora occupato, non c'è
	// niente di eleggibile: il prelievo aspetta.
	taken := make(chan scheduler.Occurrence, 1)
	go func() {
		if occ, ok := q.take(); ok {
			taken <- occ
		}
	}()
	select {
	case occ := <-taken:
		t.Fatalf("prelevata %s con il job già al tetto", occ)
	case <-time.After(100 * time.Millisecond):
	}

	q.done(slow)
	select {
	case occ := <-taken:
		if occ.Job.ID != "lento" {
			t.Fatalf("dopo la liberazione del posto: atteso il job lento, ottenuto %q", occ.Job.ID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("il posto liberato non ha svegliato nessun worker")
	}
}

// Lo scheduler rioffre ciò che resta `pending` oltre la finestra di abbandono, e
// un'occorrenza può legittimamente aspettare in coda più a lungo: la seconda
// consegna non deve diventare una seconda esecuzione.
func TestUnaRiofferataGiaInCaricoVieneRiconosciuta(t *testing.T) {
	q := newQueue(64, 64, 8)
	occ := fakeOccurrence("a", 0)

	mustPush(t, q, occ)
	if err := q.push(occ); !errors.Is(err, errDuplicate) {
		t.Fatalf("seconda consegna in coda: atteso errDuplicate, ottenuto %v", err)
	}

	taken := mustTake(t, q)
	if err := q.push(occ); !errors.Is(err, errDuplicate) {
		t.Fatalf("seconda consegna mentre è in volo: atteso errDuplicate, ottenuto %v", err)
	}

	// Finita l'esecuzione l'occorrenza non è più in carico: se lo scheduler la
	// rioffre — perché il rilascio l'ha riportata a `pending` — va riaccettata.
	q.done(taken)
	if err := q.push(occ); err != nil {
		t.Fatalf("consegna dopo la fine dell'esecuzione: %v", err)
	}
}

func TestLaCodaPienaRifiutaInveceDiCrescere(t *testing.T) {
	q := newQueue(2, 2, 8)

	mustPush(t, q, fakeOccurrence("a", 0))
	mustPush(t, q, fakeOccurrence("b", 0))

	err := q.push(fakeOccurrence("c", 0))
	if !errors.Is(err, ErrQueueFull) {
		t.Fatalf("coda comune piena: atteso ErrQueueFull, ottenuto %v", err)
	}
}

// Il tetto per job protegge la coda comune: un job con ore di arretrato non deve
// far rifiutare occorrenze di job che non c'entrano niente.
func TestIlTettoPerJobProteggeLaCodaComune(t *testing.T) {
	q := newQueue(8, 2, 8)

	mustPush(t, q, fakeOccurrence("arretrato", 0))
	mustPush(t, q, fakeOccurrence("arretrato", 1))

	if err := q.push(fakeOccurrence("arretrato", 2)); !errors.Is(err, ErrQueueFull) {
		t.Fatalf("coda del job piena: atteso ErrQueueFull, ottenuto %v", err)
	}
	// C'è ancora posto per gli altri: è tutto il punto del tetto per job.
	mustPush(t, q, fakeOccurrence("sano", 0))
}

func TestLaChiusuraRestituisceLeOccorrenzeInAttesa(t *testing.T) {
	q := newQueue(64, 64, 8)
	mustPush(t, q, fakeOccurrence("a", 0))
	mustPush(t, q, fakeOccurrence("a", 1))
	mustPush(t, q, fakeOccurrence("b", 0))

	inFlight := mustTake(t, q)

	abandoned := q.close()
	if len(abandoned) != 2 {
		t.Fatalf("attese 2 occorrenze in attesa, ottenute %d", len(abandoned))
	}
	if _, ok := q.take(); ok {
		t.Fatal("la coda chiusa ha restituito altro lavoro")
	}
	if err := q.push(fakeOccurrence("c", 0)); !errors.Is(err, ErrClosed) {
		t.Fatalf("accodamento dopo la chiusura: atteso ErrClosed, ottenuto %v", err)
	}

	// La chiusura non tocca ciò che è in volo: quello lo chiude il worker, o lo
	// rilascia l'arresto.
	q.done(inFlight)
	if queued, live := q.depth(); queued != 0 || live != 0 {
		t.Fatalf("coda non vuota dopo la chiusura: %d in attesa, %d in volo", queued, live)
	}
}
