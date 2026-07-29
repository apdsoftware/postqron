package authruntime

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	auth "github.com/apdsoftware/postqron/features/f03-auth"
)

type registrationStub struct {
	registerCalls int
	resendCalls   int
	result        auth.PasswordRegistrationResult
	delivery      *auth.VerificationDelivery
}

func (stub *registrationStub) Register(
	context.Context,
	string,
	string,
	string,
	string,
	[]auth.ConsentReceipt,
) (auth.PasswordRegistrationResult, error) {
	stub.registerCalls++
	return stub.result, nil
}

func (stub *registrationStub) ResendVerification(
	context.Context,
	string,
) (*auth.VerificationDelivery, error) {
	stub.resendCalls++
	return stub.delivery, nil
}

type mailerStub struct {
	enqueued int
}

func (stub *mailerStub) EnqueueVerification(
	context.Context,
	*auth.VerificationDelivery,
	string,
) error {
	stub.enqueued++
	return nil
}

func TestCustomRegisterRejectsDisallowedOriginBeforeSideEffects(t *testing.T) {
	registration := &registrationStub{
		result: auth.PasswordRegistrationResult{
			Created: true,
			Delivery: &auth.VerificationDelivery{
				AccountID: "account-1",
				Email:     "user@example.test",
				Token:     "secret-token",
				ExpiresAt: time.Unix(1_800_000_000, 0).UTC(),
			},
		},
	}
	mailer := &mailerStub{}
	handler, err := newHandler(http.NotFoundHandler(), registration, mailer, "https://app.example.test")
	if err != nil {
		t.Fatal(err)
	}

	request := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/auth/password/register",
		strings.NewReader(`{"email":"user@example.test","password":"correct horse battery staple","confirmation":"correct horse battery staple","contract_country":"IT","consents":[]}`),
	)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", "https://evil.example.test")
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusForbidden {
		t.Fatalf("status = %d body=%s", response.Code, response.Body.String())
	}
	if registration.registerCalls != 0 {
		t.Fatalf("register calls = %d, want 0", registration.registerCalls)
	}
	if mailer.enqueued != 0 {
		t.Fatalf("mailer enqueued = %d, want 0", mailer.enqueued)
	}
}

func TestCustomResendRejectsMalformedOriginBeforeSideEffects(t *testing.T) {
	registration := &registrationStub{
		delivery: &auth.VerificationDelivery{
			AccountID: "account-1",
			Email:     "user@example.test",
			Token:     "secret-token",
			ExpiresAt: time.Unix(1_800_000_000, 0).UTC(),
		},
	}
	mailer := &mailerStub{}
	handler, err := newHandler(http.NotFoundHandler(), registration, mailer, "https://app.example.test")
	if err != nil {
		t.Fatal(err)
	}

	request := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/auth/password/verify/resend",
		strings.NewReader(`{"email":"user@example.test"}`),
	)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", "javascript:alert(1)")
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusForbidden {
		t.Fatalf("status = %d body=%s", response.Code, response.Body.String())
	}
	if registration.resendCalls != 0 {
		t.Fatalf("resend calls = %d, want 0", registration.resendCalls)
	}
	if mailer.enqueued != 0 {
		t.Fatalf("mailer enqueued = %d, want 0", mailer.enqueued)
	}
}

func TestDelegatedRoutesBypassCustomOriginGate(t *testing.T) {
	called := false
	handler, err := newHandler(
		http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			called = true
			writer.WriteHeader(http.StatusNoContent)
		}),
		&registrationStub{},
		&mailerStub{},
		"https://app.example.test",
	)
	if err != nil {
		t.Fatal(err)
	}

	request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/authorize", nil)
	request.Header.Set("Origin", "https://evil.example.test")
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if !called {
		t.Fatal("delegate was not called")
	}
	if response.Code != http.StatusNoContent {
		t.Fatalf("status = %d", response.Code)
	}
}

func TestInvalidOAuthRedirectURLIsIgnoredWithoutBlockingPasswordAuth(t *testing.T) {
	t.Setenv("POSTQRON_AUTH_GOOGLE_CLIENT_ID", "google-client")
	t.Setenv("POSTQRON_AUTH_GOOGLE_CLIENT_SECRET", "google-secret")
	t.Setenv("POSTQRON_AUTH_GOOGLE_REDIRECT_URL", "http://app.example.test/api/v1/auth/callback")
	for _, name := range []string{
		"POSTQRON_AUTH_APPLE_CLIENT_ID",
		"POSTQRON_AUTH_APPLE_CLIENT_SECRET",
		"POSTQRON_AUTH_APPLE_REDIRECT_URL",
		"POSTQRON_AUTH_FACEBOOK_CLIENT_ID",
		"POSTQRON_AUTH_FACEBOOK_CLIENT_SECRET",
		"POSTQRON_AUTH_FACEBOOK_REDIRECT_URL",
		"POSTQRON_AUTH_FACEBOOK_GRAPH_VERSION",
		"POSTQRON_AUTH_LINKEDIN_CLIENT_ID",
		"POSTQRON_AUTH_LINKEDIN_CLIENT_SECRET",
		"POSTQRON_AUTH_LINKEDIN_REDIRECT_URL",
	} {
		_ = os.Unsetenv(name)
	}
	adapters := RuntimeProviderAdapters()
	if len(adapters) != 0 {
		t.Fatalf("adapters = %#v, want none for invalid non-HTTPS redirect", adapters)
	}
}
