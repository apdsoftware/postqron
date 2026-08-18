package accountpg_test

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/apdsoftware/postqron/services/api/internal/accountpg"
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
// fallito: `make ci` deve restare verde su una macchina senza `make db-up`. Vale
// la pena dirlo qui più che altrove — questi test sono l'unica prova che una
// cancellazione non lascia in giro dati che abbiamo dichiarato cancellati, e un
// salto silenzioso darebbe l'impressione che quella prova sia stata fatta.
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
		MaxConns:        4,
		ApplicationName: "postqron-test-accountpg",
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

	name := fmt.Sprintf("pq_accountpg_%d_%d_%s", os.Getpid(), databaseCounter.Add(1), sanitized)
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

func newStore(t *testing.T) (*accountpg.Store, *pgxpool.Pool) {
	t.Helper()
	pool := newTestDatabase(t)
	store, err := accountpg.New(pool)
	if err != nil {
		t.Fatalf("costruzione dello store: %v", err)
	}
	return store, pool
}

// newThrottledStore costruisce uno store con lotto e tetto ridotti, e senza
// pausa.
//
// Serve a provare che i lotti esistono davvero: con i valori d'esercizio —
// cinquemila righe per lotto — un account di prova ci sta dentro tutto, e la
// riprendibilità dopo un'interruzione resterebbe una proprietà dichiarata e mai
// verificata.
func newThrottledStore(t *testing.T, pool *pgxpool.Pool, batch, maxBatches int) *accountpg.Store {
	t.Helper()
	store, err := accountpg.NewWithOptions(accountpg.Options{
		Pool:       pool,
		Batch:      batch,
		Pause:      time.Nanosecond,
		MaxBatches: maxBatches,
	})
	if err != nil {
		t.Fatalf("costruzione dello store strozzato: %v", err)
	}
	return store
}

// ------------------------------------------------------------------ fixture

// tenant è un account popolato in ogni tabella che possa contenere qualcosa di
// suo.
//
// I valori sono **marcatori riconoscibili**, e non è una comodità di lettura: il
// test che verifica che non sopravviva niente non guarda una lista di tabelle
// scritta a mano — cerca queste stringhe in ogni colonna di ogni tabella dello
// schema. Un marcatore che si confonde con altro testo renderebbe quel test
// incapace di distinguere il residuo dal rumore.
type tenant struct {
	Slug   string
	UserID string
	Email  string

	JobID        string
	RepositoryID string
	SecretID     string

	PaddleSubscriptionID string
	PaddleCustomerID     string
	PaddleEventID        string
	GitHubDeliveryID     string

	InstallationID int64
	ExternalID     int64

	// Markers sono tutte le stringhe che appartengono a questo account e non
	// devono comparire da nessuna parte dopo la purga.
	Markers []string
}

// seedTenant popola un account intero.
//
// Ogni tabella con una chiave verso `users` riceve almeno una riga, e le due che
// una chiave non ce l'hanno — `paddle_webhook_events` e
// `github_webhook_deliveries` — ne ricevono una legata a questo account per la
// sola strada che esiste: l'identificativo della sottoscrizione e la coppia
// (installazione, repository).
//
// `offset` distanzia gli identificativi numerici e le date fra un account e
// l'altro, perché due account di prova non devono collidere su un indice unico.
func seedTenant(t *testing.T, pool *pgxpool.Pool, slug string, offset int) tenant {
	t.Helper()
	ctx := t.Context()

	te := tenant{
		Slug:                 slug,
		Email:                slug + "@esempio-cancellazione.test",
		PaddleSubscriptionID: "sub_" + slug + "_01",
		PaddleCustomerID:     "ctm_" + slug + "_01",
		PaddleEventID:        "evt_" + slug + "_01",
		GitHubDeliveryID:     "delivery-" + slug + "-01",
		InstallationID:       int64(100000 + offset),
		ExternalID:           int64(900000 + offset),
	}

	must(t, pool.QueryRow(ctx,
		`INSERT INTO users (email, password_hash, full_name, timezone)
		 VALUES ($1, $2, $3, 'Europe/Rome') RETURNING id::text`,
		te.Email, "argon2id$"+slug, "Nome "+slug).Scan(&te.UserID))

	must(t, pool.QueryRow(ctx,
		`INSERT INTO repositories (user_id, installation_id, external_id, owner, name, last_synced_commit)
		 VALUES ($1::uuid, $2, $3, $4, $5, 'abc1234') RETURNING id::text`,
		te.UserID, te.InstallationID, te.ExternalID, "org-"+slug, "repo-"+slug).Scan(&te.RepositoryID))

	must(t, pool.QueryRow(ctx,
		`INSERT INTO jobs (user_id, repository_id, name, every_seconds, url, headers, body, next_run_at)
		 VALUES ($1::uuid, $2::uuid, $3, 60, $4, $5::jsonb, $6, now() - interval '1 minute')
		 RETURNING id::text`,
		te.UserID, te.RepositoryID, "job-"+slug,
		"https://bersaglio."+slug+".example/hook",
		`{"X-Marker": "header-`+slug+`"}`,
		"body-"+slug).Scan(&te.JobID))

	exec(t, pool, `SELECT job_executions_ensure_partitions(1, 1)`)
	exec(t, pool,
		`INSERT INTO job_executions
		     (job_id, scheduled_for, environment, attempt, status, started_at, finished_at,
		      response_status, response_excerpt)
		 VALUES ($1::uuid, now() - interval '2 minutes', 'production', 1, 'succeeded',
		         now() - interval '2 minutes', now() - interval '2 minutes', 200, $2)`,
		te.JobID, "risposta-"+slug)

	exec(t, pool,
		`INSERT INTO api_keys (user_id, name, prefix, key_hash, scopes)
		 VALUES ($1::uuid, $2, $3, $4, '{jobs:read}')`,
		te.UserID, "chiave-"+slug, "pq_"+slug, "hash-della-chiave-"+slug+"-abcdefghijklmno")

	must(t, pool.QueryRow(ctx,
		`INSERT INTO workspace_secrets (user_id, name, description, ciphertext, nonce)
		 VALUES ($1::uuid, $2, $3, $4::bytea, $5::bytea) RETURNING id::text`,
		te.UserID, "TOKEN_"+strings.ToUpper(slug), "nota-"+slug,
		[]byte("cifrato-"+slug), []byte("nonce-"+slug)).Scan(&te.SecretID))

	exec(t, pool,
		`INSERT INTO ai_credentials (user_id, provider, ciphertext, nonce)
		 VALUES ($1::uuid, 'anthropic', $2::bytea, $3::bytea)`,
		te.UserID, []byte("ai-cifrato-"+slug), []byte("ai-nonce-"+slug))

	exec(t, pool,
		`INSERT INTO sessions (user_id, token_hash, expires_at, user_agent)
		 VALUES ($1::uuid, $2, now() + interval '30 days', $3)`,
		te.UserID, hex64("a", slug), "browser-"+slug)

	exec(t, pool,
		`INSERT INTO user_tokens (user_id, purpose, token_hash, expires_at)
		 VALUES ($1::uuid, 'password_reset', $2, now() + interval '1 hour')`,
		te.UserID, hex64("b", slug))

	exec(t, pool,
		`INSERT INTO notifications (user_id, event, channel, job_id, dedupe_key, payload)
		 VALUES ($1::uuid, 'job_failed', 'email', $2::uuid, $3, $4::jsonb)`,
		te.UserID, te.JobID, "dedupe-"+slug, `{"marker": "notifica-`+slug+`"}`)

	exec(t, pool,
		`INSERT INTO subscriptions (user_id, plan_code, status, billing_period,
		                            paddle_subscription_id, paddle_customer_id)
		 VALUES ($1::uuid, 'pro', 'active', 'monthly', $2, $3)`,
		te.UserID, te.PaddleSubscriptionID, te.PaddleCustomerID)

	exec(t, pool,
		`INSERT INTO paddle_checkout_intents (user_id, plan_code, billing_period, paddle_price_id,
		                                      business_use, vat_number)
		 VALUES ($1::uuid, 'pro', 'monthly', $2, true, $3)`,
		te.UserID, "pri_"+slug, "IT"+fmt.Sprint(11111111111+offset))

	exec(t, pool,
		`INSERT INTO paddle_webhook_events (event_id, event_type, status, occurred_at,
		                                    paddle_subscription_id, paddle_customer_id)
		 VALUES ($1, 'subscription.updated', 'processed', now(), $2, $3)`,
		te.PaddleEventID, te.PaddleSubscriptionID, te.PaddleCustomerID)

	exec(t, pool,
		`INSERT INTO github_webhook_deliveries (delivery_id, event, status, installation_id,
		                                        repository_external_id, repository_full_name, ref, head_commit)
		 VALUES ($1, 'push', 'processed', $2, $3, $4, 'refs/heads/main', 'abc1234')`,
		te.GitHubDeliveryID, te.InstallationID, te.ExternalID, "org-"+slug+"/repo-"+slug)

	te.Markers = []string{
		te.UserID,
		te.Email,
		"Nome " + slug,
		"argon2id$" + slug,
		"job-" + slug,
		"bersaglio." + slug + ".example",
		"header-" + slug,
		"body-" + slug,
		"risposta-" + slug,
		"chiave-" + slug,
		"hash-della-chiave-" + slug,
		"TOKEN_" + strings.ToUpper(slug),
		"nota-" + slug,
		"cifrato-" + slug,
		"ai-cifrato-" + slug,
		"browser-" + slug,
		"dedupe-" + slug,
		"notifica-" + slug,
		"org-" + slug,
		"repo-" + slug,
		te.PaddleSubscriptionID,
		te.PaddleCustomerID,
		te.PaddleEventID,
		te.GitHubDeliveryID,
		"pri_" + slug,
	}
	return te
}

// ------------------------------------------------------------------ utilità

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("preparazione del database di prova: %v", err)
	}
}

func exec(t *testing.T, pool *pgxpool.Pool, sql string, args ...any) {
	t.Helper()
	if _, err := pool.Exec(t.Context(), sql, args...); err != nil {
		t.Fatalf("preparazione del database di prova (%s): %v", firstLine(sql), err)
	}
}

func firstLine(sql string) string {
	sql = strings.TrimSpace(sql)
	if i := strings.IndexByte(sql, '\n'); i >= 0 {
		return sql[:i]
	}
	return sql
}

// hex64 compone un'impronta di sessantaquattro caratteri esadecimali, che è la
// forma che i vincoli della 0009 pretendono.
func hex64(prefix, slug string) string {
	raw := prefix + slug
	var sb strings.Builder
	for _, c := range raw {
		fmt.Fprintf(&sb, "%02x", c)
	}
	out := sb.String()
	for len(out) < 64 {
		out += "0"
	}
	return out[:64]
}

func count(t *testing.T, pool *pgxpool.Pool, sql string, args ...any) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(t.Context(), sql, args...).Scan(&n); err != nil {
		t.Fatalf("conteggio (%s): %v", firstLine(sql), err)
	}
	return n
}

// requestNow chiede la cancellazione con una grazia data, partendo da adesso.
func requestNow(t *testing.T, store *accountpg.Store, userID string, grace time.Duration) time.Time {
	t.Helper()
	now := time.Now()
	if _, err := store.RequestDeletion(t.Context(), userID, now, now.Add(grace)); err != nil {
		t.Fatalf("richiesta di cancellazione: %v", err)
	}
	return now
}
