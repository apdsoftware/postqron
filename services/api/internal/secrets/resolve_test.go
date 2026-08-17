package secrets_test

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/apdsoftware/postqron/services/api/internal/secrets"
)

// campi estrae i campi segnalati da un errore di validazione, con il loro
// codice: è la forma su cui i test controllano *dove* l'errore è ancorato, che è
// l'informazione che l'utente riceve dopo un `git push`.
func campi(t *testing.T, err error) []string {
	t.Helper()
	invalid, ok := secrets.AsValidation(err)
	if !ok {
		t.Fatalf("errore = %v, atteso un *ValidationError", err)
	}
	out := make([]string, 0, len(invalid.Fields))
	for _, field := range invalid.Fields {
		out = append(out, field.Field+"/"+field.Code)
	}
	return out
}

// --------------------------------------------------------- i riferimenti

func TestReferencesFindsThemEverywhere(t *testing.T) {
	req := secrets.Request{
		URL: "https://api.example.com/${TENANT}/digest?key=${API_KEY}",
		Headers: map[string]string{
			"Authorization": "Bearer ${DIGEST_TOKEN}",
			"X-Trace":       "statico",
		},
		Body: `{"kind":"daily","firma":"${API_KEY}"}`,
	}

	refs, err := secrets.References(req)
	if err != nil {
		t.Fatalf("References: %v", err)
	}

	// L'ordine è deterministico: URL, header per nome crescente, corpo. Conta
	// perché finisce in un messaggio d'errore che l'utente confronta con quello
	// di prima.
	var got []string
	for _, ref := range refs {
		got = append(got, ref.Field+"="+ref.Name)
	}
	want := []string{
		"request.url=TENANT",
		"request.url=API_KEY",
		"request.headers.Authorization=DIGEST_TOKEN",
		"request.body=API_KEY",
	}
	if !slices.Equal(got, want) {
		t.Errorf("References =\n  %v\natteso\n  %v", got, want)
	}

	if names := secrets.Names(refs); !slices.Equal(names, []string{"API_KEY", "DIGEST_TOKEN", "TENANT"}) {
		t.Errorf("Names = %v", names)
	}
}

// `$${` è un `${` letterale, e un `$` da solo è testo. Senza la prima regola un
// corpo che contiene segnaposto di un altro sistema sarebbe inesprimibile; senza
// la seconda, un prezzo in dollari diventerebbe un errore di sintassi.
func TestReferencesEscapeAndLiterals(t *testing.T) {
	req := secrets.Request{
		URL:  "https://api.example.com/prezzi",
		Body: `{"template":"$${NON_UN_SEGRETO}","prezzo":"$5.00","reale":"${TOKEN}"}`,
	}

	refs, err := secrets.References(req)
	if err != nil {
		t.Fatalf("References: %v", err)
	}
	if names := secrets.Names(refs); !slices.Equal(names, []string{"TOKEN"}) {
		t.Fatalf("Names = %v, atteso [TOKEN]", names)
	}

	set := secrets.NewNameSet([]string{"TOKEN"})
	if err := set.Validate(req); err != nil {
		t.Fatalf("Validate: %v", err)
	}

	// E in esecuzione l'escape diventa il testo letterale, una volta sola.
	resolved := resolveWith(t, req, map[string]string{"TOKEN": "valore-segreto-lungo"})
	if !strings.Contains(resolved.Body(), `"template":"${NON_UN_SEGRETO}"`) {
		t.Errorf("l'escape non è diventato testo letterale: %s", resolved.Body())
	}
	if !strings.Contains(resolved.Body(), `"prezzo":"$5.00"`) {
		t.Errorf("il dollaro letterale è stato mangiato: %s", resolved.Body())
	}
}

// La sintassi sbagliata è un errore, non testo: `${digest}` scritto minuscolo
// non deve arrivare a un bersaglio esterno come stringa letterale dentro a una
// testata `Authorization`.
func TestReferencesRejectsMalformedSyntax(t *testing.T) {
	tests := []struct {
		name string
		req  secrets.Request
		code string
	}{
		{
			"minuscolo",
			secrets.Request{Headers: map[string]string{"Authorization": "Bearer ${digest_token}"}},
			"request.headers.Authorization/invalid_reference",
		},
		{
			"graffa mai chiusa",
			secrets.Request{URL: "https://x.test/${TOKEN"},
			"request.url/unterminated_reference",
		},
		{
			"riferimento vuoto",
			secrets.Request{Body: "${}"},
			"request.body/invalid_reference",
		},
		{
			"trattino nel nome",
			secrets.Request{Body: "${DIGEST-TOKEN}"},
			"request.body/invalid_reference",
		},
		{
			"riferimento nel nome della testata",
			secrets.Request{Headers: map[string]string{"${NOME}": "valore"}},
			"request.headers.${NOME}/reference_in_header_name",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := secrets.References(test.req)
			if got := campi(t, err); !slices.Contains(got, test.code) {
				t.Errorf("campi = %v, atteso %q", got, test.code)
			}
		})
	}
}

// Il messaggio dell'errore più frequente indica la correzione esatta invece
// della regola: chi ha scritto `${digest_token}` voleva `${DIGEST_TOKEN}`.
func TestLowercaseReferenceSuggestsTheFix(t *testing.T) {
	_, err := secrets.References(secrets.Request{Body: "${digest_token}"})
	invalid, ok := secrets.AsValidation(err)
	if !ok {
		t.Fatalf("errore = %v", err)
	}
	if !strings.Contains(invalid.Fields[0].Message, "${DIGEST_TOKEN}") {
		t.Errorf("il messaggio non indica la correzione: %s", invalid.Fields[0].Message)
	}
}

// ------------------------------------------------------ validazione al sync

// **Il cuore della issue.** Un riferimento a un segreto inesistente fallisce
// qui, dove chi ha fatto `git push` sta ancora guardando il terminale — non
// all'esecuzione delle tre di notte.
func TestValidateFailsOnUnknownSecret(t *testing.T) {
	set := secrets.NewNameSet([]string{"DIGEST_TOKEN"})
	req := secrets.Request{
		URL:     "https://api.example.com/digest",
		Headers: map[string]string{"Authorization": "Bearer ${DIGEST_TOKEN}"},
		Body:    `{"firma":"${FIRMA}"}`,
	}

	err := set.Validate(req)
	if err == nil {
		t.Fatal("un riferimento a un segreto inesistente è passato dalla validazione")
	}
	if got := campi(t, err); !slices.Equal(got, []string{"request.body/unknown_secret"}) {
		t.Fatalf("campi = %v", got)
	}

	// Il messaggio dice che cosa manca e che cosa c'è: nel caso normale — un
	// errore di battitura — la risposta è nell'elenco.
	invalid, _ := secrets.AsValidation(err)
	message := invalid.Fields[0].Message
	if !strings.Contains(message, `"FIRMA"`) || !strings.Contains(message, "DIGEST_TOKEN") {
		t.Errorf("il messaggio non aiuta: %s", message)
	}
}

// I motivi si accumulano: chi ha sbagliato due variabili le corregge in un push
// solo. Lo stesso nome sbagliato in due campi diversi resta però un errore per
// campo, e ripetuto due volte nello stesso campo resta uno solo.
func TestValidateReportsEveryFieldOnce(t *testing.T) {
	set := secrets.NewNameSet(nil)
	err := set.Validate(secrets.Request{
		URL:  "https://x.test/${UNO}",
		Body: "${DUE} e ancora ${DUE}",
	})

	if got := campi(t, err); !slices.Equal(got,
		[]string{"request.url/unknown_secret", "request.body/unknown_secret"}) {
		t.Errorf("campi = %v", got)
	}
}

// Una richiesta senza riferimenti passa sempre, anche su un workspace che non ha
// nessun segreto: la maggioranza dei job è così, e non deve pagare niente.
func TestValidateAcceptsARequestWithoutReferences(t *testing.T) {
	set := secrets.NewNameSet(nil)
	if err := set.Validate(secrets.Request{
		URL:     "https://api.example.com/health",
		Headers: map[string]string{"Accept": "application/json"},
	}); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

// La sintassi sbagliata e il segreto mancante arrivano insieme: sono due
// correzioni da fare nello stesso file.
func TestValidateMergesSyntaxAndUnknownNames(t *testing.T) {
	err := secrets.NewNameSet(nil).Validate(secrets.Request{
		URL:  "https://x.test/${minuscolo}",
		Body: "${MANCANTE}",
	})
	got := campi(t, err)
	if !slices.Contains(got, "request.url/invalid_reference") ||
		!slices.Contains(got, "request.body/unknown_secret") {
		t.Errorf("campi = %v: attesi entrambi i motivi", got)
	}
}

func TestNameSetBasics(t *testing.T) {
	set := secrets.NewNameSet([]string{"B", "A"})
	if !set.Has("A") || set.Has("C") {
		t.Error("Has sbagliato")
	}
	if names := set.Names(); !slices.Equal(names, []string{"A", "B"}) {
		t.Errorf("Names = %v, atteso ordine alfabetico", names)
	}
	if !secrets.ValidName("DIGEST_TOKEN_2") || secrets.ValidName("2_TOKEN") ||
		secrets.ValidName("") || secrets.ValidName(strings.Repeat("A", 65)) {
		t.Error("ValidName sbagliato")
	}
}

// --------------------------------------------------------------- redazione

// Il bersaglio riflette la nostra credenziale nel proprio messaggio d'errore, e
// l'estratto della risposta è visibile all'utente e conservato. La redazione è
// ciò che impedisce che R43 sia rispettata da noi e violata da loro.
func TestExcerptRedactsWhatTheTargetEchoesBack(t *testing.T) {
	resolved := resolveWith(t,
		secrets.Request{Headers: map[string]string{"Authorization": "Bearer ${TOKEN}"}},
		map[string]string{"TOKEN": "finto-token-del-digest"})

	risposta := []byte(`{"error":"il token finto-token-del-digest non è valido"}`)
	excerpt := resolved.Redactor().Excerpt(risposta, 0)

	if strings.Contains(excerpt.String(), "finto-token-del-digest") {
		t.Fatalf("il segreto è rimasto nell'estratto: %s", excerpt)
	}
	if !strings.Contains(excerpt.String(), "${TOKEN}") {
		t.Errorf("l'estratto non dice che cosa è stato tolto: %s", excerpt)
	}
}

// La via di fuga meno evidente: `(*http.Client).Do` mette l'URL completo nel
// testo dell'errore, quindi un segreto in querystring finirebbe nel registro
// **anche se la risposta non è mai arrivata**.
func TestErrorTextRedactsTheURL(t *testing.T) {
	resolved := resolveWith(t,
		secrets.Request{URL: "https://api.example.com/digest?key=${API_KEY}"},
		map[string]string{"API_KEY": "finta-chiave-in-querystring"})

	err := fmt.Errorf(`Get %q: dial tcp: i/o timeout`, resolved.URL())
	excerpt := resolved.Redactor().ErrorText(err, 0)

	if strings.Contains(excerpt.String(), "finta-chiave-in-querystring") {
		t.Fatalf("il segreto è rimasto nel testo dell'errore: %s", excerpt)
	}
	if excerpt.Empty() {
		t.Error("l'errore è stato cancellato invece che redatto")
	}
	if got := resolved.Redactor().ErrorText(nil, 0); !got.Empty() {
		t.Errorf("un errore nil dovrebbe dare un estratto vuoto, ha dato %q", got)
	}
}

// La redazione avviene **prima** del troncamento: al contrario, un valore a
// cavallo del taglio resterebbe visibile a metà.
func TestExcerptRedactsBeforeTruncating(t *testing.T) {
	resolved := resolveWith(t,
		secrets.Request{Body: "${TOKEN}"},
		map[string]string{"TOKEN": "segretissimo-valore"})

	raw := []byte(strings.Repeat("x", 20) + "segretissimo-valore" + strings.Repeat("y", 100))
	excerpt := resolved.Redactor().Excerpt(raw, 30)

	if strings.Contains(excerpt.String(), "segretissimo") {
		t.Fatalf("un pezzo di segreto è sopravvissuto al troncamento: %s", excerpt)
	}
	if got := len([]rune(excerpt.String())); got != 30 {
		t.Errorf("estratto di %d rune, attese 30", got)
	}
}

// Il troncamento conta le rune e non i byte: tagliare a metà una sequenza UTF-8
// produrrebbe un testo che PostgreSQL rifiuta.
func TestExcerptTruncatesRunes(t *testing.T) {
	var vuoto secrets.Redactor
	excerpt := vuoto.Excerpt([]byte("àèìòù-però-questo-è-lungo"), 5)
	if got := []rune(excerpt.String()); len(got) != 5 || string(got) != "àèìòù" {
		t.Errorf("estratto = %q", excerpt)
	}
}

// Il redattore vuoto è utilizzabile: un'esecuzione senza segreti deve poter
// produrre l'estratto della risposta allo stesso modo di una con segreti.
func TestZeroRedactorIsUsable(t *testing.T) {
	var vuoto secrets.Redactor
	if vuoto.Len() != 0 {
		t.Errorf("Len = %d", vuoto.Len())
	}
	if got := vuoto.Excerpt([]byte("risposta qualunque"), 0).String(); got != "risposta qualunque" {
		t.Errorf("Excerpt = %q", got)
	}
}

// Se un segreto è prefisso di un altro, la redazione toglie prima il più lungo:
// altrimenti resterebbe visibile la sua coda.
func TestRedactorHandlesOverlappingValues(t *testing.T) {
	resolved := resolveWith(t,
		secrets.Request{Body: "${CORTO} ${LUNGO}"},
		map[string]string{
			"CORTO": "abcdefgh",
			"LUNGO": "abcdefghij-coda-lunga",
		})

	excerpt := resolved.Redactor().Excerpt([]byte("visto: abcdefghij-coda-lunga"), 0)
	if strings.Contains(excerpt.String(), "ij-coda-lunga") {
		t.Errorf("è rimasta la coda del segreto più lungo: %s", excerpt)
	}
}

// --------------------------------------------------------------- Resolved

// Ciò che si stampa di una richiesta risolta è la richiesta **non** risolta:
// l'URL con i `${VAR}` come stavano nel `cron.yaml`.
func TestResolvedNeverPrintsItsSecrets(t *testing.T) {
	req := secrets.Request{
		URL:     "https://api.example.com/digest?key=${API_KEY}",
		Headers: map[string]string{"Authorization": "Bearer ${API_KEY}"},
		Body:    `{"firma":"${API_KEY}"}`,
	}
	resolved := resolveWith(t, req, map[string]string{"API_KEY": "ak_valore_segretissimo"})

	forms := []string{
		fmt.Sprintf("%v", resolved),
		fmt.Sprintf("%+v", resolved),
		fmt.Sprintf("%s", resolved),
		resolved.String(),
		logged(t, resolved),
	}
	for _, printed := range forms {
		if strings.Contains(printed, "ak_valore_segretissimo") {
			t.Errorf("una forma stampata contiene il segreto: %s", printed)
		}
	}

	// E i valori risolti ci sono, per chi li chiede con il metodo giusto.
	if !strings.Contains(resolved.URL(), "ak_valore_segretissimo") {
		t.Error("l'URL non è stato risolto")
	}
	if resolved.Headers()["Authorization"] != "Bearer ak_valore_segretissimo" {
		t.Errorf("header = %q", resolved.Headers()["Authorization"])
	}
	if !strings.Contains(resolved.Body(), "ak_valore_segretissimo") {
		t.Error("il corpo non è stato risolto")
	}
	if resolved.Template().URL != req.URL {
		t.Error("Template non restituisce la richiesta a riposo")
	}
}

// Gli header restituiti sono una copia: chi li modifica non modifica la
// risoluzione.
func TestResolvedHeadersAreACopy(t *testing.T) {
	resolved := resolveWith(t,
		secrets.Request{Headers: map[string]string{"Authorization": "Bearer ${TOKEN}"}},
		map[string]string{"TOKEN": "valore-segreto-lungo"})

	resolved.Headers()["Authorization"] = "manomesso"
	if resolved.Headers()["Authorization"] == "manomesso" {
		t.Error("Headers restituisce la mappa interna")
	}
}

// L'espansione non è ricorsiva: un valore che contiene `${ALTRO}` non viene
// riesaminato. Al contrario, chi può scrivere un segreto potrebbe leggerne un
// altro.
func TestExpansionIsNotRecursive(t *testing.T) {
	resolved := resolveWith(t,
		secrets.Request{Body: "${PRIMO}"},
		map[string]string{
			"PRIMO":  "prefisso-${SECONDO}",
			"SECOND": "non-deve-comparire",
		})

	if strings.Contains(resolved.Body(), "non-deve-comparire") {
		t.Fatalf("l'espansione è ricorsiva: %s", resolved.Body())
	}
	if !strings.Contains(resolved.Body(), "${SECONDO}") {
		t.Errorf("corpo = %s", resolved.Body())
	}
}
