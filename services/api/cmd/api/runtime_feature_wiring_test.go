package main

import (
	"context"
	"database/sql"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	featureruntime "github.com/apdsoftware/postqron/packages/runtime"
	"github.com/apdsoftware/postqron/services/api/internal/featurehost"
	"github.com/apdsoftware/postqron/services/api/internal/httpapi"
)

func TestApiFeatureSetAvoidsRouteCollisions(t *testing.T) {
	t.Setenv("POSTQRON_ADMIN_ALLOWLIST", "admin@example.test")
	t.Setenv("POSTQRON_ADMIN_ALLOWED_ORIGINS", "https://admin.postqron.example")
	t.Setenv("NODE_ENV", "production")
	t.Setenv("PRELAUNCH_ALLOWED_ORIGINS", "https://www.postqron.com")

	databaseURL := runtimeFeatureDatabaseURL()
	if databaseURL == "" {
		t.Skip("set TEST_DATABASE_URL or DATABASE_URL to verify active runtime wiring for F31/F34")
	}
	database, err := openDatabase(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	prepareRuntimeFeatureWiringDatabase(t, database)

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
	host, err := featurehost.New(features, registry, runtimeTestDependencies(database), nil)
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
	adminConsoleStatus, ok := statusByID["admin-console"]
	if !ok {
		t.Fatal("admin-console was not discovered in API runtime")
	}
	if adminConsoleStatus.State != featurehost.StateActive {
		t.Fatalf(
			"admin-console status = %s error=%q, want active",
			adminConsoleStatus.State,
			adminConsoleStatus.Error,
		)
	}
	prelaunchStatus, ok := statusByID["prelaunch-access"]
	if !ok {
		t.Fatal("prelaunch-access was not discovered in API runtime")
	}
	if prelaunchStatus.State != featurehost.StateActive {
		t.Fatalf(
			"prelaunch-access status = %s error=%q, want active",
			prelaunchStatus.State,
			prelaunchStatus.Error,
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

	adminSession := httptest.NewRecorder()
	handler.ServeHTTP(
		adminSession,
		httptest.NewRequest(http.MethodGet, "/api/v1/admin/session", nil),
	)
	if adminSession.Code == http.StatusNotFound {
		t.Fatal("GET /api/v1/admin/session returned 404, want mounted F31 route")
	}
	if body := adminSession.Body.String(); strings.Contains(body, "no factory registered") {
		t.Fatalf("GET /api/v1/admin/session exposed missing factory error: %s", body)
	}

	prelaunchRequest := httptest.NewRecorder()
	handler.ServeHTTP(
		prelaunchRequest,
		httptest.NewRequest(http.MethodPost, "/api/v1/prelaunch/access-requests", nil),
	)
	if prelaunchRequest.Code == http.StatusNotFound {
		t.Fatal("POST /api/v1/prelaunch/access-requests returned 404, want mounted F34 route")
	}
	if body := prelaunchRequest.Body.String(); strings.Contains(body, "no factory registered") {
		t.Fatalf("POST /api/v1/prelaunch/access-requests exposed missing factory error: %s", body)
	}

	prelaunchProbe := httptest.NewRecorder()
	handler.ServeHTTP(
		prelaunchProbe,
		httptest.NewRequest(http.MethodGet, "/api/v1/prelaunch/status", nil),
	)
	if prelaunchProbe.Code == http.StatusNotFound {
		t.Fatal("GET /api/v1/prelaunch/status returned 404, want mounted F34 status route")
	}
	if body := prelaunchProbe.Body.String(); strings.Contains(body, "no factory registered") {
		t.Fatalf("GET /api/v1/prelaunch/status exposed missing factory error: %s", body)
	}
}

func runtimeTestDependencies(database *sql.DB) featurehost.Dependencies {
	return featurehost.Dependencies{
		PostgreSQL: database,
		Config: map[string]string{
			"billing.app_domain": "app.postqron.example",
		},
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Clock:  func() time.Time { return time.Unix(1_800_000_000, 0).UTC() },
	}
}

func runtimeFeatureDatabaseURL() string {
	for _, key := range []string{"TEST_DATABASE_URL", "DATABASE_URL"} {
		if value := strings.TrimSpace(os.Getenv(key)); value != "" {
			return value
		}
	}
	return ""
}

func prepareRuntimeFeatureWiringDatabase(t *testing.T, database *sql.DB) {
	t.Helper()
	for _, statement := range []string{
		`CREATE TABLE IF NOT EXISTS auth_accounts (
			id text PRIMARY KEY,
			normalized_email text NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS f31_admin_records (
			account_id text PRIMARY KEY REFERENCES auth_accounts(id),
			email text NOT NULL,
			active boolean NOT NULL,
			updated_at timestamptz NOT NULL
		)`,
		`DELETE FROM f31_admin_records`,
		`DELETE FROM auth_accounts`,
	} {
		if _, err := database.Exec(statement); err != nil {
			t.Fatalf("prepare runtime wiring database: %v", err)
		}
	}
}
