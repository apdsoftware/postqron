// Package mailronix recapita le email transazionali già compilate attraverso
// l'API di mailronix.com (R20).
//
// # Che cosa fa, e che cosa deliberatamente non fa
//
// Mailronix è **esclusivamente un motore di recapito**: l'HTML lo compila
// internal/emailrender a partire dai template di emails/templates/, e arriva
// qui come payload completo. Di conseguenza questo package usa la sola
// **modalità a contenuto diretto** di POST /email/send — `subject` più
// `html_body`/`text_body` — e non tocca mai `template_id`, che nel contratto è
// mutuamente esclusivo con quei campi e ospiterebbe su Mailronix una logica che
// per la spec sta nel repository.
//
// La superficie è minuscola: un solo endpoint, POST /email/send, autenticato
// con `Authorization: Bearer mrx_live_<segreto>`. Tutto il resto della console
// Mailronix richiede una sessione browser e non è raggiungibile con una API
// key.
//
// # 202 non significa recapitato (R20.1)
//
// La risposta di successo è identica sia che il messaggio parta davvero sia che
// il destinatario si trovi in una suppression list per bounce o reclami
// precedenti: è una scelta deliberata di Mailronix, per non offrire a un
// chiamante il modo di verificare l'esistenza di un indirizzo altrui. [Receipt]
// contiene perciò l'`email_log_id` e nulla di più, e nessuna logica a valle può
// dedurne che l'utente sia stato raggiunto.
//
// # Il tranello di Cloudflare
//
// `api.mailronix.com` sta dietro la protezione bot di Cloudflare. Un client che
// non imposta uno `User-Agent` esplicito riceve `403` con corpo
// `error code: 1010`: è una pagina di blocco, non un errore Mailronix, e la
// richiesta non ha mai raggiunto il servizio. Somiglia a un problema di
// autenticazione o di dominio non verificato, ma il corpo non è il JSON
// `{"error":{...}}` documentato.
//
// Questo package si difende su due fronti: imposta sempre uno User-Agent
// proprio ([DefaultUserAgent]) e tratta ogni risposta che non sia il JSON
// previsto come [TransportError] — un errore di trasporto, da non interpretare
// con i codici applicativi di Mailronix e da non ritentare secondo le sue
// regole.
//
// # Ritentare
//
// Solo `429 rate_limited` (il limite è per chiave API, non per IP) e
// `503 auth_unavailable` sono transitori. `400`, `403`, `404` e `500` no:
// ritentarli consuma quota senza cambiare esito. Nemmeno gli errori di
// trasporto si ritentano, e non per pignoleria sul contratto: quando la
// risposta non arriva non si sa se la richiesta sia stata accolta, e un
// tentativo in più recapiterebbe l'email due volte. Un `429` e un `503`, al
// contrario, dicono per costruzione che nulla è stato accodato.
//
// # Segreti
//
// La chiave API è di tipo [APIKey], che si stampa oscurata: non c'è un `%v`,
// un `%+v` o un `slog` che possa farla finire in un log. I corpi dei messaggi e
// gli indirizzi dei destinatari non vengono mai registrati.
package mailronix

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/mail"
	"strings"
	"time"
)

// Path è l'unico endpoint raggiungibile con una API key.
const Path = "/email/send"

// DefaultBaseURL è la produzione di Mailronix.
const DefaultBaseURL = "https://api.mailronix.com"

// DefaultUserAgent identifica Postqron alla protezione bot di Cloudflare.
//
// Non è un dettaglio estetico: senza uno User-Agent esplicito la richiesta si
// ferma al blocco di Cloudflare con `403 error code: 1010` e non raggiunge mai
// Mailronix. Vedi la sezione «Il tranello di Cloudflare» nella doc del package.
const DefaultUserAgent = "Postqron/1.0 (+https://postqron.com)"

// StatusQueued è l'unico valore che `status` può assumere in una risposta 202.
const StatusQueued = "queued"

// maxBodyBytes limita quanto si legge di una risposta. Il JSON documentato sta
// in poche centinaia di byte; il tetto serve contro una pagina di errore di un
// intermediario, che può essere lunga a piacere.
const maxBodyBytes = 64 << 10

// Email è un messaggio già compilato, pronto per il recapito.
//
// Corrisponde alla modalità a contenuto diretto di POST /email/send, meno il
// mittente: quello appartiene alla configurazione del client, non al singolo
// messaggio.
type Email struct {
	// To è l'indirizzo del destinatario. **Uno solo**: nel contratto `to` è una
	// stringa e non un array, e più destinatari richiedono più chiamate.
	To string
	// Subject è l'oggetto, obbligatorio.
	Subject string
	// HTML è il corpo HTML, che finisce in `html_body`.
	HTML string
	// Text è il corpo testuale, che finisce in `text_body`. Almeno uno fra HTML
	// e Text dev'essere valorizzato.
	Text string
}

// Validate rifiuta un messaggio che il contratto scarterebbe con un `400`.
//
// Il controllo è locale perché un `400` non è ritentabile e consuma comunque
// una richiesta: quello che si può escludere prima di partire, si esclude
// prima di partire.
func (e Email) Validate() error {
	recipients, err := mail.ParseAddressList(e.To)
	if err != nil {
		return fmt.Errorf("%w: destinatario non valido: %w", ErrInvalidEmail, err)
	}
	if len(recipients) != 1 {
		return fmt.Errorf(
			"%w: un solo destinatario per chiamata, ne sono stati indicati %d",
			ErrInvalidEmail, len(recipients))
	}
	if strings.TrimSpace(e.Subject) == "" {
		return fmt.Errorf("%w: l'oggetto è obbligatorio", ErrInvalidEmail)
	}
	if strings.TrimSpace(e.HTML) == "" && strings.TrimSpace(e.Text) == "" {
		return fmt.Errorf("%w: serve almeno un corpo fra HTML e Text", ErrInvalidEmail)
	}
	return nil
}

// Receipt è tutto ciò che una risposta 202 permette di sapere.
//
// In particolare **non** dice che l'email sia stata recapitata: la risposta è
// identica per un destinatario in suppression list (R20.1). Registrare
// EmailLogID è lecito; dedurne il successo no.
type Receipt struct {
	// Status vale sempre "queued".
	Status string
	// EmailLogID è l'identificativo con cui ritrovare il messaggio nella
	// console Mailronix.
	EmailLogID string
}

// Client invia email transazionali a Mailronix.
//
// È sicuro usarlo da più goroutine: dopo [New] nessun campo viene più scritto.
type Client struct {
	baseURL     string
	apiKey      APIKey
	from        string
	userAgent   string
	httpClient  *http.Client
	maxAttempts int
	backoff     func(attempt int) time.Duration
	sleep       func(context.Context, time.Duration) error
	logger      *slog.Logger
}

// Option modifica un Client in costruzione.
type Option func(*Client)

// WithHTTPClient sostituisce il client HTTP. Serve ai test e a chi vuole
// controllare timeout e connessioni.
//
// Attenzione: il Client applicato da [New] rifiuta i redirect di proposito —
// l'API non ne emette, e seguirne uno significherebbe mandare l'intestazione
// `Authorization` altrove. Un client passato qui se ne assume la responsabilità.
func WithHTTPClient(hc *http.Client) Option {
	return func(c *Client) {
		if hc != nil {
			c.httpClient = hc
		}
	}
}

// WithUserAgent sostituisce lo User-Agent. Un valore vuoto viene ignorato:
// senza User-Agent la richiesta non supera la protezione bot di Cloudflare.
func WithUserAgent(ua string) Option {
	return func(c *Client) {
		if strings.TrimSpace(ua) != "" {
			c.userAgent = ua
		}
	}
}

// WithMaxAttempts fissa il numero massimo di tentativi complessivi, primo
// incluso. Un valore minore di 1 viene ignorato.
func WithMaxAttempts(n int) Option {
	return func(c *Client) {
		if n >= 1 {
			c.maxAttempts = n
		}
	}
}

// WithBackoff sostituisce l'attesa fra i tentativi. `attempt` è il numero del
// tentativo appena fallito, a partire da 1.
func WithBackoff(fn func(attempt int) time.Duration) Option {
	return func(c *Client) {
		if fn != nil {
			c.backoff = fn
		}
	}
}

// WithSleep sostituisce l'attesa vera e propria. Esiste per i test, che non
// hanno motivo di aspettare davvero.
func WithSleep(fn func(context.Context, time.Duration) error) Option {
	return func(c *Client) {
		if fn != nil {
			c.sleep = fn
		}
	}
}

// WithLogger imposta il logger. Il Client registra solo l'esito dei tentativi:
// mai la chiave, mai il destinatario, mai il corpo del messaggio.
func WithLogger(logger *slog.Logger) Option {
	return func(c *Client) {
		if logger != nil {
			c.logger = logger
		}
	}
}

// New costruisce un Client a partire da una configurazione già validata.
func New(cfg Config, opts ...Option) (*Client, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	c := &Client{
		baseURL:   cfg.baseURL(),
		apiKey:    cfg.APIKey,
		from:      cfg.From,
		userAgent: DefaultUserAgent,
		httpClient: &http.Client{
			Timeout: 20 * time.Second,
			// L'API non emette redirect. Seguirne uno significherebbe, nel
			// caso migliore, perdere l'intestazione Authorization e ricevere
			// un 401 incomprensibile; nel caso peggiore mandarla a un host
			// diverso. Meglio un errore esplicito.
			CheckRedirect: func(req *http.Request, _ []*http.Request) error {
				return fmt.Errorf("redirect inatteso verso %s", req.URL.Redacted())
			},
		},
		maxAttempts: 3,
		backoff:     defaultBackoff,
		sleep:       sleepContext,
		logger:      slog.Default(),
	}
	for _, opt := range opts {
		opt(c)
	}
	return c, nil
}

// Send accoda un messaggio e restituisce la ricevuta.
//
// Un errore restituito qui significa che il messaggio **non** è stato accodato.
// Il silenzio, però, non significa il contrario: un nil non dice che l'email
// sia stata recapitata, solo che Mailronix l'ha presa in carico (R20.1).
func (c *Client) Send(ctx context.Context, email Email) (Receipt, error) {
	if err := email.Validate(); err != nil {
		return Receipt{}, err
	}

	payload, err := json.Marshal(sendRequest{
		From:     c.from,
		To:       email.To,
		Subject:  email.Subject,
		HTMLBody: email.HTML,
		TextBody: email.Text,
	})
	if err != nil {
		return Receipt{}, fmt.Errorf("mailronix: serializzazione della richiesta: %w", err)
	}

	attempts := c.maxAttempts
	if attempts < 1 {
		// Un ciclo che non gira restituirebbe una ricevuta vuota senza errore:
		// un successo finto è il modo peggiore di fallire.
		attempts = 1
	}

	var lastErr error
	for attempt := 1; attempt <= attempts; attempt++ {
		receipt, err := c.attempt(ctx, payload)
		if err == nil {
			return receipt, nil
		}
		lastErr = err

		if !Retryable(err) || attempt == attempts {
			break
		}

		wait := c.backoff(attempt)
		// Un Retry-After esplicito ha la precedenza: è il servizio a dire
		// quando tornare, e ignorarlo è il modo più rapido per prendersi un
		// altro 429.
		if hinted, ok := RetryAfter(err); ok && hinted > wait {
			wait = hinted
		}
		c.logger.WarnContext(ctx, "invio email ritentabile fallito, nuovo tentativo",
			slog.Int("attempt", attempt),
			slog.Int("max_attempts", attempts),
			slog.Duration("wait", wait),
			slog.String("reason", reason(err)))

		if err := c.sleep(ctx, wait); err != nil {
			return Receipt{}, fmt.Errorf("mailronix: attesa fra i tentativi interrotta: %w", err)
		}
	}
	return Receipt{}, lastErr
}

// attempt esegue un singolo tentativo.
func (c *Client) attempt(ctx context.Context, payload []byte) (Receipt, error) {
	req, err := http.NewRequestWithContext(
		ctx, http.MethodPost, c.baseURL+Path, bytes.NewReader(payload))
	if err != nil {
		return Receipt{}, &TransportError{Err: err}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	// Senza questo header la richiesta muore al blocco bot di Cloudflare.
	req.Header.Set("User-Agent", c.userAgent)
	req.Header.Set("Authorization", "Bearer "+c.apiKey.reveal())

	resp, err := c.httpClient.Do(req)
	if err != nil {
		// L'errore di http.Client incorpora l'URL, mai le intestazioni: la
		// chiave non può uscire da qui.
		return Receipt{}, &TransportError{Err: err}
	}
	defer func() {
		// Si scarta il residuo per permettere il riuso della connessione;
		// l'errore non ha nulla da dire a nessuno.
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
	if err != nil {
		return Receipt{}, &TransportError{StatusCode: resp.StatusCode, Err: err}
	}

	retryAfter := parseRetryAfter(resp.Header.Get("Retry-After"))

	if resp.StatusCode == http.StatusAccepted {
		var decoded sendResponse
		if err := json.Unmarshal(body, &decoded); err != nil || decoded.EmailLogID == "" {
			// Un 202 senza il JSON previsto non è una ricevuta: potrebbe
			// venire da un intermediario, e inventarsi un email_log_id vuoto
			// sarebbe peggio che ammettere di non averlo.
			return Receipt{}, newTransportError(resp.StatusCode, body,
				errors.New("risposta 202 senza email_log_id"))
		}
		return Receipt{Status: decoded.Status, EmailLogID: decoded.EmailLogID}, nil
	}

	apiErr, ok := decodeAPIError(resp.StatusCode, body)
	if !ok {
		// Qui finisce il blocco di Cloudflare: uno status di errore con un
		// corpo che non è il JSON documentato. La richiesta può non aver mai
		// raggiunto Mailronix, quindi i suoi codici non si applicano.
		return Receipt{}, newTransportError(resp.StatusCode, body, nil)
	}
	apiErr.retryAfter = retryAfter
	return Receipt{}, apiErr
}

// sendRequest è il corpo della richiesta, modalità a contenuto diretto.
//
// `template_id` e `variables` non compaiono di proposito: nel contratto sono
// mutuamente esclusivi con questi campi, e la logica di template sta nel
// repository (R20).
type sendRequest struct {
	From     string `json:"from"`
	To       string `json:"to"`
	Subject  string `json:"subject"`
	HTMLBody string `json:"html_body,omitempty"`
	TextBody string `json:"text_body,omitempty"`
}

type sendResponse struct {
	Status     string `json:"status"`
	EmailLogID string `json:"email_log_id"`
}

// defaultBackoff attende 500ms, 1s, 2s… fino a un tetto di 8s.
func defaultBackoff(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	wait := 500 * time.Millisecond << (attempt - 1)
	if wait > 8*time.Second || wait <= 0 {
		return 8 * time.Second
	}
	return wait
}

// sleepContext attende, o smette prima se il contesto scade.
func sleepContext(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
