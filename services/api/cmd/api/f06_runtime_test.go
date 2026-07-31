package main

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"testing"
	"time"

	featureruntime "github.com/apdsoftware/postqron/packages/runtime"
	"github.com/apdsoftware/postqron/services/api/internal/featurehost"
	"github.com/apdsoftware/postqron/services/api/internal/httpapi"
)

func TestComposerFactoryMountsCredentialedRuntimeRoutes(t *testing.T) {
	t.Setenv("POSTQRON_AUTH_ALLOWED_ORIGINS", "https://postqron.com")
	t.Setenv("POSTQRON_F06_CAPABILITIES_JSON", "")
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
		[]featureruntime.Feature{discoverComposerFeature(t)},
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
		statuses[0].ID != "f06-composer" ||
		statuses[0].State != featurehost.StateActive {
		t.Fatalf("composer runtime status = %#v, want active", statuses)
	}

	for _, path := range []string{
		"/api/v1/workspaces/workspace-1/composer/capabilities",
		"/api/v1/workspaces/workspace-1/drafts",
		"/api/v1/workspaces/workspace-1/drafts/draft-1",
		"/api/v1/workspaces/workspace-1/drafts/draft-1/revisions",
		"/api/v1/workspaces/workspace-1/drafts/draft-1/validate",
		"/api/v1/workspaces/workspace-1/composer/media",
		"/api/v1/workspaces/workspace-1/composer/media/media-1",
		"/api/v1/workspaces/workspace-1/composer/media/media-1/complete",
		"/api/v1/workspaces/workspace-1/composer/media/media-1/download",
	} {
		request := httptest.NewRequest(http.MethodOptions, path, nil)
		request.Header.Set("Origin", "https://postqron.com")
		request.Header.Set("Access-Control-Request-Method", http.MethodPost)
		response := httptest.NewRecorder()
		host.PublicHandler().ServeHTTP(response, request)
		if response.Code != http.StatusNoContent ||
			response.Header().Get("Access-Control-Allow-Origin") != "https://postqron.com" ||
			response.Header().Get("Access-Control-Allow-Credentials") != "true" {
			t.Fatalf(
				"composer preflight %s = %d headers=%v body=%q",
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
			return http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
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
		"/api/v1/workspaces/workspace-1/composer/capabilities",
		nil,
	)
	request.Header.Set("Origin", "https://postqron.com")
	response := httptest.NewRecorder()
	api.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized ||
		response.Header().Get("Access-Control-Allow-Origin") != "https://postqron.com" ||
		response.Header().Get("Access-Control-Allow-Credentials") != "true" {
		t.Fatalf(
			"composer authenticated CORS = %d headers=%v body=%q",
			response.Code,
			response.Header(),
			response.Body.String(),
		)
	}
}

func TestComposerFeatureDeclaresSocialConnectionsDependencyAndDiscoveryOrder(
	t *testing.T,
) {
	features := discoverRuntimeFeatures(t)
	composerIndex := -1
	socialIndex := -1
	var composer featureruntime.Feature
	for index, feature := range features {
		switch feature.Manifest.ID {
		case "social-connections":
			socialIndex = index
		case "f06-composer":
			composerIndex = index
			composer = feature
		}
	}
	if composerIndex == -1 || socialIndex == -1 {
		t.Fatalf("discovered features missing composer/social: %#v", features)
	}
	if !slices.Contains(composer.Manifest.Dependencies, "social-connections") {
		t.Fatalf("composer dependencies = %v", composer.Manifest.Dependencies)
	}
	if socialIndex > composerIndex {
		t.Fatalf(
			"discovery order = social %d composer %d, want social before composer",
			socialIndex,
			composerIndex,
		)
	}
}

func discoverComposerFeature(t *testing.T) featureruntime.Feature {
	t.Helper()
	for _, feature := range discoverRuntimeFeatures(t) {
		if feature.Manifest.ID == "f06-composer" {
			return feature
		}
	}
	t.Fatal("f06-composer was not discovered")
	return featureruntime.Feature{}
}

func discoverRuntimeFeatures(t *testing.T) []featureruntime.Feature {
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
	return features
}
