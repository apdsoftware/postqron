package emailrender_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/apdsoftware/postqron/services/api/internal/emailrender"
)

// templatesWith copia emails/templates/ in una directory temporanea e vi
// applica delle sostituzioni. Serve a provare il comportamento multilingua
// senza aspettare le traduzioni della issue #446: qui le lingue si riempiono a
// piacere, una chiave alla volta.
//
// Un valore vuoto nella mappa cancella il file.
func templatesWith(t *testing.T, overrides map[string]string) string {
	t.Helper()

	source, err := emailrender.FindDir(".")
	if err != nil {
		t.Fatalf("FindDir: %v", err)
	}
	target := t.TempDir()

	err = filepath.WalkDir(source, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return os.MkdirAll(filepath.Join(target, relative), 0o755)
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(target, relative), content, 0o644)
	})
	if err != nil {
		t.Fatalf("copia dei template: %v", err)
	}

	for name, content := range overrides {
		path := filepath.Join(target, filepath.FromSlash(name))
		if content == "" {
			if err := os.Remove(path); err != nil {
				t.Fatalf("rimozione di %s: %v", name, err)
			}
			continue
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatalf("scrittura di %s: %v", name, err)
		}
	}
	return target
}

func rendererWith(t *testing.T, overrides map[string]string) (*emailrender.Renderer, error) {
	t.Helper()
	return emailrender.NewFromDir(templatesWith(t, overrides), testSite, emailrender.WithClock(fixedClock))
}

// Il ripiego sull'inglese è per chiave, non per file: una traduzione a metà
// mostra ciò che è tradotto e l'inglese per il resto. L'alternativa — o tutto
// o niente — significherebbe che aggiungere la prima riga di italiano non
// cambia nulla finché non ci sono anche le altre trenta.
func TestFallsBackToEnglishKeyByKey(t *testing.T) {
	r, err := rendererWith(t, map[string]string{
		"locales/it.json": `{"welcome": {"heading": "Benvenuto in {product}"}}`,
	})
	if err != nil {
		t.Fatalf("NewFromDir: %v", err)
	}

	message, err := r.Render(emailrender.EventWelcome, "it", emailrender.WelcomeData{RecipientName: "Sam"})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if !strings.Contains(message.Text, "Benvenuto in PostQron") {
		t.Errorf("il titolo tradotto non compare:\n%s", message.Text)
	}
	if !strings.Contains(message.Text, "Your account is ready.") {
		t.Errorf("le chiavi non tradotte non ricadono sull'inglese:\n%s", message.Text)
	}
	if message.Language != "it" {
		t.Errorf("Language = %q, atteso \"it\"", message.Language)
	}
}

// Una chiave presente in una traduzione ma non in inglese è il residuo di un
// testo tolto dalla sorgente: non verrebbe mai usata, e resterebbe lì a far
// credere di esserlo.
func TestRejectsOrphanTranslationKeys(t *testing.T) {
	_, err := rendererWith(t, map[string]string{
		"locales/de.json": `{"welcome": {"heading_old": "Willkommen"}}`,
	})
	if err == nil {
		t.Fatal("atteso un errore sulla chiave orfana")
	}
	if !strings.Contains(err.Error(), "welcome.heading_old") {
		t.Errorf("l'errore non nomina la chiave: %v", err)
	}
}

// Perdere un segnaposto traducendo è il modo più facile di rompere un testo, e
// il più difficile da vedere rileggendo. Fallisce il rendering, non il testo.
func TestRejectsTranslationThatDropsAPlaceholder(t *testing.T) {
	r, err := rendererWith(t, map[string]string{
		"locales/es.json": `{"welcome": {"heading": "Bienvenido"}}`,
	})
	if err != nil {
		t.Fatalf("NewFromDir: %v", err)
	}
	if _, err := r.Render(emailrender.EventWelcome, "es", emailrender.WelcomeData{}); err == nil {
		t.Fatal("atteso un errore sul segnaposto non usato")
	}
}

func TestRejectsBrokenCatalogs(t *testing.T) {
	tests := map[string]map[string]string{
		"sorgente inglese vuota":      {"locales/en.json": `{}`},
		"file di lingua mancante":     {"locales/fr.json": ""},
		"JSON non valido":             {"locales/it.json": `{"welcome":`},
		"valore che non è una string": {"locales/it.json": `{"welcome": {"heading": 3}}`},
	}
	for name, overrides := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := rendererWith(t, overrides); err == nil {
				t.Fatal("atteso un errore di caricamento")
			}
		})
	}
}

// La forma del plurale segue la regola della lingua che fornisce davvero il
// testo. In francese lo zero vuole il singolare; nelle altre quattro no. Se una
// lingua non è tradotta e si ricade sull'inglese, a decidere dev'essere
// l'inglese — altrimenti si sceglie la forma con la regola di una lingua e si
// stampa la frase di un'altra.
func TestPluralFormFollowsTheLanguageThatSuppliesTheText(t *testing.T) {
	r, err := rendererWith(t, map[string]string{
		"locales/fr.json": `{"job_failed": {` +
			`"summary_one": "SINGULIER {job} {count}",` +
			`"summary_other": "PLURIEL {job} {count}"}}`,
	})
	if err != nil {
		t.Fatalf("NewFromDir: %v", err)
	}

	data := sampleData()[emailrender.EventJobFailed].(emailrender.JobFailedData)

	// Il francese è tradotto: vale la sua regola, e lo zero è singolare.
	// (ConsecutiveFailures non può valere zero nei dati veri; qui conta la
	// scelta della forma, non il caso d'uso.)
	for count, expected := range map[int]string{1: "SINGULIER", 2: "PLURIEL"} {
		data.ConsecutiveFailures = count
		message, err := r.Render(emailrender.EventJobFailed, "fr", data)
		if err != nil {
			t.Fatalf("Render fr con %d: %v", count, err)
		}
		if !strings.Contains(message.Text, expected) {
			t.Errorf("fr con %d fallimenti: attesa la forma %s\n%s", count, expected, message.Text)
		}
	}

	// L'italiano non è tradotto: ricade sull'inglese, testo e regola insieme.
	data.ConsecutiveFailures = 1
	message, err := r.Render(emailrender.EventJobFailed, "it", data)
	if err != nil {
		t.Fatalf("Render it: %v", err)
	}
	if !strings.Contains(message.Text, "has failed 1 time in a row.") {
		t.Errorf("l'italiano non ricade sul singolare inglese:\n%s", message.Text)
	}
}

func TestNormalizeLanguage(t *testing.T) {
	tests := map[string]string{
		"it":         "it",
		"IT":         "it",
		"it-IT":      "it",
		"fr_CA":      "fr",
		" de ":       "de",
		"es-419":     "es",
		"en":         "en",
		"pt":         "en",
		"":           "en",
		"klingon":    "en",
		"it-IT-x-yz": "it",
	}
	for input, expected := range tests {
		if got := emailrender.NormalizeLanguage(input); got != expected {
			t.Errorf("NormalizeLanguage(%q) = %q, atteso %q", input, got, expected)
		}
	}
}

// Una lingua sconosciuta non è un errore: ricade sull'inglese, come la spec
// prescrive quando nessuna preferenza corrisponde (SPEC §8-bis).
func TestRenderFallsBackToEnglishForUnknownLanguage(t *testing.T) {
	r := newRenderer(t)

	message, err := r.Render(emailrender.EventWelcome, "pt-BR", emailrender.WelcomeData{})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if message.Language != "en" {
		t.Errorf("Language = %q, atteso \"en\"", message.Language)
	}
	if !strings.Contains(message.HTML, `<html lang="en"`) {
		t.Error("l'attributo lang dell'HTML non riflette la lingua effettiva")
	}
}

// L'attributo lang serve ai lettori di schermo per pronunciare il testo nella
// lingua giusta: se cambia la lingua deve cambiare anche lui.
func TestHTMLDeclaresTheLanguage(t *testing.T) {
	r, err := rendererWith(t, map[string]string{
		"locales/it.json": `{"welcome": {"heading": "Benvenuto in {product}"}}`,
	})
	if err != nil {
		t.Fatalf("NewFromDir: %v", err)
	}
	message, err := r.Render(emailrender.EventWelcome, "it", emailrender.WelcomeData{})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if !strings.Contains(message.HTML, `<html lang="it" dir="ltr">`) {
		t.Errorf("attributo lang mancante o sbagliato:\n%.200s", message.HTML)
	}
}

func TestSiteValidate(t *testing.T) {
	valid := testSite

	tests := map[string]func(*emailrender.Site){
		"nome del prodotto vuoto": func(s *emailrender.Site) { s.ProductName = " " },
		"URL pubblico vuoto":      func(s *emailrender.Site) { s.PublicBaseURL = "" },
		"URL senza schema":        func(s *emailrender.Site) { s.PublicBaseURL = "postqron.test" },
		"schema non navigabile":   func(s *emailrender.Site) { s.AppBaseURL = "ftp://app.postqron.test" },
		"barra finale":            func(s *emailrender.Site) { s.AppBaseURL = "https://app.postqron.test/" },
		"supporto non valido":     func(s *emailrender.Site) { s.SupportEmail = "supporto chiocciola" },
	}
	for name, corrupt := range tests {
		t.Run(name, func(t *testing.T) {
			site := valid
			corrupt(&site)
			if err := site.Validate(); err == nil {
				t.Fatal("atteso un errore di convalida")
			}
			if _, err := emailrender.NewFromDir(templatesWith(t, nil), site); err == nil {
				t.Fatal("New ha accettato un Site non valido")
			}
		})
	}

	if err := valid.Validate(); err != nil {
		t.Errorf("il Site di prova non passa la convalida: %v", err)
	}
}

func TestFindDir(t *testing.T) {
	dir, err := emailrender.FindDir(".")
	if err != nil {
		t.Fatalf("FindDir: %v", err)
	}
	if filepath.Base(dir) != "templates" {
		t.Errorf("FindDir ha trovato %q", dir)
	}
	if _, err := os.Stat(filepath.Join(dir, "layout.html.tmpl")); err != nil {
		t.Errorf("la directory trovata non contiene il layout: %v", err)
	}

	t.Run("la variabile d'ambiente ha la precedenza", func(t *testing.T) {
		custom := t.TempDir()
		t.Setenv(emailrender.DirEnvVar, custom)
		got, err := emailrender.FindDir(".")
		if err != nil {
			t.Fatalf("FindDir: %v", err)
		}
		if got != custom {
			t.Errorf("FindDir = %q, atteso %q", got, custom)
		}
	})

	t.Run("una directory inesistente è un errore", func(t *testing.T) {
		t.Setenv(emailrender.DirEnvVar, filepath.Join(t.TempDir(), "assente"))
		if _, err := emailrender.FindDir("."); err == nil {
			t.Fatal("atteso un errore sulla directory indicata")
		}
	})
}

// Il renderer non muta niente dopo il caricamento: i template non vengono
// riassociati a una FuncMap per lingua, e il contesto è un valore per invio.
// Il test serve a `go test -race`, che è come gira in CI.
func TestRenderIsSafeForConcurrentUse(t *testing.T) {
	r := newRenderer(t)
	data := sampleData()

	done := make(chan error, len(emailrender.Languages)*len(emailrender.Events()))
	for _, language := range emailrender.Languages {
		for _, event := range emailrender.Events() {
			go func() {
				_, err := r.Render(event, language, data[event])
				done <- err
			}()
		}
	}
	deadline := time.After(30 * time.Second)
	for range cap(done) {
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("Render: %v", err)
			}
		case <-deadline:
			t.Fatal("rendering concorrente non terminato")
		}
	}
}
