package httpapi_test

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/apdsoftware/postqron/services/api/internal/config"
	"github.com/apdsoftware/postqron/services/api/internal/httpapi"
)

func newRouter(t *testing.T) http.Handler {
	t.Helper()
	cfg, err := config.LoadFrom(func(string) string { return "" })
	if err != nil {
		t.Fatalf("configurazione di default non valida: %v", err)
	}
	return httpapi.NewRouter(cfg, "test", slog.New(slog.NewTextHandler(io.Discard, nil)), httpapi.Deps{})
}

func TestHealthz(t *testing.T) {
	rec := httptest.NewRecorder()
	newRouter(t).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, atteso 200", rec.Code)
	}
	if got := rec.Header().Get("Content-Type"); got != "application/json; charset=utf-8" {
		t.Errorf("Content-Type = %q", got)
	}

	var body httpapi.Health
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("corpo non decodificabile: %v", err)
	}
	if body.Status != "ok" {
		t.Errorf("status = %q, atteso \"ok\"", body.Status)
	}
	if body.Env != config.EnvDevelopment {
		t.Errorf("env = %q, atteso %q", body.Env, config.EnvDevelopment)
	}
	if body.Version != "test" {
		t.Errorf("version = %q, atteso \"test\"", body.Version)
	}
}

func TestHealthzRifiutaMetodiDiversiDaGET(t *testing.T) {
	rec := httptest.NewRecorder()
	newRouter(t).ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/healthz", nil))

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, atteso 405", rec.Code)
	}
}

func TestRottaSconosciuta(t *testing.T) {
	rec := httptest.NewRecorder()
	newRouter(t).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/non-esiste", nil))

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, atteso 404", rec.Code)
	}
}
