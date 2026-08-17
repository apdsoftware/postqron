package mailronix

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// ErrInvalidEmail segnala un messaggio che non rispetta il contratto e che
// Mailronix scarterebbe con un `400`. Viene rilevato prima di partire.
var ErrInvalidEmail = errors.New("mailronix: messaggio non valido")

// Codici di errore applicativi documentati. Sono i valori stabili su cui fare
// branching; `message` è testo descrittivo in italiano, non pensato per essere
// interpretato.
const (
	CodeInvalidRequest              = "invalid_request"
	CodeMissingTemplateVariable     = "missing_template_variable"
	CodeUnauthenticated             = "unauthenticated"
	CodeDomainNotVerified           = "domain_not_verified"
	CodeTenantSuspended             = "tenant_suspended"
	CodeFreeDailyRecipientsExceeded = "free_daily_recipients_exceeded"
	CodePlanLimitExceeded           = "plan_limit_exceeded"
	CodeTemplateNotFound            = "template_not_found"
	CodeRateLimited                 = "rate_limited"
	CodeInternalError               = "internal_error"
	CodeAuthUnavailable             = "auth_unavailable"
)

// APIError è un errore applicativo di Mailronix: la risposta era davvero il
// JSON `{"error":{"code":…,"message":…}}` documentato, quindi la richiesta ha
// raggiunto il servizio e i suoi codici si applicano.
type APIError struct {
	// StatusCode è lo stato HTTP della risposta.
	StatusCode int
	// Code è il codice stabile dell'errore.
	Code string
	// Message è la descrizione, in italiano, così come arrivata.
	Message string

	retryAfter time.Duration
}

func (e *APIError) Error() string {
	return fmt.Sprintf("mailronix: %d %s: %s", e.StatusCode, e.Code, e.Message)
}

// Retryable distingue i due errori transitori da tutti gli altri.
//
// Solo `429 rate_limited` — il limite è per chiave API, non per IP — e
// `503 auth_unavailable` passano: `400`, `403`, `404` e `500` descrivono uno
// stato che un tentativo in più non cambia, e ritentarli consuma quota a vuoto.
func (e *APIError) Retryable() bool {
	return e.StatusCode == http.StatusTooManyRequests ||
		e.StatusCode == http.StatusServiceUnavailable
}

// RetryAfter riporta l'attesa suggerita dall'omonima intestazione, se c'era.
func (e *APIError) RetryAfter() (time.Duration, bool) {
	return e.retryAfter, e.retryAfter > 0
}

// TransportError è una risposta che non è il JSON documentato, oppure
// l'assenza di una risposta.
//
// Il caso che conta è il blocco bot di Cloudflare davanti a
// `api.mailronix.com`: senza uno User-Agent esplicito la richiesta riceve
// `403` con corpo `error code: 1010` senza mai raggiungere Mailronix.
// Interpretarlo con i codici applicativi porterebbe a cercare un problema di
// autenticazione o di dominio non verificato che non esiste — e a ritentare o
// non ritentare per la ragione sbagliata.
type TransportError struct {
	// StatusCode è lo stato HTTP, oppure 0 se non c'è stata risposta.
	StatusCode int
	// Snippet è l'inizio del corpo, ripulito e troncato, per la diagnosi.
	Snippet string
	// CloudflareCode è il codice della pagina di blocco, per esempio 1010, o 0
	// se il corpo non ne conteneva uno.
	CloudflareCode int
	// Err è l'errore sottostante, quando la risposta non è arrivata affatto.
	Err error
}

func (e *TransportError) Error() string {
	switch {
	case e.BotBlocked():
		return fmt.Sprintf(
			"mailronix: bloccato dalla protezione bot di Cloudflare (HTTP %d, error code: %d): "+
				"la richiesta non ha raggiunto Mailronix, verificare lo User-Agent",
			e.StatusCode, e.CloudflareCode)
	case e.StatusCode == 0:
		return fmt.Sprintf("mailronix: richiesta non completata: %v", e.Err)
	case e.Err != nil:
		return fmt.Sprintf("mailronix: risposta HTTP %d non interpretabile: %v (corpo: %q)",
			e.StatusCode, e.Err, e.Snippet)
	default:
		return fmt.Sprintf("mailronix: risposta HTTP %d non è il JSON previsto (corpo: %q)",
			e.StatusCode, e.Snippet)
	}
}

func (e *TransportError) Unwrap() error { return e.Err }

// BotBlocked indica il blocco di Cloudflare.
func (e *TransportError) BotBlocked() bool { return e.CloudflareCode != 0 }

// Retryable è sempre falso.
//
// Non è una lettura del contratto di Mailronix — che qui non si applica, perché
// la richiesta può non essere mai arrivata — ma la conseguenza di quel dubbio:
// senza sapere se il messaggio sia stato accodato, un tentativo in più rischia
// di recapitarlo due volte. Un blocco di Cloudflare, per parte sua, non si
// risolve riprovando: si risolve con uno User-Agent.
func (e *TransportError) Retryable() bool { return false }

// retryable è il comportamento comune dei due tipi di errore.
type retryable interface{ Retryable() bool }

// Retryable indica se ha senso ritentare l'invio che ha prodotto err.
//
// Un errore sconosciuto non è ritentabile: nel dubbio si preferisce un'email
// non partita a una partita due volte.
func Retryable(err error) bool {
	var r retryable
	if errors.As(err, &r) {
		return r.Retryable()
	}
	return false
}

// RetryAfter riporta l'attesa suggerita da Mailronix, se l'errore ne porta una.
func RetryAfter(err error) (time.Duration, bool) {
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		return apiErr.RetryAfter()
	}
	return 0, false
}

// Code estrae il codice applicativo di err, o la stringa vuota se err non è un
// [APIError] — in particolare se è un errore di trasporto, dove i codici di
// Mailronix non si applicano.
func Code(err error) string {
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		return apiErr.Code
	}
	return ""
}

// errorEnvelope è la forma documentata di un errore.
type errorEnvelope struct {
	Error *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// decodeAPIError interpreta il corpo di una risposta di errore.
//
// Il secondo valore è falso quando il corpo non è il JSON documentato: è la
// discriminante fra errore applicativo ed errore di trasporto, e non si basa
// sul Content-Type, che una pagina di blocco può dichiarare a piacere.
func decodeAPIError(status int, body []byte) (*APIError, bool) {
	var envelope errorEnvelope
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil, false
	}
	if envelope.Error == nil || envelope.Error.Code == "" {
		return nil, false
	}
	return &APIError{
		StatusCode: status,
		Code:       envelope.Error.Code,
		Message:    envelope.Error.Message,
	}, true
}

// cloudflareCodePattern riconosce il corpo delle pagine di blocco di
// Cloudflare — `error code: 1010` per il rilevamento bot, ma anche 1020, 1015 e
// compagnia: la forma è la stessa e vale la pena riconoscerle tutte.
var cloudflareCodePattern = regexp.MustCompile(`(?i)error code:\s*(\d{3,4})`)

// newTransportError costruisce l'errore di trasporto, riconoscendo il blocco di
// Cloudflare e riducendo il corpo a un frammento leggibile.
func newTransportError(status int, body []byte, err error) *TransportError {
	te := &TransportError{StatusCode: status, Snippet: snippet(body), Err: err}
	if match := cloudflareCodePattern.FindSubmatch(body); match != nil {
		if code, convErr := strconv.Atoi(string(match[1])); convErr == nil {
			te.CloudflareCode = code
		}
	}
	return te
}

// snippetLimit è quanto si conserva del corpo: abbastanza per riconoscere una
// pagina di blocco, troppo poco perché un log diventi illeggibile.
const snippetLimit = 200

// secretPattern riconosce una chiave o un bearer token nel corpo di una
// risposta.
//
// Non è paranoia gratuita: il frammento finisce in un messaggio d'errore e
// quindi in un log, e il corpo di una risposta non è nostro. Un intermediario
// che rimanda indietro la richiesta per diagnostica — succede — ci restituirebbe
// l'intestazione Authorization, e da lì la chiave entrerebbe nei log per una
// strada che nessuno ha scelto.
var secretPattern = regexp.MustCompile(`(?i)(bearer\s+)?\bmrx_[a-z]+_[A-Za-z0-9._~+/-]+`)

// snippet riduce un corpo a una riga stampabile, senza segreti.
func snippet(body []byte) string {
	body = secretPattern.ReplaceAll(body, []byte("[chiave rimossa]"))
	cleaned := strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' || r == '\r' {
			return ' '
		}
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, string(body))
	cleaned = strings.Join(strings.Fields(cleaned), " ")
	if len(cleaned) > snippetLimit {
		return cleaned[:snippetLimit] + "…"
	}
	return cleaned
}

// parseRetryAfter interpreta l'intestazione omonima, nelle due forme ammesse
// da HTTP: un numero di secondi o una data.
func parseRetryAfter(value string) time.Duration {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	if seconds, err := strconv.Atoi(value); err == nil {
		if seconds <= 0 {
			return 0
		}
		return time.Duration(seconds) * time.Second
	}
	if date, err := http.ParseTime(value); err == nil {
		if wait := time.Until(date); wait > 0 {
			return wait
		}
	}
	return 0
}

// reason riassume un errore per il log, senza esporne il corpo.
func reason(err error) string {
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		return fmt.Sprintf("%d %s", apiErr.StatusCode, apiErr.Code)
	}
	var te *TransportError
	if errors.As(err, &te) {
		if te.BotBlocked() {
			return fmt.Sprintf("cloudflare %d", te.CloudflareCode)
		}
		return "trasporto"
	}
	return "sconosciuto"
}
