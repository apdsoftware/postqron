package retention_test

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
)

// TestMain carica il `.env` del monorepo: le POSTGRES_* che descrivono il
// database di sviluppo stanno lì, ed è lo stesso file con cui `make db-up` ha
// creato il container (AGENTS.md §7).
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
// messaggio di skip dice cosa non è stato verificato, perché un salto
// silenzioso darebbe l'impressione che la cancellazione sia stata provata
// quando non lo è stata — e su questo pacchetto è proprio la cosa da non far
// credere.
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
		t.Skipf("PostgreSQL non raggiungibile su %s:%s, retention non verificata (avvia `make db-up`): %v",
			cfg.Postgres.Host, cfg.Postgres.Port, err)
	}
	defer admin.Close()

	name := testDatabaseName(t)
	if _, err := admin.Exec(ctx, fmt.Sprintf("DROP DATABASE IF EXISTS %q WITH (FORCE)", name)); err != nil {
		t.Fatalf("pulizia del database di prova: %v", err)
	}
	if _, err := admin.Exec(ctx, fmt.Sprintf("CREATE DATABASE %q", name)); err != nil {
		t.Skipf("creazione del database di prova non consentita, retention non verificata: %v", err)
	}

	target := cfg.Postgres
	target.Database = name
	pool, err := database.Open(ctx, target, database.Options{
		MaxConns:        8,
		ApplicationName: "postqron-test-retention",
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

	name := fmt.Sprintf("pq_reten_%d_%d_%s", os.Getpid(), databaseCounter.Add(1), sanitized)
	if len(name) > 63 {
		name = name[:63]
	}
	return name
}

// applyMigrations applica le migrazioni reali del repository.
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

	if _, err := migrate.New(conn, migrations, nil).Up(t.Context(), 0); err != nil {
		t.Fatalf("applicazione delle migrazioni: %v", err)
	}
}

// ------------------------------------------------------------------- fixture

// newUser crea un utente sul piano indicato e restituisce il suo id.
//
// `plan` vuoto significa **nessuna sottoscrizione**, che è il caso di ogni
// account appena registrato: l'utente ricade su `free` per il `coalesce` di
// `PlanForUser`, e la retention deve trattarlo allo stesso modo.
func newUser(t *testing.T, pool *pgxpool.Pool, plan string) string {
	t.Helper()

	var userID string
	email := fmt.Sprintf("u%d@example.test", databaseCounter.Add(1))
	if err := pool.QueryRow(t.Context(),
		`INSERT INTO users (email, password_hash) VALUES ($1, 'x') RETURNING id::text`,
		email).Scan(&userID); err != nil {
		t.Fatalf("creazione dell'utente: %v", err)
	}
	if plan == "" {
		return userID
	}
	if _, err := pool.Exec(t.Context(),
		`INSERT INTO subscriptions (user_id, plan_code, status) VALUES ($1::uuid, $2, 'active')`,
		userID, plan); err != nil {
		t.Fatalf("creazione della sottoscrizione %q: %v", plan, err)
	}
	return userID
}

// newJob crea un job dell'utente e restituisce il suo id.
func newJob(t *testing.T, pool *pgxpool.Pool, userID string) string {
	t.Helper()

	var jobID string
	name := fmt.Sprintf("job%d", databaseCounter.Add(1))
	if err := pool.QueryRow(t.Context(),
		`INSERT INTO jobs (user_id, name, url, every_seconds)
		 VALUES ($1::uuid, $2, 'https://example.test/hook', 60)
		 RETURNING id::text`,
		userID, name).Scan(&jobID); err != nil {
		t.Fatalf("creazione del job: %v", err)
	}
	return jobID
}

// ensurePartition crea la partizione del giorno che contiene l'istante dato.
//
// Serve ai test che scrivono nel passato: la 0006 prepara solo la finestra
// attorno a oggi, e una riga vecchia non ha dove andare finché la sua giornata
// non esiste.
func ensurePartition(t *testing.T, pool *pgxpool.Pool, when time.Time) {
	t.Helper()
	if _, err := pool.Exec(t.Context(),
		`SELECT job_executions_ensure_partition($1::date)`, when.UTC()); err != nil {
		t.Fatalf("creazione della partizione di %s: %v", when.UTC().Format(time.DateOnly), err)
	}
}

// insertExecution scrive un'esecuzione conclusa all'istante indicato.
func insertExecution(t *testing.T, pool *pgxpool.Pool, jobID string, when time.Time, attempt int) {
	t.Helper()
	ensurePartition(t, pool, when)
	if _, err := pool.Exec(t.Context(),
		`INSERT INTO job_executions
		     (job_id, scheduled_for, environment, attempt, status, started_at, finished_at, response_status)
		 VALUES ($1::uuid, $2::timestamptz, 'production', $3, 'succeeded',
		         $2::timestamptz, $2::timestamptz + interval '1 second', 200)`,
		jobID, when.UTC(), attempt); err != nil {
		t.Fatalf("inserimento dell'esecuzione a %s: %v", when.UTC().Format(time.RFC3339), err)
	}
}

// countExecutions conta le righe di un job.
func countExecutions(t *testing.T, pool *pgxpool.Pool, jobID string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(t.Context(),
		`SELECT count(*) FROM job_executions WHERE job_id = $1::uuid`, jobID).Scan(&n); err != nil {
		t.Fatalf("conteggio delle esecuzioni: %v", err)
	}
	return n
}

// partitionNames elenca le partizioni giornaliere esistenti, ordinate.
func partitionNames(t *testing.T, pool *pgxpool.Pool) []string {
	t.Helper()
	rows, err := pool.Query(t.Context(),
		`SELECT c.relname
		   FROM pg_class c
		   JOIN pg_inherits i ON i.inhrelid = c.oid
		  WHERE i.inhparent = 'job_executions'::regclass
		  ORDER BY c.relname`)
	if err != nil {
		t.Fatalf("elenco delle partizioni: %v", err)
	}
	defer rows.Close()

	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("lettura del nome della partizione: %v", err)
		}
		names = append(names, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("elenco delle partizioni: %v", err)
	}
	return names
}

// hasPartition dice se esiste la partizione del giorno indicato.
func hasPartition(t *testing.T, pool *pgxpool.Pool, day time.Time) bool {
	t.Helper()
	want := "job_executions_" + day.UTC().Format("20060102")
	for _, name := range partitionNames(t, pool) {
		if name == want {
			return true
		}
	}
	return false
}

// dropAllPartitions svuota la tabella dalle sue partizioni.
//
// Serve a ricostruire la condizione in cui il motore si trova quando nessuno
// prepara le partizioni future: non è un'ipotesi di laboratorio, è ciò che
// succede due settimane dopo il deploy se questo pacchetto non gira.
func dropAllPartitions(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	for _, name := range partitionNames(t, pool) {
		if _, err := pool.Exec(t.Context(), fmt.Sprintf("DROP TABLE %q", name)); err != nil {
			t.Fatalf("eliminazione della partizione %q: %v", name, err)
		}
	}
}
