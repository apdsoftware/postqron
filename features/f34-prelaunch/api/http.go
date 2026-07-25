package prelaunch

import (
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
)

type HTTPHandler struct {
	service *Service
	mode    Mode
	origins OriginPolicy
}

func NewHTTPHandler(
	service *Service,
	mode Mode,
	origins OriginPolicy,
) (*HTTPHandler, error) {
	if service == nil || origins == nil {
		return nil, errors.New("prelaunch HTTP dependencies are required")
	}
	return &HTTPHandler{service: service, mode: mode, origins: origins}, nil
}

func (handler *HTTPHandler) AccessRequests(
	writer http.ResponseWriter,
	request *http.Request,
) {
	if !handler.applyOrigin(writer, request) {
		writeError(writer, http.StatusForbidden, "origin_not_allowed")
		return
	}
	if request.Method == http.MethodOptions {
		writer.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
		writer.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		writer.Header().Set("Access-Control-Max-Age", "600")
		writer.WriteHeader(http.StatusNoContent)
		return
	}
	if request.Method != http.MethodPost {
		writeError(writer, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}

	request.Body = http.MaxBytesReader(writer, request.Body, 16<<10)
	input, formRequest, returnPath, err := decodeAccessRequest(request)
	if err != nil {
		if formRequest {
			handler.redirectForm(writer, request, returnPath, "invalid")
			return
		}
		writeError(writer, http.StatusBadRequest, "invalid_request")
		return
	}
	_, err = handler.service.Submit(
		request.Context(),
		input,
		clientIdentity(request),
	)
	switch {
	case errors.Is(err, ErrRateLimited):
		if formRequest {
			handler.redirectForm(writer, request, returnPath, "rate")
			return
		}
		writer.Header().Set("Retry-After", "600")
		writeError(writer, http.StatusTooManyRequests, "rate_limited")
	case errors.Is(err, ErrConsentRequired),
		errors.Is(err, ErrInvalidEmail),
		errors.Is(err, ErrInvalidPolicy),
		errors.Is(err, ErrMarketingConsent):
		if formRequest {
			handler.redirectForm(writer, request, returnPath, "invalid")
			return
		}
		writeError(writer, http.StatusUnprocessableEntity, "invalid_request")
	case err != nil:
		if formRequest {
			handler.redirectForm(writer, request, returnPath, "error")
			return
		}
		writeError(writer, http.StatusServiceUnavailable, "temporarily_unavailable")
	default:
		if formRequest {
			handler.redirectForm(writer, request, returnPath, "success")
			return
		}
		writeJSON(writer, http.StatusAccepted, map[string]string{
			"status": "accepted",
		})
	}
}

func decodeAccessRequest(
	request *http.Request,
) (AccessRequest, bool, string, error) {
	contentType, _, _ := mime.ParseMediaType(
		request.Header.Get("Content-Type"),
	)
	if contentType == "application/x-www-form-urlencoded" {
		if err := request.ParseForm(); err != nil {
			return AccessRequest{}, true, "", err
		}
		return AccessRequest{
				Email:                request.PostForm.Get("email"),
				Locale:               request.PostForm.Get("locale"),
				AccessConsent:        request.PostForm.Get("access_consent") == "true",
				MarketingConsent:     request.PostForm.Get("marketing_consent") == "true",
				ConsentPolicyVersion: request.PostForm.Get("consent_policy_version"),
			},
			true,
			request.PostForm.Get("return_path"),
			nil
	}

	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	var input AccessRequest
	if err := decoder.Decode(&input); err != nil {
		return AccessRequest{}, false, "", err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return AccessRequest{}, false, "", errors.New("multiple JSON values")
	}
	return input, false, "", nil
}

func (handler *HTTPHandler) redirectForm(
	writer http.ResponseWriter,
	request *http.Request,
	returnPath string,
	result string,
) {
	if !validReturnPath(returnPath) {
		returnPath = "/prelaunch/access"
	}
	origin, ok := normalizeOrigin(request.Header.Get("Origin"))
	if !ok || !handler.origins.Allows(origin) {
		writeError(writer, http.StatusForbidden, "origin_not_allowed")
		return
	}
	location := origin + returnPath + "?result=" + url.QueryEscape(result)
	http.Redirect(writer, request, location, http.StatusSeeOther)
}

func validReturnPath(value string) bool {
	parsed, err := url.ParseRequestURI(value)
	if err != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return false
	}
	path := parsed.Path
	for _, locale := range []string{"en", "it", "es", "fr", "de"} {
		path = strings.TrimPrefix(path, "/"+locale)
	}
	return path == "/prelaunch/access"
}

func (handler *HTTPHandler) Status(
	writer http.ResponseWriter,
	request *http.Request,
) {
	if request.Method != http.MethodGet {
		writeError(writer, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{
		"status":         "ok",
		"prelaunch_mode": handler.mode.Enabled,
		"configuration":  handler.mode.Source,
	})
}

func (handler *HTTPHandler) applyOrigin(
	writer http.ResponseWriter,
	request *http.Request,
) bool {
	origin := request.Header.Get("Origin")
	if origin == "" {
		return true
	}
	if !handler.origins.Allows(origin) {
		return false
	}
	writer.Header().Set("Access-Control-Allow-Origin", origin)
	writer.Header().Set("Vary", "Origin")
	return true
}

func clientIdentity(request *http.Request) string {
	if candidate := strings.TrimSpace(
		request.Header.Get("CF-Connecting-IP"),
	); net.ParseIP(candidate) != nil {
		return candidate
	}
	host, _, err := net.SplitHostPort(request.RemoteAddr)
	if err == nil && net.ParseIP(host) != nil {
		return host
	}
	if net.ParseIP(request.RemoteAddr) != nil {
		return request.RemoteAddr
	}
	return "unknown"
}

type OriginPolicy interface {
	Allows(string) bool
}

type StaticOriginPolicy struct {
	allowed map[string]struct{}
}

func NewOriginPolicy(rawValue, environment string) *StaticOriginPolicy {
	allowed := make(map[string]struct{})
	for _, value := range strings.Split(rawValue, ",") {
		if origin, ok := normalizeOrigin(value); ok {
			allowed[origin] = struct{}{}
		}
	}
	if environment != "production" {
		for _, origin := range []string{
			"http://localhost:3000",
			"http://127.0.0.1:3000",
		} {
			allowed[origin] = struct{}{}
		}
	}
	return &StaticOriginPolicy{allowed: allowed}
}

func (policy *StaticOriginPolicy) Allows(value string) bool {
	origin, ok := normalizeOrigin(value)
	if !ok {
		return false
	}
	_, exists := policy.allowed[origin]
	return exists
}

func normalizeOrigin(value string) (string, bool) {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") ||
		parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" ||
		parsed.Fragment != "" || (parsed.Path != "" && parsed.Path != "/") {
		return "", false
	}
	return parsed.Scheme + "://" + parsed.Host, true
}

func writeJSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.Header().Set("Cache-Control", "no-store")
	writer.Header().Set("X-Content-Type-Options", "nosniff")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}

func writeError(writer http.ResponseWriter, status int, code string) {
	writeJSON(writer, status, map[string]string{"error": code})
}

func environment() string {
	return os.Getenv("NODE_ENV")
}
