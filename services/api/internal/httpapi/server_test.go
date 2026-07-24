package httpapi

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	featureruntime "github.com/apdsoftware/postqron/packages/runtime"
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
}
