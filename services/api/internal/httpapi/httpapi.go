// Package httpapi espone il router HTTP del servizio.
//
// Contiene l'health check, le rotte di autenticazione (R14) e le rotte REST dei
// job, delle esecuzioni e del trigger manuale (R8).
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

	"github.com/apdsoftware/postqron/services/api/internal/auth"
	"github.com/apdsoftware/postqron/services/api/internal/config"
	"github.com/apdsoftware/postqron/services/api/internal/jobs"
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

	// TrustedProxies elenca le reti da cui il servizio accetta la testata
	// `X-Forwarded-For`. Vuoto significa «nessuna»: vedi [ClientIP].
	TrustedProxies []netip.Prefix
}

// Health è il corpo della risposta di /healthz.
type Health struct {
	Status  string `json:"status"`
	Env     string `json:"env"`
	Version string `json:"version"`
}

// NewRouter costruisce il router del servizio.
func NewRouter(cfg config.Config, version string, logger *slog.Logger, deps Deps) http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, r, logger, http.StatusOK, Health{
			Status:  "ok",
			Env:     cfg.Env,
			Version: version,
		})
	})

	if deps.Auth != nil {
		guard := newGuard(cfg, logger, deps.Auth)
		newAuthAPI(guard, logger, deps).routes(mux)

		// Le rotte dei job vivono dietro lo stesso guard: senza autenticazione
		// non c'è un utente a cui ancorare né i job né i limiti di piano, quindi
		// registrarle da sole non avrebbe senso.
		if deps.Jobs != nil {
			newJobsAPI(guard, logger, deps.Jobs).routes(mux)
		} else {
			logger.Warn("rotte dei job non registrate: nessun servizio jobs configurato")
		}
	} else {
		logger.Warn("rotte di autenticazione non registrate: nessun servizio auth configurato")
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
