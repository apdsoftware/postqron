package dispatch_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/apdsoftware/postqron/services/api/internal/dispatch"
	"github.com/apdsoftware/postqron/services/api/internal/scheduler"
)

// I test di R41: un job più lento del proprio intervallo, con ciascuna delle
// tre politiche.
//
// La forma è la stessa in tutti e tre, ed è scelta perché misuri la
// **concorrenza vera** invece di una sequenza. Le occorrenze si consegnano tutte
// insieme, l'esecutore si blocca su un canale che il test tiene chiuso, e ciò
// che si guarda è quante ne stanno dentro nello stesso istante: è la sola
// domanda che «sovrapposte» pone. Un test che le consegnasse una per volta
// aspettando l'esito passerebbe anche con la politica applicata al contrario.

// concorrenza conta quante esecuzioni ci sono dentro adesso e quante ce ne sono
// state al massimo insieme, per corsia (job, ambiente) e in totale.
type concorrenza struct {
	mu      sync.Mutex
	dentro  map[string]int
	picco   map[string]int
	entrate map[string]int
}

func nuovaConcorrenza() *concorrenza {
	return &concorrenza{
		dentro:  map[string]int{},
		picco:   map[string]int{},
		entrate: map[string]int{},
	}
}

func (c *concorrenza) entra(chiave string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.dentro[chiave]++
	c.entrate[chiave]++
	c.picco[chiave] = max(c.picco[chiave], c.dentro[chiave])
}

func (c *concorrenza) esce(chiave string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.dentro[chiave]--
}

func (c *concorrenza) leggi(chiave string) (picco, entrate int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.picco[chiave], c.entrate[chiave]
}

// corsia è la chiave su cui la politica di R41 decide: job e ambiente.
func corsia(occ scheduler.Occurrence) string { return occ.Job.ID + "/" + occ.Environment }

// occorrenzaConPolitica è un'occorrenza di un job con la politica indicata.
func occorrenzaConPolitica(jobID string, secondi int, p scheduler.OverlapPolicy) scheduler.Occurrence {
	occ := fakeOccurrence(jobID, secondi)
	occ.Job.Overlap = p
	return occ
}

// esecutoreBloccante tiene ogni esecuzione dentro finché il canale non si
// chiude, e nel frattempo conta la concorrenza.
//
// È l'equivalente esatto di «il job impiega più del proprio intervallo»: non c'è
// nessuna durata da tarare, e quindi nessuna soglia temporale che su una
// macchina lenta comincia a fallire da sola.
func esecutoreBloccante(c *concorrenza, sblocca <-chan struct{}) dispatch.Executor {
	return dispatch.ExecutorFunc(func(ctx context.Context, occ scheduler.Occurrence) (dispatch.Result, error) {
		c.entra(corsia(occ))
		defer c.esce(corsia(occ))
		select {
		case <-sblocca:
		case <-ctx.Done():
			return dispatch.Result{}, ctx.Err()
		}
		return dispatch.Result{ResponseStatus: 200}, nil
	})
}

// ------------------------------------------------------------------- skip

// TestUnJobPiuLentoDelProprioIntervalloSaltaLeOccorrenzeInEccesso è il
// predefinito di R41 al lavoro.
//
// Dieci occorrenze consegnate mentre la prima non finisce mai: ne parte una, e
// le altre nove vengono rifiutate **subito**, non accodate. Il rifiuto è
// [dispatch.ErrOverlapSkipped] e non un errore qualunque, perché è la
// differenza fra una riga che qualcuno riprenderà e una che va chiusa: lo
// scheduler la chiude `skipped` con il motivo, e questo test verifica che il
// pool gliene dia il modo.
func TestUnJobPiuLentoDelProprioIntervalloSaltaLeOccorrenzeInEccesso(t *testing.T) {
	const occorrenze = 10

	store := newMemStore()
	conta := nuovaConcorrenza()
	sblocca := make(chan struct{})

	pool := newPool(t, dispatch.Options{
		Store:        store,
		Executor:     esecutoreBloccante(conta, sblocca),
		Workers:      8,
		DrainTimeout: time.Second,
	})

	saltate := 0
	for i := range occorrenze {
		err := hand(t, pool, store, occorrenzaConPolitica("fatture", i, scheduler.OverlapSkip))
		switch {
		case err == nil:
		case errors.Is(err, scheduler.ErrOverlap):
			// Deve essere riconoscibile **dallo scheduler**, che conosce solo il
			// sentinella del proprio package: è la quarta clausola del contratto.
			saltate++
		default:
			t.Fatalf("occorrenza %d rifiutata per un motivo che non è la sovrapposizione: %v", i, err)
		}
	}

	if saltate != occorrenze-1 {
		t.Fatalf("saltate %d occorrenze su %d, attese %d: con `skip` ne passa una sola",
			saltate, occorrenze, occorrenze-1)
	}
	eventually(t, 5*time.Second, func() bool { return pool.Stats().InFlight == 1 },
		"l'unica occorrenza ammessa non è partita: %+v", pool.Stats())

	// Nessuna coda: è la proprietà che distingue `skip` da `queue`. Un job
	// stabilmente più lento del proprio intervallo non deve lasciare dietro di sé
	// un arretrato che qualcuno prima o poi eseguirà.
	if st := pool.Stats(); st.Queued != 0 {
		t.Fatalf("con `skip` sono rimaste %d occorrenze in attesa, attese zero", st.Queued)
	}
	if st := pool.Stats(); st.Overlapped != int64(occorrenze-1) {
		t.Fatalf("Overlapped = %d, attese %d", st.Overlapped, occorrenze-1)
	}
	if st := pool.Stats(); st.Refused != 0 {
		t.Fatalf("Refused = %d: una sovrapposizione non è un rifiuto, e contarla lì "+
			"farebbe scrivere allo scheduler che la riga va ripresa", st.Refused)
	}

	close(sblocca)
	eventually(t, 5*time.Second, func() bool { return pool.Stats().Succeeded == 1 },
		"l'occorrenza ammessa non si è chiusa: %+v", pool.Stats())

	if picco, entrate := conta.leggi("fatture/production"); picco != 1 || entrate != 1 {
		t.Fatalf("con `skip` sono entrate %d esecuzioni (picco %d), attesa una sola", entrate, picco)
	}
}

// TestConSkipUnAltroAmbienteDelloStessoJobNonVieneFermato: la politica guarda la
// corsia, non il job.
//
// Un job che vive in staging e in produzione produce due esecuzioni per
// occorrenza, con esiti e alert separati (R23). Sono due cose diverse, e
// «la precedente è ancora in corso» va letto su ciascuna per conto proprio:
// bloccare la produzione perché staging è lento sarebbe un ritardo che nessuno
// ha chiesto.
func TestConSkipUnAltroAmbienteDelloStessoJobNonVieneFermato(t *testing.T) {
	store := newMemStore()
	conta := nuovaConcorrenza()
	sblocca := make(chan struct{})

	pool := newPool(t, dispatch.Options{
		Store:        store,
		Executor:     esecutoreBloccante(conta, sblocca),
		Workers:      8,
		DrainTimeout: time.Second,
	})

	produzione := occorrenzaConPolitica("sync", 0, scheduler.OverlapSkip)
	staging := occorrenzaConPolitica("sync", 0, scheduler.OverlapSkip)
	staging.Environment = "staging"

	if err := hand(t, pool, store, produzione); err != nil {
		t.Fatalf("produzione: %v", err)
	}
	if err := hand(t, pool, store, staging); err != nil {
		t.Fatalf("staging è stato fermato dalla corsia di produzione: %v", err)
	}

	eventually(t, 5*time.Second, func() bool { return pool.Stats().InFlight == 2 },
		"i due ambienti non stanno girando insieme: %+v", pool.Stats())

	// La seconda occorrenza di produzione invece la trova occupata.
	err := hand(t, pool, store, occorrenzaConPolitica("sync", 1, scheduler.OverlapSkip))
	if !errors.Is(err, scheduler.ErrOverlap) {
		t.Fatalf("la seconda occorrenza di produzione è stata accettata: %v", err)
	}

	close(sblocca)
	eventually(t, 5*time.Second, func() bool { return pool.Stats().Succeeded == 2 },
		"le due esecuzioni non si sono chiuse: %+v", pool.Stats())
}

// ------------------------------------------------------------------ queue

// TestConQueueLeEsecuzioniDelloStessoJobRestanoInFila: nessuna occorrenza si
// perde e nessuna si sovrappone.
//
// Le due proprietà vanno provate insieme, perché ciascuna da sola è
// soddisfacibile sbagliando: eseguirle tutte in parallelo non ne perde nessuna,
// e saltarle tutte non ne sovrappone nessuna.
func TestConQueueLeEsecuzioniDelloStessoJobRestanoInFila(t *testing.T) {
	const occorrenze = 6

	store := newMemStore()
	conta := nuovaConcorrenza()

	// Ogni esecuzione dura un istante e conta la concorrenza mentre è dentro.
	// Il picco è la misura: con `queue` non può mai superare uno.
	exec := dispatch.ExecutorFunc(func(_ context.Context, occ scheduler.Occurrence) (dispatch.Result, error) {
		conta.entra(corsia(occ))
		// Un momento di sosta dentro l'esecuzione: senza, due esecuzioni
		// sovrapposte potrebbero non incontrarsi mai e il test passerebbe anche
		// con la politica non applicata.
		time.Sleep(2 * time.Millisecond)
		conta.esce(corsia(occ))
		return dispatch.Result{ResponseStatus: 200}, nil
	})

	pool := newPool(t, dispatch.Options{
		Store:        store,
		Executor:     exec,
		Workers:      8,
		DrainTimeout: 5 * time.Second,
	})

	for i := range occorrenze {
		if err := hand(t, pool, store, occorrenzaConPolitica("fatturazione", i, scheduler.OverlapQueue)); err != nil {
			t.Fatalf("con `queue` l'occorrenza %d è stata rifiutata: %v", i, err)
		}
	}

	eventually(t, 10*time.Second, func() bool { return pool.Stats().Succeeded == occorrenze },
		"con `queue` non sono state eseguite tutte le occorrenze: %+v", pool.Stats())

	picco, entrate := conta.leggi("fatturazione/production")
	if entrate != occorrenze {
		t.Fatalf("eseguite %d occorrenze su %d: con `queue` non se ne perde nessuna", entrate, occorrenze)
	}
	if picco != 1 {
		t.Fatalf("picco di esecuzioni contemporanee = %d: con `queue` le esecuzioni sono serializzate", picco)
	}
	if st := pool.Stats(); st.Overlapped != 0 {
		t.Fatalf("Overlapped = %d: `queue` non salta niente", st.Overlapped)
	}
}

// TestConQueueUnJobInFilaNonBloccaGliAltri: la serializzazione è del job, non
// del pool.
//
// È lo stesso principio di R3 letto attraverso R41: un job che aspetta sé
// stesso non deve far aspettare nessun altro. Senza il salto in [queue.pick] un
// worker si metterebbe in attesa della corsia occupata, e con abbastanza job
// così il pool si fermerebbe.
func TestConQueueUnJobInFilaNonBloccaGliAltri(t *testing.T) {
	const rapidi = 200

	store := newMemStore()
	conta := nuovaConcorrenza()
	sblocca := make(chan struct{})
	var fatti sync.WaitGroup
	fatti.Add(rapidi)

	exec := dispatch.ExecutorFunc(func(ctx context.Context, occ scheduler.Occurrence) (dispatch.Result, error) {
		if occ.Job.ID == "lento" {
			conta.entra(corsia(occ))
			defer conta.esce(corsia(occ))
			select {
			case <-sblocca:
			case <-ctx.Done():
				return dispatch.Result{}, ctx.Err()
			}
			return dispatch.Result{ResponseStatus: 200}, nil
		}
		fatti.Done()
		return dispatch.Result{ResponseStatus: 200}, nil
	})

	pool := newPool(t, dispatch.Options{
		Store:            store,
		Executor:         exec,
		Workers:          8,
		QueueDepthPerJob: 256,
		DrainTimeout:     time.Second,
	})

	// Prima l'arretrato del job lento, poi i rapidi: con una coda che aspettasse
	// la corsia invece di saltarla, i rapidi non partirebbero mai.
	for i := range 100 {
		if err := hand(t, pool, store, occorrenzaConPolitica("lento", i, scheduler.OverlapQueue)); err != nil {
			t.Fatalf("occorrenza lenta %d: %v", i, err)
		}
	}
	for i := range rapidi {
		occ := occorrenzaConPolitica(fmt.Sprintf("rapido-%d", i), 0, scheduler.OverlapQueue)
		if err := hand(t, pool, store, occ); err != nil {
			t.Fatalf("job rapido %d: %v", i, err)
		}
	}

	fine := make(chan struct{})
	go func() { fatti.Wait(); close(fine) }()
	select {
	case <-fine:
	case <-time.After(30 * time.Second):
		t.Fatalf("i job rapidi non sono passati mentre il job lento aspettava sé stesso: %+v", pool.Stats())
	}

	if picco, _ := conta.leggi("lento/production"); picco != 1 {
		t.Fatalf("il job lento ha avuto %d esecuzioni insieme, `queue` ne ammette una", picco)
	}

	close(sblocca)
}

// ------------------------------------------------------------------ allow

// TestConAllowLeEsecuzioniSiSovrappongonoFinoAlTettoDiRisorse.
//
// `allow` è l'unica delle tre che ammette la sovrapposizione, e il test guarda
// entrambe le facce: che avvenga davvero, e che si fermi lo stesso al tetto di
// risorse per job — che non è una politica e non è negoziabile.
func TestConAllowLeEsecuzioniSiSovrappongonoFinoAlTettoDiRisorse(t *testing.T) {
	const (
		occorrenze  = 20
		tettoPerJob = 4
	)

	store := newMemStore()
	conta := nuovaConcorrenza()
	sblocca := make(chan struct{})

	pool := newPool(t, dispatch.Options{
		Store:             store,
		Executor:          esecutoreBloccante(conta, sblocca),
		Workers:           16,
		MaxInFlightPerJob: tettoPerJob,
		QueueDepthPerJob:  64,
		DrainTimeout:      time.Second,
	})

	for i := range occorrenze {
		if err := hand(t, pool, store, occorrenzaConPolitica("sincronizza", i, scheduler.OverlapAllow)); err != nil {
			t.Fatalf("con `allow` l'occorrenza %d è stata rifiutata: %v", i, err)
		}
	}

	eventually(t, 5*time.Second, func() bool { return pool.Stats().InFlight == tettoPerJob },
		"con `allow` non si sono sovrapposte fino al tetto: %+v", pool.Stats())

	// Un istante di assestamento: se il tetto non tenesse, il picco salirebbe
	// oltre proprio adesso.
	time.Sleep(50 * time.Millisecond)
	if picco, _ := conta.leggi("sincronizza/production"); picco != tettoPerJob {
		t.Fatalf("picco di esecuzioni contemporanee = %d, atteso il tetto per job %d", picco, tettoPerJob)
	}
	if st := pool.Stats(); st.Overlapped != 0 {
		t.Fatalf("Overlapped = %d: `allow` non salta niente", st.Overlapped)
	}

	close(sblocca)
	eventually(t, 10*time.Second, func() bool { return pool.Stats().Succeeded == occorrenze },
		"con `allow` non sono state eseguite tutte le occorrenze: %+v", pool.Stats())
}

// ------------------------------------------- il confine con la sospensione

// TestUnOccorrenzaDiUnJobSospesoLiberaLaPropriaCorsia è la cucitura fra R58 e
// R41, e vale la pena provarla perché le due si incontrano in un punto solo.
//
// Un downgrade sospende il job (R58): `enabled` va a falso, e l'occorrenza che
// era già in coda viene chiusa `skipped` senza partire. Quella chiusura salta
// l'esecuzione, quindi salta anche il posto che il worker avrebbe occupato — e
// se il posto **non** venisse restituito, la corsia resterebbe occupata da un
// fantasma. Con la politica predefinita `skip` la conseguenza sarebbe permanente:
// alla riaccensione, ogni occorrenza successiva troverebbe la corsia piena e
// verrebbe saltata per sempre. Un job che non riparte più dopo un downgrade
// annullato è precisamente il guasto che nessuno collegherebbe a questa riga.
func TestUnOccorrenzaDiUnJobSospesoLiberaLaPropriaCorsia(t *testing.T) {
	store := newMemStore()
	conta := nuovaConcorrenza()
	sblocca := make(chan struct{})
	defer close(sblocca)

	pool := newPool(t, dispatch.Options{
		Store:        store,
		Executor:     esecutoreBloccante(conta, sblocca),
		Workers:      4,
		DrainTimeout: time.Second,
	})

	// Un'esecuzione in staging che non finisce mai. Serve a tenere **viva** la
	// coda del job: senza, il job sparirebbe dalla mappa appena svuotato e la sua
	// corsia se ne andrebbe con lui, cioè il fantasma verrebbe raccolto per un
	// motivo che non c'entra niente con la contabilità delle corsie. Con la coda
	// viva, ciò che resta occupato resta occupato — che è la condizione in cui il
	// difetto sarebbe permanente.
	staging := occorrenzaConPolitica("fatture", 0, scheduler.OverlapSkip)
	staging.Environment = "staging"
	if err := hand(t, pool, store, staging); err != nil {
		t.Fatalf("consegna in staging: %v", err)
	}
	eventually(t, 5*time.Second, func() bool { return pool.Stats().InFlight == 1 },
		"l'esecuzione di staging non è partita: %+v", pool.Stats())

	// Com'è la riga di produzione dopo un downgrade: spenta, e ripresa dal
	// recupero perché era rimasta `pending`.
	sospesa := occorrenzaConPolitica("fatture", 1, scheduler.OverlapSkip)
	sospesa.Job.Enabled = false
	sospesa.Recovered = true
	if err := hand(t, pool, store, sospesa); err != nil {
		t.Fatalf("consegna dell'occorrenza sospesa: %v", err)
	}
	eventually(t, 5*time.Second, func() bool { return store.statusOf(sospesa) == "skipped" },
		"l'occorrenza del job sospeso non è stata chiusa: %q", store.statusOf(sospesa))
	eventually(t, 5*time.Second, func() bool { return pool.Stats().InFlight == 1 },
		"il posto della sospesa non è stato restituito: %+v", pool.Stats())

	// Il job torna acceso. La corsia di produzione dev'essere libera: se il
	// fantasma fosse rimasto, `skip` rifiuterebbe questa e tutte le successive.
	riaccesa := occorrenzaConPolitica("fatture", 2, scheduler.OverlapSkip)
	if err := hand(t, pool, store, riaccesa); err != nil {
		t.Fatalf("dopo la riaccensione la corsia di produzione risulta ancora occupata: %v", err)
	}
	eventually(t, 5*time.Second, func() bool { return pool.Stats().InFlight == 2 },
		"l'occorrenza dopo la riaccensione non è partita: %+v", pool.Stats())
}

// ------------------------------------------------------- le esenzioni

// TestUnTentativoSuccessivoNonVieneSaltatoDallaPropriaPolitica: un retry non è
// un'occorrenza che scatta.
//
// Il caso peggiore che questa esenzione evita è preciso: un job `skip` che
// fallisce, il cui tentativo successivo arriva mentre la corsia risulta ancora
// occupata, non ritenterebbe **mai** — e R5 smetterebbe di valere per tutti i
// job con il predefinito.
func TestUnTentativoSuccessivoNonVieneSaltatoDallaPropriaPolitica(t *testing.T) {
	store := newMemStore()
	conta := nuovaConcorrenza()
	sblocca := make(chan struct{})

	pool := newPool(t, dispatch.Options{
		Store:        store,
		Executor:     esecutoreBloccante(conta, sblocca),
		Workers:      8,
		DrainTimeout: time.Second,
	})

	primo := occorrenzaConPolitica("fatture", 0, scheduler.OverlapSkip)
	if err := hand(t, pool, store, primo); err != nil {
		t.Fatalf("primo tentativo: %v", err)
	}
	eventually(t, 5*time.Second, func() bool { return pool.Stats().InFlight == 1 },
		"il primo tentativo non è partito: %+v", pool.Stats())

	secondo := primo
	secondo.Attempt = 2
	if err := hand(t, pool, store, secondo); err != nil {
		t.Fatalf("il tentativo successivo è stato saltato dalla politica del job: %v", err)
	}

	// Un trigger manuale, per la stessa ragione: non è un'occorrenza che scatta,
	// è una persona che ha premuto un tasto.
	manuale := occorrenzaConPolitica("fatture", 2, scheduler.OverlapSkip)
	manuale.Manual = true
	if err := hand(t, pool, store, manuale); err != nil {
		t.Fatalf("il trigger manuale è stato saltato dalla politica del job: %v", err)
	}

	close(sblocca)
	eventually(t, 5*time.Second, func() bool { return pool.Stats().Succeeded == 3 },
		"non si sono chiuse tutte e tre: %+v", pool.Stats())
}
