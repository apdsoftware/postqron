package dispatch_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/apdsoftware/postqron/services/api/internal/dispatch"
	"github.com/apdsoftware/postqron/services/api/internal/scheduler"
)

// ------------------------------------------------------------------ fixture

// memStore è [dispatch.Store] in memoria: le stesse transizioni condizionate,
// senza database.
//
// Serve a provare ciò che il database non c'entra — l'isolamento di R3 è una
// proprietà della coda, e mille job rapidi più uno lento non hanno bisogno di
// PostgreSQL per essere osservati. La transizione atomica di R4 è invece una
// proprietà del database e va provata contro quello vero: è postgres_test.go.
type memStore struct {
	mu      sync.Mutex
	status  map[string]string
	records map[string]dispatch.Record
	// claimErr, se impostata, fa fallire ogni presa.
	claimErr error
}

func newMemStore() *memStore {
	return &memStore{status: map[string]string{}, records: map[string]dispatch.Record{}}
}

func occurrenceKey(occ scheduler.Occurrence) string {
	return fmt.Sprintf("%s|%s|%s|%d",
		occ.Job.ID, occ.ScheduledFor.UTC().Format(time.RFC3339Nano), occ.Environment, occ.Attempt)
}

// seed scrive la riga `pending` che lo scheduler avrebbe già inserito.
func (s *memStore) seed(occ scheduler.Occurrence) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.status[occurrenceKey(occ)] = "pending"
}

func (s *memStore) transition(occ scheduler.Occurrence, from, to string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := occurrenceKey(occ)
	if s.status[key] != from {
		return false
	}
	s.status[key] = to
	return true
}

func (s *memStore) Claim(_ context.Context, occ scheduler.Occurrence) (bool, error) {
	if s.claimErr != nil {
		return false, s.claimErr
	}
	return s.transition(occ, "pending", "running"), nil
}

func (s *memStore) Finish(_ context.Context, occ scheduler.Occurrence, rec dispatch.Record) (bool, error) {
	if !s.transition(occ, "running", string(rec.Outcome)) {
		return false, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.records[occurrenceKey(occ)] = rec
	return true, nil
}

func (s *memStore) Skip(_ context.Context, occ scheduler.Occurrence, reason string) (bool, error) {
	if !s.transition(occ, "pending", "skipped") {
		return false, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.records[occurrenceKey(occ)] = dispatch.Record{Outcome: dispatch.Skipped, Error: estratto(reason)}
	return true, nil
}

func (s *memStore) Release(_ context.Context, occ scheduler.Occurrence) (bool, error) {
	return s.transition(occ, "running", "pending"), nil
}

func (s *memStore) statusOf(occ scheduler.Occurrence) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.status[occurrenceKey(occ)]
}

func (s *memStore) recordOf(occ scheduler.Occurrence) dispatch.Record {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.records[occurrenceKey(occ)]
}

// fakeOccurrence costruisce un'occorrenza come la consegnerebbe lo scheduler.
func fakeOccurrence(jobID string, seconds int) scheduler.Occurrence {
	return scheduler.Occurrence{
		Job: scheduler.Job{
			ID:      jobID,
			Name:    jobID,
			URL:     "https://example.com/hook",
			Method:  "POST",
			Timeout: 30 * time.Second,
			Enabled: true,
		},
		ScheduledFor: time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC).Add(time.Duration(seconds) * time.Second),
		Environment:  "production",
		Attempt:      1,
	}
}

// testLogger porta i log del pool nell'output del test senza scriverci dopo che
// è finito: `t.Log` da una goroutine sopravvissuta al test fa panic.
type testWriter struct {
	t    *testing.T
	mu   sync.Mutex
	done bool
}

func (w *testWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if !w.done {
		w.t.Log(strings.TrimRight(string(p), "\n"))
	}
	return len(p), nil
}

func testLogger(t *testing.T) *slog.Logger {
	t.Helper()
	w := &testWriter{t: t}
	t.Cleanup(func() {
		w.mu.Lock()
		defer w.mu.Unlock()
		w.done = true
	})
	return slog.New(slog.NewTextHandler(w, &slog.HandlerOptions{Level: slog.LevelWarn}))
}

// newPool costruisce un pool già avviato e lo ferma alla fine del test.
func newPool(t *testing.T, opts dispatch.Options) *dispatch.Pool {
	t.Helper()
	if opts.Logger == nil {
		opts.Logger = testLogger(t)
	}
	pool, err := dispatch.New(opts)
	if err != nil {
		t.Fatalf("dispatch.New: %v", err)
	}
	pool.Start()
	t.Cleanup(func() {
		_ = pool.Shutdown(context.Background())
	})
	return pool
}

// hand consegna un'occorrenza al pool dopo aver scritto la riga `pending` che lo
// scheduler avrebbe già inserito.
func hand(t *testing.T, pool *dispatch.Pool, store *memStore, occ scheduler.Occurrence) error {
	t.Helper()
	store.seed(occ)
	return pool.Dispatch(context.Background(), occ)
}

// eventually aspetta che una condizione diventi vera. Le asserzioni su un pool
// riguardano goroutine che stanno lavorando: una lettura immediata misurerebbe
// solo la velocità della macchina.
func eventually(t *testing.T, timeout time.Duration, cond func() bool, format string, args ...any) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf(format, args...)
}

// ------------------------------------------------------------- l'isolamento

// TestUnJobLentoNonRitardaIJobRapidi è la prova di R3, e la forma del test è
// scelta perché fallisca invece di rallentare.
//
// Cento occorrenze di un job che non finisce mai vengono consegnate **per
// prime**, poi mille job rapidi. Con una FIFO condivisa i primi trentadue
// prelievi sarebbero tutti del job lento, i worker resterebbero bloccati lì e i
// mille rapidi non partirebbero mai: il test non diventerebbe lento, andrebbe in
// timeout. Non c'è nessuna soglia temporale da tarare — o i rapidi passano
// mentre il lento è fermo, o non passano affatto.
func TestUnJobLentoNonRitardaIJobRapidi(t *testing.T) {
	const (
		slowOccurrences = 100
		fastJobs        = 1000
		inFlightPerJob  = 4
	)

	store := newMemStore()
	blocked := make(chan struct{})
	var fast sync.WaitGroup
	fast.Add(fastJobs)

	// Quanti worker il job lento tiene occupati, adesso e al massimo.
	var (
		mu             sync.Mutex
		slow, slowPeak int
	)
	enter := func() {
		mu.Lock()
		defer mu.Unlock()
		slow++
		slowPeak = max(slowPeak, slow)
	}
	leave := func() {
		mu.Lock()
		defer mu.Unlock()
		slow--
	}
	live := func() (int, int) {
		mu.Lock()
		defer mu.Unlock()
		return slow, slowPeak
	}

	exec := dispatch.ExecutorFunc(func(ctx context.Context, occ scheduler.Occurrence) (dispatch.Result, error) {
		if occ.Job.ID == "lento" {
			enter()
			defer leave()
			select {
			case <-blocked:
			case <-ctx.Done():
				return dispatch.Result{}, ctx.Err()
			}
			return dispatch.Result{ResponseStatus: 200}, nil
		}
		fast.Done()
		return dispatch.Result{ResponseStatus: 200}, nil
	})

	pool := newPool(t, dispatch.Options{
		Store:             store,
		Executor:          exec,
		Workers:           32,
		MaxInFlightPerJob: inFlightPerJob,
		QueueDepth:        4096,
		QueueDepthPerJob:  256,
		DrainTimeout:      time.Second,
	})

	for i := range slowOccurrences {
		if err := hand(t, pool, store, fakeOccurrence("lento", i)); err != nil {
			t.Fatalf("consegna dell'occorrenza lenta %d: %v", i, err)
		}
	}
	for i := range fastJobs {
		if err := hand(t, pool, store, fakeOccurrence(fmt.Sprintf("rapido-%d", i), 0)); err != nil {
			t.Fatalf("consegna del job rapido %d: %v", i, err)
		}
	}

	done := make(chan struct{})
	go func() { fast.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		st := pool.Stats()
		t.Fatalf("i job rapidi non sono passati mentre il job lento occupava il pool: %+v", st)
	}

	// L'ultima manciata di rapidi ha appena finito di eseguire: si aspetta che
	// anche il loro esito sia scritto, poi si guarda la fotografia.
	eventually(t, 10*time.Second, func() bool { return pool.Stats().Succeeded == fastJobs },
		"esiti dei job rapidi non registrati: %+v", pool.Stats())

	// Il job lento tiene esattamente il suo tetto di worker, non uno di più: è il
	// numero che lascia gli altri ventotto ai job rapidi.
	inFlight, peak := live()
	if peak != inFlightPerJob {
		t.Fatalf("il job lento ha tenuto fino a %d worker, il tetto è %d", peak, inFlightPerJob)
	}
	if inFlight != inFlightPerJob {
		t.Fatalf("attese %d esecuzioni in volo del job lento, trovate %d", inFlightPerJob, inFlight)
	}
	if st := pool.Stats(); st.Queued != slowOccurrences-inFlightPerJob {
		t.Fatalf("attese %d occorrenze del job lento in attesa, trovate %d",
			slowOccurrences-inFlightPerJob, st.Queued)
	}

	close(blocked)
	eventually(t, 30*time.Second, func() bool {
		return pool.Stats().Succeeded == fastJobs+slowOccurrences
	}, "il job lento non ha smaltito il proprio arretrato: %+v", pool.Stats())
}

// ------------------------------------------------------- il ciclo di vita

func TestUnOccorrenzaEseguitaArrivaAUnoStatoTerminale(t *testing.T) {
	cases := []struct {
		name   string
		result dispatch.Result
		err    error
		want   dispatch.Outcome
	}{
		{"risposta sotto il 400", dispatch.Result{ResponseStatus: 204}, nil, dispatch.Succeeded},
		{"risposta di errore", dispatch.Result{ResponseStatus: 503}, nil, dispatch.Failed},
		{"errore di rete", dispatch.Result{}, errors.New("connessione rifiutata"), dispatch.Failed},
		{"timeout", dispatch.Result{}, context.DeadlineExceeded, dispatch.TimedOut},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := newMemStore()
			pool := newPool(t, dispatch.Options{
				Store:    store,
				Executor: dispatch.ExecutorFunc(func(context.Context, scheduler.Occurrence) (dispatch.Result, error) { return tc.result, tc.err }),
				Workers:  2,
			})

			occ := fakeOccurrence("a", 0)
			if err := hand(t, pool, store, occ); err != nil {
				t.Fatalf("consegna: %v", err)
			}
			eventually(t, 5*time.Second, func() bool {
				return store.statusOf(occ) == string(tc.want)
			}, "atteso lo stato %q, trovato %q", tc.want, store.statusOf(occ))

			if got := store.recordOf(occ).ResponseStatus; got != tc.result.ResponseStatus {
				t.Fatalf("atteso lo status %d, trovato %d", tc.result.ResponseStatus, got)
			}
		})
	}
}

// La riga presa da qualcun altro è il cancello di R4 visto da qui: l'esecutore
// non viene nemmeno chiamato.
func TestUnOccorrenzaGiaPresaNonVieneEseguita(t *testing.T) {
	store := newMemStore()
	var executed int
	var mu sync.Mutex

	pool := newPool(t, dispatch.Options{
		Store: store,
		Executor: dispatch.ExecutorFunc(func(context.Context, scheduler.Occurrence) (dispatch.Result, error) {
			mu.Lock()
			executed++
			mu.Unlock()
			return dispatch.Result{ResponseStatus: 200}, nil
		}),
		Workers: 2,
	})

	occ := fakeOccurrence("a", 0)
	store.seed(occ)
	// Qualcun altro l'ha già portata a `running`.
	if ok, _ := store.Claim(context.Background(), occ); !ok {
		t.Fatal("presa preliminare fallita")
	}

	if err := pool.Dispatch(context.Background(), occ); err != nil {
		t.Fatalf("consegna: %v", err)
	}
	eventually(t, 5*time.Second, func() bool { return pool.Stats().Lost == 1 },
		"l'occorrenza già presa non è stata riconosciuta: %+v", pool.Stats())

	mu.Lock()
	defer mu.Unlock()
	if executed != 0 {
		t.Fatalf("l'esecutore è stato chiamato %d volte su un'occorrenza non nostra", executed)
	}
}

// Un esecutore che va in panico è un bug suo: senza rete si porterebbe dietro il
// processo, cioè tutti gli altri job. Il worker sopravvive e continua a servire.
func TestUnEsecutoreCheVaInPanicoNonFermaIlPool(t *testing.T) {
	store := newMemStore()
	pool := newPool(t, dispatch.Options{
		Store: store,
		Executor: dispatch.ExecutorFunc(func(_ context.Context, occ scheduler.Occurrence) (dispatch.Result, error) {
			if occ.Job.ID == "esplosivo" {
				panic("riferimento nullo nell'esecutore")
			}
			return dispatch.Result{ResponseStatus: 200}, nil
		}),
		Workers:           1,
		MaxInFlightPerJob: 1,
	})

	boom := fakeOccurrence("esplosivo", 0)
	ok := fakeOccurrence("sano", 0)
	for _, occ := range []scheduler.Occurrence{boom, ok} {
		if err := hand(t, pool, store, occ); err != nil {
			t.Fatalf("consegna di %s: %v", occ, err)
		}
	}

	eventually(t, 5*time.Second, func() bool { return store.statusOf(ok) == string(dispatch.Succeeded) },
		"il worker non ha ripreso a lavorare dopo il panico: %+v", pool.Stats())
	if got := store.statusOf(boom); got != string(dispatch.Failed) {
		t.Fatalf("l'occorrenza andata in panico non è stata chiusa: stato %q", got)
	}
}

// Un database che non risponde non deve far eseguire niente né perdere la riga:
// resta `pending`, che è dove il recupero la cerca.
func TestUnaPresaFallitaLasciaLaRigaDovEra(t *testing.T) {
	store := newMemStore()
	store.claimErr = errors.New("connessione al database persa")
	executed := make(chan struct{}, 1)

	pool := newPool(t, dispatch.Options{
		Store: store,
		Executor: dispatch.ExecutorFunc(func(context.Context, scheduler.Occurrence) (dispatch.Result, error) {
			executed <- struct{}{}
			return dispatch.Result{ResponseStatus: 200}, nil
		}),
		Workers: 2,
	})

	occ := fakeOccurrence("a", 0)
	if err := hand(t, pool, store, occ); err != nil {
		t.Fatalf("consegna: %v", err)
	}
	eventually(t, 5*time.Second, func() bool { return pool.Stats().Errors == 1 },
		"l'errore della presa non è stato contato: %+v", pool.Stats())

	if len(executed) != 0 {
		t.Fatal("l'occorrenza è stata eseguita senza essere stata presa")
	}
	if got := store.statusOf(occ); got != "pending" {
		t.Fatalf("atteso lo stato pending, trovato %q", got)
	}
}

// Un job messo in pausa fra l'accodamento e l'esecuzione: l'occorrenza si chiude
// senza partire, che è ciò che `skipped` significa.
func TestUnJobInPausaChiudeLOccorrenzaSenzaEseguirla(t *testing.T) {
	store := newMemStore()
	executed := make(chan struct{}, 1)

	pool := newPool(t, dispatch.Options{
		Store: store,
		Executor: dispatch.ExecutorFunc(func(context.Context, scheduler.Occurrence) (dispatch.Result, error) {
			executed <- struct{}{}
			return dispatch.Result{ResponseStatus: 200}, nil
		}),
		Workers: 2,
	})

	occ := fakeOccurrence("in-pausa", 0)
	occ.Job.Enabled = false
	occ.Recovered = true

	if err := hand(t, pool, store, occ); err != nil {
		t.Fatalf("consegna: %v", err)
	}
	eventually(t, 5*time.Second, func() bool { return store.statusOf(occ) == "skipped" },
		"atteso lo stato skipped, trovato %q", store.statusOf(occ))

	if len(executed) != 0 {
		t.Fatal("un job in pausa è stato eseguito")
	}
	if reason := store.recordOf(occ).Error.String(); !strings.Contains(reason, "pausa") {
		t.Fatalf("il motivo dello scarto non dice che il job è in pausa: %q", reason)
	}
}

// R38: il punto in cui la issue #455 innesterà il rifiuto degli indirizzi
// interni. Ciò che questo package garantisce è che il guard venga chiamato
// prima dell'esecutore e che un rifiuto lasci un'esecuzione visibile.
func TestUnaDestinazioneRifiutataNonRaggiungeLEsecutore(t *testing.T) {
	store := newMemStore()
	executed := make(chan struct{}, 1)

	pool := newPool(t, dispatch.Options{
		Store: store,
		Executor: dispatch.ExecutorFunc(func(context.Context, scheduler.Occurrence) (dispatch.Result, error) {
			executed <- struct{}{}
			return dispatch.Result{ResponseStatus: 200}, nil
		}),
		Guard: dispatch.GuardFunc(func(_ context.Context, occ scheduler.Occurrence) error {
			return errors.New("169.254.169.254 è un indirizzo riservato")
		}),
		Workers: 2,
	})

	occ := fakeOccurrence("verso-linterno", 0)
	if err := hand(t, pool, store, occ); err != nil {
		t.Fatalf("consegna: %v", err)
	}
	eventually(t, 5*time.Second, func() bool { return store.statusOf(occ) == string(dispatch.Failed) },
		"atteso lo stato failed, trovato %q", store.statusOf(occ))

	if len(executed) != 0 {
		t.Fatal("l'esecutore ha ricevuto un'occorrenza rifiutata dal guard")
	}
	if got := store.recordOf(occ).Error.String(); !strings.Contains(got, "169.254.169.254") {
		t.Fatalf("l'errore registrato non riporta il motivo del rifiuto: %q", got)
	}
	if st := pool.Stats(); st.Blocked != 1 {
		t.Fatalf("atteso 1 rifiuto contato, trovati %d", st.Blocked)
	}
}

// ------------------------------------------------------------ la redazione (R43)

// TestNellaRigaVaIlTestoRedattoDallEsecutoreNonIlSuoErrore è la quarta clausola
// di [dispatch.Executor], ed è ciò che rende la issue #496 utile fra sei mesi.
//
// L'esecutore restituisce due cose diverse: un `error`, che questo package usa
// per *classificare* l'esito, e un [dispatch.Result.ErrorText] già redatto, che è
// il testo da scrivere. Se `record` tornasse a scrivere `err.Error()` — la riga
// che c'era prima di questa issue, e che somiglia moltissimo a quella giusta —
// il valore di un segreto risolto finirebbe nella colonna `error`, che l'utente
// rilegge dall'API.
//
// Il compilatore ne difende metà: `rec.Error` è un [secrets.Excerpt] e una
// stringa non ci entra. Questa prova difende l'altra metà, cioè che il testo
// scritto sia proprio quello che l'esecutore ha redatto.
func TestNellaRigaVaIlTestoRedattoDallEsecutoreNonIlSuoErrore(t *testing.T) {
	store := newMemStore()

	// L'errore grezzo contiene la credenziale, come quello di un driver o di una
	// libreria TLS che cita l'indirizzo a cui stava andando; il testo redatto no.
	const credenziale = "finta-credenziale-del-cliente"
	pool := newPool(t, dispatch.Options{
		Store: store,
		Executor: dispatch.ExecutorFunc(func(context.Context, scheduler.Occurrence) (dispatch.Result, error) {
			return dispatch.Result{ErrorText: estratto("richiesta non riuscita: dial tcp ${TOKEN}")},
				errors.New("richiesta non riuscita: dial tcp " + credenziale)
		}),
		Workers: 2,
	})

	occ := fakeOccurrence("con-segreto", 0)
	if err := hand(t, pool, store, occ); err != nil {
		t.Fatalf("consegna: %v", err)
	}
	eventually(t, 5*time.Second, func() bool { return store.statusOf(occ) == string(dispatch.Failed) },
		"atteso lo stato failed, trovato %q", store.statusOf(occ))

	got := store.recordOf(occ).Error.String()
	if strings.Contains(got, credenziale) {
		t.Fatalf("il messaggio dell'errore è finito sulla riga, credenziale compresa: %q", got)
	}
	if !strings.Contains(got, "${TOKEN}") {
		t.Fatalf("sulla riga non c'è il testo redatto dall'esecutore: %q", got)
	}
}

// TestUnEsecutoreSenzaTestoLasciaComunqueUnaRigaLeggibile: il ripiego del caso
// in cui la quarta clausola non viene rispettata. La riga si chiude lo stesso —
// un'esecuzione senza esito sarebbe peggio — e dice che il dettaglio non è
// arrivato, invece di ripescare il messaggio dell'errore.
func TestUnEsecutoreSenzaTestoLasciaComunqueUnaRigaLeggibile(t *testing.T) {
	store := newMemStore()
	pool := newPool(t, dispatch.Options{
		Store: store,
		Executor: dispatch.ExecutorFunc(func(context.Context, scheduler.Occurrence) (dispatch.Result, error) {
			return dispatch.Result{}, errors.New("finta-credenziale-del-cliente non valida")
		}),
		Workers: 2,
	})

	occ := fakeOccurrence("senza-testo", 0)
	if err := hand(t, pool, store, occ); err != nil {
		t.Fatalf("consegna: %v", err)
	}
	eventually(t, 5*time.Second, func() bool { return store.statusOf(occ) == string(dispatch.Failed) },
		"atteso lo stato failed, trovato %q", store.statusOf(occ))

	got := store.recordOf(occ).Error.String()
	if strings.Contains(got, "finta-credenziale-del-cliente") {
		t.Fatalf("il messaggio dell'errore è finito sulla riga: %q", got)
	}
	if got == "" {
		t.Fatal("l'esecuzione è fallita senza nessun motivo scritto")
	}
}

// ------------------------------------------------------------ la contropressione

func TestLaCodaPienaVieneDettaAlloScheduler(t *testing.T) {
	store := newMemStore()
	pool, err := dispatch.New(dispatch.Options{
		Store:            store,
		Executor:         dispatch.ExecutorFunc(func(context.Context, scheduler.Occurrence) (dispatch.Result, error) { return dispatch.Result{}, nil }),
		Logger:           testLogger(t),
		QueueDepth:       2,
		QueueDepthPerJob: 2,
	})
	if err != nil {
		t.Fatalf("dispatch.New: %v", err)
	}
	// Nessun worker avviato: la coda si riempie e resta piena.

	for i := range 2 {
		if err := pool.Dispatch(context.Background(), fakeOccurrence("a", i)); err != nil {
			t.Fatalf("consegna %d: %v", i, err)
		}
	}
	if err := pool.Dispatch(context.Background(), fakeOccurrence("a", 2)); !errors.Is(err, dispatch.ErrQueueFull) {
		t.Fatalf("atteso ErrQueueFull, ottenuto %v", err)
	}
	if st := pool.Stats(); st.Refused != 1 {
		t.Fatalf("atteso 1 rifiuto contato, trovati %d", st.Refused)
	}
}

// La riofferta di un'occorrenza già in carico non è un rifiuto: dirlo allo
// scheduler come errore gli farebbe scrivere che il dispatch ha respinto del
// lavoro, che è falso.
func TestUnaRiofferataNonVieneSegnalataComeRifiuto(t *testing.T) {
	store := newMemStore()
	release := make(chan struct{})
	pool := newPool(t, dispatch.Options{
		Store: store,
		Executor: dispatch.ExecutorFunc(func(ctx context.Context, occ scheduler.Occurrence) (dispatch.Result, error) {
			<-release
			return dispatch.Result{ResponseStatus: 200}, nil
		}),
		Workers:      2,
		DrainTimeout: time.Second,
	})
	t.Cleanup(func() { close(release) })

	occ := fakeOccurrence("a", 0)
	if err := hand(t, pool, store, occ); err != nil {
		t.Fatalf("prima consegna: %v", err)
	}
	eventually(t, 5*time.Second, func() bool { return pool.Stats().Claimed == 1 },
		"l'occorrenza non è mai stata presa")

	if err := pool.Dispatch(context.Background(), occ); err != nil {
		t.Fatalf("seconda consegna: atteso nessun errore, ottenuto %v", err)
	}
	st := pool.Stats()
	if st.Duplicated != 1 {
		t.Fatalf("attesa 1 riofferta contata, trovate %d", st.Duplicated)
	}
	if st.Refused != 0 {
		t.Fatalf("la riofferta è stata contata come rifiuto: %+v", st)
	}
}

// Run è la forma che userà chi fa girare il pool accanto allo scheduler: la
// chiusura del contesto deve fermarlo, e l'arresto che ne segue è quello
// ordinario.
func TestRunSiFermaAllaChiusuraDelContesto(t *testing.T) {
	store := newMemStore()
	pool, err := dispatch.New(dispatch.Options{
		Store: store,
		Executor: dispatch.ExecutorFunc(func(context.Context, scheduler.Occurrence) (dispatch.Result, error) {
			return dispatch.Result{ResponseStatus: 200}, nil
		}),
		Logger:  testLogger(t),
		Workers: 4,
	})
	if err != nil {
		t.Fatalf("dispatch.New: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	stopped := make(chan error, 1)
	go func() { stopped <- pool.Run(ctx) }()

	occ := fakeOccurrence("a", 0)
	store.seed(occ)
	if err := pool.Dispatch(context.Background(), occ); err != nil {
		t.Fatalf("consegna: %v", err)
	}
	eventually(t, 5*time.Second, func() bool { return store.statusOf(occ) == string(dispatch.Succeeded) },
		"il pool avviato da Run non ha eseguito l'occorrenza")

	cancel()
	select {
	case err := <-stopped:
		if err != nil {
			t.Fatalf("arresto: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run non è tornata alla chiusura del contesto")
	}
}

func TestDopoLArrestoIlDispatchRifiuta(t *testing.T) {
	store := newMemStore()
	pool := newPool(t, dispatch.Options{
		Store:    store,
		Executor: dispatch.ExecutorFunc(func(context.Context, scheduler.Occurrence) (dispatch.Result, error) { return dispatch.Result{}, nil }),
		Workers:  2,
	})

	if err := pool.Shutdown(context.Background()); err != nil {
		t.Fatalf("arresto: %v", err)
	}
	if err := pool.Dispatch(context.Background(), fakeOccurrence("a", 0)); !errors.Is(err, dispatch.ErrClosed) {
		t.Fatalf("atteso ErrClosed, ottenuto %v", err)
	}
}

// ---------------------------------------------------------------- l'arresto

func TestLArrestoAspettaLeEsecuzioniInVolo(t *testing.T) {
	store := newMemStore()
	pool := newPool(t, dispatch.Options{
		Store: store,
		Executor: dispatch.ExecutorFunc(func(ctx context.Context, occ scheduler.Occurrence) (dispatch.Result, error) {
			select {
			case <-time.After(200 * time.Millisecond):
				return dispatch.Result{ResponseStatus: 200}, nil
			case <-ctx.Done():
				return dispatch.Result{}, ctx.Err()
			}
		}),
		Workers:      4,
		DrainTimeout: 10 * time.Second,
	})

	occ := fakeOccurrence("a", 0)
	if err := hand(t, pool, store, occ); err != nil {
		t.Fatalf("consegna: %v", err)
	}
	eventually(t, 5*time.Second, func() bool { return pool.Stats().Claimed == 1 },
		"l'occorrenza non è mai stata presa")

	if err := pool.Shutdown(context.Background()); err != nil {
		t.Fatalf("arresto: %v", err)
	}
	if got := store.statusOf(occ); got != string(dispatch.Succeeded) {
		t.Fatalf("l'esecuzione in volo non è stata portata a termine: stato %q", got)
	}
}

// Ciò che non fa in tempo torna `pending`: è la forma in cui il recupero dello
// scheduler lo ritrova. Una riga lasciata `running` non la chiuderebbe più
// nessuno.
func TestLArrestoRilasciaCioCheNonFaInTempo(t *testing.T) {
	store := newMemStore()
	pool := newPool(t, dispatch.Options{
		Store: store,
		Executor: dispatch.ExecutorFunc(func(ctx context.Context, occ scheduler.Occurrence) (dispatch.Result, error) {
			<-ctx.Done()
			return dispatch.Result{}, ctx.Err()
		}),
		Workers:      4,
		DrainTimeout: 100 * time.Millisecond,
	})

	occ := fakeOccurrence("interminabile", 0)
	if err := hand(t, pool, store, occ); err != nil {
		t.Fatalf("consegna: %v", err)
	}
	eventually(t, 5*time.Second, func() bool { return pool.Stats().Claimed == 1 },
		"l'occorrenza non è mai stata presa")

	err := pool.Shutdown(context.Background())
	if err == nil {
		t.Fatal("l'arresto non ha segnalato le esecuzioni rilasciate")
	}
	if got := store.statusOf(occ); got != "pending" {
		t.Fatalf("l'esecuzione interrotta non è tornata in attesa: stato %q", got)
	}
	if st := pool.Stats(); st.Released != 1 {
		t.Fatalf("atteso 1 rilascio contato, trovati %d", st.Released)
	}
}

// Le occorrenze mai prese non si toccano: la loro riga è già `pending`, che è
// esattamente dove il recupero le cerca.
func TestLeOccorrenzeInAttesaRestanoIntoccate(t *testing.T) {
	store := newMemStore()
	blocked := make(chan struct{})
	pool := newPool(t, dispatch.Options{
		Store: store,
		Executor: dispatch.ExecutorFunc(func(ctx context.Context, occ scheduler.Occurrence) (dispatch.Result, error) {
			select {
			case <-blocked:
			case <-ctx.Done():
			}
			return dispatch.Result{ResponseStatus: 200}, nil
		}),
		Workers:           1,
		MaxInFlightPerJob: 1,
		DrainTimeout:      100 * time.Millisecond,
	})
	t.Cleanup(func() { close(blocked) })

	inFlight := fakeOccurrence("a", 0)
	waiting := fakeOccurrence("a", 1)
	for _, occ := range []scheduler.Occurrence{inFlight, waiting} {
		if err := hand(t, pool, store, occ); err != nil {
			t.Fatalf("consegna di %s: %v", occ, err)
		}
	}
	eventually(t, 5*time.Second, func() bool { return pool.Stats().Claimed == 1 },
		"la prima occorrenza non è mai stata presa")

	_ = pool.Shutdown(context.Background())

	if got := store.statusOf(waiting); got != "pending" {
		t.Fatalf("l'occorrenza mai presa è stata toccata: stato %q", got)
	}
}
