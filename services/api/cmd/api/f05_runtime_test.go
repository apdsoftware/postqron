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

func TestSocialConnectionsFactoryMountsRuntimeRoutes(t *testing.T) {
	for _, key := range []string{
		"POSTQRON_F05_META_ENABLED",
		"POSTQRON_F05_META_GRAPH_VERSION",
		"POSTQRON_F05_CIPHER_KEY_ID",
		"POSTQRON_F05_CIPHER_KEY_BASE64",
		"POSTQRON_F05_FACEBOOK_CLIENT_ID",
		"POSTQRON_F05_FACEBOOK_CLIENT_SECRET",
		"POSTQRON_F05_FACEBOOK_REDIRECT_URL",
		"POSTQRON_F05_FACEBOOK_LOGIN_CONFIG_ID",
		"POSTQRON_F05_FACEBOOK_APP_REVIEW_APPROVED",
		"POSTQRON_F05_INSTAGRAM_CLIENT_ID",
		"POSTQRON_F05_INSTAGRAM_CLIENT_SECRET",
		"POSTQRON_F05_INSTAGRAM_REDIRECT_URL",
		"POSTQRON_F05_INSTAGRAM_APP_REVIEW_APPROVED",
	} {
		t.Setenv(key, "")
	}
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
		[]featureruntime.Feature{discoverSocialConnectionsFeature(t)},
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
		statuses[0].ID != "social-connections" ||
		statuses[0].State != featurehost.StateActive {
		t.Fatalf("social connections runtime status = %#v, want active", statuses)
	}

	response := httptest.NewRecorder()
	host.PublicHandler().ServeHTTP(
		response,
		httptest.NewRequest(
			http.MethodGet,
			"/api/v1/social-authorizations/callback",
			nil,
		),
	)
	if response.Code != http.StatusConflict {
		t.Fatalf(
			"social callback status = %d body=%q, want mounted 409 response",
			response.Code,
			response.Body.String(),
		)
	}

	for _, path := range []string{
		"/api/v1/workspaces/workspace-1/social-connections/bootstrap",
		"/api/v1/workspaces/workspace-1/social-authorizations",
		"/api/v1/social-authorizations/callback",
		"/api/v1/workspaces/workspace-1/social-connections",
		"/api/v1/workspaces/workspace-1/social-connections/connection-1/reconnect",
		"/api/v1/workspaces/workspace-1/social-connections/connection-1",
	} {
		preflight := httptest.NewRequest(http.MethodOptions, path, nil)
		preflight.Header.Set("Origin", "https://postqron.com")
		preflight.Header.Set("Access-Control-Request-Method", http.MethodPost)
		preflightResponse := httptest.NewRecorder()
		host.PublicHandler().ServeHTTP(preflightResponse, preflight)
		if preflightResponse.Code != http.StatusNoContent ||
			preflightResponse.Header().Get("Access-Control-Allow-Origin") !=
				"https://postqron.com" ||
			preflightResponse.Header().Get("Access-Control-Allow-Credentials") !=
				"true" {
			t.Fatalf(
				"social preflight %s = %d headers=%v body=%q",
				path,
				preflightResponse.Code,
				preflightResponse.Header(),
				preflightResponse.Body.String(),
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
	unauthenticated := httptest.NewRequest(
		http.MethodGet,
		"/api/v1/workspaces/workspace-1/social-connections/bootstrap",
		nil,
	)
	unauthenticated.Header.Set("Origin", "https://postqron.com")
	unauthenticatedResponse := httptest.NewRecorder()
	api.ServeHTTP(unauthenticatedResponse, unauthenticated)
	if unauthenticatedResponse.Code != http.StatusUnauthorized ||
		unauthenticatedResponse.Header().Get("Access-Control-Allow-Origin") !=
			"https://postqron.com" ||
		unauthenticatedResponse.Header().Get("Access-Control-Allow-Credentials") !=
			"true" {
		t.Fatalf(
			"authenticated route CORS = %d headers=%v body=%q",
			unauthenticatedResponse.Code,
			unauthenticatedResponse.Header(),
			unauthenticatedResponse.Body.String(),
		)
	}
}

func discoverSocialConnectionsFeature(t *testing.T) featureruntime.Feature {
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
		if feature.Manifest.ID == "social-connections" {
			return feature
		}
	}
	t.Fatal("social-connections was not discovered")
	return featureruntime.Feature{}
}
