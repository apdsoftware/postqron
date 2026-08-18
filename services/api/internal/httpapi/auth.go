package httpapi

import (
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/apdsoftware/postqron/services/api/internal/auth"
)

// maxUserAgentLength tronca la stringa dichiarata dal client prima di
// conservarla: è un valore arbitrario che finisce in una colonna di testo, e non
// c'è motivo per cui debba poter essere lungo un megabyte.
const maxUserAgentLength = 400

// authAPI raccoglie le rotte di autenticazione (R14).
//
// Il riconoscimento del chiamante e la gestione del cookie stanno nel [guard],
// che le rotte dei job usano a loro volta: vedi identity.go.
type authAPI struct {
	*guard
	svc  *auth.Service
	log  *slog.Logger
	deps Deps
}

func newAuthAPI(guard *guard, logger *slog.Logger, deps Deps) *authAPI {
	return &authAPI{guard: guard, svc: deps.Auth, log: logger, deps: deps}
}

func (a *authAPI) routes(mux *http.ServeMux) {
	mux.HandleFunc("POST /auth/register", a.register)
	mux.HandleFunc("POST /auth/login", a.login)
	mux.HandleFunc("POST /auth/logout", a.logout)

	mux.HandleFunc("GET /auth/session", a.authenticated(a.currentSession))
	mux.HandleFunc("GET /auth/sessions", a.authenticated(a.listSessions))
	mux.HandleFunc("DELETE /auth/sessions", a.authenticated(a.revokeOtherSessions))
	mux.HandleFunc("DELETE /auth/sessions/{id}", a.authenticated(a.revokeSession))

	mux.HandleFunc("POST /auth/password/forgot", a.forgotPassword)
	mux.HandleFunc("POST /auth/password/reset", a.resetPassword)
	mux.HandleFunc("POST /auth/password/change", a.authenticated(a.changePassword))

	mux.HandleFunc("POST /auth/email/verify", a.verifyEmail)
	mux.HandleFunc("POST /auth/email/verify/resend", a.authenticated(a.resendVerification))
}

// ------------------------------------------------------------------- corpi

type registerRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
	FullName string `json:"full_name"`
}

type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type forgotPasswordRequest struct {
	Email string `json:"email"`
}

type resetPasswordRequest struct {
	Token    string `json:"token"`
	Password string `json:"password"`
}

type changePasswordRequest struct {
	CurrentPassword string `json:"current_password"`
	NewPassword     string `json:"new_password"`
}

type verifyEmailRequest struct {
	Token string `json:"token"`
}

// acceptedResponse è la risposta di registrazione e recupero password.
//
// È deliberatamente vaga, e uguale nei due casi possibili: dire «ti abbiamo
// scritto» senza dire *se* c'era un account è tutto il punto.
type acceptedResponse struct {
	Status  string `json:"status"`
	Message string `json:"message"`
}

func accepted() acceptedResponse {
	return acceptedResponse{
		Status:  "accepted",
		Message: "Se l'indirizzo è utilizzabile, riceverai un'email con le istruzioni.",
	}
}

// UserResponse è l'utente come lo vede il client. Non contiene l'hash della
// password, e nemmeno il campo: quello che non si serializza non può sfuggire
// per errore.
type UserResponse struct {
	ID              string     `json:"id"`
	Email           string     `json:"email"`
	FullName        string     `json:"full_name"`
	Role            string     `json:"role"`
	Timezone        string     `json:"timezone"`
	EmailVerified   bool       `json:"email_verified"`
	EmailVerifiedAt *time.Time `json:"email_verified_at,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
	LastLoginAt     *time.Time `json:"last_login_at,omitempty"`
}

func userResponse(u auth.User) UserResponse {
	return UserResponse{
		ID:              u.ID,
		Email:           u.Email,
		FullName:        u.FullName,
		Role:            u.Role,
		Timezone:        u.Timezone,
		EmailVerified:   u.EmailVerified(),
		EmailVerifiedAt: u.EmailVerifiedAt,
		CreatedAt:       u.CreatedAt,
		LastLoginAt:     u.LastLoginAt,
	}
}

// SessionResponse è una sessione come compare nell'elenco dei dispositivi.
//
// Non contiene né il token né la sua impronta: la prima cosa è un segreto, la
// seconda è materiale che non serve a nessun client.
type SessionResponse struct {
	ID         string    `json:"id"`
	CreatedAt  time.Time `json:"created_at"`
	LastUsedAt time.Time `json:"last_used_at"`
	ExpiresAt  time.Time `json:"expires_at"`
	IPAddress  string    `json:"ip_address,omitempty"`
	UserAgent  string    `json:"user_agent,omitempty"`
	Current    bool      `json:"current"`
}

// SessionEnvelope è la risposta di login e delle rotte che riguardano la
// sessione corrente.
type SessionEnvelope struct {
	User    UserResponse    `json:"user"`
	Session SessionResponse `json:"session"`
}

// ------------------------------------------------------------------ handler

// register registra un account.
//
// Risponde 202 in ogni caso in cui la richiesta era ben formata, che l'indirizzo
// fosse libero o già preso.
func (a *authAPI) register(w http.ResponseWriter, r *http.Request) {
	var body registerRequest
	if !a.decode(w, r, &body) {
		return
	}

	err := a.svc.Register(r.Context(), auth.RegisterInput{
		Email:    body.Email,
		Password: body.Password,
		FullName: body.FullName,
		Client:   a.client(r),
	})
	if err != nil {
		a.fail(w, r, err)
		return
	}
	writeJSON(w, r, a.log, http.StatusAccepted, accepted())
}

// login apre una sessione.
func (a *authAPI) login(w http.ResponseWriter, r *http.Request) {
	var body loginRequest
	if !a.decode(w, r, &body) {
		return
	}

	result, err := a.svc.Login(r.Context(), auth.LoginInput{
		Email:    body.Email,
		Password: body.Password,
		Client:   a.client(r),
	})
	if err != nil {
		a.fail(w, r, err)
		return
	}

	a.setSessionCookie(w, result)
	writeJSON(w, r, a.log, http.StatusOK, SessionEnvelope{
		User:    userResponse(result.User),
		Session: sessionResponse(result.Session, true),
	})
}

// logout chiude la sessione corrente.
//
// Risponde 204 anche se non c'era nessuna sessione: il risultato voluto è già
// vero, e un errore darebbe a chi ha in mano un token scaduto la conferma che lo
// era. Il cookie viene comunque cancellato.
func (a *authAPI) logout(w http.ResponseWriter, r *http.Request) {
	if err := a.svc.Logout(r.Context(), a.sessionToken(r)); err != nil {
		a.fail(w, r, err)
		return
	}
	a.clearSessionCookie(w)
	w.WriteHeader(http.StatusNoContent)
}

// currentSession restituisce l'utente collegato.
func (a *authAPI) currentSession(w http.ResponseWriter, r *http.Request, user auth.User, session auth.Session) {
	writeJSON(w, r, a.log, http.StatusOK, SessionEnvelope{
		User:    userResponse(user),
		Session: sessionResponse(session, true),
	})
}

// listSessions elenca le sessioni vive dell'utente.
func (a *authAPI) listSessions(w http.ResponseWriter, r *http.Request, user auth.User, session auth.Session) {
	sessions, err := a.svc.ListSessions(r.Context(), user.ID)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	out := make([]SessionResponse, 0, len(sessions))
	for _, s := range sessions {
		out = append(out, sessionResponse(s, s.ID == session.ID))
	}
	writeJSON(w, r, a.log, http.StatusOK, map[string]any{"sessions": out})
}

// revokeSession chiude una sessione indicata per identificativo.
func (a *authAPI) revokeSession(w http.ResponseWriter, r *http.Request, user auth.User, session auth.Session) {
	id := r.PathValue("id")
	if err := a.svc.RevokeSession(r.Context(), user.ID, id); err != nil {
		a.fail(w, r, err)
		return
	}
	// Chiudere la propria sessione dal pannello equivale a un logout: il cookie
	// che il browser ha in mano non vale più, e lasciarlo lì lo farebbe scoprire
	// alla richiesta successiva con un 401 inspiegabile.
	if id == session.ID {
		a.clearSessionCookie(w)
	}
	w.WriteHeader(http.StatusNoContent)
}

// revokeOtherSessions chiude tutte le sessioni tranne quella in uso.
func (a *authAPI) revokeOtherSessions(w http.ResponseWriter, r *http.Request, user auth.User, session auth.Session) {
	n, err := a.svc.RevokeOtherSessions(r.Context(), user.ID, session.ID)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	writeJSON(w, r, a.log, http.StatusOK, map[string]any{"revoked": n})
}

// forgotPassword avvia il recupero password.
func (a *authAPI) forgotPassword(w http.ResponseWriter, r *http.Request) {
	var body forgotPasswordRequest
	if !a.decode(w, r, &body) {
		return
	}
	if err := a.svc.RequestPasswordReset(r.Context(), body.Email, a.client(r)); err != nil {
		a.fail(w, r, err)
		return
	}
	writeJSON(w, r, a.log, http.StatusAccepted, accepted())
}

// resetPassword consuma il token di recupero e imposta la password nuova.
func (a *authAPI) resetPassword(w http.ResponseWriter, r *http.Request) {
	var body resetPasswordRequest
	if !a.decode(w, r, &body) {
		return
	}
	if err := a.svc.ResetPassword(r.Context(), body.Token, body.Password, a.client(r)); err != nil {
		a.fail(w, r, err)
		return
	}
	// Tutte le sessioni sono state revocate, compresa quella eventualmente in
	// corso su questo browser.
	a.clearSessionCookie(w)
	w.WriteHeader(http.StatusNoContent)
}

// changePassword cambia la password di un utente collegato.
func (a *authAPI) changePassword(w http.ResponseWriter, r *http.Request, user auth.User, session auth.Session) {
	var body changePasswordRequest
	if !a.decode(w, r, &body) {
		return
	}
	result, err := a.svc.ChangePassword(r.Context(), user, session, body.CurrentPassword, body.NewPassword)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	// La sessione corrente ha un token nuovo: quello vecchio è stato revocato con
	// tutti gli altri.
	a.setSessionCookie(w, result)
	writeJSON(w, r, a.log, http.StatusOK, SessionEnvelope{
		User:    userResponse(result.User),
		Session: sessionResponse(result.Session, true),
	})
}

// verifyEmail conferma l'indirizzo.
func (a *authAPI) verifyEmail(w http.ResponseWriter, r *http.Request) {
	var body verifyEmailRequest
	if !a.decode(w, r, &body) {
		return
	}
	if err := a.svc.VerifyEmail(r.Context(), body.Token, a.client(r)); err != nil {
		a.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// resendVerification rimanda l'email di conferma dell'indirizzo.
func (a *authAPI) resendVerification(w http.ResponseWriter, r *http.Request, user auth.User, _ auth.Session) {
	if err := a.svc.ResendEmailVerification(r.Context(), user, a.client(r)); err != nil {
		a.fail(w, r, err)
		return
	}
	writeJSON(w, r, a.log, http.StatusAccepted, accepted())
}

// ------------------------------------------------------------------- supporto

// client raccoglie provenienza e user agent della richiesta.
func (a *authAPI) client(r *http.Request) auth.Client {
	userAgent := r.UserAgent()
	if len(userAgent) > maxUserAgentLength {
		userAgent = userAgent[:maxUserAgentLength]
	}
	return auth.Client{
		IP:        ClientIP(r, a.deps.TrustedProxies),
		UserAgent: strings.ToValidUTF8(userAgent, ""),
	}
}

// decode legge il corpo e risponde 400 se non è valido.
func (a *authAPI) decode(w http.ResponseWriter, r *http.Request, dst any) bool {
	if err := decodeJSON(w, r, dst); err != nil {
		return writeDecodeError(w, r, a.log, err)
	}
	return true
}

// fail traduce un errore del servizio in una risposta HTTP.
//
// La corrispondenza vera sta in writeAuthError (identity.go): qui resta solo il
// ripiego sull'errore interno, il cui messaggio non esce mai — potrebbe
// contenere dettagli sul database. Nel log ci va tutto.
func (a *authAPI) fail(w http.ResponseWriter, r *http.Request, err error) {
	a.guard.failAuth(w, r, err)
}

func sessionResponse(s auth.Session, current bool) SessionResponse {
	out := SessionResponse{
		ID:         s.ID,
		CreatedAt:  s.CreatedAt,
		LastUsedAt: s.LastUsedAt,
		ExpiresAt:  s.ExpiresAt,
		UserAgent:  s.UserAgent,
		Current:    current,
	}
	if s.IPAddress != nil {
		out.IPAddress = s.IPAddress.String()
	}
	return out
}
