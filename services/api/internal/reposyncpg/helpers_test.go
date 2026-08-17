package reposyncpg_test

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
	"github.com/apdsoftware/postqron/services/api/internal/jobs"
	"github.com/apdsoftware/postqron/services/api/internal/migrate"
	"github.com/apdsoftware/postqron/services/api/internal/reposync"
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
// silenzioso darebbe l'impressione che le query siano state provate.
//
// Qui si verifica ciò che internal/reposync non può provare da solo:
// l'atomicità della riconciliazione, l'unicità del nome dentro il repository,
// e il fatto che `enabled` non compaia in nessuna delle scritture del sync.
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
	// Due connessioni, non quattro come negli altri package con test sul
	// database. I test di questo package sono sequenziali e la sola concorrenza
	// che esercitano è quella fra transazioni, che una coppia regge: la suite
	// gira comunque in meno di due secondi, quindi il margine non costa niente
	// qui e resta disponibile a chi arriva dopo.
	//
	// Il `max_connections` del contenitore è ora 200 (docker-compose.yml,
	// 24ddd686) e il tetto non è più vicino. Vale la pena ricordare com'era: a
	// 100, nove package con database usa e getta in parallelo lo esaurivano, e
	// l'errore — «sorry, too many clients already» — arrivava a un test a caso
	// e non a quello che aveva consumato le connessioni.
	pool, err := database.Open(ctx, target, database.Options{
		MaxConns:        2,
		MinConns:        1,
		ApplicationName: "postqron-test-reposyncpg",
	})
	if err != nil {
		t.Fatalf("connessione al database di prova: %v", err)
	}

	t.Cleanup(func() {
		pool.Close()
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

	name := fmt.Sprintf("pq_rsyncpg_%d_%d_%s", os.Getpid(), databaseCounter.Add(1), sanitized)
	if len(name) > 63 {
		name = name[:63]
	}
	return name
}

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

// ------------------------------------------------------------------ fixture

// creaUtente inserisce un utente e restituisce il suo identificativo.
func creaUtente(t *testing.T, pool *pgxpool.Pool, email string) string {
	t.Helper()
	var id string
	err := pool.QueryRow(t.Context(),
		`INSERT INTO users (email, password_hash) VALUES ($1, 'x') RETURNING id::text`,
		email).Scan(&id)
	if err != nil {
		t.Fatalf("creazione dell'utente: %v", err)
	}
	return id
}

// collegaRepository inserisce una riga di `repositories`.
func collegaRepository(t *testing.T, pool *pgxpool.Pool, userID string, externalID int64) reposync.Repository {
	t.Helper()
	var id string
	err := pool.QueryRow(t.Context(),
		`INSERT INTO repositories (user_id, installation_id, external_id, owner, name)
		 VALUES ($1::uuid, 4242, $2, 'acme', 'api') RETURNING id::text`,
		userID, externalID).Scan(&id)
	if err != nil {
		t.Fatalf("collegamento del repository: %v", err)
	}
	return reposync.Repository{
		ID:             id,
		UserID:         userID,
		InstallationID: 4242,
		Owner:          "acme",
		Name:           "api",
		DefaultBranch:  "main",
		ConfigPath:     "cron.yaml",
		Enabled:        true,
	}
}

// jobDelFile è un job come lo restituisce internal/cronyaml.
func jobDelFile(nome string) jobs.Job {
	job := jobs.NewJob()
	job.Name = nome
	job.Every = 5 * time.Minute
	job.URL = "https://esempio.it/" + nome
	job.Method = jobs.MethodGET
	return job
}

// leggiJob rilegge un job dal database per nome, dentro un repository.
func leggiJob(t *testing.T, pool *pgxpool.Pool, repositoryID, nome string) (jobs.Job, bool) {
	t.Helper()
	var job jobs.Job
	var archivedAt *time.Time
	var nextRunAt *time.Time
	err := pool.QueryRow(t.Context(),
		`SELECT id::text, name, url, timezone, enabled, archived_at, next_run_at
		   FROM jobs WHERE repository_id = $1::uuid AND name = $2`,
		repositoryID, nome).Scan(&job.ID, &job.Name, &job.URL, &job.Timezone,
		&job.Enabled, &archivedAt, &nextRunAt)
	if err != nil {
		return jobs.Job{}, false
	}
	job.ArchivedAt = archivedAt
	job.NextRunAt = nextRunAt
	return job, true
}

func contaJob(t *testing.T, pool *pgxpool.Pool, repositoryID string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(t.Context(),
		`SELECT count(*) FROM jobs WHERE repository_id = $1::uuid`, repositoryID).Scan(&n); err != nil {
		t.Fatalf("conteggio dei job: %v", err)
	}
	return n
}

const (
	commitUno = "1111111111111111111111111111111111111111"
	commitDue = "2222222222222222222222222222222222222222"
)
