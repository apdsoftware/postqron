package dispatch_test

import (
	"context"
	"fmt"
	"os"
	"strings"
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
// fallito: `make ci` deve restare verde su una macchina senza `make db-up`. Ciò
// che si perde saltando è la parte di R4 che vive nel database — la transizione
// atomica da `pending` a `running` — che è una proprietà di PostgreSQL e non si
// può provare altrove. L'isolamento di R3, invece, resta coperto: sta nella
// coda, e pool_test.go lo prova senza database.
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
		ApplicationName: "postqron-test-dispatch",
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
	ensurePartitions(t, pool)
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

	name := fmt.Sprintf("pq_disp_%d_%d_%s", os.Getpid(), databaseCounter.Add(1), sanitized)
	if len(name) > 63 {
		name = name[:63]
	}
	return name
}

// applyMigrations applica le migrazioni reali del repository: i test girano
// contro lo schema che va in produzione, vincoli e partizioni compresi.
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

// ensurePartitions prepara le partizioni giornaliere: senza, l'inserimento di
// un'occorrenza fallisce (migrazione 0006).
func ensurePartitions(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	if _, err := pool.Exec(t.Context(), `SELECT job_executions_ensure_partitions()`); err != nil {
		t.Fatalf("preparazione delle partizioni: %v", err)
	}
}

// ------------------------------------------------------------------- fixture

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
	Name      string
	Every     time.Duration
	NextRunAt *time.Time
	Enabled   bool
}

func createJob(t *testing.T, pool *pgxpool.Pool, userID string, spec jobSpec) string {
	t.Helper()

	if spec.Name == "" {
		spec.Name = fmt.Sprintf("job-%d", databaseCounter.Add(1))
	}
	if spec.Every == 0 {
		spec.Every = time.Minute
	}

	var id string
	err := pool.QueryRow(t.Context(),
		`INSERT INTO jobs (user_id, name, every_seconds, timezone, environments,
		                   url, method, next_run_at, enabled)
		 VALUES ($1, $2, $3, 'UTC', ARRAY['production']::environment[],
		         'https://example.com/hook', 'POST', $4, $5)
		 RETURNING id::text`,
		userID, spec.Name, int32(spec.Every/time.Second), spec.NextRunAt, spec.Enabled,
	).Scan(&id)
	if err != nil {
		t.Fatalf("creazione del job %q: %v", spec.Name, err)
	}
	return id
}

// newOccurrence costruisce l'occorrenza e la sua riga `pending`, cioè lo stato
// esatto in cui lo scheduler la consegna al dispatch.
func newOccurrence(t *testing.T, pool *pgxpool.Pool, jobID string, at time.Time) scheduler.Occurrence {
	t.Helper()
	if _, err := pool.Exec(t.Context(),
		`INSERT INTO job_executions (job_id, scheduled_for, environment, status, triggered_by)
		 VALUES ($1::uuid, $2, 'production', 'pending', 'schedule')`, jobID, at); err != nil {
		t.Fatalf("inserimento dell'occorrenza: %v", err)
	}
	return scheduler.Occurrence{
		Job: scheduler.Job{
			ID:      jobID,
			Name:    "job-di-prova",
			URL:     "https://example.com/hook",
			Method:  "POST",
			Timeout: 30 * time.Second,
			Enabled: true,
		},
		ScheduledFor: at,
		Environment:  "production",
		Attempt:      1,
	}
}

// executionRow è la riga di `job_executions` come serve alle asserzioni.
type executionRow struct {
	Status          string
	StartedAt       *time.Time
	FinishedAt      *time.Time
	DurationMS      *int32
	ResponseStatus  *int16
	ResponseExcerpt *string
	Error           *string
}

func readExecution(t *testing.T, pool *pgxpool.Pool, occ scheduler.Occurrence) executionRow {
	t.Helper()
	var row executionRow
	err := pool.QueryRow(t.Context(),
		`SELECT status::text, started_at, finished_at, duration_ms,
		        response_status, response_excerpt, error
		   FROM job_executions
		  WHERE job_id = $1::uuid AND scheduled_for = $2
		    AND environment = $3::environment AND attempt = $4`,
		occ.Job.ID, occ.ScheduledFor, occ.Environment, occ.Attempt,
	).Scan(&row.Status, &row.StartedAt, &row.FinishedAt, &row.DurationMS,
		&row.ResponseStatus, &row.ResponseExcerpt, &row.Error)
	if err != nil {
		t.Fatalf("lettura dell'esecuzione: %v", err)
	}
	return row
}
