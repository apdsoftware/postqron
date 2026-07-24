package auth

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHTTPCallbackSetsOnlySecureSessionCookie(t *testing.T) {
	service, _, providers := newTestService(t, nil)
	providers[ProviderApple].identity = ExternalIdentity{
		Subject:       "apple-http",
		Email:         "http@example.test",
		EmailVerified: true,
	}
	_, state := beginRegistration(t, service, ProviderApple)
	handler, err := NewHandler(service)
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}
	request := httptest.NewRequest(
		http.MethodGet,
		"/api/v1/auth/callback?state="+state+"&code=apple-code",
		nil,
	)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("callback status = %d, body = %s", response.Code, response.Body.String())
	}
	cookies := response.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("callback cookies = %v", cookies)
	}
	cookie := cookies[0]
	if cookie.Name != SessionCookieName ||
		!cookie.Secure ||
		!cookie.HttpOnly ||
		cookie.SameSite != http.SameSiteLaxMode ||
		cookie.Path != "/" {
		t.Fatalf("insecure session cookie: %+v", cookie)
	}
	var payload map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode callback response: %v", err)
	}
	if _, exposed := payload["session_token"]; exposed {
		t.Fatal("callback exposed the raw session token in JSON")
	}
}

func TestHTTPRejectsUnknownInputAndReturnsRetryableProviderError(t *testing.T) {
	service, store, providers := newTestService(t, nil)
	handler, err := NewHandler(service)
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}
	unknownField := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/auth/authorize",
		strings.NewReader(`{"provider":"google","unexpected":true}`),
	)
	unknownField.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, unknownField)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("unknown-field status = %d", response.Code)
	}

	providers[ProviderGoogle].identity = ExternalIdentity{
		Subject:       "google-http-error",
		Email:         "error@example.test",
		EmailVerified: true,
	}
	providers[ProviderGoogle].exchangeErrs = []error{
		&ProviderError{Code: "upstream_timeout", Retryable: true},
	}
	_, state := beginRegistration(t, service, ProviderGoogle)
	callback := httptest.NewRequest(
		http.MethodGet,
		"/api/v1/auth/callback?state="+state+"&code=provider-code",
		nil,
	)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, callback)
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("provider-error status = %d, body = %s", response.Code, response.Body.String())
	}
	if len(response.Result().Cookies()) != 0 {
		t.Fatal("failed callback set a session cookie")
	}
	if len(store.Snapshot().Sessions) != 0 {
		t.Fatal("failed callback persisted a session")
	}
	var payload struct {
		Error struct {
			Code      string `json:"code"`
			Retryable bool   `json:"retryable"`
		} `json:"error"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode error response: %v", err)
	}
	if payload.Error.Code != CodeProviderUnavailable || !payload.Error.Retryable {
		t.Fatalf("unexpected error payload: %+v", payload)
	}
}
