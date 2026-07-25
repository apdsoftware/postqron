package httpapi

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	featureruntime "github.com/apdsoftware/postqron/packages/runtime"
	"github.com/apdsoftware/postqron/services/api/internal/featurehost"
)

func TestHealth(t *testing.T) {
	handler := New(nil, "test", slog.New(slog.NewTextHandler(io.Discard, nil)))
	request := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}
	if body := response.Body.String(); !strings.Contains(body, `"version":"test"`) {
		t.Fatalf("body = %q, want version", body)
	}
}

func TestFeatureCatalogDoesNotExposePaths(t *testing.T) {
	features := []featureruntime.Feature{{
		Directory:    "/secret/workspace/path",
		ManifestPath: "/secret/workspace/path/feature.yaml",
		Manifest: featureruntime.Manifest{
			ID:      "platform",
			Kind:    "api",
			Version: "0.1.0",
		},
	}}
	handler := New(features, "test", slog.New(slog.NewTextHandler(io.Discard, nil)))
	request := httptest.NewRequest(http.MethodGet, "/api/v1/features", nil)
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}
	body := response.Body.String()
	if strings.Contains(body, "/secret/") {
		t.Fatalf("body exposes a local path: %q", body)
	}
	if !strings.Contains(body, `"id":"platform"`) {
		t.Fatalf("body = %q, want platform feature", body)
	}
	if !strings.Contains(body, `"status":"active"`) {
		t.Fatalf("body = %q, want active feature status", body)
	}
}

func TestHostedFeatureErrorAppearsInCatalogAndReadiness(t *testing.T) {
	required := true
	features := []featureruntime.Feature{{
		Manifest: featureruntime.Manifest{
			ID:          "broken",
			Kind:        "api",
			Version:     "0.1.0",
			Entrypoints: featureruntime.Entrypoints{Server: "./broken.go"},
			Required:    &required,
			Server: featureruntime.ServerModule{
				Routes: []featureruntime.ServerRoute{{
					Path:       "/broken",
					Handler:    "broken",
					Methods:    []string{http.MethodGet},
					Visibility: "public",
				}},
			},
		},
	}}
	host, err := featurehost.New(
		features,
		featurehost.NewRegistry(),
		featurehost.Dependencies{
			Database: struct{}{},
			Config:   map[string]string{},
			Logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
			Clock:    time.Now,
		},
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := host.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	handler := NewWithHost(
		host,
		"test",
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)

	catalog := httptest.NewRecorder()
	handler.ServeHTTP(
		catalog,
		httptest.NewRequest(http.MethodGet, "/api/v1/features", nil),
	)
	if body := catalog.Body.String(); !strings.Contains(body, `"status":"error"`) ||
		!strings.Contains(body, `no factory registered`) {
		t.Fatalf("catalog = %q, want readable feature error", body)
	}

	readiness := httptest.NewRecorder()
	handler.ServeHTTP(
		readiness,
		httptest.NewRequest(http.MethodGet, "/readyz", nil),
	)
	if readiness.Code != http.StatusServiceUnavailable {
		t.Fatalf("readiness status = %d, want %d", readiness.Code, http.StatusServiceUnavailable)
	}
}
