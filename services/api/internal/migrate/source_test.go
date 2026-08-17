package migrate_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/apdsoftware/postqron/services/api/internal/migrate"
)

func TestLoadPairsUpAndDown(t *testing.T) {
	dir := writeMigrations(t, map[string]string{
		"0001_primo.up.sql":     "CREATE TABLE a ();",
		"0001_primo.down.sql":   "DROP TABLE a;",
		"0002_secondo.up.sql":   "CREATE TABLE b ();",
		"0002_secondo.down.sql": "DROP TABLE b;",
		"README.md":             "non è una migrazione",
	})

	migrations, err := migrate.Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(migrations) != 2 {
		t.Fatalf("attese 2 migrazioni, trovate %d", len(migrations))
	}
	if migrations[0].Version != 1 || migrations[1].Version != 2 {
		t.Fatalf("ordinamento sbagliato: %v", migrations)
	}
	if migrations[0].Name != "primo" {
		t.Errorf("nome = %q, atteso %q", migrations[0].Name, "primo")
	}
	if migrations[0].Up != "CREATE TABLE a ();" {
		t.Errorf("up = %q", migrations[0].Up)
	}
	if migrations[0].Down != "DROP TABLE a;" {
		t.Errorf("down = %q", migrations[0].Down)
	}
	if migrations[0].String() != "0001_primo" {
		t.Errorf("String() = %q", migrations[0].String())
	}
}

// Una migrazione senza `down` non è accettabile: lo schema resterebbe senza via
// di ritorno (db/migrations/README.md).
func TestLoadRejectsIncompletePairs(t *testing.T) {
	tests := map[string]map[string]string{
		"manca il down": {"0001_solo.up.sql": "SELECT 1;"},
		"manca l'up":    {"0001_solo.down.sql": "SELECT 1;"},
	}
	for name, files := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := migrate.Load(writeMigrations(t, files)); err == nil {
				t.Fatal("attesa una segnalazione di coppia incompleta")
			}
		})
	}
}

func TestLoadRejectsMalformedNames(t *testing.T) {
	tests := map[string]string{
		"numerazione corta":  "001_breve.up.sql",
		"numero assente":     "primo.up.sql",
		"direzione assente":  "0001_primo.sql",
		"maiuscole nel nome": "0001_Primo.up.sql",
		"estensione doppia":  "0001_primo.up.sql.bak",
	}
	for name, file := range tests {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, file), []byte("SELECT 1;"), 0o600); err != nil {
				t.Fatal(err)
			}
			migrations, err := migrate.Load(dir)
			// `.up.sql.bak` non finisce per .sql: non è una migrazione, viene
			// ignorato e la directory risulta vuota. Le altre sono file .sql
			// con un nome fuori convenzione e devono essere segnalate, non
			// saltate in silenzio.
			if !strings.HasSuffix(file, ".sql") {
				if err != nil || len(migrations) != 0 {
					t.Fatalf("%s: atteso che venisse ignorato, ottenuto %d migrazioni ed errore %v",
						file, len(migrations), err)
				}
				return
			}
			if err == nil {
				t.Fatalf("%s: atteso un errore di nome non conforme", file)
			}
		})
	}
}

func TestLoadRejectsDuplicateVersion(t *testing.T) {
	dir := writeMigrations(t, map[string]string{
		"0001_primo.up.sql":     "SELECT 1;",
		"0001_primo.down.sql":   "SELECT 1;",
		"0001_diverso.up.sql":   "SELECT 2;",
		"0001_diverso.down.sql": "SELECT 2;",
	})
	_, err := migrate.Load(dir)
	if err == nil {
		t.Fatal("attesa una segnalazione di versione duplicata")
	}
	if !strings.Contains(err.Error(), "0001") {
		t.Errorf("l'errore non indica la versione in conflitto: %v", err)
	}
}

func TestLoadRejectsEmptyFiles(t *testing.T) {
	dir := writeMigrations(t, map[string]string{
		"0001_vuota.up.sql":   "   \n\t\n",
		"0001_vuota.down.sql": "DROP TABLE a;",
	})
	if _, err := migrate.Load(dir); err == nil {
		t.Fatal("attesa una segnalazione di file vuoto")
	}
}

// Il checksum copre entrambe le direzioni: modificare il `down` di una
// migrazione già applicata è vietato quanto modificarne l'`up`.
func TestChecksumCoversBothDirections(t *testing.T) {
	base := map[string]string{
		"0001_primo.up.sql":   "CREATE TABLE a ();",
		"0001_primo.down.sql": "DROP TABLE a;",
	}
	original, err := migrate.Load(writeMigrations(t, base))
	if err != nil {
		t.Fatal(err)
	}

	changedUp := map[string]string{
		"0001_primo.up.sql":   "CREATE TABLE a (id int);",
		"0001_primo.down.sql": "DROP TABLE a;",
	}
	afterUp, err := migrate.Load(writeMigrations(t, changedUp))
	if err != nil {
		t.Fatal(err)
	}

	changedDown := map[string]string{
		"0001_primo.up.sql":   "CREATE TABLE a ();",
		"0001_primo.down.sql": "DROP TABLE IF EXISTS a;",
	}
	afterDown, err := migrate.Load(writeMigrations(t, changedDown))
	if err != nil {
		t.Fatal(err)
	}

	if original[0].Checksum == afterUp[0].Checksum {
		t.Error("una modifica all'up non cambia il checksum")
	}
	if original[0].Checksum == afterDown[0].Checksum {
		t.Error("una modifica al down non cambia il checksum")
	}
}

func TestFindDirWalksUp(t *testing.T) {
	t.Setenv("POSTQRON_MIGRATIONS_DIR", "")

	dir, err := migrate.FindDir(".")
	if err != nil {
		t.Fatalf("FindDir: %v", err)
	}
	if filepath.ToSlash(dir) == "" || !strings.HasSuffix(filepath.ToSlash(dir), migrate.DirName) {
		t.Fatalf("FindDir = %q, atteso un percorso che termina con %q", dir, migrate.DirName)
	}
	if _, err := os.Stat(filepath.Join(dir, "0001_types_and_helpers.up.sql")); err != nil {
		t.Errorf("la directory trovata non contiene la prima migrazione: %v", err)
	}
}

func TestFindDirPrefersEnvironment(t *testing.T) {
	custom := writeMigrations(t, map[string]string{
		"0001_primo.up.sql":   "SELECT 1;",
		"0001_primo.down.sql": "SELECT 1;",
	})
	t.Setenv("POSTQRON_MIGRATIONS_DIR", custom)

	dir, err := migrate.FindDir(".")
	if err != nil {
		t.Fatalf("FindDir: %v", err)
	}
	if dir != custom {
		t.Errorf("FindDir = %q, atteso %q", dir, custom)
	}
}

func TestFindDirRejectsMissingEnvironmentPath(t *testing.T) {
	t.Setenv("POSTQRON_MIGRATIONS_DIR", filepath.Join(t.TempDir(), "inesistente"))

	if _, err := migrate.FindDir("."); err == nil {
		t.Fatal("atteso un errore su POSTQRON_MIGRATIONS_DIR inesistente")
	}
}

// Le migrazioni reali del repository devono superare tutti i controlli di Load.
func TestRepositoryMigrationsAreWellFormed(t *testing.T) {
	migrations := loadMigrations(t)

	for i, mig := range migrations {
		if mig.Version != i+1 {
			t.Errorf("numerazione con un salto: %s ha versione %d, attesa %d", mig, mig.Version, i+1)
		}
	}
}
