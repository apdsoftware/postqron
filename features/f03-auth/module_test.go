package auth

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestRuntimeAuthServiceDisablesOAuthWithoutSealerAndKeepsPasswordRuntime(t *testing.T) {
	t.Setenv(authEncryptionKeyEnv, "")
	t.Setenv(authGoogleClientIDEnv, "google-client")
	t.Setenv(authGoogleClientSecretEnv, "google-secret")
	t.Setenv(
		authGoogleRedirectEnv,
		"https://app.example.test/api/v1/auth/callback",
	)

	authService, err := newRuntimeAuthService(
		NewMemoryStore(),
		func() time.Time { return testNow },
	)
	if err != nil {
		t.Fatalf("newRuntimeAuthService() error = %v", err)
	}
	if authService.isAvailableProvider(ProviderGoogle) {
		t.Fatal("google provider remained available without a runtime sealer")
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
	registrationService, err := NewPasswordRegistrationService(PasswordRegistrationConfig{
		Store: newRegistrationMemoryStore(),
		Now:   func() time.Time { return testNow },
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
		t.Fatalf("NewRuntimeHandler() error = %v", err)
	}

	login := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/auth/password/login",
		strings.NewReader(`{"email":"owner@example.test","password":"correct horse battery staple"}`),
	)
	login.Header.Set("Content-Type", "application/json")
	login.Header.Set("Origin", "https://postqron.com")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, login)
	if response.Code != http.StatusOK {
		t.Fatalf("password login status = %d body=%s", response.Code, response.Body.String())
	}

	authorize := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/auth/authorize",
		strings.NewReader(`{"provider":"google"}`),
	)
	authorize.Header.Set("Content-Type", "application/json")
	authorize.Header.Set("Origin", "https://postqron.com")
	authorizeResponse := httptest.NewRecorder()
	handler.ServeHTTP(authorizeResponse, authorize)
	if authorizeResponse.Code != http.StatusBadRequest ||
		!strings.Contains(authorizeResponse.Body.String(), CodeUnsupportedProvider) {
		t.Fatalf(
			"authorize without sealer = %d %s",
			authorizeResponse.Code,
			authorizeResponse.Body.String(),
		)
	}
}

func TestRuntimeAuthServiceIgnoresInvalidProviderConfigAndKeepsValidProvider(t *testing.T) {
	t.Setenv(
		authEncryptionKeyEnv,
		base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef")),
	)
	t.Setenv(authGoogleClientIDEnv, "google-client")
	t.Setenv(authGoogleClientSecretEnv, "google-secret")
	t.Setenv(
		authGoogleRedirectEnv,
		"https://app.example.test/api/v1/auth/callback",
	)
	t.Setenv(authLinkedInClientIDEnv, "linkedin-client")
	t.Setenv(authLinkedInClientSecretEnv, "linkedin-secret")
	t.Setenv(authLinkedInRedirectEnv, "http://app.example.test/api/v1/auth/callback")

	authService, err := newRuntimeAuthService(
		NewMemoryStore(),
		func() time.Time { return testNow },
	)
	if err != nil {
		t.Fatalf("newRuntimeAuthService() error = %v", err)
	}
	if !authService.isAvailableProvider(ProviderGoogle) {
		t.Fatal("valid google provider was filtered out")
	}
	if authService.isAvailableProvider(ProviderLinkedIn) {
		t.Fatal("invalid linkedin provider remained available")
	}
}

func TestPasswordServiceValidateSession(t *testing.T) {
	passwordHash, err := HashPassword(
		"correct horse battery staple",
		DefaultPasswordParameters(),
	)
	if err != nil {
		t.Fatal(err)
	}
	store := &passwordStoreFixture{
		context: PasswordSessionContext{
			AccountID:       "account-owner",
			PasswordHash:    passwordHash,
			AuthenticatedAt: testNow,
		},
	}
	service, err := NewPasswordService(
		store,
		func() time.Time { return testNow },
		time.Hour,
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.ValidateSession(context.Background(), "opaque-current-session"); err != nil {
		t.Fatalf("ValidateSession() error = %v", err)
	}
	store.context = PasswordSessionContext{}
	if err := service.ValidateSession(context.Background(), "opaque-current-session"); err != ErrPasswordUnauthenticated {
		t.Fatalf("ValidateSession() = %v", err)
	}
}
