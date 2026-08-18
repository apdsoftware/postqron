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
// I primi quattro sono gli eventi di R21. La corrispondenza con il tipo
// `notification_event` del database (migrazione 0008) è quasi uno a uno:
// `welcome`, `job_failed` e `plan_changed` hanno lo stesso nome, mentre l'enum
// chiama `security` ciò che qui è `security_alert`. L'enum ha anche
// `job_recovered`, che R21 non copre e per cui non esiste template.
//
// Il quinto, [EventProductUpdate], **non** è di R21 e non ha una riga in quel
// tipo enumerato: le email di marketing non passano dalla coda delle notifiche.
// Vedi [Kind].
type Event string

const (
	EventWelcome       Event = "welcome"
	EventJobFailed     Event = "job_failed"
	EventPlanChanged   Event = "plan_changed"
	EventSecurityAlert Event = "security_alert"
	EventProductUpdate Event = "product_update"
)

// Events restituisce gli eventi coperti, in ordine stabile.
func Events() []Event {
	return []Event{
		EventWelcome, EventJobFailed, EventPlanChanged, EventSecurityAlert,
		EventProductUpdate,
	}
}

// ------------------------------------------------------------------- natura

// Kind è la natura di un'email, e la distinzione più importante di questo
// package.
//
// # Perché è un tipo e non un commento
//
// La privacy policy separa le due famiglie in ogni aspetto. Le transazionali
// (§2.7) «are not marketing and you cannot unsubscribe from them without closing
// your account, because they are how the service tells you things». Le altre
// (§2.8) hanno consenso separato e «every marketing message carries an
// unsubscribe link».
//
// Sbagliare in un senso o nell'altro sono due difetti, e nessuno dei due è
// piccolo: un link di disiscrizione su un avviso di job fallito è un link che
// l'utente userà, smettendo di ricevere gli avvisi che lo tengono informato su
// un servizio che paga; una promozione senza è un illecito.
//
// La difesa non può essere una convenzione, perché una convenzione si dimentica
// alla sesta email. È [KindOf]: **nessun evento si compila senza aver dichiarato
// la propria natura**, e chi aggiunge un template nuovo non trova un valore
// predefinito su cui appoggiarsi — trova un errore, e deve rispondere alla
// domanda «è marketing?» prima che il codice funzioni.
type Kind string

const (
	// KindTransactional è l'email che il servizio manda perché l'utente ha un
	// account e riguarda il servizio che ha chiesto (§2.7). Non ha
	// disiscrizione, non consulta nessun consenso.
	KindTransactional Kind = "transactional"
	// KindMarketing è l'email che si manda solo con il consenso dell'Art.
	// 6(1)(a) e che porta sempre un link di disiscrizione (§2.8).
	KindMarketing Kind = "marketing"
)

// kinds dichiara la natura di ogni evento.
//
// **Non ha un valore predefinito ed è deliberato.** Un `default:` in [KindOf]
// avrebbe reso l'omissione silenziosa, e la direzione in cui sarebbe caduta —
// qualunque delle due si scegliesse — sarebbe stata sbagliata la metà delle
// volte. Un evento assente da questa mappa non si compila affatto: è il modo
// più economico per rendere la domanda obbligatoria invece che facoltativa.
var kinds = map[Event]Kind{
	EventWelcome:       KindTransactional,
	EventJobFailed:     KindTransactional,
	EventPlanChanged:   KindTransactional,
	EventSecurityAlert: KindTransactional,
	EventProductUpdate: KindMarketing,
}

// KindOf dice se un evento è transazionale o di marketing.
//
// Il secondo valore è falso per un evento che non ha dichiarato la propria
// natura. Chi chiama **deve** trattarlo come un errore e non come «probabilmente
// transazionale»: vedi [Kind].
func KindOf(event Event) (Kind, bool) {
	kind, ok := kinds[event]
	return kind, ok
}

// IsMarketing è la forma breve di [KindOf] per chi deve solo decidere.
//
// Un evento senza natura dichiarata risponde `false, false`: non è marketing
// perché non è niente, e il secondo valore lo dice.
func IsMarketing(event Event) (marketing bool, declared bool) {
	kind, ok := KindOf(event)
	return ok && kind == KindMarketing, ok
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
	EventProductUpdate: "#4278e5",
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
//
// # I due conteggi di R58
//
// Un downgrade può fermare dei job, e quando succede questa email è il posto in
// cui l'utente lo scopre. I conteggi sono **due e restano due** per la stessa
// ragione per cui l'enumerato `job_suspension_reason` della 0013 li distingue:
// i rimedi non sono lo stesso. A chi ha job fermi per il numero si chiede di
// sceglierne alcuni da riaccendere; a chi li ha fermi per la risoluzione si
// chiede di cambiare la schedulazione, e riaccenderne un altro non gli
// libererebbe posto. Un totale unico costringerebbe il testo a dire una delle
// due cose a tutti, e a metà dei destinatari direbbe quella sbagliata.
type PlanChangedData struct {
	// PreviousPlan è il nome del piano da cui si proviene.
	PreviousPlan string
	// NewPlan è il nome del piano in vigore.
	NewPlan string
	// EffectiveAt è il momento da cui vale il nuovo piano.
	EffectiveAt time.Time

	// SuspendedByJobLimit è il numero di job fermati perché il piano di
	// destinazione ne consente meno di quanti ne erano accesi (R58,
	// `plan_job_limit`). Zero quando non è successo.
	SuspendedByJobLimit int
	// SuspendedByResolution è il numero di job fermati perché la loro
	// schedulazione è più fitta di quanto il piano consenta (R58,
	// `plan_resolution`). È indipendente dal precedente: si applica anche
	// quando il tetto sul numero non è stato superato.
	SuspendedByResolution int
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
	if d.SuspendedByJobLimit < 0 || d.SuspendedByResolution < 0 {
		return fmt.Errorf("i job sospesi non possono essere negativi: %d per il tetto, %d per la risoluzione",
			d.SuspendedByJobLimit, d.SuspendedByResolution)
	}
	return nil
}

// HasSuspension dice se questo cambio di piano ha fermato qualcosa.
func (d PlanChangedData) HasSuspension() bool {
	return d.SuspendedByJobLimit > 0 || d.SuspendedByResolution > 0
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

// ------------------------------------------------------------- aggiornamento

// maxParagraphs limita quante parti può avere il corpo di un aggiornamento.
//
// Non è una regola editoriale: è il tetto che impedisce a un errore di
// composizione — un ciclo che accoda invece di sostituire — di produrre un
// messaggio da megabyte e farlo rifiutare da Mailronix con un `400`, che non è
// ritentabile.
const maxParagraphs = 20

// ProductUpdateData è il contesto dell'unica email di marketing (§2.8).
//
// # Perché il testo arriva da qui e non dai locales
//
// Ogni altro evento ha i propri testi in emails/templates/locales/, perché sono
// gli stessi a ogni invio: un avviso di job fallito dice sempre la stessa cosa,
// con dentro dei valori diversi. Una comunicazione di prodotto no — il suo testo
// **è** il messaggio, cambia a ogni invio, e metterlo nei locales significherebbe
// una modifica al repository per ogni comunicazione.
//
// Struttura e piè di pagina restano invece nei locales, dove sono uguali per
// tutti: è quella parte a portare la frase che dice perché si sta ricevendo il
// messaggio e il link per non riceverne più.
//
// # UnsubscribeURL non è una comodità
//
// È obbligatorio, e la sua assenza è un errore di compilazione, non un piè di
// pagina più corto. §2.8 dice «every marketing message carries an unsubscribe
// link»: la parola è *every*, e l'unico modo di renderla vera senza contare
// sull'attenzione di chi scrive il prossimo invio è che un messaggio senza quel
// link non esista.
//
// L'indirizzo contiene un valore firmato che autorizza la disiscrizione, ed è
// deliberato che sia in chiaro dentro l'email: §2.8 promette che il link
// funzioni «with one click and without signing in», cioè senza che chi lo apre
// debba dimostrare altro. Non è quindi un segreto trapelato nel corpo — è la
// promessa, scritta dove serve. Ciò che non deve fare è autorizzare qualcosa
// **oltre** la disiscrizione, e la firma è di dominio distinto perché non possa.
type ProductUpdateData struct {
	// Headline è il titolo del messaggio, già nella lingua del destinatario.
	Headline string
	// Paragraphs è il corpo, una voce per capoverso. I capoversi sono separati
	// qui e non da righe vuote dentro una stringa perché la variante HTML e
	// quella testuale li impaginano in modo diverso, e una sola stringa
	// costringerebbe uno dei due template a interpretare l'altro.
	Paragraphs []string

	// CallToActionLabel e CallToActionURL sono il pulsante, facoltativo. O
	// entrambi o nessuno: un pulsante senza etichetta non si legge, uno senza
	// indirizzo non porta da nessuna parte.
	CallToActionLabel string
	CallToActionURL   string

	// UnsubscribeURL è il link di disiscrizione di §2.8. Obbligatorio: vedi
	// sopra.
	UnsubscribeURL string
}

func (d ProductUpdateData) validate() error {
	if strings.TrimSpace(d.Headline) == "" {
		return errors.New("Headline è obbligatorio")
	}
	if len(d.Paragraphs) == 0 {
		return errors.New("Paragraphs è obbligatorio: un messaggio senza corpo non è un messaggio")
	}
	if len(d.Paragraphs) > maxParagraphs {
		return fmt.Errorf("Paragraphs ha %d voci, il massimo è %d", len(d.Paragraphs), maxParagraphs)
	}
	for i, paragraph := range d.Paragraphs {
		if strings.TrimSpace(paragraph) == "" {
			return fmt.Errorf("Paragraphs[%d] è vuoto", i)
		}
	}
	if (strings.TrimSpace(d.CallToActionLabel) == "") != (strings.TrimSpace(d.CallToActionURL) == "") {
		return fmt.Errorf("il pulsante vuole etichetta e indirizzo insieme (etichetta %q, indirizzo %q)",
			d.CallToActionLabel, d.CallToActionURL)
	}
	if d.CallToActionURL != "" {
		if err := validateLinkURL("CallToActionURL", d.CallToActionURL); err != nil {
			return err
		}
	}
	if strings.TrimSpace(d.UnsubscribeURL) == "" {
		return errors.New(
			"UnsubscribeURL è obbligatorio: la privacy policy §2.8 promette un link di disiscrizione " +
				"in ogni messaggio di marketing, e un messaggio senza non deve poter esistere")
	}
	return validateLinkURL("UnsubscribeURL", d.UnsubscribeURL)
}

// HasCallToAction dice se questo messaggio ha un pulsante.
func (d ProductUpdateData) HasCallToAction() bool {
	return strings.TrimSpace(d.CallToActionURL) != ""
}

// validateLinkURL rifiuta un indirizzo che in un client di posta non porterebbe
// da nessuna parte.
//
// `https` soltanto, e assoluto: un percorso relativo dentro un'email non ha una
// radice a cui riferirsi, e `http` in chiaro viene già rifiutato dal controllo
// sui vincoli dei client di posta — meglio dirlo qui, dove il valore entra.
func validateLinkURL(field, raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("%s non è un URL valido: %w", field, err)
	}
	if parsed.Scheme != "https" {
		return fmt.Errorf("%s deve usare https, non %q", field, parsed.Scheme)
	}
	if parsed.Host == "" {
		return fmt.Errorf("%s non ha un host: in un'email un indirizzo relativo non porta da nessuna parte", field)
	}
	return nil
}
