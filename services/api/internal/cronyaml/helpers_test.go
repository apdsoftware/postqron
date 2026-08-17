package cronyaml_test

import (
	"strings"
	"testing"
	"time"

	"github.com/apdsoftware/postqron/services/api/internal/cronyaml"
	"github.com/apdsoftware/postqron/services/api/internal/jobs"
	"github.com/apdsoftware/postqron/services/api/internal/secrets"
)

// I test di questo package sono scritti su sorgenti **inline** e non su file in
// `testdata/`, e la ragione è che riga e colonna sono ciò che va provato: un
// file separato costringerebbe a contare le righe in una finestra diversa da
// quella in cui si legge l'asserzione, e il primo commento aggiunto in cima al
// file romperebbe metà della suite senza che nessuno capisca perché.
//
// Un `.yaml` in `testdata/` avrebbe anche un secondo problema, meno ovvio:
// `make ci` passa Prettier su ogni file YAML del repository (vedi
// `.prettierignore`), e diversi di questi sorgenti sono **deliberatamente
// malformati** — è il loro scopo.

// yamlSource toglie il primo a capo di un letterale grezzo, così che la riga 1
// del sorgente sia la prima riga scritta dopo il backtick e i numeri di riga
// delle asserzioni si possano contare a occhio.
func yamlSource(s string) []byte {
	return []byte(strings.TrimPrefix(s, "\n"))
}

// teamPlan è il piano Team di SPEC §8: risoluzione fino al secondo, ambienti
// separati, nessun tetto rigido al numero di job. È il piano di riferimento dei
// test che non riguardano i limiti, perché è quello che non ne applica.
func teamPlan() jobs.Plan {
	fairUse := 1000
	return jobs.Plan{
		Code:                "team",
		Name:                "Team",
		FairUseJobs:         &fairUse,
		MinInterval:         time.Second,
		LogRetention:        30 * 24 * time.Hour,
		EnvironmentsEnabled: true,
	}
}

// proPlan è il piano Pro di SPEC §8: 200 job, risoluzione minima 10 secondi.
func proPlan() jobs.Plan {
	maxJobs := 200
	return jobs.Plan{
		Code:                "pro",
		Name:                "Pro",
		MaxJobs:             &maxJobs,
		MinInterval:         10 * time.Second,
		LogRetention:        15 * 24 * time.Hour,
		EnvironmentsEnabled: true,
	}
}

// options sono le opzioni di un workspace Team con i segreti indicati.
func options(secretNames ...string) cronyaml.Options {
	return cronyaml.Options{
		Plan:    teamPlan(),
		Secrets: secretsOf(secretNames...),
	}
}

// secretsOf è l'insieme dei nomi dei segreti vivi di un workspace.
func secretsOf(names ...string) secrets.NameSet { return secrets.NewNameSet(names) }

// mustParse pretende un file valido e restituisce il risultato.
func mustParse(t *testing.T, source string, opts cronyaml.Options) *cronyaml.File {
	t.Helper()
	file, err := cronyaml.Parse(t.Context(), yamlSource(source), opts)
	if err != nil {
		t.Fatalf("Parse ha rifiutato un file che doveva accettare:\n%v", err)
	}
	return file
}

// mustReject pretende un file invalido e restituisce i motivi.
func mustReject(t *testing.T, source string, opts cronyaml.Options) []cronyaml.Error {
	t.Helper()
	file, err := cronyaml.Parse(t.Context(), yamlSource(source), opts)
	if err == nil {
		t.Fatalf("Parse ha accettato un file che doveva rifiutare: %+v", file)
	}
	if file != nil {
		// R13: un file non valido non deve produrre niente da riconciliare.
		t.Errorf("Parse ha restituito un file **e** un errore: un file invalido non deve poter modificare lo stato esistente")
	}
	parse, ok := cronyaml.AsParseError(err)
	if !ok {
		t.Fatalf("l'errore non è un *ParseError ma %T: %v", err, err)
	}
	return parse.Errors
}

// find cerca il rifiuto di un campo. Il confronto è sul percorso completo,
// perché è quello che un client userebbe.
func find(t *testing.T, items []cronyaml.Error, path string) cronyaml.Error {
	t.Helper()
	for _, item := range items {
		if item.Path == path {
			return item
		}
	}
	t.Fatalf("nessun errore sul campo %q; ricevuti:\n%s", path, format(items))
	return cronyaml.Error{}
}

// findCode cerca il rifiuto con un certo codice.
func findCode(t *testing.T, items []cronyaml.Error, code string) cronyaml.Error {
	t.Helper()
	for _, item := range items {
		if item.Code == code {
			return item
		}
	}
	t.Fatalf("nessun errore con codice %q; ricevuti:\n%s", code, format(items))
	return cronyaml.Error{}
}

func format(items []cronyaml.Error) string {
	var out strings.Builder
	for _, item := range items {
		out.WriteString("  " + item.Error() + " [" + item.Code + "]\n")
	}
	return out.String()
}

// contains asserisce che il messaggio contenga il rimedio atteso. È
// deliberatamente un controllo sul **testo**: il messaggio è il prodotto, e un
// test che verifica solo il codice di errore lascerebbe libero di peggiorarlo.
func contains(t *testing.T, item cronyaml.Error, want string) {
	t.Helper()
	if !strings.Contains(item.Message, want) {
		t.Errorf("il messaggio su %s non contiene %q:\n  %s", item.Path, want, item.Message)
	}
}

func at(t *testing.T, item cronyaml.Error, line, column int) {
	t.Helper()
	if item.Line != line || item.Column != column {
		t.Errorf("errore su %s alla posizione %d:%d, attesa %d:%d\n  %s",
			item.Path, item.Line, item.Column, line, column, item.Message)
	}
}
