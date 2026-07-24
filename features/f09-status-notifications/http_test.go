package statusnotifications

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type authenticatorStub struct {
	accountID string
	err       error
}

func (authenticator authenticatorStub) AccountID(*http.Request) (string, error) {
	return authenticator.accountID, authenticator.err
}

func TestHTTPStatusAndIdempotentRetry(t *testing.T) {
	now := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	service, _, _, _ := newTestService(t, now)
	mustSchedule(t, service, now)
	if _, err := service.ConsumePublication(
		context.Background(),
		publicationEvent(
			"http-failure",
			"destination-1",
			"instagram-1",
			"dead_letter",
			now.Add(time.Minute),
		),
	); err != nil {
		t.Fatalf("failure fixture error = %v", err)
	}
	handler, err := NewHTTPHandler(
		service,
		authenticatorStub{accountID: "account-1"},
	)
	if err != nil {
		t.Fatalf("NewHTTPHandler() error = %v", err)
	}
	mux := http.NewServeMux()
	handler.Register(mux)

	getRequest := httptest.NewRequest(
		http.MethodGet,
		"/api/v1/workspaces/workspace-1/publication-status/post-1",
		nil,
	)
	getResponse := httptest.NewRecorder()
	mux.ServeHTTP(getResponse, getRequest)
	if getResponse.Code != http.StatusOK {
		t.Fatalf("GET status = %d, body = %s", getResponse.Code, getResponse.Body)
	}
	var view PostView
	if err := json.Unmarshal(getResponse.Body.Bytes(), &view); err != nil {
		t.Fatalf("decode GET response: %v", err)
	}
	if view.Status != StatusFailed ||
		view.Destinations[0].Status != DestinationFailed {
		t.Fatalf("GET view = %+v", view)
	}

	retryRequest := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/workspaces/workspace-1/publication-status/post-1/"+
			"destinations/destination-1/retry",
		strings.NewReader(`{"idempotency_key":"http-retry-1"}`),
	)
	retryResponse := httptest.NewRecorder()
	mux.ServeHTTP(retryResponse, retryRequest)
	if retryResponse.Code != http.StatusAccepted {
		t.Fatalf(
			"POST retry = %d, body = %s",
			retryResponse.Code,
			retryResponse.Body,
		)
	}
	var result EnqueueResult
	if err := json.Unmarshal(retryResponse.Body.Bytes(), &result); err != nil {
		t.Fatalf("decode retry response: %v", err)
	}
	if !result.Created || result.ID == "" || result.State != QueuePending {
		t.Fatalf("retry result = %+v", result)
	}
}

func TestHTTPDoesNotExposeAuthenticationErrors(t *testing.T) {
	now := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	service, _, _, _ := newTestService(t, now)
	handler, err := NewHTTPHandler(
		service,
		authenticatorStub{err: errors.New("session token was invalid")},
	)
	if err != nil {
		t.Fatalf("NewHTTPHandler() error = %v", err)
	}
	mux := http.NewServeMux()
	handler.Register(mux)
	request := httptest.NewRequest(
		http.MethodGet,
		"/api/v1/workspaces/workspace-1/publication-status/post-1",
		nil,
	)
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("GET status = %d, want 403", response.Code)
	}
	if strings.Contains(response.Body.String(), "session token") {
		t.Fatalf("authentication detail leaked: %s", response.Body)
	}
}
