package dispatch_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/apdsoftware/postqron/services/api/internal/dispatch"
	"github.com/apdsoftware/postqron/services/api/internal/scheduler"
)

// Questo file mette insieme i due package sul database vero. I test di
// pool_test.go provano il comportamento del pool, quelli di postgres_test.go le
// transizioni di stato; qui si verifica che il contratto fra scheduler e
// dispatch regga davvero da un capo all'altro — compresa la parte in cui la
// riga rilasciata dall'arresto ritorna, che è l'unico modo di dimostrare che
// «il recupero la ritrova» non è solo un'intenzione.

func newEngine(t *testing.T, pool *pgxpool.Pool, d scheduler.Dispatcher) *scheduler.Engine {
	t.Helper()
	engine, err := scheduler.New(scheduler.Options{
		Pool:       pool,
		Dispatcher: d,
		Logger:     testLogger(t),
	})
	if err != nil {
		t.Fatalf("scheduler.New: %v", err)
	}
	return engine
}

func TestLoSchedulerAccodaEIlPoolPortaLOccorrenzaATermine(t *testing.T) {
	db := newTestDatabase(t)
	user := createUser(t, db)
	past := time.Now().UTC().Add(-time.Second).Truncate(time.Second)
	job := createJob(t, db, user, jobSpec{Every: time.Minute, NextRunAt: &past, Enabled: true})

	var (
		mu       sync.Mutex
		received []scheduler.Occurrence
	)
	pool := newPool(t, dispatch.Options{
		Store: dispatch.NewPostgresStore(db),
		Executor: dispatch.ExecutorFunc(func(_ context.Context, occ scheduler.Occurrence) (dispatch.Result, error) {
			mu.Lock()
			received = append(received, occ)
			mu.Unlock()
			return dispatch.Result{ResponseStatus: 200, ResponseExcerpt: "ok"}, nil
		}),
		Workers:      4,
		DrainTimeout: 5 * time.Second,
	})

	engine := newEngine(t, db, pool)
	st, err := engine.Tick(t.Context())
	if err != nil {
		t.Fatalf("passata dello scheduler: %v", err)
	}
	if st.Enqueued != 1 {
		t.Fatalf("attesa 1 occorrenza accodata, ottenute %d (%+v)", st.Enqueued, st)
	}

	eventually(t, 10*time.Second, func() bool { return pool.Stats().Succeeded == 1 },
		"l'occorrenza non è arrivata a termine: %+v", pool.Stats())

	mu.Lock()
	defer mu.Unlock()
	if len(received) != 1 {
		t.Fatalf("l'esecutore ha ricevuto %d occorrenze", len(received))
	}
	row := readExecution(t, db, received[0])
	if row.Status != "succeeded" {
		t.Fatalf("atteso lo stato succeeded, trovato %q", row.Status)
	}
	if row.ResponseStatus == nil || *row.ResponseStatus != 200 {
		t.Fatalf("status della risposta non registrato: %v", row.ResponseStatus)
	}
	// Il job ha ricevuto il suo target e la sua identità: il dispatch non
	// ricarica niente dal database, gli arriva tutto dallo scheduler.
	if received[0].Job.URL == "" || received[0].Job.ID != job {
		t.Fatalf("occorrenza incompleta: %+v", received[0].Job)
	}
}

// L'arresto pulito visto da entrambe le parti: ciò che il pool rilascia, il
// recupero dello scheduler lo ritrova e lo riconsegna. È la proprietà che
// impedisce a un riavvio di lasciare esecuzioni appese per sempre.
func TestCioCheLArrestoRilasciaIlRecuperoLoRitrova(t *testing.T) {
	db := newTestDatabase(t)
	user := createUser(t, db)
	past := time.Now().UTC().Add(-time.Second).Truncate(time.Second)
	createJob(t, db, user, jobSpec{Every: time.Minute, NextRunAt: &past, Enabled: true})

	store := dispatch.NewPostgresStore(db)

	// Primo processo: prende l'occorrenza e non la finisce mai.
	first, err := dispatch.New(dispatch.Options{
		Store: store,
		Executor: dispatch.ExecutorFunc(func(ctx context.Context, _ scheduler.Occurrence) (dispatch.Result, error) {
			<-ctx.Done()
			return dispatch.Result{}, ctx.Err()
		}),
		Workers:      4,
		DrainTimeout: 100 * time.Millisecond,
		Logger:       testLogger(t),
	})
	if err != nil {
		t.Fatalf("dispatch.New: %v", err)
	}
	first.Start()

	engine := newEngine(t, db, first)
	if _, err := engine.Tick(t.Context()); err != nil {
		t.Fatalf("passata dello scheduler: %v", err)
	}
	eventually(t, 10*time.Second, func() bool { return first.Stats().Claimed == 1 },
		"l'occorrenza non è mai stata presa: %+v", first.Stats())

	if err := first.Shutdown(context.Background()); err == nil {
		t.Fatal("l'arresto non ha segnalato l'esecuzione rilasciata")
	}
	if st := first.Stats(); st.Released != 1 {
		t.Fatalf("atteso 1 rilascio, trovati %d", st.Released)
	}

	// Secondo processo: parte, recupera, e questa volta porta a termine.
	second := newPool(t, dispatch.Options{
		Store: store,
		Executor: dispatch.ExecutorFunc(func(context.Context, scheduler.Occurrence) (dispatch.Result, error) {
			return dispatch.Result{ResponseStatus: 200}, nil
		}),
		Workers:      4,
		DrainTimeout: 5 * time.Second,
	})

	recovered := newEngine(t, db, second)
	if err := recovered.Recover(t.Context()); err != nil {
		t.Fatalf("recupero: %v", err)
	}
	eventually(t, 10*time.Second, func() bool { return second.Stats().Succeeded == 1 },
		"l'occorrenza rilasciata non è stata ripresa: %+v", second.Stats())

	var statuses []string
	rows, err := db.Query(t.Context(), `SELECT status::text FROM job_executions`)
	if err != nil {
		t.Fatalf("lettura delle esecuzioni: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			t.Fatalf("lettura di un'esecuzione: %v", err)
		}
		statuses = append(statuses, s)
	}
	if len(statuses) != 1 || statuses[0] != "succeeded" {
		t.Fatalf("attesa una sola esecuzione riuscita, trovate %v", statuses)
	}
}

// Due pool sullo stesso database — due repliche del motore, o lo stesso
// processo dopo una ripresa — ricevono la stessa occorrenza. La esegue uno solo,
// e non per accordo fra loro: perché l'UPDATE condizionato riesce una volta
// sola.
func TestDuePoolSullaStessaOccorrenzaLaEseguonoUnaVoltaSola(t *testing.T) {
	db := newTestDatabase(t)
	user := createUser(t, db)
	job := createJob(t, db, user, jobSpec{Enabled: true})
	occ := newOccurrence(t, db, job, time.Now().UTC().Truncate(time.Second))

	var (
		mu       sync.Mutex
		executed int
	)
	exec := dispatch.ExecutorFunc(func(context.Context, scheduler.Occurrence) (dispatch.Result, error) {
		mu.Lock()
		executed++
		mu.Unlock()
		return dispatch.Result{ResponseStatus: 200}, nil
	})

	store := dispatch.NewPostgresStore(db)
	pools := make([]*dispatch.Pool, 2)
	for i := range pools {
		pools[i] = newPool(t, dispatch.Options{
			Store: store, Executor: exec, Workers: 4, DrainTimeout: 5 * time.Second,
		})
	}
	for i, p := range pools {
		if err := p.Dispatch(t.Context(), occ); err != nil {
			t.Fatalf("consegna al pool %d: %v", i, err)
		}
	}

	eventually(t, 10*time.Second, func() bool {
		first, second := pools[0].Stats(), pools[1].Stats()
		return first.Claimed+second.Claimed == 1 &&
			first.Lost+second.Lost == 1 &&
			first.Succeeded+second.Succeeded == 1
	}, "le due prese non si sono escluse: %+v / %+v", pools[0].Stats(), pools[1].Stats())

	mu.Lock()
	defer mu.Unlock()
	if executed != 1 {
		t.Fatalf("l'occorrenza è stata eseguita %d volte", executed)
	}
	if row := readExecution(t, db, occ); row.Status != "succeeded" {
		t.Fatalf("atteso lo stato succeeded, trovato %q", row.Status)
	}
}
