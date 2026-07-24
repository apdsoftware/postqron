package operations

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestHealthHandlerServesLivenessReadinessAndMetrics(t *testing.T) {
	metrics := &Metrics{}
	handler := NewHealthHandler(
		"test-version",
		map[string]ReadinessCheck{
			"database": func(context.Context) error { return nil },
			"secrets":  func(context.Context) error { return nil },
		},
		time.Second,
		metrics,
	)
	mux := http.NewServeMux()
	handler.Register(mux)

	live := httptest.NewRecorder()
	mux.ServeHTTP(live, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if live.Code != http.StatusOK || !strings.Contains(live.Body.String(), `"version":"test-version"`) {
		t.Fatalf("liveness = %d %s", live.Code, live.Body.String())
	}

	ready := httptest.NewRecorder()
	mux.ServeHTTP(ready, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if ready.Code != http.StatusOK || !strings.Contains(ready.Body.String(), `"status":"ready"`) {
		t.Fatalf("readiness = %d %s", ready.Code, ready.Body.String())
	}

	exposition := httptest.NewRecorder()
	mux.ServeHTTP(exposition, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if exposition.Code != http.StatusOK ||
		!strings.Contains(exposition.Body.String(), "postqron_readiness 1") {
		t.Fatalf("metrics = %d %s", exposition.Code, exposition.Body.String())
	}
}

func TestReadinessHidesDependencyErrorsAndTimesOut(t *testing.T) {
	metrics := &Metrics{}
	handler := NewHealthHandler(
		"test",
		map[string]ReadinessCheck{
			"database": func(context.Context) error {
				return errors.New("postgres://admin:password@private-database/customer")
			},
			"queue": func(ctx context.Context) error {
				<-ctx.Done()
				return ctx.Err()
			},
		},
		10*time.Millisecond,
		metrics,
	)
	response := httptest.NewRecorder()

	handler.Ready(response, httptest.NewRequest(http.MethodGet, "/readyz", nil))

	body := response.Body.String()
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d: %s", response.Code, http.StatusServiceUnavailable, body)
	}
	for _, forbidden := range []string{"password", "private-database", "customer"} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("readiness body exposes %q: %s", forbidden, body)
		}
	}
	for _, dependency := range []string{`"name":"database"`, `"name":"queue"`} {
		if !strings.Contains(body, dependency) {
			t.Fatalf("readiness body missing %q: %s", dependency, body)
		}
	}
	if metrics.Snapshot().Ready {
		t.Fatal("readiness metric = true, want false")
	}
}
