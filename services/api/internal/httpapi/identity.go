package httpapi

import (
	"errors"
	"log/slog"
	"net/http"
	"net/netip"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/apdsoftware/postqron/services/api/internal/apikeys"
	"github.com/apdsoftware/postqron/services/api/internal/auth"
	"github.com/apdsoftware/postqron/services/api/internal/config"
)

// SessionCookieName è il nome del cookie che porta il token di sessione.
const SessionCookieName = "pq_session"

// IdentityKind è il modo in cui il chiamante si è autenticato.
type IdentityKind string

const (
	// IdentitySession è una sessione di browser (R14).
	IdentitySession IdentityKind = "session"
	// IdentityAPIKey è una chiave API (R9).
	IdentityAPIKey IdentityKind = "api_key"
)

// Identity è il chiamante autenticato di una rotta.
//
// Esiste come tipo a sé, invece della coppia (utente, sessione), perché la
// sessione non è l'unico modo di autenticarsi: una chiave API non ha una
// sessione — ha degli scope. Le rotte dei job prendono una Identity e non
// toccano il cookie, ed è ciò che ha permesso a R9 di aggiungere un
// riconoscitore qui e nient'altro altrove.
type Identity struct {
	// Kind dice quale credenziale ha autenticato la richiesta. Il valore zero non
	// è nessuna delle due, e [Identity.Allows] lo tratta come «non autorizza
	// niente»: una Identity costruita per sbaglio non apre nessuna porta.
	Kind IdentityKind

	User auth.User

	// Session è la sessione di browser che ha autenticato la richiesta. È nil
	// per le autenticazioni che non ne hanno una.
	Session *auth.Session

	// Key è la chiave API che ha autenticato la richiesta. È nil per le sessioni.
	// Non contiene il valore in chiaro, che non esiste più dopo la creazione.
	Key *apikeys.Key

	// Scopes sono i permessi della credenziale usata, ed è il campo su cui
	// [Identity.Allows] decide. Una sessione di login non ne ha, e non è una
	// lacuna: vale per tutto ciò che l'utente può fare, perché chi ha la password
	// non ha bisogno del permesso di sé stesso.
	Scopes []apikeys.Scope
}

// UserID è l'identificativo dell'utente collegato.
func (i Identity) UserID() string { return i.User.ID }

// Allows indica se la credenziale usata porta lo scope richiesto.
//
// Una sessione autorizza tutto: chi si è autenticato con email e password è
// l'utente, e gli scope esistono per limitare le *deleghe*, non il titolare. Una
// chiave autorizza solo ciò che ha, e qualunque altra cosa — compreso il valore
// zero della struttura — non autorizza niente. È un `switch` esplicito e non un
// controllo su `Key == nil` proprio per questo: la forma «se non è una chiave
// allora può tutto» è quella che, aggiungendo un terzo modo di autenticarsi,
// concede per sbaglio invece di negare.
//
// La decisione guarda [Identity.Scopes] e non [Identity.Key]: c'è un solo elenco
// su cui il permesso si verifica, quindi non esiste la possibilità che i due
// divergano e che la risposta dipenda da quale dei due si è consultato.
func (i Identity) Allows(scope apikeys.Scope) bool {
	switch i.Kind {
	case IdentitySession:
		return true
	case IdentityAPIKey:
		return slices.Contains(i.Scopes, scope)
	default:
		return false
	}
}

// errKeysUnavailable segnala che una chiave API è arrivata a un servizio che non
// sa verificarle. È un errore di configurazione, non della richiesta.
var errKeysUnavailable = errors.New("servizio delle chiavi API non configurato")

// guard riconosce il chiamante e gestisce il cookie di sessione.
type guard struct {
	svc  *auth.Service
	keys *apikeys.Service
	log  *slog.Logger

	// trustedProxies serve al rate limiting dell'autenticazione con chiave, che
	// come quello del login usa l'indirizzo del chiamante come secchio: vedi
	// [ClientIP].
	trustedProxies []netip.Prefix

	// quota applica i limiti di R10. Sta nel guard e non nelle rotte perché è il
	// punto da cui passano tutte: vedi quota.go.
	quota *quota

	secure   bool
	sameSite http.SameSite
}

func newGuard(cfg config.Config, logger *slog.Logger, deps Deps) *guard {
	return &guard{
		svc:            deps.Auth,
		keys:           deps.APIKeys,
		log:            logger,
		trustedProxies: deps.TrustedProxies,
		quota:          newQuota(logger, deps),
		// `Secure` in produzione e in staging, non in sviluppo: su
		// http://localhost un cookie Secure non verrebbe mai memorizzato, e il
		// risultato sarebbe uno sviluppatore che disattiva il flag per far
		// funzionare le cose in locale — cioè che lo disattiva anche altrove.
		secure: cfg.Env != config.EnvDevelopment,
		// Lax e non Strict: con Strict il cookie non viaggia quando l'utente
		// arriva sulla dashboard da un link esterno (per esempio quello di
		// un'email di alert), e si troverebbe scollegato senza capire perché. Lax
		// blocca comunque le richieste POST cross-site, che sono quelle che
		// contano per il CSRF; il resto lo chiude il controllo sul Content-Type
		// in decodeJSON.
		sameSite: http.SameSiteLaxMode,
	}
}

// authHandler è un handler che ha bisogno dell'utente collegato e della sua
// sessione. Lo usano le rotte di autenticazione, che sulla sessione operano.
type authHandler func(w http.ResponseWriter, r *http.Request, user auth.User, session auth.Session)

// identityHandler è un handler che ha bisogno solo di sapere chi chiama.
type identityHandler func(w http.ResponseWriter, r *http.Request, identity Identity)

// authenticated rifiuta le richieste senza una sessione valida.
func (g *guard) authenticated(next authHandler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !g.quota.allowCeiling(w, r, g.credentialKey(r)) {
			return
		}
		user, session, err := g.resolve(w, r)
		if err != nil {
			g.failAuth(w, r, err)
			return
		}
		next(w, r, user, session)
	}
}

// scoped rifiuta le richieste di chi non si è autenticato, in qualunque modo
// ammesso, e quelle la cui credenziale non porta lo scope richiesto.
//
// **È l'unico punto in cui gli scope si applicano**, e sta qui e non negli
// handler per una ragione che vale più della simmetria: uno scope verificato
// dentro al singolo handler è uno scope che il prossimo handler dimentica, e la
// dimenticanza non si vede — la rotta funziona, semplicemente funziona anche per
// chi non doveva. Scritto all'altezza della registrazione della rotta, invece,
// il permesso richiesto è visibile accanto al metodo e al percorso: vedi
// jobsAPI.routes.
//
// **È anche il punto in cui si applicano i limiti di R10**, e per la stessa
// ragione: una quota verificata dentro al gestore è una quota che il gestore
// successivo dimentica. L'ordine dei tre controlli non è indifferente — tetto
// tecnico, riconoscimento, scope, quota di piano — e ciascuno sta dove sta
// perché costa meno di quello che lo segue: vedi quota.go.
func (g *guard) scoped(scope apikeys.Scope, next identityHandler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !g.quota.allowCeiling(w, r, g.credentialKey(r)) {
			return
		}
		identity, err := g.identify(w, r)
		if err != nil {
			g.failAuth(w, r, err)
			return
		}
		if !identity.Allows(scope) {
			g.failScope(w, r, identity, scope)
			return
		}
		// Dopo lo scope: una richiesta che non ha il permesso di scrivere non
		// scrive, quindi non consuma la capacità di scrittura del piano. Prima del
		// gestore: è lì che sta il lavoro che la quota protegge.
		if writeScopes[scope] && !g.quota.allowWrite(w, r, identity) {
			return
		}
		next(w, r, identity)
	}
}

// identify riconosce il chiamante, con la sessione o con una chiave API.
func (g *guard) identify(w http.ResponseWriter, r *http.Request) (Identity, error) {
	if token := g.apiKeyToken(r); token != "" {
		if g.keys == nil {
			// Le rotte sono registrate ma il servizio delle chiavi non c'è: è una
			// configurazione incompleta, non una credenziale sbagliata. Rifiutare come
			// «chiave non valida» manderebbe il proprietario a cercare il problema
			// nella chiave.
			g.log.ErrorContext(r.Context(),
				"chiave API ricevuta ma nessun servizio apikeys configurato",
				slog.String("path", r.URL.Path))
			return Identity{}, errKeysUnavailable
		}
		user, key, err := g.keys.Authenticate(r.Context(), token, apikeys.Client{
			IP: ClientIP(r, g.trustedProxies),
		})
		if err != nil {
			return Identity{}, err
		}
		return Identity{Kind: IdentityAPIKey, User: user, Key: &key, Scopes: key.Scopes}, nil
	}

	user, session, err := g.resolve(w, r)
	if err != nil {
		return Identity{}, err
	}
	return Identity{Kind: IdentitySession, User: user, Session: &session}, nil
}

func (g *guard) resolve(w http.ResponseWriter, r *http.Request) (auth.User, auth.Session, error) {
	user, session, err := g.svc.Authenticate(r.Context(), g.sessionToken(r))
	if err != nil && errors.Is(err, auth.ErrUnauthenticated) {
		// Il cookie che il client ha mandato non vale più: cancellarlo evita che
		// continui a inviarlo a ogni richiesta successiva.
		g.clearSessionCookie(w)
	}
	return user, session, err
}

// sessionToken legge il token di sessione dal cookie o dalla testata
// Authorization.
//
// Il cookie è la strada dei browser: `HttpOnly` mette il token fuori dalla
// portata di JavaScript, quindi una XSS sulla dashboard non se lo porta via —
// cosa che accadrebbe se il token stesse in localStorage. `Authorization:
// Bearer` c'è per i client che non sono browser e non hanno un cookie jar.
//
// Il cookie ha la precedenza: se ci sono entrambi, la sessione del browser è
// quella che l'utente si aspetta di usare. Le chiavi API sono l'eccezione, e la
// gestisce [guard.apiKeyToken].
func (g *guard) sessionToken(r *http.Request) string {
	if cookie, err := r.Cookie(SessionCookieName); err == nil && cookie.Value != "" {
		return cookie.Value
	}
	if value, ok := bearer(r); ok && !apikeys.LooksLikeToken(value) {
		return value
	}
	return ""
}

// apiKeyToken legge una chiave API dalla testata Authorization, se c'è.
//
// Sessioni e chiavi arrivano entrambe come `Bearer <valore>`, e il prefisso
// `pq_live_` è ciò che le distingue senza doverle provare entrambe: provarle
// entrambe farebbe di ogni chiave scaduta anche un tentativo di sessione, cioè
// due contatori di rate limiting mossi da una richiesta sola.
//
// **La chiave ha la precedenza sul cookie**, al contrario di quanto fa
// [guard.sessionToken] fra cookie e Bearer. Il motivo è lo scope: se una
// richiesta porta sia un cookie sia una chiave, dare la precedenza al cookie
// eseguirebbe l'operazione con i poteri pieni dell'utente ignorando i limiti
// della chiave che il chiamante ha dichiarato di voler usare — un'escalation
// silenziosa, e per di più invisibile a chi legge la risposta. Non apre nessuna
// via al CSRF, perché una pagina di terzi può far mandare al browser il cookie
// ma non può impostare la testata `Authorization`.
func (g *guard) apiKeyToken(r *http.Request) string {
	if value, ok := bearer(r); ok && apikeys.LooksLikeToken(value) {
		return value
	}
	return ""
}

func bearer(r *http.Request) (string, bool) {
	scheme, value, found := strings.Cut(r.Header.Get("Authorization"), " ")
	if !found || !strings.EqualFold(scheme, "Bearer") {
		return "", false
	}
	value = strings.TrimSpace(value)
	return value, value != ""
}

func (g *guard) setSessionCookie(w http.ResponseWriter, result auth.LoginResult) {
	http.SetCookie(w, &http.Cookie{
		Name:  SessionCookieName,
		Value: result.Token,
		Path:  "/",
		// La scadenza del cookie segue quella della sessione nel database. Se le
		// due divergessero, la più corta vincerebbe comunque: la verifica vera è
		// quella lato server.
		Expires:  result.Session.ExpiresAt,
		MaxAge:   int(time.Until(result.Session.ExpiresAt).Seconds()),
		HttpOnly: true,
		Secure:   g.secure,
		SameSite: g.sameSite,
	})
}

func (g *guard) clearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   g.secure,
		SameSite: g.sameSite,
	})
}

// failScope risponde a una credenziale valida che non porta lo scope richiesto.
//
// 403 e non 401: la credenziale è buona e riprovarla non cambierà niente —
// quello che serve è una chiave diversa. `WWW-Authenticate` con
// `error="insufficient_scope"` è la forma di RFC 6750 §3.1, che dice al client
// *quale* permesso mancava invece di lasciarlo indovinare.
//
// Nel log ci va lo scope mancante e il prefisso della chiave, non la chiave.
func (g *guard) failScope(w http.ResponseWriter, r *http.Request, identity Identity, scope apikeys.Scope) {
	prefix := ""
	if identity.Key != nil {
		prefix = identity.Key.Prefix
	}
	g.log.InfoContext(r.Context(), "scope insufficiente",
		slog.String("path", r.URL.Path),
		slog.String("required_scope", string(scope)),
		slog.String("api_key_prefix", prefix))

	w.Header().Set("WWW-Authenticate",
		`Bearer error="insufficient_scope", scope="`+string(scope)+`"`)
	writeErrorDetail(w, r, g.log, http.StatusForbidden, ErrorDetail{
		Code: "insufficient_scope",
		Message: "Questa chiave API non ha il permesso `" + string(scope) +
			"`. Le chiavi non si modificano: creane una nuova con lo scope che ti serve.",
		Scope: string(scope),
	})
}

// failAuth risponde a un errore di autenticazione.
func (g *guard) failAuth(w http.ResponseWriter, r *http.Request, err error) {
	if handled := writeAuthError(w, r, g.log, err); handled {
		return
	}
	g.log.ErrorContext(r.Context(), "errore interno nell'autenticazione",
		slog.String("path", r.URL.Path), slog.Any("error", err))
	writeError(w, r, g.log, http.StatusInternalServerError, "internal_error",
		"Errore interno. Riprova più tardi.")
}

// writeAuthError traduce gli errori del servizio di autenticazione.
//
// È l'unico posto in cui questa corrispondenza esiste, e questo è voluto: la
// proprietà «un login mancato risponde 401 con lo stesso codice qualunque sia la
// causa» si verifica leggendo questa funzione, non ispezionando dieci handler.
// Il secondo valore è falso quando l'errore non è dell'autenticazione, così che
// anche le rotte dei job possano provare qui per prime senza inghiottire i
// propri errori.
func writeAuthError(w http.ResponseWriter, r *http.Request, logger *slog.Logger, err error) bool {
	var limited *auth.RateLimitedError
	switch {
	case errors.As(err, &limited):
		// Retry-After in secondi: è la forma che i client capiscono senza
		// interpretare una data.
		w.Header().Set("Retry-After", strconv.Itoa(int(limited.RetryAfter.Seconds())))
		writeError(w, r, logger, http.StatusTooManyRequests, "rate_limited",
			"Troppi tentativi. Riprova più tardi.")

	case errors.Is(err, auth.ErrInvalidCredentials):
		writeError(w, r, logger, http.StatusUnauthorized, "invalid_credentials",
			"Email o password non corretti.")

	case errors.Is(err, auth.ErrUnauthenticated):
		writeError(w, r, logger, http.StatusUnauthorized, "unauthenticated",
			"Sessione assente o scaduta.")

	case errors.Is(err, apikeys.ErrInvalidKey):
		// Un codice distinto da `unauthenticated` perché il rimedio è diverso: chi
		// arriva qui non deve rifare il login, deve controllare la chiave. Il
		// messaggio non distingue «inesistente» da «revocata» da «scaduta»: quella
		// differenza è ciò che trasformerebbe l'API in un oracolo su una chiave
		// trovata in giro.
		w.Header().Set("WWW-Authenticate", `Bearer error="invalid_token"`)
		writeError(w, r, logger, http.StatusUnauthorized, "invalid_api_key",
			"Chiave API assente, revocata o scaduta.")

	case errors.Is(err, apikeys.ErrKeyNotFound):
		writeError(w, r, logger, http.StatusNotFound, "api_key_not_found",
			"Chiave non trovata.")

	case errors.Is(err, apikeys.ErrTooManyKeys):
		writeError(w, r, logger, http.StatusConflict, "too_many_api_keys",
			"Hai raggiunto il numero massimo di chiavi attive: revocane una prima di crearne un'altra.")

	case errors.Is(err, auth.ErrAccountSuspended):
		writeError(w, r, logger, http.StatusForbidden, "account_suspended",
			"Account sospeso. Contatta l'assistenza.")

	case errors.Is(err, auth.ErrInvalidToken):
		writeError(w, r, logger, http.StatusBadRequest, "invalid_token",
			"Link non valido o scaduto. Richiedine uno nuovo.")

	case errors.Is(err, auth.ErrInvalidEmail):
		writeError(w, r, logger, http.StatusBadRequest, "invalid_email",
			"Indirizzo email non valido.")

	case errors.Is(err, auth.ErrPasswordTooShort),
		errors.Is(err, auth.ErrPasswordTooLong),
		errors.Is(err, auth.ErrPasswordBlank):
		writeError(w, r, logger, http.StatusBadRequest, "weak_password", err.Error())

	case errors.Is(err, auth.ErrSessionNotFound):
		writeError(w, r, logger, http.StatusNotFound, "session_not_found",
			"Sessione non trovata.")

	default:
		return false
	}
	return true
}
