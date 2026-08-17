package emailrender_test

import (
	"encoding/xml"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/apdsoftware/postqron/services/api/internal/emailrender"
)

// testSite non è il sito vero: gli URL servono solo a rendere verificabile il
// prefisso di lingua nei link.
var testSite = emailrender.Site{
	ProductName:   "Postqron",
	PublicBaseURL: "https://postqron.test",
	AppBaseURL:    "https://app.postqron.test",
	SupportEmail:  "support@postqron.test",
}

var fixedClock = func() time.Time { return time.Date(2026, 8, 17, 9, 41, 0, 0, time.UTC) }

func newRenderer(t *testing.T) *emailrender.Renderer {
	t.Helper()
	dir, err := emailrender.FindDir(".")
	if err != nil {
		t.Fatalf("FindDir: %v", err)
	}
	r, err := emailrender.NewFromDir(dir, testSite, emailrender.WithClock(fixedClock))
	if err != nil {
		t.Fatalf("NewFromDir: %v", err)
	}
	return r
}

// sampleData copre i quattro eventi di R21 con dati plausibili.
func sampleData() map[emailrender.Event]any {
	when := time.Date(2026, 8, 17, 6, 12, 0, 0, time.UTC)
	return map[emailrender.Event]any{
		emailrender.EventWelcome: emailrender.WelcomeData{RecipientName: "Sam"},
		emailrender.EventJobFailed: emailrender.JobFailedData{
			JobID:               "0f2d1c9e-3a44-4b7f-9c11-5d6e7f8a9b0c",
			JobName:             "nightly-invoices",
			Environment:         emailrender.EnvironmentProduction,
			ConsecutiveFailures: 3,
			LastAttemptAt:       when,
			FailureKind:         emailrender.FailureHTTPStatus,
			HTTPStatus:          502,
		},
		emailrender.EventPlanChanged: emailrender.PlanChangedData{
			PreviousPlan: "Free",
			NewPlan:      "Pro",
			EffectiveAt:  when,
		},
		emailrender.EventSecurityAlert: emailrender.SecurityAlertData{
			Kind:         emailrender.SecurityAPIKeyRevoked,
			OccurredAt:   when,
			ResourceName: "CI deploy",
			SourceIP:     "203.0.113.7",
		},
	}
}

// Il giro completo: quattro eventi per cinque lingue. Oggi quattro lingue su
// cinque ricadono interamente sull'inglese, ed è il punto — la struttura regge
// le cinque lingue prima che le traduzioni esistano (issue #446).
func TestRenderCoversEveryEventInEveryLanguage(t *testing.T) {
	r := newRenderer(t)
	data := sampleData()

	for _, event := range emailrender.Events() {
		for _, language := range emailrender.Languages {
			t.Run(string(event)+"/"+language, func(t *testing.T) {
				message, err := r.Render(event, language, data[event])
				if err != nil {
					t.Fatalf("Render: %v", err)
				}
				if message.Language != language {
					t.Errorf("Language = %q, atteso %q", message.Language, language)
				}
				if strings.TrimSpace(message.Subject) == "" {
					t.Error("oggetto vuoto")
				}
				if strings.TrimSpace(message.Text) == "" {
					t.Error("corpo testuale vuoto")
				}
				if !strings.HasPrefix(message.HTML, "<!DOCTYPE html>") {
					t.Errorf("l'HTML non comincia con il doctype: %.40q", message.HTML)
				}
				assertWellFormed(t, message.HTML)
				assertNoLeftoverPlaceholder(t, message)
			})
		}
	}
}

// L'HTML prodotto dev'essere ben formato secondo XML. Non è pedanteria: i
// motori di rendering dei client di posta sono tolleranti in modi diversi fra
// loro, e un tag lasciato aperto è la classica differenza che si vede solo su
// Outlook. Verificarlo con encoding/xml costa zero dipendenze.
func assertWellFormed(t *testing.T, markup string) {
	t.Helper()
	decoder := xml.NewDecoder(strings.NewReader(markup))
	for {
		_, err := decoder.Token()
		if err == io.EOF {
			return
		}
		if err != nil {
			t.Fatalf("HTML non ben formato: %v", err)
		}
	}
}

// Un `{segnaposto}` che arriva all'utente è un testo rotto. Il renderer li
// tratta come errore, quindi qui non dovrebbero mai comparire.
func assertNoLeftoverPlaceholder(t *testing.T, message emailrender.Message) {
	t.Helper()
	for name, body := range map[string]string{
		"oggetto": message.Subject,
		"testo":   message.Text,
		"html":    message.HTML,
	} {
		// Il blocco <style> usa le graffe del CSS: si guarda solo il testo.
		if name == "html" {
			continue
		}
		if strings.Contains(body, "{") {
			t.Errorf("%s contiene una graffa: %q", name, body)
		}
	}
}

func TestRenderWelcome(t *testing.T) {
	r := newRenderer(t)

	message, err := r.Render(emailrender.EventWelcome, "en", emailrender.WelcomeData{RecipientName: "Sam"})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}

	if message.Subject != "Welcome to Postqron" {
		t.Errorf("oggetto = %q", message.Subject)
	}
	if !strings.Contains(message.Text, "Hi Sam,") {
		t.Errorf("manca il saluto con il nome:\n%s", message.Text)
	}
	if !strings.Contains(message.HTML, "<title>Welcome to Postqron</title>") {
		t.Error("il <title> non riporta l'oggetto")
	}
	// Il pulsante punta alla dashboard, nella lingua del messaggio.
	if !strings.Contains(message.HTML, `href="https://app.postqron.test/en/jobs/new"`) {
		t.Errorf("link del pulsante assente o senza prefisso di lingua:\n%s", message.HTML)
	}
	if !strings.Contains(message.Text, "https://app.postqron.test/en/jobs/new") {
		t.Error("il corpo testuale non riporta il link del pulsante")
	}
	if !strings.Contains(message.HTML, "support@postqron.test") {
		t.Error("il piè di pagina non riporta l'indirizzo di supporto")
	}
	if !strings.Contains(message.HTML, "2026 Postqron") {
		t.Error("il piè di pagina non riporta l'anno dell'orologio iniettato")
	}
}

// Il nome può mancare: la registrazione chiede l'email, non l'anagrafe.
func TestRenderWelcomeWithoutName(t *testing.T) {
	r := newRenderer(t)

	message, err := r.Render(emailrender.EventWelcome, "en", emailrender.WelcomeData{})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if !strings.Contains(message.Text, "Hi,") {
		t.Errorf("manca il saluto impersonale:\n%s", message.Text)
	}
	if strings.Contains(message.Text, "Hi ,") {
		t.Error("saluto con nome vuoto: «Hi ,»")
	}
}

func TestRenderJobFailed(t *testing.T) {
	r := newRenderer(t)
	data := sampleData()[emailrender.EventJobFailed].(emailrender.JobFailedData)

	message, err := r.Render(emailrender.EventJobFailed, "en", data)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}

	if message.Subject != "nightly-invoices is failing in Production" {
		t.Errorf("oggetto = %q", message.Subject)
	}
	if !strings.Contains(message.Text, "nightly-invoices has failed 3 times in a row.") {
		t.Errorf("manca il riepilogo al plurale:\n%s", message.Text)
	}
	// La causa arriva dalla classificazione, non da un messaggio d'errore.
	if !strings.Contains(message.Text, "The endpoint answered with HTTP 502.") {
		t.Errorf("manca l'esito dell'ultimo tentativo:\n%s", message.Text)
	}
	if !strings.Contains(message.Text, "2026-08-17 06:12 UTC") {
		t.Errorf("l'istante non è in UTC con l'unità dichiarata:\n%s", message.Text)
	}
	if !strings.Contains(message.HTML,
		`href="https://app.postqron.test/en/jobs/0f2d1c9e-3a44-4b7f-9c11-5d6e7f8a9b0c/executions"`) {
		t.Errorf("link alla cronologia assente o malformato:\n%s", message.HTML)
	}
}

// Un solo fallimento vuole il singolare. La forma la sceglie il catalogo, ed è
// il pezzo di struttura che le traduzioni della #446 erediteranno.
func TestRenderJobFailedSingular(t *testing.T) {
	r := newRenderer(t)
	data := sampleData()[emailrender.EventJobFailed].(emailrender.JobFailedData)
	data.ConsecutiveFailures = 1

	message, err := r.Render(emailrender.EventJobFailed, "en", data)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if !strings.Contains(message.Text, "has failed 1 time in a row.") {
		t.Errorf("manca il riepilogo al singolare:\n%s", message.Text)
	}
}

// R20.1: Mailronix risponde 202 identico anche verso un destinatario in
// suppression list, quindi il recapito non è osservabile. L'alert deve dirlo,
// invece di lasciar credere che il silenzio significhi «nessun problema».
func TestJobFailedDisclosesThatDeliveryIsNotObservable(t *testing.T) {
	r := newRenderer(t)

	message, err := r.Render(emailrender.EventJobFailed, "en", sampleData()[emailrender.EventJobFailed])
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	for _, body := range []string{message.Text, message.HTML} {
		if !strings.Contains(body, "We cannot tell whether this message reached you") {
			t.Errorf("manca l'avvertenza sul recapito non osservabile:\n%s", body)
		}
		if !strings.Contains(body, "execution history in the dashboard is the record to rely on") {
			t.Error("manca il rimando alla cronologia come fonte attendibile")
		}
	}
}

func TestRenderPlanChanged(t *testing.T) {
	r := newRenderer(t)

	message, err := r.Render(emailrender.EventPlanChanged, "en", sampleData()[emailrender.EventPlanChanged])
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if message.Subject != "Your Postqron plan is now Pro" {
		t.Errorf("oggetto = %q", message.Subject)
	}
	if !strings.Contains(message.Text, "Your account moved from Free to Pro.") {
		t.Errorf("manca il riepilogo della variazione:\n%s", message.Text)
	}
	// Nessuna cifra: il listino sta su Paddle, non qui.
	for _, forbidden := range []string{"€", "$", "/month", "per month"} {
		if strings.Contains(message.Text, forbidden) {
			t.Errorf("il testo nomina un prezzo (%q): il listino non appartiene a questo template", forbidden)
		}
	}

	// Senza sospensioni non si nomina nessun job fermo: sarebbe un allarme per
	// un utente che ha soltanto cambiato piano.
	if strings.Contains(message.Text, "paused") {
		t.Errorf("un cambio di piano senza sospensioni parla di job fermi:\n%s", message.Text)
	}
}

// R58: quando il downgrade ferma dei job, l'email deve dire **cosa deve fare
// l'utente**, e i due motivi vanno tenuti distinti perché i rimedi lo sono.
//
// È il caso in cui una comunicazione mediocre costa un cliente: i suoi job sono
// fermi, e se lo scopre quando quello che serviva non parte, l'ha scoperto
// troppo tardi.
func TestRenderPlanChangedSpellsOutSuspendedJobs(t *testing.T) {
	r := newRenderer(t)

	data := emailrender.PlanChangedData{
		PreviousPlan:          "Pro",
		NewPlan:               "Free",
		EffectiveAt:           time.Date(2026, 8, 17, 6, 12, 0, 0, time.UTC),
		SuspendedByJobLimit:   7,
		SuspendedByResolution: 1,
	}

	message, err := r.Render(emailrender.EventPlanChanged, "en", data)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}

	for _, variant := range []struct{ name, body string }{
		{"testo", message.Text},
		{"HTML", message.HTML},
	} {
		// I due conteggi, ciascuno con il proprio rimedio.
		for _, expected := range []string{
			"7 jobs are paused",
			"Turn back on as many as the plan allows",
			"1 job is paused",
			"change its schedule",
			"Nothing was deleted",
		} {
			if !strings.Contains(variant.body, expected) {
				t.Errorf("%s: manca %q\n%s", variant.name, expected, variant.body)
			}
		}
		// Il link alla pagina da cui si riaccende, che è l'azione richiesta.
		if !strings.Contains(variant.body, "https://app.postqron.test/en/jobs") {
			t.Errorf("%s: manca il link ai job, che è la cosa da fare:\n%s", variant.name, variant.body)
		}
	}
}

// Il plurale si sceglie per conteggio, e la forma singolare esiste per
// entrambi i motivi: «1 jobs are paused» in un'email che chiede un'azione è il
// modo più rapido di far sembrare il messaggio un guasto.
func TestRenderPlanChangedUsesTheRightPluralForm(t *testing.T) {
	r := newRenderer(t)
	when := time.Date(2026, 8, 17, 6, 12, 0, 0, time.UTC)

	cases := []struct {
		name     string
		data     emailrender.PlanChangedData
		expected string
		absent   string
	}{
		{
			name: "un solo job per il tetto",
			data: emailrender.PlanChangedData{
				PreviousPlan: "Pro", NewPlan: "Free", EffectiveAt: when,
				SuspendedByJobLimit: 1,
			},
			expected: "1 job is paused",
			// L'altro motivo non c'è: non se ne parla.
			absent: "runs more often",
		},
		{
			name: "solo la risoluzione, con il numero che rientra",
			data: emailrender.PlanChangedData{
				PreviousPlan: "Pro", NewPlan: "Free", EffectiveAt: when,
				SuspendedByResolution: 2,
			},
			expected: "2 jobs are paused because they run more often",
			absent:   "as many as the plan allows",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			message, err := r.Render(emailrender.EventPlanChanged, "en", tc.data)
			if err != nil {
				t.Fatalf("Render: %v", err)
			}
			if !strings.Contains(message.Text, tc.expected) {
				t.Errorf("manca %q:\n%s", tc.expected, message.Text)
			}
			if strings.Contains(message.Text, tc.absent) {
				t.Errorf("compare %q, che riguarda l'altro motivo:\n%s", tc.absent, message.Text)
			}
		})
	}
}

func TestRenderSecurityAlert(t *testing.T) {
	r := newRenderer(t)
	data := sampleData()[emailrender.EventSecurityAlert].(emailrender.SecurityAlertData)

	message, err := r.Render(emailrender.EventSecurityAlert, "en", data)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if message.Subject != "Security alert on your Postqron account" {
		t.Errorf("oggetto = %q", message.Subject)
	}
	if !strings.Contains(message.Text, "An API key of your account was revoked.") {
		t.Errorf("manca la descrizione dell'evento:\n%s", message.Text)
	}
	// Della risorsa si stampa il nome, mai il valore.
	if !strings.Contains(message.Text, "CI deploy") {
		t.Error("manca il nome della risorsa coinvolta")
	}
	if !strings.Contains(message.Text, "203.0.113.7") {
		t.Error("manca l'indirizzo di origine")
	}
	if !strings.Contains(message.Text, "never asks you for your password or for an API key by email") {
		t.Errorf("manca l'avvertenza anti-phishing:\n%s", message.Text)
	}
}

// I campi facoltativi spariscono invece di lasciare un'etichetta senza valore.
func TestRenderSecurityAlertOmitsEmptyDetails(t *testing.T) {
	r := newRenderer(t)

	message, err := r.Render(emailrender.EventSecurityAlert, "en", emailrender.SecurityAlertData{
		Kind:       emailrender.SecurityPasswordReset,
		OccurredAt: time.Date(2026, 8, 17, 6, 12, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if strings.Contains(message.Text, "Source IP address") {
		t.Errorf("etichetta dell'IP presente senza valore:\n%s", message.Text)
	}
	if strings.Contains(message.Text, "Item involved") {
		t.Errorf("etichetta della risorsa presente senza valore:\n%s", message.Text)
	}
	assertWellFormed(t, message.HTML)
}

// Ogni tipo di evento di sicurezza ha la sua frase: una chiave mancante fa
// fallire il rendering, non produce un paragrafo vuoto.
func TestRenderSecurityAlertCoversEveryKind(t *testing.T) {
	r := newRenderer(t)
	kinds := []emailrender.SecurityEventKind{
		emailrender.SecurityPasswordReset,
		emailrender.SecurityAPIKeyCreated,
		emailrender.SecurityAPIKeyRevoked,
		emailrender.SecurityAccountImpersonated,
	}
	for _, kind := range kinds {
		t.Run(string(kind), func(t *testing.T) {
			message, err := r.Render(emailrender.EventSecurityAlert, "en", emailrender.SecurityAlertData{
				Kind:       kind,
				OccurredAt: time.Date(2026, 8, 17, 6, 12, 0, 0, time.UTC),
			})
			if err != nil {
				t.Fatalf("Render: %v", err)
			}
			if strings.TrimSpace(message.Text) == "" {
				t.Fatal("corpo vuoto")
			}
		})
	}
}

// I dati dell'utente finiscono nell'HTML: html/template li deve neutralizzare.
func TestRenderEscapesUserData(t *testing.T) {
	r := newRenderer(t)
	data := sampleData()[emailrender.EventJobFailed].(emailrender.JobFailedData)
	data.JobName = `<script>alert("x")</script>`

	message, err := r.Render(emailrender.EventJobFailed, "en", data)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if strings.Contains(message.HTML, "<script>") {
		t.Error("il nome del job è finito nell'HTML senza escape")
	}
	if !strings.Contains(message.HTML, "&lt;script&gt;") {
		t.Errorf("il nome del job non compare nemmeno con escape:\n%s", message.HTML)
	}
	assertWellFormed(t, message.HTML)
}

// Un nome che contiene delle graffe è un dato, non un segnaposto: non deve
// innescare una seconda sostituzione né far fallire il rendering.
func TestRenderTreatsBracesInDataAsText(t *testing.T) {
	r := newRenderer(t)
	data := sampleData()[emailrender.EventJobFailed].(emailrender.JobFailedData)
	data.JobName = "sync-{tenant}"

	message, err := r.Render(emailrender.EventJobFailed, "en", data)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if !strings.Contains(message.Subject, "sync-{tenant}") {
		t.Errorf("oggetto = %q", message.Subject)
	}
}

func TestRenderRejectsWrongDataType(t *testing.T) {
	r := newRenderer(t)

	if _, err := r.Render(emailrender.EventWelcome, "en", emailrender.PlanChangedData{}); err == nil {
		t.Fatal("atteso un errore di tipo del contesto")
	}
	if _, err := r.Render(emailrender.Event("nope"), "en", emailrender.WelcomeData{}); err == nil {
		t.Fatal("atteso un errore di evento sconosciuto")
	}
}

func TestRenderRejectsIncompleteData(t *testing.T) {
	r := newRenderer(t)
	valid := sampleData()

	tests := map[string]struct {
		event emailrender.Event
		data  any
	}{
		"job senza identificativo": {emailrender.EventJobFailed, func() any {
			d := valid[emailrender.EventJobFailed].(emailrender.JobFailedData)
			d.JobID = ""
			return d
		}()},
		"ambiente non previsto": {emailrender.EventJobFailed, func() any {
			d := valid[emailrender.EventJobFailed].(emailrender.JobFailedData)
			d.Environment = "collaudo"
			return d
		}()},
		"zero fallimenti": {emailrender.EventJobFailed, func() any {
			d := valid[emailrender.EventJobFailed].(emailrender.JobFailedData)
			d.ConsecutiveFailures = 0
			return d
		}()},
		"stato HTTP fuori intervallo": {emailrender.EventJobFailed, func() any {
			d := valid[emailrender.EventJobFailed].(emailrender.JobFailedData)
			d.HTTPStatus = 42
			return d
		}()},
		"causa non classificata": {emailrender.EventJobFailed, func() any {
			d := valid[emailrender.EventJobFailed].(emailrender.JobFailedData)
			d.FailureKind = "boh"
			return d
		}()},
		"piano invariato": {emailrender.EventPlanChanged, func() any {
			d := valid[emailrender.EventPlanChanged].(emailrender.PlanChangedData)
			d.PreviousPlan = d.NewPlan
			return d
		}()},
		"variazione senza decorrenza": {emailrender.EventPlanChanged, func() any {
			d := valid[emailrender.EventPlanChanged].(emailrender.PlanChangedData)
			d.EffectiveAt = time.Time{}
			return d
		}()},
		"evento di sicurezza sconosciuto": {emailrender.EventSecurityAlert, func() any {
			d := valid[emailrender.EventSecurityAlert].(emailrender.SecurityAlertData)
			d.Kind = "ransomware"
			return d
		}()},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := r.Render(test.event, "en", test.data); err == nil {
				t.Fatal("atteso un errore di convalida")
			}
		})
	}
}
