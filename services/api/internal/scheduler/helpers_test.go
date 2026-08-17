package scheduler_test

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/apdsoftware/postqron/services/api/internal/config"
	"github.com/apdsoftware/postqron/services/api/internal/database"
	"github.com/apdsoftware/postqron/services/api/internal/dotenv"
	"github.com/apdsoftware/postqron/services/api/internal/migrate"
	"github.com/apdsoftware/postqron/services/api/internal/scheduler"
)

// TestMain carica il `.env` del monorepo: le POSTGRES_* che descrivono il
// database di sviluppo stanno lì (AGENTS.md §7).
func TestMain(m *testing.M) {
	workdir, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if _, err := dotenv.LoadNearest(workdir); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	os.Exit(m.Run())
}

var databaseCounter atomic.Int64

// newTestDatabase crea un database usa e getta con lo schema applicato.
//
// Se PostgreSQL non è raggiungibile il test viene **saltato** invece che
// fallito: `make ci` deve restare verde su una macchina senza `make db-up`. Il
// messaggio di skip dice cosa non è stato verificato — qui è quasi tutto, perché
// l'idempotenza di questo package è una proprietà del database e non si può
// provare senza.
func newTestDatabase(t *testing.T) *pgxpool.Pool {
	t.Helper()
	ctx := t.Context()

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("configurazione non valida: %v", err)
	}

	admin, err := database.Open(ctx, cfg.Postgres, database.Options{
		MaxConns:        1,
		ApplicationName: "postqron-test-admin",
	})
	if err != nil {
		t.Skipf("PostgreSQL non raggiungibile su %s:%s, test saltato (avvia `make db-up`): %v",
			cfg.Postgres.Host, cfg.Postgres.Port, err)
	}
	defer admin.Close()

	name := testDatabaseName(t)
	if _, err := admin.Exec(ctx, fmt.Sprintf("DROP DATABASE IF EXISTS %q WITH (FORCE)", name)); err != nil {
		t.Fatalf("pulizia del database di prova: %v", err)
	}
	if _, err := admin.Exec(ctx, fmt.Sprintf("CREATE DATABASE %q", name)); err != nil {
		t.Skipf("creazione del database di prova non consentita, test saltato: %v", err)
	}

	target := cfg.Postgres
	target.Database = name
	pool, err := database.Open(ctx, target, database.Options{
		MaxConns:        8,
		ApplicationName: "postqron-test-scheduler",
	})
	if err != nil {
		t.Fatalf("connessione al database di prova: %v", err)
	}

	t.Cleanup(func() {
		pool.Close()
		// Il contesto del test è già chiuso quando gira il cleanup.
		cleanupCtx := context.Background()
		cleaner, err := database.Open(cleanupCtx, cfg.Postgres, database.Options{MaxConns: 1})
		if err != nil {
			t.Logf("database %q non eliminato: %v", name, err)
			return
		}
		defer cleaner.Close()
		if _, err := cleaner.Exec(cleanupCtx,
			fmt.Sprintf("DROP DATABASE IF EXISTS %q WITH (FORCE)", name)); err != nil {
			t.Logf("database %q non eliminato: %v", name, err)
		}
	})

	applyMigrations(t, pool)
	return pool
}

// testDatabaseName compone un nome legale: il limite di PostgreSQL è 63 byte.
func testDatabaseName(t *testing.T) string {
	t.Helper()
	sanitized := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			return r
		case r >= 'A' && r <= 'Z':
			return r + ('a' - 'A')
		default:
			return '_'
		}
	}, t.Name())

	name := fmt.Sprintf("pq_sched_%d_%d_%s", os.Getpid(), databaseCounter.Add(1), sanitized)
	if len(name) > 63 {
		name = name[:63]
	}
	return name
}

// applyMigrations applica le migrazioni reali del repository: i test girano
// contro lo schema che va in produzione, indici e partizioni compresi.
func applyMigrations(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()

	dir, err := migrate.FindDir(".")
	if err != nil {
		t.Fatalf("directory delle migrazioni non trovata: %v", err)
	}
	migrations, err := migrate.Load(dir)
	if err != nil {
		t.Fatalf("caricamento delle migrazioni: %v", err)
	}

	conn, err := pool.Acquire(t.Context())
	if err != nil {
		t.Fatalf("acquisizione della connessione: %v", err)
	}
	t.Cleanup(conn.Release)

	migrator := migrate.New(conn, migrations, nil)
	if _, err := migrator.Up(t.Context(), 0); err != nil {
		t.Fatalf("applicazione delle migrazioni: %v", err)
	}
}

// ------------------------------------------------------------------- fixture

// createUser crea l'utente proprietario dei job di prova.
func createUser(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	var id string
	err := pool.QueryRow(t.Context(),
		`INSERT INTO users (email, full_name) VALUES ($1, 'Mario Rossi') RETURNING id::text`,
		fmt.Sprintf("mario.rossi+%d@example.com", databaseCounter.Add(1)),
	).Scan(&id)
	if err != nil {
		t.Fatalf("creazione dell'utente: %v", err)
	}
	return id
}

// jobSpec descrive un job di prova. I campi a zero prendono un default sensato.
type jobSpec struct {
	Name         string
	Expression   string
	Every        time.Duration
	Timezone     string
	Environments []string
	NextRunAt    *time.Time
	Enabled      *bool
}

// createJob inserisce un job. `schedule` ed `every_seconds` restano mutuamente
// esclusivi: è il database a pretenderlo.
func createJob(t *testing.T, pool *pgxpool.Pool, userID string, spec jobSpec) string {
	t.Helper()

	if spec.Name == "" {
		spec.Name = fmt.Sprintf("job-%d", databaseCounter.Add(1))
	}
	if spec.Expression == "" && spec.Every == 0 {
		spec.Every = time.Minute
	}
	if spec.Timezone == "" {
		spec.Timezone = "UTC"
	}
	if len(spec.Environments) == 0 {
		spec.Environments = []string{"production"}
	}
	enabled := true
	if spec.Enabled != nil {
		enabled = *spec.Enabled
	}

	var (
		expression *string
		every      *int32
	)
	if spec.Expression != "" {
		expression = &spec.Expression
	} else {
		seconds := int32(spec.Every / time.Second)
		every = &seconds
	}

	var id string
	err := pool.QueryRow(t.Context(),
		`INSERT INTO jobs (user_id, name, schedule, every_seconds, timezone,
		                   environments, url, method, next_run_at, enabled)
		 VALUES ($1, $2, $3, $4, $5, $6::text[]::environment[], 'https://example.com/hook',
		         'POST', $7, $8)
		 RETURNING id::text`,
		userID, spec.Name, expression, every, spec.Timezone,
		spec.Environments, spec.NextRunAt, enabled,
	).Scan(&id)
	if err != nil {
		t.Fatalf("creazione del job %q: %v", spec.Name, err)
	}
	return id
}

// nextRunAt legge la prossima occorrenza di un job.
func nextRunAt(t *testing.T, pool *pgxpool.Pool, jobID string) *time.Time {
	t.Helper()
	var at *time.Time
	if err := pool.QueryRow(t.Context(),
		`SELECT next_run_at FROM jobs WHERE id = $1`, jobID).Scan(&at); err != nil {
		t.Fatalf("lettura di next_run_at: %v", err)
	}
	return at
}

// executionRow è una riga di `job_executions` come serve alle asserzioni.
type executionRow struct {
	JobID        string
	ScheduledFor time.Time
	Environment  string
	Attempt      int16
	Status       string
}

func (r executionRow) key() string {
	return fmt.Sprintf("%s|%s|%s|%d", r.JobID, r.ScheduledFor.UTC().Format(time.RFC3339Nano), r.Environment, r.Attempt)
}

// executions legge tutte le esecuzioni registrate, in ordine.
func executions(t *testing.T, pool *pgxpool.Pool) []executionRow {
	t.Helper()
	rows, err := pool.Query(t.Context(),
		`SELECT job_id::text, scheduled_for, environment::text, attempt, status::text
		   FROM job_executions ORDER BY scheduled_for, environment, attempt`)
	if err != nil {
		t.Fatalf("lettura delle esecuzioni: %v", err)
	}
	defer rows.Close()

	var out []executionRow
	for rows.Next() {
		var r executionRow
		if err := rows.Scan(&r.JobID, &r.ScheduledFor, &r.Environment, &r.Attempt, &r.Status); err != nil {
			t.Fatalf("lettura di un'esecuzione: %v", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("lettura delle esecuzioni: %v", err)
	}
	return out
}

// ---------------------------------------------------------------- dispatcher

// recorder è un dispatch che si limita a prendere nota. È anche l'unico posto in
// cui i test possono osservare i duplicati: due consegne della stessa occorrenza
// finiscono qui, anche se sul database la riga è una sola.
type recorder struct {
	mu       sync.Mutex
	received []scheduler.Occurrence
	// fail, se impostata, decide quali occorrenze rifiutare.
	fail func(scheduler.Occurrence) error
}

func (r *recorder) Dispatch(_ context.Context, occ scheduler.Occurrence) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.fail != nil {
		if err := r.fail(occ); err != nil {
			return err
		}
	}
	r.received = append(r.received, occ)
	return nil
}

func (r *recorder) all() []scheduler.Occurrence {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]scheduler.Occurrence, len(r.received))
	copy(out, r.received)
	return out
}

func (r *recorder) count() int { return len(r.all()) }

// keys sono le occorrenze ricevute nella forma della chiave naturale, per
// scoprire i duplicati.
func (r *recorder) keys() []string {
	occs := r.all()
	out := make([]string, 0, len(occs))
	for _, occ := range occs {
		out = append(out, fmt.Sprintf("%s|%s|%s|%d",
			occ.Job.ID, occ.ScheduledFor.UTC().Format(time.RFC3339Nano), occ.Environment, occ.Attempt))
	}
	return out
}

// duplicates elenca le chiavi consegnate più di una volta.
func duplicates(keys []string) []string {
	seen := map[string]int{}
	for _, k := range keys {
		seen[k]++
	}
	var out []string
	for k, n := range seen {
		if n > 1 {
			out = append(out, fmt.Sprintf("%s ×%d", k, n))
		}
	}
	return out
}

// ------------------------------------------------------------------- logger

// testWriter porta i log del motore dentro l'output del test, senza scriverci
// dopo che è finito: `t.Log` da una goroutine sopravvissuta al test fa panic.
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

// newEngine costruisce un motore con l'orologio sotto controllo del test.
func newEngine(t *testing.T, pool *pgxpool.Pool, d scheduler.Dispatcher, now *time.Time, mutate func(*scheduler.Options)) *scheduler.Engine {
	t.Helper()
	opts := scheduler.Options{
		Pool:       pool,
		Dispatcher: d,
		Now:        func() time.Time { return *now },
		Logger:     testLogger(t),
	}
	if mutate != nil {
		mutate(&opts)
	}
	engine, err := scheduler.New(opts)
	if err != nil {
		t.Fatalf("scheduler.New: %v", err)
	}
	return engine
}
