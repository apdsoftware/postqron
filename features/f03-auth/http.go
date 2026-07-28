package auth

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	SessionCookieName = "__Host-postqron_session"
	CSRFCookieName    = "__Host-postqron_csrf"
)

type Handler struct {
	service *Service
}

func NewHandler(service *Service) (http.Handler, error) {
	if service == nil {
		return nil, errors.New("auth service is required")
	}
	handler := &Handler{service: service}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/auth/authorize", handler.authorize)
	mux.HandleFunc("GET /api/v1/auth/callback", handler.callback)
	mux.HandleFunc("POST /api/v1/auth/callback", handler.callback)
	mux.HandleFunc("POST /api/v1/auth/link", handler.link)
	mux.HandleFunc("POST /api/v1/auth/logout", handler.logout)
	mux.HandleFunc("POST /api/v1/auth/sessions/revoke", handler.revokeSessions)
	mux.HandleFunc("DELETE /api/v1/auth/providers/{provider}", handler.unlink)
	return mux, nil
}

func (h *Handler) authorize(response http.ResponseWriter, request *http.Request) {
	var input struct {
		Provider        Provider         `json:"provider"`
		ReturnTo        string           `json:"return_to"`
		ContractCountry string           `json:"contract_country"`
		Consents        []ConsentReceipt `json:"consents"`
	}
	if err := decodeJSON(response, request, &input); err != nil {
		writeError(response, err)
		return
	}
	authorization, err := h.service.Begin(request.Context(), BeginRequest{
		Provider:        input.Provider,
		ReturnTo:        input.ReturnTo,
		ContractCountry: input.ContractCountry,
		Consents:        input.Consents,
	})
	if err != nil {
		writeError(response, err)
		return
	}
	writeJSON(response, http.StatusCreated, map[string]any{
		"authorization_url": authorization.URL,
		"expires_at":        authorization.ExpiresAt,
	})
}

func (h *Handler) callback(response http.ResponseWriter, request *http.Request) {
	if err := request.ParseForm(); err != nil {
		writeError(response, newError(
			CodeInvalidRequest,
			"Risposta del provider non valida. Riprova.",
			true,
			err,
		))
		return
	}
	result, err := h.service.Callback(request.Context(), CallbackRequest{
		State:         request.Form.Get("state"),
		Code:          request.Form.Get("code"),
		ProviderError: request.Form.Get("error"),
	})
	if err != nil {
		writeError(response, err)
		return
	}
	if result.SessionToken != "" {
		setAuthCookies(response, result.SessionToken, result.SessionExpiry)
	}
	writeJSON(response, http.StatusOK, map[string]any{
		"account_id": result.AccountID,
		"linked":     result.Linked,
		"onboarding": result.Onboarding,
		"return_to":  result.ReturnTo,
	})
}

func (h *Handler) link(response http.ResponseWriter, request *http.Request) {
	if err := requireSameOrigin(request); err != nil {
		writeError(response, err)
		return
	}
	sessionToken, err := sessionCookie(request)
	if err != nil {
		writeError(response, err)
		return
	}
	var input struct {
		Provider Provider `json:"provider"`
		ReturnTo string   `json:"return_to"`
	}
	if err := decodeJSON(response, request, &input); err != nil {
		writeError(response, err)
		return
	}
	authorization, err := h.service.BeginLink(request.Context(), BeginLinkRequest{
		Provider:     input.Provider,
		ReturnTo:     input.ReturnTo,
		SessionToken: sessionToken,
	})
	if err != nil {
		writeError(response, err)
		return
	}
	writeJSON(response, http.StatusCreated, map[string]any{
		"authorization_url": authorization.URL,
		"expires_at":        authorization.ExpiresAt,
	})
}

func (h *Handler) logout(response http.ResponseWriter, request *http.Request) {
	if err := requireSameOrigin(request); err != nil {
		writeError(response, err)
		return
	}
	sessionToken, _ := sessionCookie(request)
	if err := h.service.Logout(request.Context(), sessionToken); err != nil {
		writeError(response, err)
		return
	}
	clearAuthCookies(response)
	response.WriteHeader(http.StatusNoContent)
}

func (h *Handler) revokeSessions(response http.ResponseWriter, request *http.Request) {
	if err := requireSameOrigin(request); err != nil {
		writeError(response, err)
		return
	}
	sessionToken, err := sessionCookie(request)
	if err != nil {
		writeError(response, err)
		return
	}
	if err := h.service.RevokeAllSessions(request.Context(), sessionToken); err != nil {
		writeError(response, err)
		return
	}
	clearAuthCookies(response)
	response.WriteHeader(http.StatusNoContent)
}

func (h *Handler) unlink(response http.ResponseWriter, request *http.Request) {
	if err := requireSameOrigin(request); err != nil {
		writeError(response, err)
		return
	}
	sessionToken, err := sessionCookie(request)
	if err != nil {
		writeError(response, err)
		return
	}
	if err := h.service.UnlinkProvider(
		request.Context(),
		sessionToken,
		Provider(request.PathValue("provider")),
	); err != nil {
		writeError(response, err)
		return
	}
	response.WriteHeader(http.StatusNoContent)
}

func decodeJSON(response http.ResponseWriter, request *http.Request, target any) error {
	request.Body = http.MaxBytesReader(response, request.Body, 64<<10)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return newError(
			CodeInvalidRequest,
			"Richiesta non valida.",
			false,
			err,
		)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return newError(
			CodeInvalidRequest,
			"Richiesta non valida.",
			false,
			err,
		)
	}
	return nil
}

func sessionCookie(request *http.Request) (string, error) {
	cookie, err := request.Cookie(SessionCookieName)
	if err != nil || strings.TrimSpace(cookie.Value) == "" {
		return "", newError(
			CodeUnauthenticated,
			"Sessione non valida. Accedi di nuovo.",
			true,
			err,
		)
	}
	return cookie.Value, nil
}

func requireSameOrigin(request *http.Request) error {
	switch request.Header.Get("Sec-Fetch-Site") {
	case "cross-site":
		return newError(
			CodeInvalidRequest,
			"Origine della richiesta non valida.",
			false,
			nil,
		)
	}
	origin := request.Header.Get("Origin")
	if origin == "" {
		return nil
	}
	parsed, err := url.Parse(origin)
	if err != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") ||
		!strings.EqualFold(parsed.Host, request.Host) {
		return newError(
			CodeInvalidRequest,
			"Origine della richiesta non valida.",
			false,
			err,
		)
	}
	return nil
}

func setAuthCookies(response http.ResponseWriter, token string, expiresAt time.Time) {
	http.SetCookie(response, &http.Cookie{
		Name:     SessionCookieName,
		Value:    token,
		Path:     "/",
		Secure:   true,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Expires:  expiresAt,
		MaxAge:   int(time.Until(expiresAt).Seconds()),
	})
	http.SetCookie(response, &http.Cookie{
		Name:     CSRFCookieName,
		Value:    csrfTokenValue(token),
		Path:     "/",
		Secure:   true,
		HttpOnly: false,
		SameSite: http.SameSiteLaxMode,
		Expires:  expiresAt,
		MaxAge:   int(time.Until(expiresAt).Seconds()),
	})
}

func clearAuthCookies(response http.ResponseWriter) {
	http.SetCookie(response, &http.Cookie{
		Name:     SessionCookieName,
		Path:     "/",
		Secure:   true,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
	http.SetCookie(response, &http.Cookie{
		Name:     CSRFCookieName,
		Path:     "/",
		Secure:   true,
		HttpOnly: false,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
}

func writeError(response http.ResponseWriter, err error) {
	code, message, retryable := ErrorDetails(err)
	status := http.StatusBadRequest
	switch code {
	case CodeUnauthenticated, CodeReauthenticationRequired:
		status = http.StatusUnauthorized
	case CodeIdentityConflict, CodeLinkingRequired, CodeLastProvider, CodeConflict:
		status = http.StatusConflict
	case CodeProviderUnavailable, CodeInternal:
		status = http.StatusServiceUnavailable
	}
	writeJSON(response, status, map[string]any{
		"error": map[string]any{
			"code":      code,
			"message":   message,
			"retryable": retryable,
		},
	})
}

func writeJSON(response http.ResponseWriter, status int, payload any) {
	response.Header().Set("Content-Type", "application/json")
	response.WriteHeader(status)
	_ = json.NewEncoder(response).Encode(payload)
}
