package dotenv_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/apdsoftware/postqron/services/api/internal/dotenv"
)

func TestParse(t *testing.T) {
	input := `
# la connessione al database
POSTGRES_HOST=127.0.0.1
POSTGRES_PORT=5433   # commento a fine riga

export POSTGRES_USER=postqron
POSTGRES_PASSWORD="con spazi e \"apici\""
LITERALE='niente \n interpretato'
MULTILINEA="prima\nseconda"
VUOTA=
`
	values, err := dotenv.Parse(strings.NewReader(input))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	want := map[string]string{
		"POSTGRES_HOST":     "127.0.0.1",
		"POSTGRES_PORT":     "5433",
		"POSTGRES_USER":     "postqron",
		"POSTGRES_PASSWORD": `con spazi e "apici"`,
		"LITERALE":          `niente \n interpretato`,
		"MULTILINEA":        "prima\nseconda",
		"VUOTA":             "",
	}
	for key, expected := range want {
		if values[key] != expected {
			t.Errorf("%s = %q, atteso %q", key, values[key], expected)
		}
	}
	if len(values) != len(want) {
		t.Errorf("lette %d variabili, attese %d: %v", len(values), len(want), values)
	}
}

func TestParseRejectsMalformedLines(t *testing.T) {
	tests := map[string]string{
		"manca l'uguale":            "POSTGRES_HOST 127.0.0.1",
		"nome con trattino":         "POSTGRES-HOST=127.0.0.1",
		"nome che inizia per cifra": "1POSTGRES=x",
		"nome vuoto":                "=valore",
	}
	for name, line := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := dotenv.Parse(strings.NewReader(line)); err == nil {
				t.Fatalf("attesa una segnalazione per %q", line)
			}
		})
	}
}

// L'ambiente esplicito vince sul file: un `POSTGRES_PORT=5544 make migrate`
// deve avere effetto, altrimenti il file diventerebbe una sorgente silenziosa
// che sovrascrive una scelta deliberata.
func TestLoadDoesNotOverrideExistingVariables(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	content := "POSTQRON_TEST_ESISTENTE=dal_file\nPOSTQRON_TEST_NUOVA=dal_file\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	t.Setenv("POSTQRON_TEST_ESISTENTE", "dall_ambiente")
	os.Unsetenv("POSTQRON_TEST_NUOVA")
	t.Cleanup(func() { os.Unsetenv("POSTQRON_TEST_NUOVA") })

	if err := dotenv.Load(path); err != nil {
		t.Fatalf("Load: %v", err)
	}

	if got := os.Getenv("POSTQRON_TEST_ESISTENTE"); got != "dall_ambiente" {
		t.Errorf("il file ha sovrascritto l'ambiente: %q", got)
	}
	if got := os.Getenv("POSTQRON_TEST_NUOVA"); got != "dal_file" {
		t.Errorf("la variabile mancante non è stata impostata: %q", got)
	}
}

// In produzione la configurazione arriva dall'ambiente e il file non esiste:
// non trovarlo non è un errore.
func TestLoadIgnoresMissingFile(t *testing.T) {
	if err := dotenv.Load(filepath.Join(t.TempDir(), ".env")); err != nil {
		t.Fatalf("un file assente non dev'essere un errore: %v", err)
	}
}

func TestLoadReportsMalformedFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(path, []byte("questa riga non ha un uguale\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := dotenv.Load(path)
	if err == nil {
		t.Fatal("atteso un errore sul file malformato")
	}
	if !strings.Contains(err.Error(), path) {
		t.Errorf("l'errore non indica il file: %v", err)
	}
}

// LoadNearest risale le directory: `go run ./cmd/migrate` deve trovare lo
// stesso `.env` da qualunque punto del monorepo lo si lanci.
func TestLoadNearestWalksUp(t *testing.T) {
	root := t.TempDir()
	nested := filepath.Join(root, "services", "api")
	if err := os.MkdirAll(nested, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".env"), []byte("POSTQRON_TEST_RISALITA=trovata\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	os.Unsetenv("POSTQRON_TEST_RISALITA")
	t.Cleanup(func() { os.Unsetenv("POSTQRON_TEST_RISALITA") })

	if err := dotenv.LoadNearest(nested); err != nil {
		t.Fatalf("LoadNearest: %v", err)
	}
	if got := os.Getenv("POSTQRON_TEST_RISALITA"); got != "trovata" {
		t.Errorf("POSTQRON_TEST_RISALITA = %q, atteso %q", got, "trovata")
	}
}

func TestLoadNearestIgnoresAbsentFile(t *testing.T) {
	// Una directory temporanea può avere un `.env` in un antenato solo per una
	// coincidenza del filesystem di prova: la radice del sistema non ne ha.
	if err := dotenv.LoadNearest(string(filepath.Separator)); err != nil {
		t.Fatalf("assenza di .env non dev'essere un errore: %v", err)
	}
}
