package adminconsole

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
)

const (
	sessionCookieName          = "__Host-postqron_session"
	maxAdminRequestBytes       = 8 << 10
	AdminAllowedOriginsEnvName = "POSTQRON_ADMIN_ALLOWED_ORIGINS"
)

type HTTPHandler struct {
	service        *Service
	authenticator  Authenticator
	allowedOrigins map[string]struct{}
	handler        http.Handler
}

func NewHandler(
	service *Service,
	authenticator Authenticator,
	allowedOrigins ...string,
) (http.Handler, error) {
	if service == nil || authenticator == nil {
		return nil, errors.New("admin HTTP dependencies are required")
	}
	origins, err := newAdminOriginPolicy(allowedOrigins)
	if err != nil {
		return nil, err
	}
	admin := &HTTPHandler{
		service:        service,
		authenticator:  authenticator,
		allowedOrigins: origins,
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
	admin.handler = admin.cors(admin.authorize(mux))
	return admin, nil
}

func (handler *HTTPHandler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	handler.handler.ServeHTTP(writer, request)
}

func ParseAllowedOrigins(raw string) ([]string, error) {
	seen := make(map[string]struct{})
	var origins []string
	for _, candidate := range strings.Split(raw, ",") {
		if strings.TrimSpace(candidate) == "" {
			continue
		}
		normalized, err := normalizeAdminOrigin(candidate)
		if err != nil {
			return nil, fmt.Errorf("invalid admin allowed origin: %w", err)
		}
		if _, duplicate := seen[normalized]; duplicate {
			continue
		}
		seen[normalized] = struct{}{}
		origins = append(origins, normalized)
	}
	if len(origins) == 0 {
		return nil, errors.New("admin allowed origins must not be empty")
	}
	sort.Strings(origins)
	return origins, nil
}

func AllowedOriginsFromEnvironment(
	lookup func(string) (string, bool),
) ([]string, error) {
	if lookup == nil {
		return nil, errors.New("environment lookup is required")
	}
	raw, exists := lookup(AdminAllowedOriginsEnvName)
	if !exists {
		return nil, fmt.Errorf("%s is required", AdminAllowedOriginsEnvName)
	}
	return ParseAllowedOrigins(raw)
}

func newAdminOriginPolicy(origins []string) (map[string]struct{}, error) {
	normalized, err := ParseAllowedOrigins(strings.Join(origins, ","))
	if err != nil {
		return nil, err
	}
	policy := make(map[string]struct{}, len(normalized))
	for _, origin := range normalized {
		policy[origin] = struct{}{}
	}
	return policy, nil
}

func normalizeAdminOrigin(value string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil {
		return "", errors.New("origin is invalid")
	}
	scheme := strings.ToLower(parsed.Scheme)
	if (scheme != "http" && scheme != "https") ||
		parsed.Host == "" ||
		parsed.Hostname() == "" ||
		parsed.User != nil ||
		strings.Contains(parsed.Host, "*") ||
		strings.HasSuffix(parsed.Host, ":") ||
		parsed.RawQuery != "" ||
		parsed.Fragment != "" ||
		(parsed.Path != "" && parsed.Path != "/") {
		return "", errors.New("origin is invalid")
	}
	host := strings.ToLower(parsed.Host)
	switch {
	case scheme == "https" && strings.HasSuffix(host, ":443"):
		host = strings.TrimSuffix(host, ":443")
	case scheme == "http" && strings.HasSuffix(host, ":80"):
		host = strings.TrimSuffix(host, ":80")
	}
	return scheme + "://" + host, nil
}

func (handler *HTTPHandler) cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		rawOrigin := request.Header.Get("Origin")
		if rawOrigin == "" {
			next.ServeHTTP(writer, request)
			return
		}
		origin, err := normalizeAdminOrigin(rawOrigin)
		if err != nil {
			writeError(writer, http.StatusForbidden, "ADMIN_ORIGIN_FORBIDDEN")
			return
		}
		if _, allowed := handler.allowedOrigins[origin]; !allowed {
			writeError(writer, http.StatusForbidden, "ADMIN_ORIGIN_FORBIDDEN")
			return
		}
		writer.Header().Set("Access-Control-Allow-Origin", origin)
		writer.Header().Set("Access-Control-Allow-Credentials", "true")
		writer.Header().Add("Vary", "Origin")
		if request.Method == http.MethodOptions {
			writer.Header().Set("Access-Control-Allow-Methods", "GET, PUT, DELETE")
			writer.Header().Set(
				"Access-Control-Allow-Headers",
				"Content-Type, X-CSRF-Token, Idempotency-Key",
			)
			writer.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(writer, request)
	})
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
