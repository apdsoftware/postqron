package adminconsole

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
)

const (
	sessionCookieName    = "__Host-postqron_session"
	maxAdminRequestBytes = 8 << 10
)

type HTTPHandler struct {
	service       *Service
	authenticator Authenticator
	handler       http.Handler
}

func NewHandler(service *Service, authenticator Authenticator) (http.Handler, error) {
	if service == nil || authenticator == nil {
		return nil, errors.New("admin HTTP dependencies are required")
	}
	admin := &HTTPHandler{
		service:       service,
		authenticator: authenticator,
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/admin/session", admin.session)
	mux.HandleFunc("GET /api/v1/admin/dashboard", admin.dashboard)
	mux.HandleFunc("GET /api/v1/admin/search", admin.search)
	mux.HandleFunc(
		"PUT /api/v1/admin/workspaces/{workspace_id}/internal-plan",
		admin.assignInternalPlan,
	)
	mux.HandleFunc(
		"DELETE /api/v1/admin/workspaces/{workspace_id}/internal-plan",
		admin.revokeInternalPlan,
	)
	mux.HandleFunc("PUT /api/v1/admin/admins/{account_id}", admin.addAdmin)
	mux.HandleFunc("DELETE /api/v1/admin/admins/{account_id}", admin.removeAdmin)
	admin.handler = admin.authorize(mux)
	return admin, nil
}

func (handler *HTTPHandler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	handler.handler.ServeHTTP(writer, request)
}

type adminContextKey struct{}

type authorizedRequest struct {
	principal Principal
	session   Session
}

func (handler *HTTPHandler) authorize(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		cookie, err := request.Cookie(sessionCookieName)
		if err != nil || strings.TrimSpace(cookie.Value) == "" {
			writeError(writer, http.StatusUnauthorized, "ADMIN_UNAUTHENTICATED")
			return
		}
		session, err := handler.authenticator.Session(request.Context(), cookie.Value)
		if err != nil {
			writeError(writer, http.StatusUnauthorized, "ADMIN_UNAUTHENTICATED")
			return
		}
		principal, err := handler.service.Authorize(request.Context(), session)
		if err != nil {
			if errors.Is(err, ErrUnauthenticated) {
				writeError(writer, http.StatusUnauthorized, "ADMIN_UNAUTHENTICATED")
				return
			}
			if errors.Is(err, ErrAdministrationUnavailable) {
				writeError(writer, http.StatusServiceUnavailable, "ADMIN_UNAVAILABLE")
				return
			}
			// All verified-email, allowlist, and directory denials are
			// intentionally indistinguishable.
			writeError(writer, http.StatusForbidden, "ADMIN_FORBIDDEN")
			return
		}
		ctx := context.WithValue(request.Context(), adminContextKey{}, authorizedRequest{
			principal: principal,
			session:   session,
		})
		next.ServeHTTP(writer, request.WithContext(ctx))
	})
}

func authorized(request *http.Request) authorizedRequest {
	value, _ := request.Context().Value(adminContextKey{}).(authorizedRequest)
	return value
}

func (handler *HTTPHandler) session(writer http.ResponseWriter, request *http.Request) {
	auth := authorized(request)
	writeJSON(writer, http.StatusOK, map[string]any{
		"account": map[string]string{
			"id":    auth.principal.AccountID,
			"email": auth.principal.Email,
		},
		"authenticated_at": auth.principal.AuthenticatedAt,
		"csrf_token":       auth.session.CSRFToken,
	})
}

func (handler *HTTPHandler) dashboard(writer http.ResponseWriter, request *http.Request) {
	value, err := handler.service.Dashboard(request.Context())
	if err != nil {
		writeError(writer, http.StatusServiceUnavailable, "ADMIN_UNAVAILABLE")
		return
	}
	writeJSON(writer, http.StatusOK, value)
}

func (handler *HTTPHandler) search(writer http.ResponseWriter, request *http.Request) {
	value, err := handler.service.Search(request.Context(), request.URL.Query().Get("q"))
	if err != nil {
		if errors.Is(err, ErrInvalidRequest) {
			writeError(writer, http.StatusBadRequest, "ADMIN_INVALID_SEARCH")
			return
		}
		writeError(writer, http.StatusServiceUnavailable, "ADMIN_UNAVAILABLE")
		return
	}
	writeJSON(writer, http.StatusOK, value)
}

func (handler *HTTPHandler) assignInternalPlan(
	writer http.ResponseWriter,
	request *http.Request,
) {
	handler.mutate(writer, request, "internal_plan.assign", request.PathValue("workspace_id"), false)
}

func (handler *HTTPHandler) revokeInternalPlan(
	writer http.ResponseWriter,
	request *http.Request,
) {
	handler.mutate(writer, request, "internal_plan.revoke", request.PathValue("workspace_id"), false)
}

func (handler *HTTPHandler) addAdmin(writer http.ResponseWriter, request *http.Request) {
	handler.mutate(writer, request, "admin.add", request.PathValue("account_id"), true)
}

func (handler *HTTPHandler) removeAdmin(writer http.ResponseWriter, request *http.Request) {
	handler.mutate(writer, request, "admin.remove", request.PathValue("account_id"), true)
}

func (handler *HTTPHandler) mutate(
	writer http.ResponseWriter,
	request *http.Request,
	action, subjectID string,
	includeEmail bool,
) {
	var payload struct {
		Confirmed bool   `json:"confirmed"`
		Email     string `json:"email"`
		Reason    string `json:"reason"`
	}
	if err := decodeJSON(writer, request, &payload); err != nil {
		writeError(writer, http.StatusBadRequest, "ADMIN_INVALID_REQUEST")
		return
	}
	if !includeEmail && payload.Email != "" {
		writeError(writer, http.StatusBadRequest, "ADMIN_INVALID_REQUEST")
		return
	}
	auth := authorized(request)
	result, err := handler.service.Mutate(
		request.Context(),
		auth.principal,
		auth.session,
		MutationRequest{
			Action:         action,
			SubjectID:      subjectID,
			SubjectEmail:   payload.Email,
			Reason:         payload.Reason,
			Confirmed:      payload.Confirmed,
			CSRFToken:      request.Header.Get("X-CSRF-Token"),
			IdempotencyKey: request.Header.Get("Idempotency-Key"),
		},
	)
	if err != nil {
		switch {
		case errors.Is(err, ErrForbidden):
			writeError(writer, http.StatusForbidden, "ADMIN_FORBIDDEN")
		case errors.Is(err, ErrCSRF):
			writeError(writer, http.StatusForbidden, "ADMIN_CSRF_INVALID")
		case errors.Is(err, ErrRecentReauthRequired):
			writeError(writer, http.StatusUnauthorized, "ADMIN_REAUTH_REQUIRED")
		case errors.Is(err, ErrIdempotencyKeyRequired):
			writeError(writer, http.StatusBadRequest, "ADMIN_IDEMPOTENCY_REQUIRED")
		case errors.Is(err, ErrInvalidRequest):
			writeError(writer, http.StatusBadRequest, "ADMIN_INVALID_REQUEST")
		default:
			writeError(writer, http.StatusServiceUnavailable, "ADMIN_UNAVAILABLE")
		}
		return
	}
	writeJSON(writer, http.StatusOK, result)
}

func decodeJSON(writer http.ResponseWriter, request *http.Request, destination any) error {
	if request.Header.Get("Content-Type") != "application/json" {
		return ErrInvalidRequest
	}
	decoder := json.NewDecoder(http.MaxBytesReader(
		writer,
		request.Body,
		maxAdminRequestBytes,
	))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return ErrInvalidRequest
	}
	return nil
}

func writeJSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.Header().Set("Cache-Control", "no-store")
	writer.Header().Set("X-Content-Type-Options", "nosniff")
	writer.Header().Set("Referrer-Policy", "no-referrer")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}

func writeError(writer http.ResponseWriter, status int, code string) {
	writeJSON(writer, status, map[string]string{"error": code})
}
