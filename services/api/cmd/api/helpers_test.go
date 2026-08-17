package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

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

// newTestDatabase crea un database usa e getta con lo schema applicato.
//
// Se PostgreSQL non è raggiungibile il test viene **saltato** invece che
// fallito: `make ci` deve restare verde su una macchina senza `make db-up`. Qui
// il messaggio di skip conta più che altrove — ciò che non viene verificato è
// l'unica prova che il prodotto fa la cosa per cui esiste.
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
		t.Skipf("PostgreSQL non raggiungibile su %s:%s, prova end-to-end saltata (avvia `make db-up`): %v",
			cfg.Postgres.Host, cfg.Postgres.Port, err)
	}
	defer admin.Close()

	name := testDatabaseName(t)
	if _, err := admin.Exec(ctx, fmt.Sprintf("DROP DATABASE IF EXISTS %q WITH (FORCE)", name)); err != nil {
		t.Fatalf("pulizia del database di prova: %v", err)
	}
	if _, err := admin.Exec(ctx, fmt.Sprintf("CREATE DATABASE %q", name)); err != nil {
		t.Skipf("creazione del database di prova non consentita, prova saltata: %v", err)
	}

	target := cfg.Postgres
	target.Database = name
	pool, err := database.Open(ctx, target, database.Options{
		MaxConns:        8,
		ApplicationName: "postqron-test-e2e",
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

	name := fmt.Sprintf("pq_e2e_%d_%d_%s", os.Getpid(), databaseCounter.Add(1), sanitized)
	if len(name) > 63 {
		name = name[:63]
	}
	return name
}

// applyMigrations applica le migrazioni reali del repository: la prova gira
// contro lo schema che va in produzione, partizioni comprese.
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

// ------------------------------------------------------------------- logger

// testWriter porta i log dentro l'output del test, senza scriverci dopo che è
// finito: il motore ha goroutine che possono sopravvivere di un istante alla
// fine, e `t.Log` da lì fa panic.
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

// ------------------------------------------------------------------ bersaglio

// bersaglio è il servizio HTTP che i job di prova chiamano: fatto in casa,
// vero, e con un registro di ciò che ha ricevuto.
//
// Non è un mock dell'esecutore: è un server HTTP che parla il protocollo. È la
// differenza fra provare che il motore *crede* di aver chiamato qualcuno e
// provare che qualcuno *è stato chiamato*.
type bersaglio struct {
	*httptest.Server
	mu       sync.Mutex
	ricevute []chiamata
}

// chiamata è una richiesta arrivata al bersaglio.
type chiamata struct {
	Method  string
	Path    string
	Headers http.Header
	Body    string
}

func nuovoBersaglio(t *testing.T) *bersaglio {
	t.Helper()
	b := &bersaglio{}
	b.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		corpo, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("lettura del corpo ricevuto: %v", err)
		}
		b.mu.Lock()
		b.ricevute = append(b.ricevute, chiamata{
			Method:  r.Method,
			Path:    r.URL.Path,
			Headers: r.Header.Clone(),
			Body:    string(corpo),
		})
		b.mu.Unlock()

		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		fmt.Fprintf(w, "eco:%s", corpo)
	}))
	t.Cleanup(b.Close)
	return b
}

// chiamate restituisce le richieste arrivate finora.
func (b *bersaglio) chiamate() []chiamata {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]chiamata(nil), b.ricevute...)
}

// chiamateSu filtra per percorso.
func (b *bersaglio) chiamateSu(path string) []chiamata {
	var out []chiamata
	for _, c := range b.chiamate() {
		if c.Path == path {
			out = append(out, c)
		}
	}
	return out
}
