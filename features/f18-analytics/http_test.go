package analytics

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestHTTPOverviewUsesIntervalAndChannelFilter(t *testing.T) {
	now := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	service := newTestService(
		t,
		NewMemoryRepository(),
		&fakeResolver{adapter: &fakeAdapter{}},
		&fakePermissions{},
		&fakeLimiter{},
		&allowViewer{},
		func() time.Time { return now },
	)
	handler, err := NewHTTPHandler(service, staticAuthenticator("account-1"))
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(
		http.MethodGet,
		"/api/v1/workspaces/workspace-1/analytics"+
			"?from=2026-07-01T00:00:00Z&to=2026-08-01T00:00:00Z"+
			"&channel_id=facebook-1",
		nil,
	)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	var body Overview
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if !body.From.Equal(time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)) ||
		!body.To.Equal(time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("interval = %v - %v", body.From, body.To)
	}
	if response.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("analytics response must not be cached")
	}
}

func TestHTTPOverviewRequiresAuthentication(t *testing.T) {
	service := newTestService(
		t,
		NewMemoryRepository(),
		&fakeResolver{adapter: &fakeAdapter{}},
		&fakePermissions{},
		&fakeLimiter{},
		&allowViewer{},
		time.Now,
	)
	handler, err := NewHTTPHandler(service, staticAuthenticator(""))
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(
		http.MethodGet,
		"/api/v1/workspaces/workspace-1/analytics"+
			"?from=2026-07-01T00:00:00Z&to=2026-08-01T00:00:00Z",
		nil,
	)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d", response.Code)
	}
}

type staticAuthenticator string

func (authenticator staticAuthenticator) AccountID(*http.Request) (string, bool) {
	return string(authenticator), authenticator != ""
}

type denyViewer struct{}

func (*denyViewer) CanViewAnalytics(context.Context, string, string) error {
	return ErrForbidden
}
