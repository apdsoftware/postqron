package cronyaml_test

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"testing"

	"github.com/apdsoftware/postqron/services/api/internal/cronyaml"
)

// fakeGuard registra gli URL su cui è stato interrogato e rifiuta quelli che
// contengono una parola chiave. Il testo dell'errore è quello che l'utente
// vedrebbe: [jobs.TargetGuard] è deliberatamente poco descrittivo, e questo
// package lo riporta senza aggiungerci niente.
type fakeGuard struct {
	visti    []string
	rifiuta  string
	chiamate int
}

func (g *fakeGuard) CheckTarget(_ context.Context, target *url.URL) error {
	g.chiamate++
	g.visti = append(g.visti, target.String())
	if g.rifiuta != "" && strings.Contains(target.Host, g.rifiuta) {
		return errors.New("destinazione non ammessa")
	}
	return nil
}

// TestIlControlloSuiTargetVieneInterrogatoPerOgniJob (R38).
func TestIlControlloSuiTargetVieneInterrogatoPerOgniJob(t *testing.T) {
	guard := &fakeGuard{rifiuta: "interno.lan"}

	items := mustReject(t, `
version: 1
jobs:
  - name: pubblico
    every: 1m
    request: { url: https://esempio.it/ok }
  - name: interno
    every: 1m
    request: { url: https://interno.lan/segreto }
`, cronyaml.Options{Plan: teamPlan(), Guard: guard})

	if guard.chiamate != 2 {
		t.Errorf("il controllo è stato interrogato %d volte, attese 2 (una per job)", guard.chiamate)
	}
	item := find(t, items, "jobs[1].request.url")
	at(t, item, 8, 21)
	// Il testo della guardia arriva all'utente **così com'è**: [jobs.TargetGuard]
	// è deliberatamente poco descrittivo, perché distinguere «loopback» da
	// «link-local» da «nome inesistente» risponderebbe, a chiunque abbia un
	// account gratuito, alla domanda «questo nome, dalla vostra rete, risolve
	// internamente?». Il rimedio lo aggiunge questo package, e non dice niente
	// della nostra rete.
	contains(t, item, "Destinazione non ammessa")
	if !hasRemedy(item.Message) {
		t.Errorf("un rifiuto della guardia non dice come correggere:\n  %s", item.Message)
	}
}

// TestUnRiferimentoNellHostSaltaIlControlloSuiTarget.
//
// Con un `${VAR}` nell'host non c'è un nome da risolvere: il segnaposto non è il
// bersaglio vero, e interrogare il controllo direbbe qualcosa di un host che non
// esiste — rifiutando un file legittimo, o peggio accettandolo per il motivo
// sbagliato.
//
// Non è una falla, ed è importante che sia chiaro **perché**: questo controllo
// anticipa la diagnosi e non è la difesa. La difesa è l'apertura della
// connessione, che avviene sull'URL risolto, per ogni salto della catena di
// redirect, e che non passa da qui (vedi [jobs.TargetGuard]).
func TestUnRiferimentoNellHostSaltaIlControlloSuiTarget(t *testing.T) {
	guard := &fakeGuard{}

	mustParse(t, `
version: 1
jobs:
  - name: con-riferimento-nell-host
    every: 1m
    request: { url: "https://${HOST}/hook" }
  - name: con-riferimento-nel-percorso
    every: 1m
    request: { url: "https://esempio.it/${PERCORSO}" }
`, cronyaml.Options{Plan: teamPlan(), Secrets: secretsOf("HOST", "PERCORSO"), Guard: guard})

	if guard.chiamate != 1 {
		t.Fatalf("il controllo è stato interrogato %d volte, attesa 1: solo il job il cui host è scritto per intero", guard.chiamate)
	}
	if !strings.Contains(guard.visti[0], "esempio.it") {
		t.Errorf("il controllo è stato interrogato su %q", guard.visti[0])
	}
}

// TestIlControlloSuiTargetEFacoltativo: senza guardia il parser fa tutto il
// resto. Serve a chi valida un file fuori dal sync — per esempio un comando che
// controlla il `cron.yaml` prima del commit — e non ha una rete da proteggere.
func TestIlControlloSuiTargetEFacoltativo(t *testing.T) {
	mustParse(t, `
version: 1
jobs:
  - name: uno
    every: 1m
    request: { url: https://esempio.it/ok }
`, cronyaml.Options{Plan: teamPlan()})
}

// TestIlNomeDelFileCompareNegliErrori: il `cron.yaml` sta nella radice del
// repository, ma chi valida può leggerlo da un percorso diverso, e l'errore deve
// nominare il file che l'utente ha davanti.
func TestIlNomeDelFileCompareNegliErrori(t *testing.T) {
	_, err := cronyaml.Parse(t.Context(), yamlSource(`
version: 1
jobs:
  - name: uno
    every: 1x
    request: { url: https://esempio.it/x }
`), cronyaml.Options{Source: ".postqron/cron.yaml", Plan: teamPlan()})

	if err == nil {
		t.Fatal("Parse non ha rifiutato il file")
	}
	if !strings.Contains(err.Error(), ".postqron/cron.yaml:4:12:") {
		t.Errorf("il nome del file non compare nell'errore:\n%s", err)
	}
}
