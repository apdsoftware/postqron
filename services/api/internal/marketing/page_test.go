package marketing_test

import (
	"os"
	"regexp"
	"strings"
	"testing"
	"unicode"

	"github.com/apdsoftware/postqron/services/api/internal/emailrender"
	"github.com/apdsoftware/postqron/services/api/internal/marketing"
)

func newPage(t *testing.T) *marketing.Page {
	t.Helper()
	page, err := marketing.NewPage(newRenderer(t), testSite)
	if err != nil {
		t.Fatalf("NewPage: %v", err)
	}
	return page
}

var stati = []marketing.PageState{
	marketing.PageConfirm, marketing.PageDone,
	marketing.PageAlready, marketing.PageInvalid,
}

// La pagina si compila in tutti gli stati e in tutte le lingue.
//
// Le quattro lingue che ancora ricadono sull'inglese sono il punto, come per le
// email (issue #446): la struttura regge le cinque lingue prima che le
// traduzioni esistano, e una chiave dimenticata si vede qui e non in produzione.
func TestLaPaginaSiCompilaInOgniStatoEOgniLingua(t *testing.T) {
	page := newPage(t)

	for _, state := range stati {
		for _, language := range emailrender.Languages {
			t.Run(string(state)+"/"+language, func(t *testing.T) {
				html, err := page.Render(state, language, "u-1.abc")
				if err != nil {
					t.Fatalf("Render: %v", err)
				}
				if !strings.Contains(string(html), `lang="`+language+`"`) {
					t.Errorf("la pagina non dichiara la lingua %q", language)
				}
				if !strings.Contains(string(html), `<meta name="viewport"`) {
					t.Error("manca il viewport: su mobile la pagina verrebbe rimpicciolita")
				}
			})
		}
	}
}

// Solo la conferma ha un form, e quel form è una `POST`.
//
// È la forma della promessa: la `GET` mostra e la `POST` agisce. Un form con
// `method="get"` rimetterebbe la revoca su una lettura, che è esattamente il
// difetto da cui questa pagina esiste per difendere.
func TestSoloLaConfermaHaUnFormEQuelFormEUnaPost(t *testing.T) {
	page := newPage(t)
	const token = "u-1.firmafinta"

	confirm, err := page.Render(marketing.PageConfirm, "en", token)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	testo := string(confirm)

	if !strings.Contains(testo, `method="post"`) {
		t.Error("la pagina di conferma non ha un form in POST")
	}
	if !strings.Contains(testo, `action="`+marketing.UnsubscribePath+`"`) {
		t.Errorf("il form non punta a %s", marketing.UnsubscribePath)
	}
	if !strings.Contains(testo, `name="token" value="`+token+`"`) {
		t.Error("il form non porta il token: la POST non saprebbe chi disiscrivere")
	}
	// Senza JavaScript: la pagina la apre un client di posta, e una promessa
	// legale non deve dipendere dall'esecuzione di codice.
	if strings.Contains(strings.ToLower(testo), "<script") {
		t.Error("la pagina contiene JavaScript")
	}

	for _, state := range []marketing.PageState{marketing.PageDone, marketing.PageAlready, marketing.PageInvalid} {
		html, err := page.Render(state, "en", token)
		if err != nil {
			t.Fatalf("Render %s: %v", state, err)
		}
		if strings.Contains(string(html), "<form") {
			t.Errorf("lo stato %s ha un form: non c'è niente da confermare", state)
		}
		if strings.Contains(string(html), token) {
			t.Errorf("lo stato %s ripete il token nella risposta senza averne bisogno", state)
		}
	}
}

// La pagina dice, prima di agire, che le transazionali continuano.
//
// È l'altra promessa di §2.8 — «unsubscribing stops marketing email only» — e
// questa pagina è l'unico posto in cui possiamo dirlo *prima* che l'utente
// decida. Una disiscrizione silenziosa su `GET` non avrebbe modo di mantenerla.
func TestLaConfermaDiceCheLeTransazionaliContinuano(t *testing.T) {
	page := newPage(t)

	html, err := page.Render(marketing.PageConfirm, "en", "u-1.abc")
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	scope, err := newRenderer(t).Text("en", "unsubscribe.scope")
	if err != nil {
		t.Fatalf("Text: %v", err)
	}
	if !strings.Contains(string(html), scope) {
		t.Error("la pagina di conferma non dice che le email transazionali continuano ad arrivare")
	}
}

// Un token ostile non esce dal campo in cui entra.
//
// La pagina la apre chiunque abbia un link, e il link se lo può scrivere da sé:
// il valore che ci mette dentro finisce in un attributo HTML. `html/template`
// lo cita da solo, e questo test è ciò che impedisce a qualcuno di sostituirlo
// un giorno con una concatenazione di stringhe.
func TestUnTokenOstileNonEsceDalCampo(t *testing.T) {
	page := newPage(t)

	html, err := page.Render(marketing.PageConfirm, "en", `"><script>alert(1)</script>`)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if strings.Contains(string(html), "<script>alert(1)</script>") {
		t.Fatal("il token è finito nella pagina senza essere citato")
	}
}

// Nessun testo scritto dentro il template.
//
// Vale la regola di SPEC §8-bis e dei template delle email: una frase dentro la
// struttura non è traducibile, e con cinque lingue diventa una struttura da
// duplicare cinque volte.
func TestIlTemplateDellaPaginaNonContieneTesto(t *testing.T) {
	raw, err := os.ReadFile("unsubscribe.html.tmpl")
	if err != nil {
		t.Fatalf("lettura del template: %v", err)
	}

	var (
		styleBlock = regexp.MustCompile(`(?s)<style.*?</style>`)
		action     = regexp.MustCompile(`(?s)\{\{.*?\}\}`)
		tag        = regexp.MustCompile(`(?s)<.*?>`)
		entity     = regexp.MustCompile(`&#?[a-zA-Z0-9]+;`)
	)

	stripped := string(raw)
	for _, pattern := range []*regexp.Regexp{styleBlock, action, tag, entity} {
		stripped = pattern.ReplaceAllString(stripped, "")
	}
	for _, r := range stripped {
		if unicode.IsLetter(r) {
			t.Fatalf("testo letterale nel template attorno a %q: i testi stanno in %s/",
				strings.Join(strings.Fields(stripped), " "), emailrender.LocalesDir)
		}
	}
}

// Uno stato sconosciuto non produce una pagina muta.
func TestUnoStatoSconosciutoNonSiCompila(t *testing.T) {
	if _, err := newPage(t).Render(marketing.PageState("boh"), "en", ""); err == nil {
		t.Error("uno stato sconosciuto ha prodotto una pagina")
	}
}
