package emailrender

import (
	"errors"
	"fmt"
	"net/mail"
	"net/url"
	"slices"
	"strings"
	"time"
)

// Languages elenca le lingue del prodotto (SPEC §8-bis). L'ordine è quello
// della spec e l'inglese viene per primo perché è la sorgente.
var Languages = []string{"en", "it", "es", "de", "fr"}

// DefaultLanguage è la lingua sorgente e il ripiego di ogni testo mancante.
const DefaultLanguage = "en"

// Event identifica il template da compilare. I valori coincidono con i nomi dei
// file in emails/templates/.
//
// Sono gli eventi di R21. La corrispondenza con il tipo `notification_event`
// del database (migrazione 0008) è quasi uno a uno: `welcome`, `job_failed` e
// `plan_changed` hanno lo stesso nome, mentre l'enum chiama `security` ciò che
// qui è `security_alert`. L'enum ha anche `job_recovered`, che R21 non copre e
// per cui non esiste template.
type Event string

const (
	EventWelcome       Event = "welcome"
	EventJobFailed     Event = "job_failed"
	EventPlanChanged   Event = "plan_changed"
	EventSecurityAlert Event = "security_alert"
)

// Events restituisce gli eventi coperti, in ordine stabile.
func Events() []Event {
	return []Event{EventWelcome, EventJobFailed, EventPlanChanged, EventSecurityAlert}
}

// accents associa a ogni evento il colore della sua fascia. Il blu è quello del
// tema Hexagon (SPEC §4.1); il rosso e l'ambra distinguono a colpo d'occhio un
// avviso da una comunicazione ordinaria, senza affidare la distinzione al solo
// colore — il titolo la dice comunque, per chi non lo vede.
var accents = map[Event]string{
	EventWelcome:       "#4278e5",
	EventJobFailed:     "#d93b3b",
	EventPlanChanged:   "#4278e5",
	EventSecurityAlert: "#b4690e",
}

// Site raccoglie i valori del prodotto che compaiono in ogni email.
//
// Arriva dal chiamante e non dall'ambiente: questo package non legge variabili
// e non conosce configurazione, così resta compilabile e testabile senza.
type Site struct {
	// ProductName è il nome che compare nell'intestazione e nei testi.
	ProductName string
	// PublicBaseURL è la radice del sito pubblico, senza barra finale.
	PublicBaseURL string
	// AppBaseURL è la radice della dashboard cliente, senza barra finale.
	AppBaseURL string
	// SupportEmail è l'indirizzo a cui i testi invitano a scrivere.
	SupportEmail string
}

// Validate rifiuta un Site incompleto.
//
// Un campo vuoto qui non produce un errore visibile: produce un'email con un
// pulsante che punta a `/en/jobs/new`, un URL relativo che in un client di
// posta non porta da nessuna parte. Meglio fallire al primo invio.
func (s Site) Validate() error {
	if strings.TrimSpace(s.ProductName) == "" {
		return errors.New("ProductName è obbligatorio")
	}
	if err := validateBaseURL("PublicBaseURL", s.PublicBaseURL); err != nil {
		return err
	}
	if err := validateBaseURL("AppBaseURL", s.AppBaseURL); err != nil {
		return err
	}
	if _, err := mail.ParseAddress(s.SupportEmail); err != nil {
		return fmt.Errorf("SupportEmail non è un indirizzo valido: %w", err)
	}
	return nil
}

func validateBaseURL(field, raw string) error {
	if strings.TrimSpace(raw) == "" {
		return fmt.Errorf("%s è obbligatorio", field)
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("%s non è un URL valido: %w", field, err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		// I client di posta non seguono altri schemi, e html/template
		// sostituirebbe l'href con #ZgotmplZ senza dire perché.
		return fmt.Errorf("%s deve usare http o https, non %q", field, parsed.Scheme)
	}
	if parsed.Host == "" {
		return fmt.Errorf("%s non ha un host", field)
	}
	if strings.HasSuffix(raw, "/") {
		return fmt.Errorf("%s non deve finire con una barra: %q", field, raw)
	}
	return nil
}

// ---------------------------------------------------------------- benvenuto

// WelcomeData è il contesto dell'email di benvenuto (R21).
type WelcomeData struct {
	// RecipientName è il nome con cui salutare. Può essere vuoto: il saluto
	// ripiega su una formula impersonale invece di stampare una virgola sola.
	RecipientName string
}

func (d WelcomeData) validate() error { return nil }

// ------------------------------------------------------------- job fallito

// Environment è l'ambiente di esecuzione di un job, con gli stessi due valori
// del tipo `environment` del database.
type Environment string

const (
	EnvironmentStaging    Environment = "staging"
	EnvironmentProduction Environment = "production"
)

// FailureKind classifica il motivo dell'ultimo tentativo fallito.
//
// È una classificazione e non un messaggio d'errore per due ragioni. La prima è
// che un messaggio prodotto dal motore sarebbe in inglese in un'email tedesca.
// La seconda è che il testo grezzo di un errore di rete o il corpo di una
// risposta possono contenere un URL con un token nella query, e questa è
// un'email: ciò che non entra nel tipo non può uscire dal template.
type FailureKind string

const (
	FailureTimeout    FailureKind = "timeout"
	FailureConnection FailureKind = "connection"
	FailureDNS        FailureKind = "dns"
	FailureTLS        FailureKind = "tls"
	FailureHTTPStatus FailureKind = "http_status"
	FailureUnknown    FailureKind = "unknown"
)

var failureKinds = []FailureKind{
	FailureTimeout, FailureConnection, FailureDNS,
	FailureTLS, FailureHTTPStatus, FailureUnknown,
}

// JobFailedData è il contesto dell'alert di job fallito (R21).
type JobFailedData struct {
	// JobID è l'identificativo con cui comporre il link alla cronologia.
	JobID string
	// JobName è il nome del job scelto dall'utente.
	JobName string
	// Environment distingue staging da production.
	Environment Environment
	// ConsecutiveFailures è il numero di fallimenti di fila, almeno uno.
	ConsecutiveFailures int
	// LastAttemptAt è l'istante dell'ultimo tentativo.
	LastAttemptAt time.Time
	// FailureKind classifica l'esito dell'ultimo tentativo.
	FailureKind FailureKind
	// HTTPStatus ha senso solo con FailureKind uguale a FailureHTTPStatus.
	HTTPStatus int
}

func (d JobFailedData) validate() error {
	if strings.TrimSpace(d.JobID) == "" {
		return errors.New("JobID è obbligatorio")
	}
	if strings.TrimSpace(d.JobName) == "" {
		return errors.New("JobName è obbligatorio")
	}
	if d.Environment != EnvironmentStaging && d.Environment != EnvironmentProduction {
		return fmt.Errorf("Environment non valido: %q", d.Environment)
	}
	if d.ConsecutiveFailures < 1 {
		return fmt.Errorf("ConsecutiveFailures deve essere almeno 1, vale %d", d.ConsecutiveFailures)
	}
	if d.LastAttemptAt.IsZero() {
		return errors.New("LastAttemptAt è obbligatorio")
	}
	if !slices.Contains(failureKinds, d.FailureKind) {
		return fmt.Errorf("FailureKind non valido: %q", d.FailureKind)
	}
	if d.FailureKind == FailureHTTPStatus && (d.HTTPStatus < 100 || d.HTTPStatus > 599) {
		return fmt.Errorf("HTTPStatus non è un codice di stato: %d", d.HTTPStatus)
	}
	return nil
}

// ----------------------------------------------------------- cambio di piano

// PlanChangedData è il contesto della notifica di variazione piano (R21).
//
// I nomi dei piani non si traducono e non si convalidano contro un elenco: sono
// nomi commerciali, e il listino cambia senza chiedere il permesso a questo
// package. Non compaiono importi: la fattura è un documento di Paddle, e una
// cifra ripetuta qui diverge dal listino alla prima revisione.
type PlanChangedData struct {
	// PreviousPlan è il nome del piano da cui si proviene.
	PreviousPlan string
	// NewPlan è il nome del piano in vigore.
	NewPlan string
	// EffectiveAt è il momento da cui vale il nuovo piano.
	EffectiveAt time.Time
}

func (d PlanChangedData) validate() error {
	if strings.TrimSpace(d.PreviousPlan) == "" {
		return errors.New("PreviousPlan è obbligatorio")
	}
	if strings.TrimSpace(d.NewPlan) == "" {
		return errors.New("NewPlan è obbligatorio")
	}
	if d.PreviousPlan == d.NewPlan {
		return fmt.Errorf("PreviousPlan e NewPlan coincidono (%q): non c'è variazione da comunicare", d.NewPlan)
	}
	if d.EffectiveAt.IsZero() {
		return errors.New("EffectiveAt è obbligatorio")
	}
	return nil
}

// -------------------------------------------------------------- sicurezza

// SecurityEventKind è il tipo di evento di sicurezza notificato (R21).
type SecurityEventKind string

const (
	SecurityPasswordReset       SecurityEventKind = "password_reset"
	SecurityAPIKeyCreated       SecurityEventKind = "api_key_created"
	SecurityAPIKeyRevoked       SecurityEventKind = "api_key_revoked"
	SecurityAccountImpersonated SecurityEventKind = "account_impersonated"
)

var securityKinds = []SecurityEventKind{
	SecurityPasswordReset, SecurityAPIKeyCreated,
	SecurityAPIKeyRevoked, SecurityAccountImpersonated,
}

// SecurityAlertData è il contesto dell'email di evento di sicurezza (R21).
//
// La struttura non ha, e non deve avere, un campo per un token o per un link
// con un segreto dentro. Questa email racconta un fatto già avvenuto; i flussi
// che consegnano un token monouso appartengono all'autenticazione. Della
// risorsa coinvolta si porta il nome, mai il valore.
type SecurityAlertData struct {
	// Kind è l'evento accaduto.
	Kind SecurityEventKind
	// OccurredAt è quando è accaduto.
	OccurredAt time.Time
	// ResourceName è il nome leggibile della risorsa coinvolta — per esempio
	// l'etichetta di una chiave API. Facoltativo.
	ResourceName string
	// SourceIP è l'indirizzo da cui è partita l'azione. Facoltativo.
	SourceIP string
}

func (d SecurityAlertData) validate() error {
	if !slices.Contains(securityKinds, d.Kind) {
		return fmt.Errorf("Kind non valido: %q", d.Kind)
	}
	if d.OccurredAt.IsZero() {
		return errors.New("OccurredAt è obbligatorio")
	}
	return nil
}
