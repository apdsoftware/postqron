package migrate_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/apdsoftware/postqron/services/api/internal/migrate"
)

// newMigrator prepara un migratore sulle migrazioni indicate, su un database
// usa e getta.
func newMigrator(t *testing.T, pool *pgxpool.Pool, migrations []migrate.Migration) (*migrate.Migrator, *bytes.Buffer) {
	t.Helper()
	conn, err := pool.Acquire(t.Context())
	if err != nil {
		t.Fatalf("acquisizione della connessione: %v", err)
	}
	t.Cleanup(conn.Release)

	out := &bytes.Buffer{}
	return migrate.New(conn, migrations, out), out
}

// Le migrazioni reali si applicano tutte e si annullano tutte, riportando il
// database allo stato di partenza: è la definition of done della issue.
func TestUpThenDownLeavesNoResidue(t *testing.T) {
	pool := newTestDatabase(t)
	ctx := t.Context()

	migrations := loadMigrations(t)
	migrator, out := newMigrator(t, pool, migrations)

	applied, err := migrator.Up(ctx, 0)
	if err != nil {
		t.Fatalf("Up: %v\n%s", err, out)
	}
	if applied != len(migrations) {
		t.Fatalf("applicate %d migrazioni su %d", applied, len(migrations))
	}

	// Tutte le tabelle attese esistono, con i nomi della issue.
	for _, table := range []string{
		"users", "api_keys", "plans", "subscriptions", "jobs", "job_executions",
		"repositories", "ai_credentials", "audit_log", "notifications",
	} {
		var exists bool
		if err := pool.QueryRow(ctx,
			`SELECT to_regclass($1) IS NOT NULL`, table).Scan(&exists); err != nil {
			t.Fatalf("verifica di %s: %v", table, err)
		}
		if !exists {
			t.Errorf("la tabella %s non è stata creata", table)
		}
	}

	reverted, err := migrator.Down(ctx, len(migrations))
	if err != nil {
		t.Fatalf("Down: %v\n%s", err, out)
	}
	if reverted != len(migrations) {
		t.Fatalf("annullate %d migrazioni su %d", reverted, len(migrations))
	}

	// Dopo il rollback completo restano solo la tabella di stato del tool e le
	// sue righe azzerate: nessuna tabella, nessun tipo, nessuna funzione.
	var residue []string
	rows, err := pool.Query(ctx, `
		SELECT 'tabella '   || c.relname FROM pg_class c
		  JOIN pg_namespace n ON n.oid = c.relnamespace
		 WHERE n.nspname = 'public' AND c.relkind IN ('r', 'p') AND c.relname <> $1
		UNION ALL
		SELECT 'tipo '      || t.typname FROM pg_type t
		  JOIN pg_namespace n ON n.oid = t.typnamespace
		 WHERE n.nspname = 'public' AND t.typtype = 'e'
		UNION ALL
		SELECT 'funzione '  || p.proname FROM pg_proc p
		  JOIN pg_namespace n ON n.oid = p.pronamespace
		 WHERE n.nspname = 'public'`, migrate.TableName)
	if err != nil {
		t.Fatalf("verifica dei residui: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var item string
		if err := rows.Scan(&item); err != nil {
			t.Fatal(err)
		}
		residue = append(residue, item)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(residue) > 0 {
		t.Errorf("il rollback ha lasciato residui: %v", residue)
	}

	var remaining int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM `+migrate.TableName).Scan(&remaining); err != nil {
		t.Fatal(err)
	}
	if remaining != 0 {
		t.Errorf("%s contiene ancora %d righe", migrate.TableName, remaining)
	}
}

// Applicare due volte non riapplica nulla: il tool tiene traccia delle versioni
// già applicate (db/migrations/README.md).
func TestUpIsIdempotent(t *testing.T) {
	pool := newTestDatabase(t)
	ctx := t.Context()

	migrator, out := newMigrator(t, pool, loadMigrations(t))
	if _, err := migrator.Up(ctx, 0); err != nil {
		t.Fatalf("prima Up: %v\n%s", err, out)
	}

	out.Reset()
	applied, err := migrator.Up(ctx, 0)
	if err != nil {
		t.Fatalf("seconda Up: %v\n%s", err, out)
	}
	if applied != 0 {
		t.Errorf("la seconda esecuzione ha applicato %d migrazioni", applied)
	}
	if !strings.Contains(out.String(), "già aggiornato") {
		t.Errorf("output inatteso: %q", out.String())
	}
}

func TestUpAppliesOnlyTheRequestedNumber(t *testing.T) {
	pool := newTestDatabase(t)
	ctx := t.Context()

	migrator, out := newMigrator(t, pool, loadMigrations(t))
	applied, err := migrator.Up(ctx, 2)
	if err != nil {
		t.Fatalf("Up: %v\n%s", err, out)
	}
	if applied != 2 {
		t.Fatalf("applicate %d migrazioni, attese 2", applied)
	}

	version, err := migrator.Version(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if version != 2 {
		t.Errorf("versione dello schema = %d, attesa 2", version)
	}
}

func TestDownRevertsOneByDefault(t *testing.T) {
	pool := newTestDatabase(t)
	ctx := t.Context()

	migrations := loadMigrations(t)
	migrator, out := newMigrator(t, pool, migrations)
	if _, err := migrator.Up(ctx, 0); err != nil {
		t.Fatalf("Up: %v\n%s", err, out)
	}

	reverted, err := migrator.Down(ctx, 1)
	if err != nil {
		t.Fatalf("Down: %v\n%s", err, out)
	}
	if reverted != 1 {
		t.Fatalf("annullate %d migrazioni, attesa 1", reverted)
	}

	version, err := migrator.Version(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if want := len(migrations) - 1; version != want {
		t.Errorf("versione dello schema = %d, attesa %d", version, want)
	}
}

func TestDownRejectsNonPositiveCount(t *testing.T) {
	pool := newTestDatabase(t)

	migrator, _ := newMigrator(t, pool, loadMigrations(t))
	if _, err := migrator.Down(t.Context(), 0); err == nil {
		t.Fatal("atteso un errore su un numero di migrazioni non positivo")
	}
}

// Il vincolo «mai modificate dopo il merge» (AGENTS.md §8) è verificabile
// perché il checksum di ciò che è stato applicato resta nel database.
func TestModifiedMigrationIsRefused(t *testing.T) {
	pool := newTestDatabase(t)
	ctx := t.Context()

	original := []migrate.Migration{{
		Version: 1, Name: "primo",
		Up: "CREATE TABLE a ();", Down: "DROP TABLE a;",
		Checksum: "sha256:originale",
	}}
	migrator, out := newMigrator(t, pool, original)
	if _, err := migrator.Up(ctx, 0); err != nil {
		t.Fatalf("Up: %v\n%s", err, out)
	}

	tampered := []migrate.Migration{{
		Version: 1, Name: "primo",
		Up: "CREATE TABLE a (id int);", Down: "DROP TABLE a;",
		Checksum: "sha256:modificata",
	}}
	after, _ := newMigrator(t, pool, tampered)

	if _, err := after.Up(ctx, 0); err == nil {
		t.Fatal("atteso un rifiuto della migrazione modificata")
	} else if !strings.Contains(err.Error(), "modificata") {
		t.Errorf("l'errore non spiega la causa: %v", err)
	}

	// Anche il rollback dev'essere rifiutato: il `down` sul disco non
	// corrisponde più all'`up` che ha prodotto lo schema attuale.
	if _, err := after.Down(ctx, 1); err == nil {
		t.Fatal("atteso un rifiuto del rollback su una migrazione modificata")
	}
}

// Una migrazione arretrata — numerata sotto una già applicata, tipicamente
// arrivata da un altro branch — verrebbe eseguita fuori ordine.
func TestBackdatedMigrationIsRefused(t *testing.T) {
	pool := newTestDatabase(t)
	ctx := t.Context()

	migrator, out := newMigrator(t, pool, []migrate.Migration{
		{Version: 2, Name: "secondo", Up: "CREATE TABLE b ();", Down: "DROP TABLE b;", Checksum: "sha256:b"},
	})
	if _, err := migrator.Up(ctx, 0); err != nil {
		t.Fatalf("Up: %v\n%s", err, out)
	}

	withBackdated, _ := newMigrator(t, pool, []migrate.Migration{
		{Version: 1, Name: "primo", Up: "CREATE TABLE a ();", Down: "DROP TABLE a;", Checksum: "sha256:a"},
		{Version: 2, Name: "secondo", Up: "CREATE TABLE b ();", Down: "DROP TABLE b;", Checksum: "sha256:b"},
	})
	_, err := withBackdated.Up(ctx, 0)
	if err == nil {
		t.Fatal("atteso un rifiuto della migrazione arretrata")
	}
	if !strings.Contains(err.Error(), "fuori ordine") {
		t.Errorf("l'errore non spiega la causa: %v", err)
	}
}

// Una migrazione applicata di cui è sparito il file lascia lo schema in uno
// stato che il tool non sa più annullare: va detto, non ignorato.
func TestAppliedMigrationWithoutFileIsRefused(t *testing.T) {
	pool := newTestDatabase(t)
	ctx := t.Context()

	migrator, out := newMigrator(t, pool, []migrate.Migration{
		{Version: 1, Name: "primo", Up: "CREATE TABLE a ();", Down: "DROP TABLE a;", Checksum: "sha256:a"},
	})
	if _, err := migrator.Up(ctx, 0); err != nil {
		t.Fatalf("Up: %v\n%s", err, out)
	}

	empty, _ := newMigrator(t, pool, []migrate.Migration{
		{Version: 2, Name: "secondo", Up: "CREATE TABLE b ();", Down: "DROP TABLE b;", Checksum: "sha256:b"},
	})
	if _, err := empty.Up(ctx, 0); err == nil {
		t.Fatal("atteso un rifiuto: la 0001 è applicata ma non ha più un file")
	}
}

// Una migrazione che fallisce a metà non deve lasciare metà schema: la
// transazione la riporta indietro per intero, e la versione non viene registrata.
func TestFailedMigrationIsRolledBackEntirely(t *testing.T) {
	pool := newTestDatabase(t)
	ctx := t.Context()

	migrator, out := newMigrator(t, pool, []migrate.Migration{{
		Version:  1,
		Name:     "rotta",
		Up:       "CREATE TABLE prima_meta (); CREATE TABLE questa_no (id inesistente);",
		Down:     "DROP TABLE IF EXISTS prima_meta;",
		Checksum: "sha256:rotta",
	}})

	if _, err := migrator.Up(ctx, 0); err == nil {
		t.Fatalf("attesa la propagazione dell'errore SQL\n%s", out)
	}

	var exists bool
	if err := pool.QueryRow(ctx, `SELECT to_regclass('prima_meta') IS NOT NULL`).Scan(&exists); err != nil {
		t.Fatal(err)
	}
	if exists {
		t.Error("la prima istruzione della migrazione fallita è rimasta applicata")
	}

	version, err := migrator.Version(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if version != 0 {
		t.Errorf("versione dello schema = %d, attesa 0", version)
	}
}

// Il lock consultivo serializza due migratori sullo stesso database: il secondo
// non deve poter entrare mentre il primo sta lavorando.
func TestLockExcludesConcurrentMigrator(t *testing.T) {
	pool := newTestDatabase(t)
	ctx := t.Context()

	first, _ := newMigrator(t, pool, loadMigrations(t))
	second, _ := newMigrator(t, pool, loadMigrations(t))

	err := first.WithLock(ctx, func(ctx context.Context) error {
		// Il secondo migratore riprova finché il contesto glielo consente. Con
		// il lock libero entrerebbe al primo tentativo, e la funzione interna
		// segnalerebbe l'errore; tenuto, esce per scadenza del contesto.
		blocked, cancel := context.WithTimeout(ctx, 1500*time.Millisecond)
		defer cancel()
		return second.WithLock(blocked, func(context.Context) error {
			t.Error("il secondo migratore ha ottenuto il lock mentre il primo lo teneva")
			return nil
		})
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("atteso il blocco del secondo migratore, ottenuto %v", err)
	}
}

func TestStatusListsEveryMigration(t *testing.T) {
	pool := newTestDatabase(t)
	ctx := t.Context()

	migrations := loadMigrations(t)
	migrator, out := newMigrator(t, pool, migrations)
	if _, err := migrator.Up(ctx, 1); err != nil {
		t.Fatalf("Up: %v", err)
	}

	out.Reset()
	if err := migrator.Status(ctx); err != nil {
		t.Fatalf("Status: %v", err)
	}
	text := out.String()
	for _, mig := range migrations {
		if !strings.Contains(text, mig.Name) {
			t.Errorf("status non elenca %s:\n%s", mig, text)
		}
	}
	if !strings.Contains(text, "applicata") || !strings.Contains(text, "pendente") {
		t.Errorf("status non distingue applicate e pendenti:\n%s", text)
	}
}
