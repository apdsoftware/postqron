package migrate_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/apdsoftware/postqron/services/api/internal/config"
	"github.com/apdsoftware/postqron/services/api/internal/database"
	"github.com/apdsoftware/postqron/services/api/internal/dotenv"
	"github.com/apdsoftware/postqron/services/api/internal/migrate"
)

// TestMain carica il `.env` del monorepo prima di qualunque test: le
// POSTGRES_* che descrivono il database di sviluppo stanno lì, ed è lo stesso
// file con cui `make db-up` ha creato il container.
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

// migrationsDir individua db/migrations risalendo dalla directory del test.
func migrationsDir(t testing.TB) string {
	t.Helper()
	dir, err := migrate.FindDir(".")
	if err != nil {
		t.Fatalf("directory delle migrazioni non trovata: %v", err)
	}
	return dir
}

// loadMigrations legge le migrazioni reali del repository.
func loadMigrations(t testing.TB) []migrate.Migration {
	t.Helper()
	migrations, err := migrate.Load(migrationsDir(t))
	if err != nil {
		t.Fatalf("caricamento delle migrazioni: %v", err)
	}
	if len(migrations) == 0 {
		t.Fatal("nessuna migrazione trovata")
	}
	return migrations
}

// newTestDatabase crea un database usa e getta e restituisce un pool verso di
// esso. Il database viene eliminato alla fine del test.
//
// Se PostgreSQL non è raggiungibile il test viene saltato invece che fallito:
// `make ci` deve restare verde su una macchina senza `make db-up`, ma non deve
// dare l'impressione di aver verificato lo schema quando non l'ha fatto — il
// messaggio di skip lo dice.
func newTestDatabase(t testing.TB) *pgxpool.Pool {
	t.Helper()
	return newTestDatabaseWithConns(t, 2)
}

// newTestDatabaseWithConns è la stessa cosa con il pool dimensionato dal
// chiamante. I benchmark hanno bisogno di più connessioni dei test: con un pool
// stretto misurerebbero l'attesa sul pool invece della contesa sul database.
func newTestDatabaseWithConns(t testing.TB, maxConns int32) *pgxpool.Pool {
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
		MaxConns:        maxConns,
		ApplicationName: "postqron-test",
	})
	if err != nil {
		t.Fatalf("connessione al database di prova: %v", err)
	}

	t.Cleanup(func() {
		pool.Close()
		// Il contesto del test è già chiuso quando gira il cleanup: la
		// cancellazione ha bisogno del proprio.
		cleanupCtx := context.Background()
		cleaner, err := database.Open(cleanupCtx, cfg.Postgres, database.Options{MaxConns: 1})
		if err != nil {
			t.Logf("database %q non eliminato: %v", name, err)
			return
		}
		defer cleaner.Close()
		if _, err := cleaner.Exec(cleanupCtx, fmt.Sprintf("DROP DATABASE IF EXISTS %q WITH (FORCE)", name)); err != nil {
			t.Logf("database %q non eliminato: %v", name, err)
		}
	})

	return pool
}

// testDatabaseName compone un nome legale e riconoscibile: il limite di
// PostgreSQL è 63 byte, e il nome del test può essere lungo.
func testDatabaseName(t testing.TB) string {
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

	name := fmt.Sprintf("pq_test_%d_%d_%s", os.Getpid(), databaseCounter.Add(1), sanitized)
	if len(name) > 63 {
		name = name[:63]
	}
	return name
}

// applyAll applica tutte le migrazioni reali sul database indicato e
// restituisce il migratore, per i test che vogliono proseguire da lì.
func applyAll(t testing.TB, pool *pgxpool.Pool) *migrate.Migrator {
	t.Helper()
	conn, err := pool.Acquire(t.Context())
	if err != nil {
		t.Fatalf("acquisizione della connessione: %v", err)
	}
	t.Cleanup(conn.Release)

	migrator := migrate.New(conn, loadMigrations(t), nil)
	if _, err := migrator.Up(t.Context(), 0); err != nil {
		t.Fatalf("applicazione delle migrazioni: %v", err)
	}
	return migrator
}

// writeMigrations scrive una coppia up/down in una directory temporanea.
func writeMigrations(t testing.TB, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			t.Fatalf("scrittura di %s: %v", name, err)
		}
	}
	return dir
}
