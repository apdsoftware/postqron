// Package httpapi espone il router HTTP del servizio.
//
// Contiene l'health check, le rotte di autenticazione (R14), le rotte REST dei
// job, delle esecuzioni e del trigger manuale (R8), e quelle delle chiavi API
// (R9).
//
// Il riconoscimento del chiamante è in un solo punto — il guard di identity.go —
// e con esso l'applicazione degli scope: è quel punto a rendere vero, per tutte
// le rotte insieme, che una chiave di sola lettura non può scrivere.
package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net"
	"net/http"
	"net/netip"
	"slices"
	"strings"
	"time"

	"github.com/apdsoftware/postqron/services/api/internal/apikeys"
	"github.com/apdsoftware/postqron/services/api/internal/auth"
	"github.com/apdsoftware/postqron/services/api/internal/billing"
	"github.com/apdsoftware/postqron/services/api/internal/config"
	"github.com/apdsoftware/postqron/services/api/internal/githubhook"
	"github.com/apdsoftware/postqron/services/api/internal/jobs"
	"github.com/apdsoftware/postqron/services/api/internal/paddle"
	"github.com/apdsoftware/postqron/services/api/internal/secrets"
)

// maxRequestBody è la dimensione massima di un corpo JSON accettato.
//
// Le richieste di questo servizio sono manciate di campi: ottomila byte sono
// abbondanti, e un tetto basso è la difesa più economica contro chi manda un
// corpo enorme solo per far allocare memoria.
const maxRequestBody = 8 << 10

// Deps sono le dipendenze del router.
//
// Auth può essere nil: in quel caso le rotte di autenticazione non vengono
// registrate e rispondono 404. Serve ai test dell'health check e a un eventuale
// avvio in cui il database non è disponibile — ma non è la configurazione
// normale, e NewRouter lo segnala nel log.
type Deps struct {
	Auth *auth.Service

	// Jobs può essere nil: in quel caso le rotte dei job non vengono
	// registrate. Vale lo stesso discorso di Auth — è la configurazione dei
	// test dell'health check, non quella normale.
	Jobs *jobs.Service

	// APIKeys può essere nil: in quel caso le rotte `/keys` non vengono
	// registrate e nessuna richiesta si può autenticare con una chiave (R9). Le
	// rotte dei job restano raggiungibili con la sessione, quindi la dashboard
	// funziona comunque — ed è per questo che la mancanza si nota nel log
	// all'avvio invece di far fallire la costruzione del router.
	APIKeys *apikeys.Service

	// GitHubWebhook può essere nil: in quel caso la rotta `/webhooks/github`
	// non viene registrata e risponde 404 (R11). Nil è la configurazione di chi
	// non ha il segreto della GitHub App: un webhook senza segreto non è un
	// webhook meno sicuro, è un endpoint pubblico che accetta qualunque cosa, e
	// non registrarlo è l'unica alternativa accettabile a registrarlo verificato.
	GitHubWebhook *githubhook.Service

	// PaddleWebhook può essere nil: in quel caso la rotta `/webhooks/paddle` non
	// viene registrata e risponde 404 (R16). Vale la stessa regola del webhook
	// GitHub, con la posta in gioco più alta: un webhook di fatturazione senza
	// segreto non è un webhook meno sicuro, è un modo per farsi regalare un piano
	// a pagamento da chiunque conosca l'indirizzo. Nil è la configurazione di chi
	// non ha `PADDLE_WEBHOOK_SECRET`, e non registrare la rotta è l'unica
	// alternativa accettabile a registrarne una verificata.
	PaddleWebhook *paddle.Service

	// Billing può essere nil: in quel caso le rotte `/billing` non vengono
	// registrate. Senza, la dashboard non mostra il piano e nessuno può
	// comprare — ma i job continuano a girare con gli entitlement che hanno, che
	// è la degradazione giusta per una macchina di sviluppo senza Paddle.
	Billing *billing.Service

	// Secrets può essere nil: in quel caso le rotte `/secrets` non vengono
	// registrate (R42). I job continuano a girare, ma quelli che riferiscono un
	// `${VAR}` falliscono la risoluzione — e nessuno può creare il segreto che
	// manca. La mancanza si nota nel log all'avvio.
	Secrets *secrets.Service

	// TrustedProxies elenca le reti da cui il servizio accetta la testata
	// `X-Forwarded-For`. Vuoto significa «nessuna»: vedi [ClientIP].
	TrustedProxies []netip.Prefix

	// RateLimits sostituisce i tetti tecnici predefiniti di R10. Il campo zero
	// usa i valori del codice: vedi quota.go, dove è spiegato perché sono nel
	// codice e non in configurazione.
	RateLimits RateLimits

	// Readiness è la sorgente della prontezza del motore (R7). Nil significa che
	// `/readyz` non viene registrata: un processo che non ha un motore da
	// osservare non deve rispondere «pronto» a una domanda su di esso.
	Readiness Readiness

	// Metrics è la pagina delle metriche (R7). Nil, oppure MetricsToken vuoto,
	// significa che `/metrics` non viene registrata affatto: vedi
	// [observabilityAPI.routes].
	Metrics Metrics

	// MetricsToken è la credenziale dell'operatore, da [MetricsTokenEnvVar].
	// Vuota toglie `/metrics` e riduce `/readyz` al solo stato complessivo.
	MetricsToken string

	// Now sostituisce l'orologio dei limitatori. Serve ai test, che devono poter
	// far passare una finestra senza aspettarla.
	Now func() time.Time
}

// Health è il corpo della risposta di /healthz.
//
// # Che cosa dichiara, esattamente
//
// **Che questo processo è in piedi e sta servendo richieste.** Nient'altro: non
// tocca il database, non guarda il motore, e risponde `200` per definizione — se
// non fosse in grado di rispondere, non risponderebbe.
//
// Detta così sembra inutile, e invece è precisamente ciò che serve a chi riavvia
// un processo inchiodato: una liveness che può fallire per colpa del database
// farebbe ammazzare e riavviare il servizio in un ciclo che non risolve niente,
// perché il problema è altrove.
//
// La domanda diversa — «il motore sta facendo il suo mestiere?» — ha un altro
// endpoint, `/readyz`, e un altro pacchetto (internal/health). Tenerle separate
// è il punto: un processo vivo che non riesce a scrivere sul database è malato, e
// un health check che rispondesse `200` **per conto suo** direbbe che va tutto
// bene mentre non parte più niente.
type Health struct {
	Status  string `json:"status"`
	Env     string `json:"env"`
	Version string `json:"version"`
}

// NewRouter costruisce il router del servizio.
func NewRouter(cfg config.Config, version string, logger *slog.Logger, deps Deps) http.Handler {
	mux := http.NewServeMux()

	// Liveness. Vedi [Health] per che cosa dichiara e che cosa no.
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, r, logger, http.StatusOK, Health{
			Status:  "ok",
			Env:     cfg.Env,
			Version: version,
		})
	})

	// Prontezza e metriche (R7). Stanno **fuori** dal guard dell'identità: chi
	// le interroga non è un utente del prodotto ma chi gestisce il servizio, e
	// la loro credenziale è un'altra — vedi [operatorToken].
	obs := newObservabilityAPI(logger, deps)
	obs.routes(mux)
	switch {
	case deps.Readiness == nil:
		logger.Warn("rotta di prontezza non registrata: nessuna sonda del motore configurata")
	case deps.MetricsToken == "":
		logger.Warn("metriche non esposte: "+MetricsTokenEnvVar+" non impostata",
			slog.String("conseguenza", "/metrics risponde 404 e /readyz dice solo lo stato complessivo"))
	}

	if deps.Auth != nil {
		guard := newGuard(cfg, logger, deps)
		newAuthAPI(guard, logger, deps).routes(mux)

		// Le rotte dei job vivono dietro lo stesso guard: senza autenticazione
		// non c'è un utente a cui ancorare né i job né i limiti di piano, quindi
		// registrarle da sole non avrebbe senso.
		if deps.Jobs != nil {
			newJobsAPI(guard, logger, deps.Jobs).routes(mux)
		} else {
			logger.Warn("rotte dei job non registrate: nessun servizio jobs configurato")
		}

		// Le rotte delle chiavi stanno dietro lo stesso guard, e deliberatamente
		// solo dietro la *sessione*: vedi keysAPI.routes.
		if deps.APIKeys != nil {
			newKeysAPI(guard, logger, deps.APIKeys).routes(mux)
		} else {
			logger.Warn("rotte delle chiavi API non registrate: nessun servizio apikeys configurato")
		}

		// I segreti del workspace stanno dietro lo stesso guard, e come le chiavi
		// solo dietro la *sessione*: vedi secretsAPI.routes.
		if deps.Secrets != nil {
			newSecretsAPI(guard, logger, deps.Secrets).routes(mux)
		} else {
			logger.Warn("rotte dei segreti del workspace non registrate: nessun servizio secrets configurato")
		}

		// La fatturazione sta dietro lo stesso guard e, come le due sopra, solo
		// dietro la *sessione*: vedi billingAPI.routes.
		if deps.Billing != nil {
			newBillingAPI(guard, logger, deps.Billing).routes(mux)
			if !deps.Billing.CanSell() {
				// Le rotte esistono comunque: `GET /billing/subscription` funziona e
				// dice il piano in forza, che su questa macchina è quello che l'utente
				// ha e basta. È il checkout a rispondere 503.
				logger.Info("fatturazione attiva senza catalogo: il piano si legge, non si compra")
			}
		} else {
			logger.Warn("rotte di fatturazione non registrate: nessun servizio billing configurato")
		}
	} else {
		logger.Warn("rotte di autenticazione non registrate: nessun servizio auth configurato")
	}

	// Il webhook GitHub sta **fuori** dal guard, e non per dimenticanza: la
	// richiesta arriva da GitHub, che non ha né una sessione né una chiave API.
	// La sua credenziale è la firma HMAC del corpo, che il servizio verifica
	// prima di qualunque altra cosa (R11). Vedi webhooks_github.go.
	if deps.GitHubWebhook != nil {
		newGitHubWebhookAPI(logger, deps.GitHubWebhook).routes(mux)
		if !deps.GitHubWebhook.HasSink() {
			logger.Info("webhook GitHub attivo senza consumatore: le push vengono verificate e registrate, non sincronizzate")
		}
	} else {
		logger.Warn("rotta del webhook GitHub non registrata: nessun servizio githubhook configurato")
	}

	// Il webhook Paddle sta **fuori** dal guard per la stessa ragione di quello
	// GitHub: la richiesta arriva da Paddle, che non ha né una sessione né una
	// chiave API, e la sua credenziale è la firma del corpo. Vedi
	// webhooks_paddle.go.
	if deps.PaddleWebhook != nil {
		newPaddleWebhookAPI(logger, deps.PaddleWebhook).routes(mux)
		if !deps.PaddleWebhook.HasSink() {
			logger.Info("webhook Paddle attivo senza consumatore: gli eventi vengono verificati e registrati, non applicati")
		}
	} else {
		logger.Warn("rotta del webhook Paddle non registrata: nessun servizio paddle configurato")
	}

	return withCORS(cfg, mux)
}

// ------------------------------------------------------------------ risposte

// ErrorBody è l'involucro di ogni risposta di errore.
//
// Il campo che conta per il client è `code`: è stabile e non tradotto, ed è su
// quello che il frontend decide cosa mostrare. `message` è in italiano e serve
// alla diagnostica — la localizzazione dei testi è SPEC §8-bis, issue #445.
type ErrorBody struct {
	Error ErrorDetail `json:"error"`
}

// ErrorDetail è il contenuto di un errore.
//
// I campi facoltativi servono al branching applicativo di R53. `details`
// ancora i motivi di rifiuto ai campi che li causano, così che un form possa
// evidenziarli senza interpretare il messaggio; `limit` e `plan` dicono quale
// limite di piano è scattato e su quale piano, che è ciò che decide se mostrare
// un invito all'upgrade o una correzione del campo; `retry_after` ripete in
// forma leggibile dal codice ciò che la testata omonima già dice.
type ErrorDetail struct {
	Code    string `json:"code"`
	Message string `json:"message"`

	Details    []FieldErrorBody `json:"details,omitempty"`
	Limit      string           `json:"limit,omitempty"`
	Plan       string           `json:"plan,omitempty"`
	RetryAfter int              `json:"retry_after,omitempty"`

	// Scope è il permesso che mancava alla chiave API (R9). Ripete in forma
	// leggibile dal codice ciò che la testata `WWW-Authenticate` già dice, così che
	// un client possa spiegare all'utente quale chiave gli serve senza analizzare
	// una testata.
	Scope string `json:"scope,omitempty"`
}

// FieldErrorBody è un motivo di rifiuto ancorato al campo che lo causa.
//
// `field` usa la notazione a punti del corpo (`request.url`,
// `alerts.on_failure`): è il percorso dentro il JSON mandato, non un nome di
// colonna.
type FieldErrorBody struct {
	Field   string `json:"field"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

func writeJSON(w http.ResponseWriter, r *http.Request, logger *slog.Logger, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if body == nil {
		return
	}
	if err := json.NewEncoder(w).Encode(body); err != nil {
		// La risposta è già iniziata: si può solo tracciare l'errore.
		logger.ErrorContext(r.Context(), "scrittura della risposta JSON fallita",
			slog.String("path", r.URL.Path), slog.Any("error", err))
	}
}

func writeError(w http.ResponseWriter, r *http.Request, logger *slog.Logger, status int, code, message string) {
	writeJSON(w, r, logger, status, ErrorBody{Error: ErrorDetail{Code: code, Message: message}})
}

func writeErrorDetail(w http.ResponseWriter, r *http.Request, logger *slog.Logger, status int, detail ErrorDetail) {
	writeJSON(w, r, logger, status, ErrorBody{Error: detail})
}

// ------------------------------------------------------------------ richieste

var errBodyTooLarge = errors.New("corpo della richiesta troppo grande")

// decodeJSON legge il corpo JSON di una richiesta.
//
// Rifiuta i campi che non conosce: `passwrod` scritto male sarebbe altrimenti
// una password vuota, e il client non avrebbe modo di accorgersene. Rifiuta anche
// un secondo oggetto JSON dopo il primo, che è la forma più semplice di
// contrabbando di payload.
func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) error {
	return decodeJSONLimit(w, r, dst, maxRequestBody)
}

// decodeJSONLimit è decodeJSON con un tetto esplicito al corpo. Le rotte dei job
// ne hanno bisogno di uno più alto: vedi [maxJobRequestBody].
func decodeJSONLimit(w http.ResponseWriter, r *http.Request, dst any, limit int64) error {
	if contentType := r.Header.Get("Content-Type"); contentType != "" {
		mediaType, _, err := mime.ParseMediaType(contentType)
		if err != nil || mediaType != "application/json" {
			return fmt.Errorf("Content-Type non supportato: %q", contentType)
		}
	} else {
		// Una richiesta senza Content-Type non può essere un form inviato da una
		// pagina di terzi (i form ne impongono uno dei tre tipi semplici), ma
		// esigere il tipo giusto è comunque il controllo che rende impossibile
		// il CSRF via form su queste rotte.
		return errors.New("Content-Type mancante: atteso application/json")
	}

	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, limit))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			return errBodyTooLarge
		}
		return fmt.Errorf("corpo JSON non valido: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("il corpo contiene più di un oggetto JSON")
	}
	return nil
}

// ----------------------------------------------------------------- client IP

// ClientIP determina l'indirizzo del chiamante.
//
// Conta perché il rate limiting dell'autenticazione usa l'indirizzo come chiave:
// sbagliarlo significa o mettere tutti gli utenti nello stesso secchio (dietro un
// reverse proxy, `RemoteAddr` è sempre quello del proxy, e il primo attaccante
// bloccherebbe il login a tutti) o accettare un indirizzo dichiarato dal client,
// che come chiave non vale niente perché si cambia a ogni richiesta.
//
// La regola: `X-Forwarded-For` viene considerata **solo** se la connessione
// arriva da una rete in `trusted`, e in quel caso si legge da destra verso
// sinistra scartando i proxy fidati — il primo indirizzo non fidato è il
// chiamante. Gli indirizzi più a sinistra sono quelli che il client può aver
// scritto da sé, e vanno ignorati.
//
// Con `trusted` vuoto si usa sempre `RemoteAddr`. È il default deliberato: senza
// configurazione il servizio non si fida di nessuno, e chi lo mette dietro nginx
// dichiara la rete del proxy in `POSTQRON_TRUSTED_PROXIES`.
func ClientIP(r *http.Request, trusted []netip.Prefix) netip.Addr {
	remote := parseAddr(r.RemoteAddr)
	if len(trusted) == 0 || !remote.IsValid() || !inAny(remote, trusted) {
		return remote
	}

	forwarded := r.Header.Values("X-Forwarded-For")
	var hops []string
	for _, value := range forwarded {
		for _, hop := range strings.Split(value, ",") {
			if hop = strings.TrimSpace(hop); hop != "" {
				hops = append(hops, hop)
			}
		}
	}

	for i := len(hops) - 1; i >= 0; i-- {
		addr, err := netip.ParseAddr(hops[i])
		if err != nil {
			// Un valore illeggibile interrompe la catena: da lì a sinistra non
			// c'è più niente di cui fidarsi.
			return remote
		}
		addr = addr.Unmap()
		if !inAny(addr, trusted) {
			return addr
		}
	}
	return remote
}

// ParseTrustedProxies legge un elenco di reti separate da virgola.
//
// Accetta sia prefissi (`10.0.0.0/8`) sia indirizzi singoli (`127.0.0.1`), che
// diventano il prefisso con la maschera piena.
func ParseTrustedProxies(raw string) ([]netip.Prefix, error) {
	var out []netip.Prefix
	for _, item := range strings.Split(raw, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if prefix, err := netip.ParsePrefix(item); err == nil {
			out = append(out, prefix.Masked())
			continue
		}
		addr, err := netip.ParseAddr(item)
		if err != nil {
			return nil, fmt.Errorf("proxy fidato non valido: %q", item)
		}
		addr = addr.Unmap()
		out = append(out, netip.PrefixFrom(addr, addr.BitLen()))
	}
	return out, nil
}

func parseAddr(remoteAddr string) netip.Addr {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr
	}
	addr, err := netip.ParseAddr(host)
	if err != nil {
		return netip.Addr{}
	}
	return addr.Unmap()
}

func inAny(addr netip.Addr, prefixes []netip.Prefix) bool {
	return slices.ContainsFunc(prefixes, func(prefix netip.Prefix) bool {
		return prefix.Contains(addr)
	})
}

// ---------------------------------------------------------------------- CORS

// withCORS applica le regole di condivisione fra origin.
//
// Serve perché i frontend sono statici e vivono su un'altra origin (SPEC §2), e
// perché la sessione è un cookie: senza `Access-Control-Allow-Credentials` il
// browser non lo manderebbe, e senza un'origin dichiarata esplicitamente non lo
// accetterebbe — il carattere jolly non è ammesso con le credenziali, ed è anche
// il motivo per cui l'origin si riflette solo se è fra quelle configurate.
//
// Il rovescio positivo è che il preflight CORS è anche la difesa contro il CSRF
// via XHR: una pagina su un'origin non elencata non riesce nemmeno a partire.
func withCORS(cfg config.Config, next http.Handler) http.Handler {
	allowed := cfg.AllowedOrigins
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin != "" && slices.Contains(allowed, origin) {
			header := w.Header()
			header.Set("Access-Control-Allow-Origin", origin)
			header.Set("Access-Control-Allow-Credentials", "true")
			header.Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
			header.Set("Access-Control-Allow-Methods", "GET, POST, PATCH, DELETE, OPTIONS")
			header.Set("Access-Control-Max-Age", "600")
			// L'origin riflessa dipende dalla richiesta: senza Vary una cache
			// condivisa servirebbe a un'origin la risposta preparata per
			// un'altra.
			header.Add("Vary", "Origin")
		}
		if r.Method == http.MethodOptions && r.Header.Get("Access-Control-Request-Method") != "" {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}
