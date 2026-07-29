package authruntime

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
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

func TestLoginRouteStillDelegatesWhenEmailDeliveryUnavailable(t *testing.T) {
	handler, err := newHandler(
		http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			if request.URL.Path != "/api/v1/auth/password/login" {
				t.Fatalf("path = %q", request.URL.Path)
			}
			writer.WriteHeader(http.StatusNoContent)
		}),
		&registrationStub{},
		nil,
		"https://app.example.test",
	)
	if err != nil {
		t.Fatal(err)
	}

	request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/password/login", nil)
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusNoContent {
		t.Fatalf("status = %d body=%s", response.Code, response.Body.String())
	}
}

func TestRegisterReturnsEmailDeliveryUnavailableBeforeMutation(t *testing.T) {
	registration := &registrationStub{}
	handler, err := newHandler(http.NotFoundHandler(), registration, nil, "")
	if err != nil {
		t.Fatal(err)
	}

	request := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/auth/password/register",
		strings.NewReader(`{"email":"user@example.test","password":"correct horse battery staple","confirmation":"correct horse battery staple","contract_country":"IT","consents":[]}`),
	)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	assertEmailDeliveryUnavailable(t, response)
	if registration.registerCalls != 0 {
		t.Fatalf("register calls = %d, want 0", registration.registerCalls)
	}
}

func TestResendReturnsEmailDeliveryUnavailableBeforeMutation(t *testing.T) {
	registration := &registrationStub{}
	handler, err := newHandler(http.NotFoundHandler(), registration, nil, "")
	if err != nil {
		t.Fatal(err)
	}

	request := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/auth/password/verify/resend",
		strings.NewReader(`{"email":"user@example.test"}`),
	)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	assertEmailDeliveryUnavailable(t, response)
	if registration.resendCalls != 0 {
		t.Fatalf("resend calls = %d, want 0", registration.resendCalls)
	}
}

func TestNewModuleDegradesWhenEmailRuntimeUnavailable(t *testing.T) {
	database := sql.OpenDB(stubConnector{})
	t.Cleanup(func() { _ = database.Close() })

	original := newEmailService
	newEmailService = func(*sql.DB, string, func() time.Time) (verificationMailer, error) {
		return nil, errors.New("mail runtime unavailable")
	}
	t.Cleanup(func() { newEmailService = original })

	module, err := NewModule(database, "app.example.test", time.Now)
	if err != nil {
		t.Fatalf("NewModule() error = %v", err)
	}

	handler, ok := module.Handler("Auth")
	if !ok || handler == nil {
		t.Fatalf("Handler(Auth) = %v, %v", handler, ok)
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

func assertEmailDeliveryUnavailable(t *testing.T, response *httptest.ResponseRecorder) {
	t.Helper()
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d body=%s", response.Code, response.Body.String())
	}
	var body struct {
		Error struct {
			Code      string `json:"code"`
			Message   string `json:"message"`
			Retryable bool   `json:"retryable"`
		} `json:"error"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal body: %v", err)
	}
	if body.Error.Code != "AUTH_EMAIL_DELIVERY_UNAVAILABLE" {
		t.Fatalf("error code = %q", body.Error.Code)
	}
	if body.Error.Message != "Email delivery is temporarily unavailable." {
		t.Fatalf("error message = %q", body.Error.Message)
	}
	if !body.Error.Retryable {
		t.Fatal("retryable = false, want true")
	}
}

type stubConnector struct{}

func (stubConnector) Connect(context.Context) (driver.Conn, error) {
	return stubConn{}, nil
}

func (stubConnector) Driver() driver.Driver {
	return stubDriver{}
}

type stubDriver struct{}

func (stubDriver) Open(string) (driver.Conn, error) {
	return stubConn{}, nil
}

type stubConn struct{}

func (stubConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("not implemented")
}

func (stubConn) Close() error {
	return nil
}

func (stubConn) Begin() (driver.Tx, error) {
	return nil, errors.New("not implemented")
}
