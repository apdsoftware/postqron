package auth

import (
	"errors"
	"net/http"
	"net/url"
	"slices"
	"strings"
)

type PasswordHandler struct {
	service        *PasswordService
	allowedOrigins []string
}

func NewPasswordHandler(
	service *PasswordService,
	allowedOrigins ...string,
) (http.Handler, error) {
	if service == nil {
		return nil, errors.New("password service is required")
	}
	origins, err := normalizePasswordOrigins(allowedOrigins)
	if err != nil {
		return nil, err
	}
	handler := &PasswordHandler{
		service:        service,
		allowedOrigins: origins,
	}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/auth/password/login", handler.login)
	mux.HandleFunc("OPTIONS /api/v1/auth/password/login", handler.preflight)
	return handler.cors(mux), nil
}

func (handler *PasswordHandler) login(
	writer http.ResponseWriter,
	request *http.Request,
) {
	var input struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := decodeJSON(writer, request, &input); err != nil {
		writePasswordError(writer, http.StatusBadRequest)
		return
	}
	token, expiry, err := handler.service.Login(
		request.Context(),
		input.Email,
		input.Password,
	)
	if err != nil {
		if errors.Is(err, ErrInvalidCredentials) {
			writePasswordError(writer, http.StatusUnauthorized)
			return
		}
		writeJSON(writer, http.StatusServiceUnavailable, map[string]any{
			"error": map[string]any{
				"code":      "AUTH_PASSWORD_UNAVAILABLE",
				"message":   "Sign-in is temporarily unavailable.",
				"retryable": true,
			},
		})
		return
	}
	setSessionCookie(writer, token, expiry)
	writeJSON(writer, http.StatusOK, map[string]any{"authenticated": true})
}

func writePasswordError(writer http.ResponseWriter, status int) {
	writeJSON(writer, status, map[string]any{
		"error": map[string]any{
			"code":      "AUTH_INVALID_CREDENTIALS",
			"message":   "The email or password is invalid.",
			"retryable": false,
		},
	})
}

func (handler *PasswordHandler) preflight(
	writer http.ResponseWriter,
	_ *http.Request,
) {
	writer.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
	writer.Header().Set("Access-Control-Allow-Headers", "Content-Type")
	writer.Header().Set("Access-Control-Max-Age", "600")
	writer.WriteHeader(http.StatusNoContent)
}

func (handler *PasswordHandler) cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		origin := request.Header.Get("Origin")
		if origin == "" {
			next.ServeHTTP(writer, request)
			return
		}
		normalized, err := normalizePasswordOrigin(origin)
		if err != nil || !slices.Contains(handler.allowedOrigins, normalized) {
			writeJSON(writer, http.StatusForbidden, map[string]any{
				"error": map[string]any{
					"code":      "AUTH_ORIGIN_FORBIDDEN",
					"message":   "The request origin is not allowed.",
					"retryable": false,
				},
			})
			return
		}
		writer.Header().Set("Access-Control-Allow-Origin", normalized)
		writer.Header().Set("Access-Control-Allow-Credentials", "true")
		writer.Header().Add("Vary", "Origin")
		next.ServeHTTP(writer, request)
	})
}

func normalizePasswordOrigins(values []string) ([]string, error) {
	seen := make(map[string]struct{})
	var normalized []string
	for _, raw := range values {
		for _, candidate := range strings.Split(raw, ",") {
			if strings.TrimSpace(candidate) == "" {
				continue
			}
			origin, err := normalizePasswordOrigin(candidate)
			if err != nil {
				return nil, err
			}
			if _, exists := seen[origin]; exists {
				continue
			}
			seen[origin] = struct{}{}
			normalized = append(normalized, origin)
		}
	}
	slices.Sort(normalized)
	return normalized, nil
}

func normalizePasswordOrigin(value string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil ||
		(parsed.Scheme != "https" && parsed.Scheme != "http") ||
		parsed.Host == "" ||
		parsed.User != nil ||
		parsed.RawQuery != "" ||
		parsed.Fragment != "" ||
		(parsed.Path != "" && parsed.Path != "/") {
		return "", errors.New("auth allowed origin must be an HTTP(S) origin")
	}
	return parsed.Scheme + "://" + parsed.Host, nil
}
