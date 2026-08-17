package httpapi

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/apdsoftware/postqron/services/api/internal/auth"
	"github.com/apdsoftware/postqron/services/api/internal/config"
)

// SessionCookieName è il nome del cookie che porta il token di sessione.
const SessionCookieName = "pq_session"

// Identity è il chiamante autenticato di una rotta.
//
// Esiste come tipo a sé, invece della coppia (utente, sessione), perché la
// sessione non sarà l'unico modo di autenticarsi: le API key sono la issue #397,
// e una chiave non ha una sessione — ha degli scope. Le rotte dei job prendono
// una Identity e non toccano il cookie, così che #397 debba aggiungere un
// riconoscitore qui e nient'altro altrove.
type Identity struct {
	User auth.User

	// Session è la sessione di browser che ha autenticato la richiesta. È nil
	// per le autenticazioni che non ne hanno una.
	Session *auth.Session

	// Scopes sono i permessi della credenziale usata. Una sessione di login non
	// ne ha: vale per tutto ciò che l'utente può fare. Le API key di #397
	// arriveranno con il proprio elenco, ed è qui che andrà verificato.
	Scopes []string
}

// UserID è l'identificativo dell'utente collegato.
func (i Identity) UserID() string { return i.User.ID }

// guard riconosce il chiamante e gestisce il cookie di sessione.
type guard struct {
	svc      *auth.Service
	log      *slog.Logger
	secure   bool
	sameSite http.SameSite
}

func newGuard(cfg config.Config, logger *slog.Logger, svc *auth.Service) *guard {
	return &guard{
		svc: svc,
		log: logger,
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
		user, session, err := g.resolve(w, r)
		if err != nil {
			g.failAuth(w, r, err)
			return
		}
		next(w, r, user, session)
	}
}

// identified rifiuta le richieste di chi non si è autenticato, in qualunque
// modo ammesso.
func (g *guard) identified(next identityHandler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, session, err := g.resolve(w, r)
		if err != nil {
			g.failAuth(w, r, err)
			return
		}
		next(w, r, Identity{User: user, Session: &session})
	}
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

// sessionToken legge il token dal cookie o dalla testata Authorization.
//
// Il cookie è la strada dei browser: `HttpOnly` mette il token fuori dalla
// portata di JavaScript, quindi una XSS sulla dashboard non se lo porta via —
// cosa che accadrebbe se il token stesse in localStorage. `Authorization:
// Bearer` c'è per i client che non sono browser e non hanno un cookie jar.
//
// Il cookie ha la precedenza: se ci sono entrambi, la sessione del browser è
// quella che l'utente si aspetta di usare.
func (g *guard) sessionToken(r *http.Request) string {
	if cookie, err := r.Cookie(SessionCookieName); err == nil && cookie.Value != "" {
		return cookie.Value
	}
	header := r.Header.Get("Authorization")
	if scheme, value, found := strings.Cut(header, " "); found && strings.EqualFold(scheme, "Bearer") {
		return strings.TrimSpace(value)
	}
	return ""
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
