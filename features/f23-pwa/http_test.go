package pwa

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

type testAuthenticator struct {
	accountID string
	err       error
}

func (authenticator testAuthenticator) AccountID(*http.Request) (string, error) {
	return authenticator.accountID, authenticator.err
}

func TestHTTPOptInAndRevoke(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	service := newTestService(
		t,
		NewMemoryRepository(),
		&fakeGateway{},
		func() time.Time { return now },
	)
	handler, err := NewHTTPHandler(
		service,
		testAuthenticator{accountID: "account-1"},
	)
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	handler.Register(mux)
	input := validInput("account-1", "https://push.example.test/device")
	body, _ := json.Marshal(map[string]any{
		"endpoint":        input.Endpoint,
		"expiration_time": nil,
		"keys": map[string]string{
			"p256dh": input.P256DH,
			"auth":   input.Auth,
		},
	})
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/push/subscriptions",
		bytes.NewReader(body),
	)
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("subscribe status=%d body=%s", response.Code, response.Body)
	}
	if bytes.Contains(response.Body.Bytes(), []byte(input.Endpoint)) ||
		bytes.Contains(response.Body.Bytes(), []byte(input.Auth)) {
		t.Fatal("subscription response exposed endpoint or push key")
	}

	body, _ = json.Marshal(map[string]string{"endpoint": input.Endpoint})
	request = httptest.NewRequest(
		http.MethodDelete,
		"/api/v1/push/subscriptions",
		bytes.NewReader(body),
	)
	response = httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatalf("revoke status=%d body=%s", response.Code, response.Body)
	}
}

func TestHTTPRejectsUnauthenticatedAndUnknownFields(t *testing.T) {
	t.Parallel()
	service := newTestService(
		t,
		NewMemoryRepository(),
		&fakeGateway{},
		time.Now,
	)
	handler, _ := NewHTTPHandler(service, testAuthenticator{})
	mux := http.NewServeMux()
	handler.Register(mux)
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/push/subscriptions",
		bytes.NewBufferString(`{"endpoint":"https://push.example.test/a"}`),
	)
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status=%d", response.Code)
	}

	handler, _ = NewHTTPHandler(service, testAuthenticator{accountID: "account-1"})
	mux = http.NewServeMux()
	handler.Register(mux)
	request = httptest.NewRequest(
		http.MethodDelete,
		"/api/v1/push/subscriptions",
		bytes.NewBufferString(
			`{"endpoint":"https://push.example.test/a","unexpected":true}`,
		),
	).WithContext(context.Background())
	response = httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("unknown field status=%d", response.Code)
	}
}
