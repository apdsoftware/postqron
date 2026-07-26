package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type passwordStoreFixture struct {
	credential PasswordCredential
	failures   int
	session    PasswordSession
}

func (store *passwordStoreFixture) CredentialByEmail(
	context.Context,
	string,
) (PasswordCredential, bool, error) {
	return store.credential, store.credential.AccountID != "", nil
}

func (store *passwordStoreFixture) RecordPasswordFailure(
	context.Context,
	string,
	time.Time,
) error {
	store.failures++
	return nil
}

func (store *passwordStoreFixture) CompletePasswordLogin(
	_ context.Context,
	session PasswordSession,
	_ string,
	_ time.Time,
) error {
	store.session = session
	return nil
}

func TestArgon2idPasswordHashAndLoginSession(t *testing.T) {
	hash, err := HashPassword(
		"correct horse battery staple",
		DefaultPasswordParameters(),
	)
	if err != nil {
		t.Fatalf("HashPassword() error = %v", err)
	}
	if !strings.HasPrefix(hash, "$argon2id$v=19$m=65536,t=3,p=1$") {
		t.Fatalf("unexpected password hash metadata: %q", hash)
	}
	valid, err := VerifyPassword(hash, "correct horse battery staple")
	if err != nil || !valid {
		t.Fatalf("VerifyPassword() = %v, %v", valid, err)
	}
	valid, err = VerifyPassword(hash, "incorrect password")
	if err != nil || valid {
		t.Fatalf("wrong VerifyPassword() = %v, %v", valid, err)
	}

	store := &passwordStoreFixture{credential: PasswordCredential{
		AccountID:    "account-admin",
		PasswordHash: hash,
	}}
	service, err := NewPasswordService(
		store,
		func() time.Time { return testNow },
		time.Hour,
	)
	if err != nil {
		t.Fatalf("NewPasswordService() error = %v", err)
	}
	token, expiry, err := service.Login(
		context.Background(),
		" ADMIN@example.test ",
		"correct horse battery staple",
	)
	if err != nil || token == "" {
		t.Fatalf("Login() = %q, %v, %v", token, expiry, err)
	}
	if store.session.AccountID != "account-admin" ||
		store.session.TokenHash == token ||
		store.session.ExpiresAt != testNow.Add(time.Hour) {
		t.Fatalf("stored session = %+v", store.session)
	}
}

func TestPasswordLoginIsGenericAndCORSIsRestricted(t *testing.T) {
	hash, err := HashPassword(
		"correct horse battery staple",
		DefaultPasswordParameters(),
	)
	if err != nil {
		t.Fatal(err)
	}
	store := &passwordStoreFixture{credential: PasswordCredential{
		AccountID:    "account-admin",
		PasswordHash: hash,
	}}
	service, err := NewPasswordService(
		store,
		func() time.Time { return testNow },
		time.Hour,
	)
	if err != nil {
		t.Fatal(err)
	}
	handler, err := NewPasswordHandler(service, "https://postqron.com")
	if err != nil {
		t.Fatal(err)
	}

	request := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/auth/password/login",
		strings.NewReader(`{"email":"admin@example.test","password":"wrong password value"}`),
	)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", "https://postqron.com")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized ||
		!strings.Contains(response.Body.String(), "AUTH_INVALID_CREDENTIALS") ||
		strings.Contains(response.Body.String(), "admin@example.test") {
		t.Fatalf("invalid login = %d %s", response.Code, response.Body.String())
	}
	if response.Header().Get("Access-Control-Allow-Origin") != "https://postqron.com" ||
		response.Header().Get("Access-Control-Allow-Credentials") != "true" {
		t.Fatalf("allowed CORS headers = %v", response.Header())
	}
	if store.failures != 1 {
		t.Fatalf("password failures = %d", store.failures)
	}

	forbidden := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/auth/password/login",
		strings.NewReader(`{"email":"admin@example.test","password":"correct horse battery staple"}`),
	)
	forbidden.Header.Set("Origin", "https://evil.example")
	rejected := httptest.NewRecorder()
	handler.ServeHTTP(rejected, forbidden)
	if rejected.Code != http.StatusForbidden {
		t.Fatalf("forbidden origin = %d %s", rejected.Code, rejected.Body.String())
	}
}
