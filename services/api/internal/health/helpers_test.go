package health_test

import (
	"context"
	"fmt"
	"io"
	"log/slog"
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

func testLogger(t *testing.T) *slog.Logger {
	t.Helper()
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// newTestDatabase crea un database usa e getta con lo schema applicato.
//
// Se PostgreSQL non è raggiungibile il test viene **saltato** invece che
// fallito: `make ci` deve restare verde su una macchina senza `make db-up`. Ciò
// che si perde saltando è quasi tutto: le sonde di questo pacchetto sono
// domande al database, e non c'è modo di provarle senza.
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
		t.Skipf("PostgreSQL non raggiungibile su %s:%s, prontezza non verificata (avvia `make db-up`): %v",
			cfg.Postgres.Host, cfg.Postgres.Port, err)
	}
	defer admin.Close()

	name := testDatabaseName(t)
	if _, err := admin.Exec(ctx, fmt.Sprintf("DROP DATABASE IF EXISTS %q WITH (FORCE)", name)); err != nil {
		t.Fatalf("pulizia del database di prova: %v", err)
	}
	if _, err := admin.Exec(ctx, fmt.Sprintf("CREATE DATABASE %q", name)); err != nil {
		t.Skipf("creazione del database di prova non consentita, prontezza non verificata: %v", err)
	}

	target := cfg.Postgres
	target.Database = name
	pool, err := database.Open(ctx, target, database.Options{
		MaxConns:        4,
		ApplicationName: "postqron-test-health",
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

	name := fmt.Sprintf("pq_health_%d_%d_%s", os.Getpid(), databaseCounter.Add(1), sanitized)
	if len(name) > 63 {
		name = name[:63]
	}
	return name
}

// applyMigrations applica le migrazioni reali del repository: le sonde guardano
// lo schema che va in produzione, partizioni comprese.
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

// dropPartitionsAfter elimina le partizioni successive al giorno indicato.
//
// È il modo di ricostruire una finestra che si sta consumando: non un'ipotesi di
// laboratorio, ma esattamente ciò che succede quando la manutenzione smette di
// preparare le partizioni future e nessuno se ne accorge.
func dropPartitionsAfter(t *testing.T, pool *pgxpool.Pool, day time.Time) {
	t.Helper()
	for _, name := range partitionNames(t, pool) {
		when, err := time.Parse("20060102", name[len(name)-8:])
		if err != nil {
			t.Fatalf("nome di partizione inatteso %q: %v", name, err)
		}
		if when.After(day) {
			if _, err := pool.Exec(t.Context(), fmt.Sprintf("DROP TABLE %q", name)); err != nil {
				t.Fatalf("eliminazione della partizione %q: %v", name, err)
			}
		}
	}
}

// dropAllPartitions svuota la tabella dalle sue partizioni: lo stato in cui il
// motore non può più scrivere nulla.
func dropAllPartitions(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	for _, name := range partitionNames(t, pool) {
		if _, err := pool.Exec(t.Context(), fmt.Sprintf("DROP TABLE %q", name)); err != nil {
			t.Fatalf("eliminazione della partizione %q: %v", name, err)
		}
	}
}

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

// battito è un [health.Engine] sotto controllo del test: l'istante dell'ultima
// passata riuscita si sceglie invece di aspettarlo.
type battito struct {
	last time.Time
	ok   bool
}

func (b battito) LastTick() (time.Time, bool) { return b.last, b.ok }
