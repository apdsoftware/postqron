package accountprivacyruntime

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCredentialedCORSAllowsAccountRequestsAndPreflight(t *testing.T) {
	called := false
	handler := credentialedCORS(http.HandlerFunc(func(
		writer http.ResponseWriter,
		_ *http.Request,
	) {
		called = true
		writer.WriteHeader(http.StatusUnauthorized)
	}), map[string]struct{}{"https://postqron.com": {}})

	get := httptest.NewRequest(http.MethodGet, "/api/v1/account", nil)
	get.Header.Set("Origin", "https://postqron.com")
	getResponse := httptest.NewRecorder()
	handler.ServeHTTP(getResponse, get)

	if !called || getResponse.Code != http.StatusUnauthorized {
		t.Fatalf("GET result: called=%v status=%d", called, getResponse.Code)
	}
	assertCredentialedAccountCORS(t, getResponse.Header())

	called = false
	preflight := httptest.NewRequest(http.MethodOptions, "/api/v1/account", nil)
	preflight.Header.Set("Origin", "https://postqron.com")
	preflight.Header.Set("Access-Control-Request-Method", http.MethodGet)
	preflightResponse := httptest.NewRecorder()
	handler.ServeHTTP(preflightResponse, preflight)

	if called || preflightResponse.Code != http.StatusNoContent {
		t.Fatalf(
			"OPTIONS result: called=%v status=%d",
			called,
			preflightResponse.Code,
		)
	}
	assertCredentialedAccountCORS(t, preflightResponse.Header())
	if methods := preflightResponse.Header().Get("Access-Control-Allow-Methods"); !strings.Contains(methods, "GET") {
		t.Fatalf("Access-Control-Allow-Methods = %q", methods)
	}
}

func TestCredentialedCORSRejectsUnknownAccountOrigin(t *testing.T) {
	called := false
	handler := credentialedCORS(http.HandlerFunc(func(
		http.ResponseWriter,
		*http.Request,
	) {
		called = true
	}), map[string]struct{}{"https://postqron.com": {}})
	request := httptest.NewRequest(http.MethodGet, "/api/v1/account", nil)
	request.Header.Set("Origin", "https://evil.example")
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if called || response.Code != http.StatusForbidden {
		t.Fatalf("unknown origin: called=%v status=%d", called, response.Code)
	}
	if response.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatalf("unexpected CORS headers: %v", response.Header())
	}
}

func assertCredentialedAccountCORS(t *testing.T, header http.Header) {
	t.Helper()
	if header.Get("Access-Control-Allow-Origin") != "https://postqron.com" ||
		header.Get("Access-Control-Allow-Credentials") != "true" ||
		!strings.Contains(header.Get("Vary"), "Origin") {
		t.Fatalf("credentialed CORS headers = %v", header)
	}
}
