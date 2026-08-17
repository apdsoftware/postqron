package mailronix

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/apdsoftware/postqron/services/api/internal/auth"
	"github.com/apdsoftware/postqron/services/api/internal/emailrender"
)

// senderSpia registra gli invii senza parlare con nessuno.
type senderSpia struct {
	mu      sync.Mutex
	inviate []Email
	receipt Receipt
	err     error
}

func (s *senderSpia) Send(_ context.Context, email Email) (Receipt, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.inviate = append(s.inviate, email)
	if s.err != nil {
		return Receipt{}, s.err
	}
	return s.receipt, nil
}

func (s *senderSpia) ultima(t *testing.T) Email {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.inviate) == 0 {
		t.Fatal("nessuna email inviata")
	}
	return s.inviate[len(s.inviate)-1]
}

// rendererSpia compila senza template: al Mailer interessa la traduzione da
// auth.Message a evento, non il contenuto.
type rendererSpia struct {
	evento    emailrender.Event
	lingua    string
	dati      any
	messaggio emailrender.Message
	err       error
}

func (r *rendererSpia) Render(event emailrender.Event, language string, data any) (emailrender.Message, error) {
	r.evento, r.lingua, r.dati = event, language, data
	if r.err != nil {
		return emailrender.Message{}, r.err
	}
	return r.messaggio, nil
}

func nuovoMailer(t *testing.T, sender Sender, renderer Renderer, opts ...MailerOption) *Mailer {
	t.Helper()
	mailer, err := NewMailer(sender, renderer, opts...)
	if err != nil {
		t.Fatalf("NewMailer: %v", err)
	}
	return mailer
}

// TestMailerCambioPassword copre l'unica corrispondenza esistente fra i
// messaggi dell'autenticazione (#396) e i template compilabili (#418).
func TestMailerCambioPassword(t *testing.T) {
	quando := time.Date(2026, 8, 17, 10, 30, 0, 0, time.UTC)
	sender := &senderSpia{receipt: Receipt{Status: StatusQueued, EmailLogID: "log-1"}}
	renderer := &rendererSpia{messaggio: emailrender.Message{
		Subject:  "Password modificata",
		HTML:     "<html>ciao</html>",
		Text:     "ciao",
		Language: "en",
	}}
	mailer := nuovoMailer(t, sender, renderer, WithMailerClock(func() time.Time { return quando }))

	err := mailer.Send(t.Context(), auth.Message{
		Kind:   auth.KindPasswordChanged,
		To:     "utente@example.com",
		UserID: "u-1",
	})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}

	if renderer.evento != emailrender.EventSecurityAlert {
		t.Errorf("evento = %q, atteso %q", renderer.evento, emailrender.EventSecurityAlert)
	}
	dati, ok := renderer.dati.(emailrender.SecurityAlertData)
	if !ok {
		t.Fatalf("dati = %T, attesa SecurityAlertData", renderer.dati)
	}
	if dati.Kind != emailrender.SecurityPasswordReset {
		t.Errorf("Kind = %q", dati.Kind)
	}
	if !dati.OccurredAt.Equal(quando) {
		t.Errorf("OccurredAt = %v, atteso %v", dati.OccurredAt, quando)
	}

	inviata := sender.ultima(t)
	if inviata.To != "utente@example.com" {
		t.Errorf("To = %q", inviata.To)
	}
	if inviata.Subject != "Password modificata" || inviata.HTML != "<html>ciao</html>" || inviata.Text != "ciao" {
		t.Errorf("il messaggio compilato non è arrivato intatto al client: %+v", inviata)
	}
}

// TestMailerMessaggiSenzaTemplate documenta il buco fra le due issue: tre
// messaggi su quattro portano un token monouso e non hanno un template, perché
// emailrender rifiuta per costruzione un campo per un segreto.
func TestMailerMessaggiSenzaTemplate(t *testing.T) {
	senzaTemplate := []auth.MessageKind{
		auth.KindEmailVerification,
		auth.KindRegistrationAttempt,
		auth.KindPasswordReset,
	}
	for _, kind := range senzaTemplate {
		t.Run(string(kind), func(t *testing.T) {
			sender := &senderSpia{}
			mailer := nuovoMailer(t, sender, &rendererSpia{})

			err := mailer.Send(t.Context(), auth.Message{
				Kind:  kind,
				To:    "utente@example.com",
				Token: "token-segretissimo",
			})
			if !errors.Is(err, ErrNoTemplate) {
				t.Fatalf("errore = %v, atteso ErrNoTemplate", err)
			}
			if len(sender.inviate) != 0 {
				t.Errorf("inviate %d email pur senza template", len(sender.inviate))
			}
			// Il token non deve uscire nemmeno dal messaggio d'errore, che
			// auth.Service registra nel log.
			if strings.Contains(err.Error(), "token-segretissimo") {
				t.Errorf("il token è finito nell'errore: %v", err)
			}
		})
	}
}

func TestMailerTipoSconosciuto(t *testing.T) {
	sender := &senderSpia{}
	mailer := nuovoMailer(t, sender, &rendererSpia{})
	err := mailer.Send(t.Context(), auth.Message{Kind: "inventato", To: "a@example.com"})
	if !errors.Is(err, ErrNoTemplate) {
		t.Fatalf("errore = %v, atteso ErrNoTemplate", err)
	}
}

func TestMailerDestinatarioMancante(t *testing.T) {
	sender := &senderSpia{}
	mailer := nuovoMailer(t, sender, &rendererSpia{})
	err := mailer.Send(t.Context(), auth.Message{Kind: auth.KindPasswordChanged})
	if !errors.Is(err, ErrInvalidEmail) {
		t.Fatalf("errore = %v, atteso ErrInvalidEmail", err)
	}
	if len(sender.inviate) != 0 {
		t.Error("email inviata senza destinatario")
	}
}

func TestMailerPropagaGliErrori(t *testing.T) {
	t.Run("compilazione fallita", func(t *testing.T) {
		sender := &senderSpia{}
		renderer := &rendererSpia{err: errors.New("template rotto")}
		mailer := nuovoMailer(t, sender, renderer)
		if err := mailer.Send(t.Context(), auth.Message{
			Kind: auth.KindPasswordChanged, To: "a@example.com",
		}); err == nil {
			t.Fatal("errore atteso")
		}
		if len(sender.inviate) != 0 {
			t.Error("email inviata pur senza corpo compilato")
		}
	})

	t.Run("invio fallito", func(t *testing.T) {
		voluto := &APIError{StatusCode: 403, Code: CodeDomainNotVerified, Message: "non verificato"}
		sender := &senderSpia{err: voluto}
		renderer := &rendererSpia{messaggio: emailrender.Message{Subject: "x", Text: "y", Language: "en"}}
		mailer := nuovoMailer(t, sender, renderer)

		err := mailer.Send(t.Context(), auth.Message{
			Kind: auth.KindPasswordChanged, To: "a@example.com",
		})
		if !errors.Is(err, error(voluto)) {
			t.Fatalf("errore = %v, atteso quello del client", err)
		}
		if Code(err) != CodeDomainNotVerified {
			t.Errorf("Code = %q", Code(err))
		}
	})
}

// TestMailerLogSenzaDatiPersonali verifica cosa finisce nel log a invio
// riuscito: l'identificativo sì, il destinatario no, e nessuna parola sul
// recapito, che la risposta non dice (R20.1).
func TestMailerLogSenzaDatiPersonali(t *testing.T) {
	var registrato bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&registrato, &slog.HandlerOptions{Level: slog.LevelDebug}))

	sender := &senderSpia{receipt: Receipt{Status: StatusQueued, EmailLogID: "log-42"}}
	renderer := &rendererSpia{messaggio: emailrender.Message{Subject: "x", Text: "y", Language: "en"}}
	mailer := nuovoMailer(t, sender, renderer, WithMailerLogger(logger))

	if err := mailer.Send(t.Context(), auth.Message{
		Kind:   auth.KindPasswordChanged,
		To:     "personale@example.com",
		UserID: "u-9",
		Token:  "token-segretissimo",
	}); err != nil {
		t.Fatalf("Send: %v", err)
	}

	log := registrato.String()
	if !strings.Contains(log, "log-42") {
		t.Errorf("email_log_id assente dal log: %s", log)
	}
	for _, vietato := range []string{"personale@example.com", "token-segretissimo"} {
		if strings.Contains(log, vietato) {
			t.Errorf("il log contiene %q: %s", vietato, log)
		}
	}
	// R20.1: nulla nella riga di log può far credere che l'email sia arrivata.
	for _, parola := range []string{"recapitat", "consegnat"} {
		if strings.Contains(strings.ToLower(log), parola) {
			t.Errorf("il log dichiara un recapito che la risposta non dice: %s", log)
		}
	}
}

func TestMailerLingua(t *testing.T) {
	renderer := &rendererSpia{messaggio: emailrender.Message{Subject: "x", Text: "y"}}
	mailer := nuovoMailer(t, &senderSpia{}, renderer)
	if err := mailer.Send(t.Context(), auth.Message{
		Kind: auth.KindPasswordChanged, To: "a@example.com",
	}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if renderer.lingua != emailrender.DefaultLanguage {
		t.Errorf("lingua = %q, atteso il ripiego %q", renderer.lingua, emailrender.DefaultLanguage)
	}

	mailer = nuovoMailer(t, &senderSpia{}, renderer, WithLanguage("it"))
	if err := mailer.Send(t.Context(), auth.Message{
		Kind: auth.KindPasswordChanged, To: "a@example.com",
	}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if renderer.lingua != "it" {
		t.Errorf("lingua = %q, attesa it", renderer.lingua)
	}
}

func TestNewMailerRifiutaDipendenzeMancanti(t *testing.T) {
	if _, err := NewMailer(nil, &rendererSpia{}); err == nil {
		t.Error("errore atteso senza Sender")
	}
	if _, err := NewMailer(&senderSpia{}, nil); err == nil {
		t.Error("errore atteso senza Renderer")
	}
}

func TestSiteFromEnv(t *testing.T) {
	predefinito := SiteFromEnv(env(nil))
	if err := predefinito.Validate(); err != nil {
		t.Fatalf("i valori predefiniti non superano la validazione di emailrender: %v", err)
	}
	if predefinito != DefaultSite() {
		t.Errorf("Site = %+v, atteso %+v", predefinito, DefaultSite())
	}

	sovrascritto := SiteFromEnv(env(map[string]string{
		EnvProductName:   "Postqron Staging",
		EnvPublicBaseURL: "https://staging.postqron.com/",
		EnvAppBaseURL:    "https://app.staging.postqron.com/",
		EnvSupportEmail:  "aiuto@postqron.com",
	}))
	if err := sovrascritto.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	// La barra finale va tolta: emailrender la rifiuta, e comporrebbe URL con
	// due barre di fila.
	if sovrascritto.PublicBaseURL != "https://staging.postqron.com" {
		t.Errorf("PublicBaseURL = %q", sovrascritto.PublicBaseURL)
	}
	if sovrascritto.AppBaseURL != "https://app.staging.postqron.com" {
		t.Errorf("AppBaseURL = %q", sovrascritto.AppBaseURL)
	}
	if sovrascritto.ProductName != "Postqron Staging" || sovrascritto.SupportEmail != "aiuto@postqron.com" {
		t.Errorf("Site = %+v", sovrascritto)
	}
}

func TestNewMailerFromEnvSenzaChiave(t *testing.T) {
	_, err := NewMailerFromEnv(env(nil), t.TempDir(), slog.Default())
	if !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("errore = %v, atteso ErrNotConfigured", err)
	}
}

// TestMailerConTemplateVeri chiude il giro: i template di emails/templates/,
// compilati da emailrender, arrivano al client come payload completo (R20).
func TestMailerConTemplateVeri(t *testing.T) {
	dir, err := emailrender.FindDir(".")
	if err != nil {
		t.Skipf("emails/templates/ non trovata: %v", err)
	}
	renderer, err := emailrender.NewFromDir(dir, DefaultSite())
	if err != nil {
		t.Fatalf("emailrender.NewFromDir: %v", err)
	}

	sender := &senderSpia{receipt: Receipt{Status: StatusQueued, EmailLogID: "log-vero"}}
	mailer := nuovoMailer(t, sender, renderer)

	if err := mailer.Send(t.Context(), auth.Message{
		Kind:   auth.KindPasswordChanged,
		To:     "utente@example.com",
		UserID: "u-1",
	}); err != nil {
		t.Fatalf("Send: %v", err)
	}

	inviata := sender.ultima(t)
	// Quello che parte dev'essere accettabile dal contratto: è lo stesso
	// controllo che il client farebbe prima di partire davvero.
	if err := inviata.Validate(); err != nil {
		t.Fatalf("il messaggio compilato non supera il contratto: %v", err)
	}
	if !strings.Contains(inviata.HTML, "<html") {
		t.Errorf("HTML non compilato: %.120s", inviata.HTML)
	}
	if strings.Contains(inviata.HTML, "{{") {
		t.Errorf("l'HTML contiene segnaposto non sostituiti: R20 vuole il payload già completo")
	}
}
