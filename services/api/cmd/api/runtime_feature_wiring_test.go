package main

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	featureruntime "github.com/apdsoftware/postqron/packages/runtime"
	"github.com/apdsoftware/postqron/services/api/internal/featurehost"
	"github.com/apdsoftware/postqron/services/api/internal/httpapi"
)

func TestApiFeatureSetAvoidsRouteCollisions(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", "..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	discovered, err := featureruntime.Discover(
		filepath.Join(root, "services", "api", "features"),
		filepath.Join(root, "features"),
	)
	if err != nil {
		t.Fatal(err)
	}
	features, err := featureruntime.FilterKind(discovered, "api")
	if err != nil {
		t.Fatal(err)
	}
	registry := featurehost.NewRegistry()
	if err := registerFeatureFactories(registry); err != nil {
		t.Fatal(err)
	}
	host, err := featurehost.New(features, registry, runtimeTestDependencies(t), nil)
	if err != nil {
		t.Fatalf("featurehost.New() error = %v", err)
	}
	if host == nil {
		t.Fatal("featurehost.New() returned a nil host")
	}
	if err := host.Start(context.Background()); err != nil {
		t.Fatalf("host.Start() error = %v", err)
	}

	statusByID := map[string]featurehost.Status{}
	for _, status := range host.Statuses() {
		statusByID[status.ID] = status
	}
	cookieConsentStatus, ok := statusByID["cookie-consent-api"]
	if !ok {
		t.Fatal("cookie-consent-api was not discovered in API runtime")
	}
	if cookieConsentStatus.State != featurehost.StateActive {
		t.Fatalf(
			"cookie-consent-api status = %s error=%q, want active",
			cookieConsentStatus.State,
			cookieConsentStatus.Error,
		)
	}

	handler, err := httpapi.NewWithHost(
		host,
		func(next http.Handler) http.Handler { return next },
		"test",
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)
	if err != nil {
		t.Fatalf("httpapi.NewWithHost() error = %v", err)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(
		response,
		httptest.NewRequest(http.MethodGet, "/api/v1/cookie-preferences", nil),
	)
	if response.Code == http.StatusNotFound {
		t.Fatal("GET /api/v1/cookie-preferences returned 404, want mounted F26 route")
	}
	if body := response.Body.String(); strings.Contains(body, "no factory registered") {
		t.Fatalf("GET /api/v1/cookie-preferences exposed missing factory error: %s", body)
	}
}

func runtimeTestDependencies(t *testing.T) featurehost.Dependencies {
	t.Helper()
	database, err := openDatabase(
		"postgres://unused:unused@127.0.0.1:1/unused?sslmode=disable",
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	return featurehost.Dependencies{
		PostgreSQL: database,
		Config: map[string]string{
			"billing.app_domain": "app.postqron.example",
		},
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Clock:  func() time.Time { return time.Unix(1_800_000_000, 0).UTC() },
	}
}
