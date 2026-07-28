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
	mux.HandleFunc("POST /api/v1/auth/password/change", handler.changePassword)
	mux.HandleFunc("OPTIONS /api/v1/auth/password/change", handler.preflight)
	mux.HandleFunc("POST /api/v1/auth/logout", handler.logout)
	mux.HandleFunc("OPTIONS /api/v1/auth/logout", handler.preflight)
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
	setAuthCookies(writer, token, expiry)
	writeJSON(writer, http.StatusOK, map[string]any{"authenticated": true})
}

func (handler *PasswordHandler) changePassword(
	writer http.ResponseWriter,
	request *http.Request,
) {
	sessionToken, err := sessionCookie(request)
	if err != nil {
		clearAuthCookies(writer)
		writePasswordOperationError(writer, ErrPasswordUnauthenticated)
		return
	}
	var input struct {
		CurrentPassword string `json:"current_password"`
		NewPassword     string `json:"new_password"`
		Confirmation    string `json:"confirmation"`
	}
	if err := decodeJSON(writer, request, &input); err != nil {
		writePasswordOperationError(writer, ErrPasswordPolicy)
		return
	}
	newToken, expiry, err := handler.service.ChangePassword(
		request.Context(),
		sessionToken,
		request.Header.Get("X-CSRF-Token"),
		input.CurrentPassword,
		input.NewPassword,
		input.Confirmation,
	)
	if err != nil {
		if errors.Is(err, ErrPasswordUnauthenticated) {
			clearAuthCookies(writer)
		}
		writePasswordOperationError(writer, err)
		return
	}
	setAuthCookies(writer, newToken, expiry)
	writeJSON(writer, http.StatusOK, map[string]any{"changed": true})
}

func (handler *PasswordHandler) logout(
	writer http.ResponseWriter,
	request *http.Request,
) {
	sessionToken, err := sessionCookie(request)
	if err != nil {
		clearAuthCookies(writer)
		writer.WriteHeader(http.StatusNoContent)
		return
	}
	if err := handler.service.Logout(
		request.Context(),
		sessionToken,
		request.Header.Get("X-CSRF-Token"),
	); err != nil {
		writePasswordOperationError(writer, err)
		return
	}
	clearAuthCookies(writer)
	writer.WriteHeader(http.StatusNoContent)
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

func writePasswordOperationError(writer http.ResponseWriter, err error) {
	status := http.StatusServiceUnavailable
	code := "AUTH_PASSWORD_UNAVAILABLE"
	message := "The operation is temporarily unavailable."
	retryable := true
	switch {
	case errors.Is(err, ErrPasswordUnauthenticated):
		status = http.StatusUnauthorized
		code = "AUTH_UNAUTHENTICATED"
		message = "Your session expired. Sign in again."
		retryable = false
	case errors.Is(err, ErrPasswordCSRFInvalid):
		status = http.StatusForbidden
		code = "AUTH_CSRF_INVALID"
		message = "The security token is invalid. Reload and try again."
		retryable = false
	case errors.Is(err, ErrPasswordReauthRequired):
		status = http.StatusUnauthorized
		code = "AUTH_REAUTHENTICATION_REQUIRED"
		message = "Sign in again before changing your password."
		retryable = false
	case errors.Is(err, ErrCurrentPasswordInvalid):
		status = http.StatusBadRequest
		code = "AUTH_CURRENT_PASSWORD_INVALID"
		message = "The current password is invalid."
		retryable = false
	case errors.Is(err, ErrPasswordConfirmation):
		status = http.StatusBadRequest
		code = "AUTH_PASSWORD_CONFIRMATION_MISMATCH"
		message = "The password confirmation does not match."
		retryable = false
	case errors.Is(err, ErrPasswordPolicy):
		status = http.StatusBadRequest
		code = "AUTH_PASSWORD_WEAK"
		message = "Use a different password containing at least 12 characters."
		retryable = false
	case errors.Is(err, ErrPasswordChangeRateLimited):
		status = http.StatusTooManyRequests
		code = "AUTH_PASSWORD_CHANGE_RATE_LIMITED"
		message = "Too many attempts. Wait before trying again."
		retryable = true
		writer.Header().Set("Retry-After", "300")
	case errors.Is(err, ErrPasswordChangeConflict):
		status = http.StatusConflict
		code = "AUTH_PASSWORD_CHANGE_CONFLICT"
		message = "The password or session changed. Sign in again."
		retryable = false
	}
	writeJSON(writer, status, map[string]any{
		"error": map[string]any{
			"code":      code,
			"message":   message,
			"retryable": retryable,
		},
	})
}

func (handler *PasswordHandler) preflight(
	writer http.ResponseWriter,
	_ *http.Request,
) {
	writer.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
	writer.Header().Set(
		"Access-Control-Allow-Headers",
		"Content-Type, X-CSRF-Token",
	)
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
