package mailronix

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/apdsoftware/postqron/services/api/internal/auth"
	"github.com/apdsoftware/postqron/services/api/internal/emailrender"
)

// Mailer collega il client all'astrazione [auth.Mailer] lasciata dalla #396.
//
// È il punto in cui le tre parti si incontrano: l'autenticazione dice *che
// cosa* va comunicato ([auth.Message]), emailrender lo compila (#418), questo
// package lo recapita. Nessuna delle tre conosce le altre due.
//
// # Quello che l'aggancio non copre, e perché
//
// [auth.MessageKind] elenca quattro messaggi; emails/templates/ ne compila
// quattro, ma non gli stessi. La sovrapposizione è una sola:
//
//	auth.KindPasswordChanged  →  emailrender.EventSecurityAlert
//
// Per gli altri tre — verifica dell'indirizzo, tentativo di registrazione su un
// indirizzo già preso, recupero password — **non esiste un template**, e non è
// una svista né dell'una né dell'altra issue: sono tutti messaggi che portano
// un token monouso, e [emailrender.SecurityAlertData] è deliberatamente priva
// di un campo per un segreto, con un test che lo verifica. Servono tre template
// nuovi e un tipo di dati che il token lo accetti; entrambi stanno fuori dal
// perimetro della #419, che non può toccare emails/templates/ né
// internal/emailrender.
//
// Finché non arrivano, quei tre messaggi restituiscono [ErrNoTemplate].
// [auth.Service] lo registra come errore e non lo mostra al client, che è il
// comportamento voluto: nessuna regressione rispetto a [auth.LogMailer], che
// oggi non recapita niente, ma un errore rumoroso invece di un silenzio.
type Mailer struct {
	sender   Sender
	renderer Renderer
	logger   *slog.Logger
	now      func() time.Time
	language string
}

// Sender è la parte di [Client] che serve al Mailer. Averla come interfaccia
// permette ai test di verificare l'aggancio senza un server HTTP.
type Sender interface {
	Send(ctx context.Context, email Email) (Receipt, error)
}

// Renderer è la parte di [emailrender.Renderer] che serve al Mailer.
type Renderer interface {
	Render(event emailrender.Event, language string, data any) (emailrender.Message, error)
}

// ErrNoTemplate segnala un messaggio dell'autenticazione per cui non esiste un
// template. Vedi la doc di [Mailer] per l'elenco e il motivo.
var ErrNoTemplate = errors.New("mailronix: nessun template per questo messaggio")

// MailerOption modifica un Mailer in costruzione.
type MailerOption func(*Mailer)

// WithMailerLogger imposta il logger dell'aggancio.
func WithMailerLogger(logger *slog.Logger) MailerOption {
	return func(m *Mailer) {
		if logger != nil {
			m.logger = logger
		}
	}
}

// WithMailerClock sostituisce l'orologio, che serve a datare gli eventi di
// sicurezza. Esiste per i test.
func WithMailerClock(now func() time.Time) MailerOption {
	return func(m *Mailer) {
		if now != nil {
			m.now = now
		}
	}
}

// WithLanguage fissa la lingua delle email dell'autenticazione.
//
// [auth.Message] non porta una lingua: il package è stato scritto prima che
// esistessero i template, e la lingua dell'utente vive nel suo profilo. Finché
// non la porta — è materia della #420, che aggancia gli eventi di dominio —
// questo è l'unico modo di sceglierla, e il ripiego è l'inglese, la sorgente di
// emailrender.
func WithLanguage(language string) MailerOption {
	return func(m *Mailer) {
		if strings.TrimSpace(language) != "" {
			m.language = language
		}
	}
}

// NewMailer costruisce l'aggancio.
func NewMailer(sender Sender, renderer Renderer, opts ...MailerOption) (*Mailer, error) {
	if sender == nil {
		return nil, errors.New("mailronix: NewMailer richiede un Sender")
	}
	if renderer == nil {
		return nil, errors.New("mailronix: NewMailer richiede un Renderer")
	}
	m := &Mailer{
		sender:   sender,
		renderer: renderer,
		logger:   slog.Default(),
		now:      time.Now,
		language: emailrender.DefaultLanguage,
	}
	for _, opt := range opts {
		opt(m)
	}
	return m, nil
}

// Mailer soddisfa il contratto della #396.
var _ auth.Mailer = (*Mailer)(nil)

// Send compila il messaggio e lo consegna a Mailronix.
//
// Il contratto di [auth.Mailer] chiede di non bloccare a lungo: qui l'invio è
// sincrono, ma [auth.Service] chiama Send fuori dal percorso della risposta
// HTTP e il client ha un timeout proprio, quindi il tempo non entra nella
// latenza vista dall'utente. La coda vera — la tabella `notifications` — è
// della #420, e si infilerà davanti a questo tipo senza cambiarlo.
func (m *Mailer) Send(ctx context.Context, msg auth.Message) error {
	event, data, err := m.resolve(msg)
	if err != nil {
		return err
	}

	rendered, err := m.renderer.Render(event, m.language, data)
	if err != nil {
		return fmt.Errorf("mailronix: compilazione di %s: %w", msg.Kind, err)
	}

	receipt, err := m.sender.Send(ctx, Email{
		To:      msg.To,
		Subject: rendered.Subject,
		HTML:    rendered.HTML,
		Text:    rendered.Text,
	})
	if err != nil {
		return err
	}

	// Si registra l'identificativo e nulla di più: né il destinatario, che è un
	// dato personale, né una parola sul recapito, che questa risposta non dice
	// (R20.1).
	m.logger.InfoContext(ctx, "email accodata presso Mailronix",
		slog.String("kind", string(msg.Kind)),
		slog.String("user_id", msg.UserID),
		slog.String("language", rendered.Language),
		slog.String("email_log_id", receipt.EmailLogID))
	return nil
}

// resolve traduce un messaggio dell'autenticazione nell'evento e nel contesto
// che emailrender sa compilare.
func (m *Mailer) resolve(msg auth.Message) (emailrender.Event, any, error) {
	if strings.TrimSpace(msg.To) == "" {
		return "", nil, fmt.Errorf("%w: destinatario mancante", ErrInvalidEmail)
	}

	switch msg.Kind {
	case auth.KindPasswordChanged:
		// L'avviso dopo un cambio o una reimpostazione della password è
		// esattamente l'evento di sicurezza `password_reset` di R21: racconta
		// un fatto avvenuto e non porta token.
		return emailrender.EventSecurityAlert, emailrender.SecurityAlertData{
			Kind:       emailrender.SecurityPasswordReset,
			OccurredAt: m.now().UTC(),
		}, nil

	case auth.KindEmailVerification, auth.KindRegistrationAttempt, auth.KindPasswordReset:
		return "", nil, fmt.Errorf("%w: %s porta un token monouso e richiede un template che emails/templates/ non ha ancora", ErrNoTemplate, msg.Kind)

	default:
		return "", nil, fmt.Errorf("%w: tipo sconosciuto %q", ErrNoTemplate, msg.Kind)
	}
}

// ---------------------------------------------------------------- da ambiente

// Variabili d'ambiente dei valori di prodotto che compaiono in ogni email.
//
// Appartengono a emailrender.Site, che per scelta non legge l'ambiente da sé:
// resta così compilabile e testabile senza. Qualcuno però deve leggerlo, e
// finché internal/config non le accoglie il posto è qui, accanto a chi le usa.
const (
	EnvProductName   = "POSTQRON_PRODUCT_NAME"
	EnvPublicBaseURL = "POSTQRON_PUBLIC_BASE_URL"
	EnvAppBaseURL    = "POSTQRON_APP_BASE_URL"
	EnvSupportEmail  = "POSTQRON_SUPPORT_EMAIL"
)

// DefaultSite sono i valori di produzione, che valgono da default perché sono
// gli unici corretti: un'email di sviluppo con i link di produzione porta a una
// pagina che esiste, una con i link vuoti a `#ZgotmplZ`.
func DefaultSite() emailrender.Site {
	return emailrender.Site{
		ProductName:   "Postqron",
		PublicBaseURL: "https://postqron.com",
		AppBaseURL:    "https://app.postqron.com",
		SupportEmail:  "support@postqron.com",
	}
}

// SiteFromEnv legge i valori di prodotto, con [DefaultSite] come base.
func SiteFromEnv(getenv Getenv) emailrender.Site {
	site := DefaultSite()
	if v := strings.TrimSpace(getenv(EnvProductName)); v != "" {
		site.ProductName = v
	}
	if v := strings.TrimSpace(getenv(EnvPublicBaseURL)); v != "" {
		site.PublicBaseURL = strings.TrimSuffix(v, "/")
	}
	if v := strings.TrimSpace(getenv(EnvAppBaseURL)); v != "" {
		site.AppBaseURL = strings.TrimSuffix(v, "/")
	}
	if v := strings.TrimSpace(getenv(EnvSupportEmail)); v != "" {
		site.SupportEmail = v
	}
	return site
}

// NewMailerFromEnv costruisce l'aggancio completo a partire dall'ambiente.
//
// workdir è il punto da cui cercare emails/templates/ risalendo; in cmd/api è
// la directory di lavoro del processo.
//
// Restituisce [ErrNotConfigured] se MAILRONIX_API_KEY manca: è la condizione
// normale in sviluppo, e sta al chiamante decidere di ripiegare su
// [auth.LogMailer]. Ogni altro errore è configurazione sbagliata, e conviene
// che fermi l'avvio invece del primo invio.
func NewMailerFromEnv(getenv Getenv, workdir string, logger *slog.Logger) (*Mailer, error) {
	cfg, err := LoadConfig(getenv)
	if err != nil {
		return nil, err
	}

	client, err := New(cfg, WithLogger(logger))
	if err != nil {
		return nil, err
	}

	dir, err := emailrender.FindDir(workdir)
	if err != nil {
		return nil, fmt.Errorf("mailronix: template delle email: %w", err)
	}
	renderer, err := emailrender.NewFromDir(dir, SiteFromEnv(getenv))
	if err != nil {
		return nil, fmt.Errorf("mailronix: template delle email: %w", err)
	}

	return NewMailer(client, renderer, WithMailerLogger(logger))
}
