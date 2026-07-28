package auth

import (
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
