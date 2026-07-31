package main

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	featureruntime "github.com/apdsoftware/postqron/packages/runtime"
	"github.com/apdsoftware/postqron/services/api/internal/featurehost"
	"github.com/apdsoftware/postqron/services/api/internal/httpapi"
)

func TestSchedulingFactoryMountsRuntimeRoutes(t *testing.T) {
	t.Setenv("POSTQRON_AUTH_ALLOWED_ORIGINS", "https://postqron.com")
	database, err := openDatabase(
		"postgres://unused:unused@127.0.0.1:1/unused?sslmode=disable",
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })

	registry := featurehost.NewRegistry()
	if err := registerFeatureFactories(registry); err != nil {
		t.Fatal(err)
	}
	host, err := featurehost.New(
		[]featureruntime.Feature{discoverSchedulingFeature(t)},
		registry,
		featurehost.Dependencies{
			PostgreSQL: database,
			Config:     map[string]string{},
			Logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
			Clock:      func() time.Time { return time.Unix(1_800_000_000, 0).UTC() },
		},
		featurehost.ValidatedMigrations{},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := host.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = host.Stop(context.Background()) })

	statuses := host.Statuses()
	if len(statuses) != 1 ||
		statuses[0].ID != "scheduling" ||
		statuses[0].State != featurehost.StateActive {
		t.Fatalf("scheduling runtime status = %#v, want active", statuses)
	}

	for _, path := range []string{
		"/api/v1/workspaces/workspace-1/calendar",
		"/api/v1/workspaces/workspace-1/scheduled-posts",
		"/api/v1/workspaces/workspace-1/scheduled-posts/post-1",
		"/api/v1/workspaces/workspace-1/scheduled-posts/post-1/reschedule",
		"/api/v1/workspaces/workspace-1/scheduled-posts/post-1/duplicate",
		"/api/v1/workspaces/workspace-1/scheduled-posts/post-1/cancel",
	} {
		preflight := httptest.NewRequest(http.MethodOptions, path, nil)
		preflight.Header.Set("Origin", "https://postqron.com")
		preflight.Header.Set("Access-Control-Request-Method", http.MethodPost)
		response := httptest.NewRecorder()
		host.PublicHandler().ServeHTTP(response, preflight)
		if response.Code != http.StatusNoContent ||
			response.Header().Get("Access-Control-Allow-Origin") !=
				"https://postqron.com" ||
			response.Header().Get("Access-Control-Allow-Credentials") != "true" {
			t.Fatalf(
				"scheduling preflight %s = %d headers=%v body=%q",
				path,
				response.Code,
				response.Header(),
				response.Body.String(),
			)
		}
	}

	api, err := httpapi.NewWithHost(
		host,
		func(http.Handler) http.Handler {
			return http.HandlerFunc(func(
				writer http.ResponseWriter,
				_ *http.Request,
			) {
				writer.WriteHeader(http.StatusUnauthorized)
			})
		},
		"test",
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(
		http.MethodGet,
		"/api/v1/workspaces/workspace-1/calendar"+
			"?from=2026-07-25T00%3A00%3A00Z&until=2026-07-26T00%3A00%3A00Z",
		nil,
	)
	request.Header.Set("Origin", "https://postqron.com")
	response := httptest.NewRecorder()
	api.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized ||
		response.Header().Get("Access-Control-Allow-Origin") !=
			"https://postqron.com" ||
		response.Header().Get("Access-Control-Allow-Credentials") != "true" {
		t.Fatalf(
			"scheduling authenticated route CORS = %d headers=%v body=%q",
			response.Code,
			response.Header(),
			response.Body.String(),
		)
	}
}

func discoverSchedulingFeature(t *testing.T) featureruntime.Feature {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", "..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	features, err := featureruntime.Discover(
		filepath.Join(root, "services", "api", "features"),
		filepath.Join(root, "features"),
	)
	if err != nil {
		t.Fatal(err)
	}
	for _, feature := range features {
		if feature.Manifest.ID == "scheduling" {
			return feature
		}
	}
	t.Fatal("scheduling was not discovered")
	return featureruntime.Feature{}
}
