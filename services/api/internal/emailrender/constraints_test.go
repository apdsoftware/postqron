package emailrender_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"unicode"

	"github.com/apdsoftware/postqron/services/api/internal/emailrender"
)

// I client di posta non sono browser. Questo test fissa i vincoli che il
// README di emails/templates spiega, così restano vincoli invece di diventare
// buone intenzioni al primo template aggiunto.
func TestRenderedHTMLObeysEmailClientConstraints(t *testing.T) {
	r := newRenderer(t)
	data := sampleData()

	// Costrutti che un browser rende e Outlook o Gmail no: il motore di Word
	// non conosce flexbox né grid, e Gmail elimina position e float.
	forbidden := map[string]string{
		"<script":           "JavaScript: rimosso da ogni client, e sospetto per i filtri antispam",
		"display:flex":      "flexbox: il motore di Word di Outlook non lo implementa",
		"display:grid":      "grid: stesso motivo di flexbox",
		"position:absolute": "posizionamento assoluto: Gmail lo elimina dallo stile",
		"position:fixed":    "posizionamento fisso: come sopra",
		"float:":            "float: reso in modo incoerente, il layout va fatto con le tabelle",
		"<img":              "immagine: i client le bloccano per impostazione predefinita",
		"http://":           "risorsa o link in chiaro",
		"<!--":              "commento HTML",
	}

	for _, event := range emailrender.Events() {
		t.Run(string(event), func(t *testing.T) {
			message, err := r.Render(event, "en", data[event])
			if err != nil {
				t.Fatalf("Render: %v", err)
			}
			lowered := strings.ToLower(message.HTML)

			for token, why := range forbidden {
				if strings.Contains(lowered, token) {
					t.Errorf("l'HTML contiene %q — %s", token, why)
				}
			}

			// Larghezza fissa più massima: l'attributo per chi ignora il CSS,
			// la proprietà per chi ignora l'attributo.
			if !strings.Contains(message.HTML, `width="600"`) {
				t.Error("manca la larghezza fissa in attributo")
			}
			if !strings.Contains(message.HTML, "max-width:600px") {
				t.Error("manca la larghezza massima nello stile")
			}
			// Le tabelle sono struttura, non dati: dichiararlo evita che un
			// lettore di schermo le annunci come tabelle da navigare.
			if strings.Count(message.HTML, "<table") != strings.Count(message.HTML, `role="presentation"`) {
				t.Error("non tutte le tabelle sono marcate role=\"presentation\"")
			}
			if !strings.Contains(message.HTML, `<meta name="viewport"`) {
				t.Error("manca il viewport: su mobile il messaggio verrebbe rimpicciolito")
			}
			// Un titolo per riga: gli assistenti vocali lo usano per navigare.
			if strings.Count(message.HTML, "<h1") != 1 {
				t.Error("l'email deve avere esattamente un <h1>")
			}
		})
	}
}

// html/template rimuove i commenti HTML dall'output. È il motivo per cui i
// commenti condizionali MSO — la via classica per dare a Outlook un markup
// tutto suo — non sono utilizzabili qui, e per cui il layout è costruito senza.
// Se un giorno il comportamento cambiasse, questo test lo direbbe.
func TestHTMLCommentsDoNotSurviveRendering(t *testing.T) {
	r, err := rendererWith(t, map[string]string{
		"welcome.html.tmpl": `{{define "preheader"}}x{{end}}` + "\n" +
			`{{define "content"}}<!--[if mso]><p>solo Outlook</p><![endif]--><h1>t</h1>{{end}}`,
	})
	if err != nil {
		t.Fatalf("NewFromDir: %v", err)
	}
	message, err := r.Render(emailrender.EventWelcome, "en", emailrender.WelcomeData{})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if strings.Contains(message.HTML, "[if mso]") {
		t.Skip("html/template ora conserva i commenti: i condizionali MSO tornano utilizzabili")
	}
	if strings.Contains(message.HTML, "solo Outlook") {
		t.Error("il commento è stato rimosso ma il suo contenuto no")
	}
}

// SPEC §8-bis: «Nessuna stringa nei componenti». Vale per i template delle
// email come per quelli di Nuxt — una frase scritta dentro la struttura non è
// traducibile, e con cinque lingue diventa una struttura da duplicare cinque
// volte. Qui si verifica che, tolti i tag e le azioni, nei template non resti
// alcuna lettera.
func TestTemplatesContainNoProse(t *testing.T) {
	dir, err := emailrender.FindDir(".")
	if err != nil {
		t.Fatalf("FindDir: %v", err)
	}
	files, err := filepath.Glob(filepath.Join(dir, "*.tmpl"))
	if err != nil {
		t.Fatalf("Glob: %v", err)
	}
	if len(files) != 2*(len(emailrender.Events())+1) {
		t.Fatalf("trovati %d template: attesi due file per evento più i due layout", len(files))
	}

	var (
		styleBlock = regexp.MustCompile(`(?s)<style.*?</style>`)
		action     = regexp.MustCompile(`(?s)\{\{.*?\}\}`)
		tag        = regexp.MustCompile(`(?s)<.*?>`)
		entity     = regexp.MustCompile(`&#?[a-zA-Z0-9]+;`)
	)

	for _, file := range files {
		t.Run(filepath.Base(file), func(t *testing.T) {
			raw, err := os.ReadFile(file)
			if err != nil {
				t.Fatalf("lettura: %v", err)
			}
			stripped := string(raw)
			for _, pattern := range []*regexp.Regexp{styleBlock, action, tag, entity} {
				stripped = pattern.ReplaceAllString(stripped, "")
			}
			for _, r := range stripped {
				if unicode.IsLetter(r) {
					t.Fatalf("testo letterale nel template attorno a %q: i testi stanno in %s/",
						strings.TrimSpace(collapse(stripped)), emailrender.LocalesDir)
				}
			}
		})
	}
}

func collapse(s string) string { return strings.Join(strings.Fields(s), " ") }

// Ogni evento ha entrambe le varianti, e non esistono template orfani.
func TestEveryEventHasBothVariants(t *testing.T) {
	dir, err := emailrender.FindDir(".")
	if err != nil {
		t.Fatalf("FindDir: %v", err)
	}
	for _, event := range emailrender.Events() {
		for _, suffix := range []string{".html.tmpl", ".txt.tmpl"} {
			if _, err := os.Stat(filepath.Join(dir, string(event)+suffix)); err != nil {
				t.Errorf("manca il template di %s: %v", event, err)
			}
		}
	}
	for _, language := range emailrender.Languages {
		path := filepath.Join(dir, emailrender.LocalesDir, language+".json")
		if _, err := os.Stat(path); err != nil {
			t.Errorf("manca il file dei testi di %q: %v", language, err)
		}
	}
}

// Nessuna struttura di questo package deve avere un campo per un segreto.
//
// È il vincolo espresso come proprietà del tipo invece che come raccomandazione
// nel README: un template non può interpolare ciò che non gli viene mai
// passato, e chi aggiungesse un campo `ResetToken` per far stare un link
// monouso nell'email di sicurezza troverebbe questo test rosso prima di
// trovarci un revisore d'accordo.
func TestDataTypesCarryNoSecrets(t *testing.T) {
	types := []reflect.Type{
		reflect.TypeOf(emailrender.Site{}),
		reflect.TypeOf(emailrender.Message{}),
		reflect.TypeOf(emailrender.WelcomeData{}),
		reflect.TypeOf(emailrender.JobFailedData{}),
		reflect.TypeOf(emailrender.PlanChangedData{}),
		reflect.TypeOf(emailrender.SecurityAlertData{}),
		// L'`UnsubscribeURL` di questa struttura porta un valore firmato, e passa
		// il controllo perché il nome non lo nasconde: è la promessa di §2.8
		// scritta dove serve, non una credenziale trapelata nel corpo. Ciò che
		// resta vietato è un campo che autorizzi qualcosa **oltre** la
		// disiscrizione.
		reflect.TypeOf(emailrender.ProductUpdateData{}),
	}
	suspicious := []string{
		"secret", "token", "password", "credential",
		"key", "bearer", "signature", "private", "hash", "salt",
	}

	for _, structType := range types {
		for i := range structType.NumField() {
			name := strings.ToLower(structType.Field(i).Name)
			for _, needle := range suspicious {
				if strings.Contains(name, needle) {
					t.Errorf("%s.%s: il nome suggerisce un segreto. "+
						"Un'email transazionale non trasporta credenziali: se serve un valore monouso, "+
						"il flusso appartiene all'autenticazione, non a questo renderer",
						structType.Name(), structType.Field(i).Name)
				}
			}
		}
	}
}

// R20.1: Mailronix risponde 202 identico verso un destinatario recapitabile e
// verso uno in suppression list, quindi il recapito non è osservabile. Un testo
// che dà per avvenuto un invio precedente afferma qualcosa che non sappiamo.
//
// Il controllo è sull'inglese perché l'inglese è la sorgente (SPEC §8-bis): le
// traduzioni della issue #446 seguono lui, e un'affermazione che non c'è qui
// non può comparire lì se non introdotta di sana pianta.
func TestEnglishCopyDoesNotAssumeDelivery(t *testing.T) {
	dir, err := emailrender.FindDir(".")
	if err != nil {
		t.Fatalf("FindDir: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, emailrender.LocalesDir, "en.json"))
	if err != nil {
		t.Fatalf("lettura di en.json: %v", err)
	}

	var nested map[string]map[string]string
	if err := json.Unmarshal(raw, &nested); err != nil {
		t.Fatalf("en.json: %v", err)
	}

	claims := []*regexp.Regexp{
		regexp.MustCompile(`(?i)\bwe\s+(have\s+|already\s+)*(e-?mailed|notified|informed|warned|alerted|told)\s+you\b`),
		regexp.MustCompile(`(?i)\byou\s+(have\s+|should\s+have\s+|must\s+have\s+)*received\b`),
		regexp.MustCompile(`(?i)\bcheck\s+your\s+inbox\b`),
		regexp.MustCompile(`(?i)\b(previous|earlier|last)\s+(e-?mail|message|alert)\s+(we|that\s+we)\s+sent\b`),
		regexp.MustCompile(`(?i)\bas\s+(we\s+)?(mentioned|stated|said)\s+in\s+(our|the)\s+(previous|earlier|last)\b`),
		regexp.MustCompile(`(?i)\bthis\s+e-?mail\s+(was|has\s+been)\s+delivered\b`),
	}

	for section, texts := range nested {
		for key, text := range texts {
			for _, claim := range claims {
				if match := claim.FindString(text); match != "" {
					t.Errorf("%s.%s presume il recapito di un messaggio precedente (%q). "+
						"Mailronix risponde 202 anche verso un destinatario in suppression list: "+
						"il recapito non è osservabile (R20.1)", section, key, match)
				}
			}
		}
	}
}
