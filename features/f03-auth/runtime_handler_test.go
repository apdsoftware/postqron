package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func TestRuntimeHandlerPasswordRegistrationDoesNotLeakVerificationToken(t *testing.T) {
	registrationStore := newRegistrationMemoryStore()
	registrationService, err := NewPasswordRegistrationService(PasswordRegistrationConfig{
		Store: registrationStore,
		Now:   func() time.Time { return testNow },
	})
	if err != nil {
		t.Fatal(err)
	}
	passwordService, err := NewPasswordService(
		&passwordStoreFixture{},
		func() time.Time { return testNow },
		time.Hour,
	)
	if err != nil {
		t.Fatal(err)
	}
	authService, _, providers := newTestService(t, nil)
	providers[ProviderGoogle].identity = ExternalIdentity{
		Subject:       "google-user",
		Email:         "user@example.test",
		EmailVerified: true,
	}
	handler, err := NewRuntimeHandler(
		authService,
		passwordService,
		registrationService,
		"https://postqron.com",
	)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/auth/password/register",
		strings.NewReader(`{
			"email":"user@example.test",
			"password":"correct horse battery staple",
			"confirmation":"correct horse battery staple",
			"contract_country":"IT",
			"consents":[
				{
					"document_key":"terms_it",
					"version":"1.0",
					"digest_sha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
					"action":"accepted",
					"purpose":"contract",
					"locale":"it-IT",
					"surface":"signup",
					"control_text_id":"signup-terms-v1"
				},
				{
					"document_key":"privacy_it",
					"version":"1.0",
					"digest_sha256":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
					"action":"acknowledged",
					"purpose":"privacy_notice",
					"locale":"it-IT",
					"surface":"signup",
					"control_text_id":"signup-privacy-v1"
				}
			]
		}`),
	)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", "https://postqron.com")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusAccepted {
		t.Fatalf("registration status = %d body=%s", response.Code, response.Body.String())
	}
	if strings.Contains(response.Body.String(), "token") {
		t.Fatalf("registration leaked token: %s", response.Body.String())
	}
	if len(response.Result().Cookies()) != 0 {
		t.Fatal("registration unexpectedly set a session cookie")
	}
	var payload map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode registration payload: %v", err)
	}
	if payload["verification_requested"] != true {
		t.Fatalf("unexpected registration payload: %v", payload)
	}
}

func TestRuntimeHandlerRejectsUnconfiguredProviderWithoutBlockingRuntime(t *testing.T) {
	registrationService, err := NewPasswordRegistrationService(PasswordRegistrationConfig{
		Store: newRegistrationMemoryStore(),
		Now:   func() time.Time { return testNow },
	})
	if err != nil {
		t.Fatal(err)
	}
	passwordService, err := NewPasswordService(
		&passwordStoreFixture{},
		func() time.Time { return testNow },
		time.Hour,
	)
	if err != nil {
		t.Fatal(err)
	}
	sealer, err := NewAESGCMSealer([]byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatal(err)
	}
	authService, err := NewService(Config{
		Store:  NewMemoryStore(),
		Sealer: sealer,
		Providers: map[Provider]ProviderAdapter{
			ProviderGoogle: &fakeProvider{config: ProviderConfig{
				ClientID:         "google-client",
				AuthorizationURL: "https://google.example.test/oauth/authorize",
				RedirectURL:      "https://app.example.test/api/v1/auth/callback",
				Scopes:           []string{"openid", "email", "profile"},
			}},
		},
		Now: func() time.Time { return testNow },
	})
	if err != nil {
		t.Fatal(err)
	}
	handler, err := NewRuntimeHandler(
		authService,
		passwordService,
		registrationService,
		"https://postqron.com",
	)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/auth/authorize",
		strings.NewReader(`{"provider":"facebook"}`),
	)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", "https://postqron.com")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("authorize status = %d body=%s", response.Code, response.Body.String())
	}
	var payload struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode authorize error: %v", err)
	}
	if payload.Error.Code != CodeUnsupportedProvider {
		t.Fatalf("unexpected authorize error: %+v", payload)
	}
}

func TestRuntimeHandlerReturnsDerivedCSRFFromPasswordSession(t *testing.T) {
	registrationService, err := NewPasswordRegistrationService(PasswordRegistrationConfig{
		Store: newRegistrationMemoryStore(),
		Now:   func() time.Time { return testNow },
	})
	if err != nil {
		t.Fatal(err)
	}
	passwordHash, err := HashPassword(
		"correct horse battery staple",
		DefaultPasswordParameters(),
	)
	if err != nil {
		t.Fatal(err)
	}
	passwordStore := &passwordStoreFixture{credential: PasswordCredential{
		AccountID:    "account-owner",
		PasswordHash: passwordHash,
	}}
	passwordService, err := NewPasswordService(
		passwordStore,
		func() time.Time { return testNow },
		time.Hour,
	)
	if err != nil {
		t.Fatal(err)
	}
	authService, _, _ := newTestService(t, nil)
	handler, err := NewRuntimeHandler(
		authService,
		passwordService,
		registrationService,
		"https://postqron.com",
	)
	if err != nil {
		t.Fatal(err)
	}
	token, _, err := passwordService.Login(
		context.Background(),
		"account-owner@example.test",
		"correct horse battery staple",
	)
	if err != nil {
		t.Fatalf("Login() error = %v", err)
	}
	passwordStore.context = PasswordSessionContext{
		AccountID:       passwordStore.session.AccountID,
		PasswordHash:    passwordHash,
		AuthenticatedAt: passwordStore.session.AuthenticatedAt,
	}

	request := httptest.NewRequest(http.MethodGet, "/api/v1/auth/csrf", nil)
	request.Header.Set("Origin", "https://postqron.com")
	request.AddCookie(&http.Cookie{
		Name:  SessionCookieName,
		Value: token,
	})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("csrf status = %d body=%s", response.Code, response.Body.String())
	}
	if response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("cache-control = %q", response.Header().Get("Cache-Control"))
	}
	if response.Header().Get("Access-Control-Allow-Origin") != "https://postqron.com" ||
		response.Header().Get("Access-Control-Allow-Credentials") != "true" {
		t.Fatalf("csrf CORS headers = %v", response.Header())
	}
	var payload map[string]string
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode csrf payload: %v", err)
	}
	if len(payload) != 1 || payload["csrf_token"] != csrfTokenValue(token) {
		t.Fatalf("unexpected csrf payload: %v", payload)
	}
	if strings.Contains(response.Body.String(), token) {
		t.Fatalf("csrf response leaked session token: %s", response.Body.String())
	}
}

func TestRuntimeHandlerCSRFRequiresAuthenticatedSessionAndSupportsPreflight(t *testing.T) {
	registrationService, err := NewPasswordRegistrationService(PasswordRegistrationConfig{
		Store: newRegistrationMemoryStore(),
		Now:   func() time.Time { return testNow },
	})
	if err != nil {
		t.Fatal(err)
	}
	passwordService, err := NewPasswordService(
		&passwordStoreFixture{},
		func() time.Time { return testNow },
		time.Hour,
	)
	if err != nil {
		t.Fatal(err)
	}
	authService, _, _ := newTestService(t, nil)
	handler, err := NewRuntimeHandler(
		authService,
		passwordService,
		registrationService,
		"https://postqron.com",
	)
	if err != nil {
		t.Fatal(err)
	}

	unauthenticated := httptest.NewRequest(http.MethodGet, "/api/v1/auth/csrf", nil)
	unauthenticated.Header.Set("Origin", "https://postqron.com")
	unauthenticatedResponse := httptest.NewRecorder()
	handler.ServeHTTP(unauthenticatedResponse, unauthenticated)
	if unauthenticatedResponse.Code != http.StatusUnauthorized ||
		unauthenticatedResponse.Header().Get("Cache-Control") != "no-store" ||
		!strings.Contains(unauthenticatedResponse.Body.String(), "AUTH_UNAUTHENTICATED") ||
		strings.Contains(unauthenticatedResponse.Body.String(), "csrf_token") {
		t.Fatalf(
			"unauthenticated csrf response = %d headers=%v body=%s",
			unauthenticatedResponse.Code,
			unauthenticatedResponse.Header(),
			unauthenticatedResponse.Body.String(),
		)
	}

	preflight := httptest.NewRequest(http.MethodOptions, "/api/v1/auth/csrf", nil)
	preflight.Header.Set("Origin", "https://postqron.com")
	preflightResponse := httptest.NewRecorder()
	handler.ServeHTTP(preflightResponse, preflight)
	if preflightResponse.Code != http.StatusNoContent {
		t.Fatalf("csrf preflight = %d %s", preflightResponse.Code, preflightResponse.Body.String())
	}
	if !strings.Contains(
		preflightResponse.Header().Get("Access-Control-Allow-Methods"),
		"GET",
	) {
		t.Fatalf(
			"csrf allow methods = %q",
			preflightResponse.Header().Get("Access-Control-Allow-Methods"),
		)
	}
}

func TestRuntimeProviderAdaptersUseLinkedInOAuthIssuer(t *testing.T) {
	t.Setenv(authLinkedInClientIDEnv, "linkedin-client")
	t.Setenv(authLinkedInClientSecretEnv, "linkedin-secret")
	t.Setenv(authLinkedInRedirectEnv, "https://app.example.test/api/v1/auth/callback")
	for _, envName := range []string{
		authGoogleClientIDEnv,
		authGoogleClientSecretEnv,
		authGoogleRedirectEnv,
		authAppleClientIDEnv,
		authAppleClientSecretEnv,
		authAppleRedirectEnv,
		authFacebookClientIDEnv,
		authFacebookClientSecretEnv,
		authFacebookRedirectEnv,
		authFacebookGraphEnv,
	} {
		_ = os.Unsetenv(envName)
	}

	adapters := runtimeProviderAdapters()
	adapter, ok := adapters[ProviderLinkedIn]
	if !ok {
		t.Fatal("linkedin adapter was not registered")
	}
	oidcAdapter, ok := adapter.(*OIDCAdapter)
	if !ok {
		t.Fatalf("linkedin adapter type = %T, want *OIDCAdapter", adapter)
	}
	if oidcAdapter.issuerURL != "https://www.linkedin.com/oauth" {
		t.Fatalf("linkedin issuer = %q", oidcAdapter.issuerURL)
	}
	if oidcAdapter.authorizationURL != "https://www.linkedin.com/oauth/v2/authorization" {
		t.Fatalf("linkedin authorization URL = %q", oidcAdapter.authorizationURL)
	}
}
