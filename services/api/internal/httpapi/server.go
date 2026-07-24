package httpapi

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	featureruntime "github.com/apdsoftware/postqron/packages/runtime"
)

type featureResponse struct {
	ID      string `json:"id"`
	Kind    string `json:"kind"`
	Version string `json:"version"`
}

type server struct {
	features []featureruntime.Feature
	logger   *slog.Logger
	version  string
}

func New(features []featureruntime.Feature, version string, logger *slog.Logger) http.Handler {
	api := &server{features: features, version: version, logger: logger}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", api.health)
	mux.HandleFunc("GET /readyz", api.ready)
	mux.HandleFunc("GET /api/v1/features", api.listFeatures)
	return api.logging(mux)
}

func (s *server) health(writer http.ResponseWriter, _ *http.Request) {
	writeJSON(writer, http.StatusOK, map[string]string{
		"status":  "ok",
		"service": "api",
		"version": s.version,
	})
}

func (s *server) ready(writer http.ResponseWriter, _ *http.Request) {
	writeJSON(writer, http.StatusOK, map[string]any{
		"status":   "ready",
		"features": len(s.features),
	})
}

func (s *server) listFeatures(writer http.ResponseWriter, _ *http.Request) {
	response := make([]featureResponse, 0, len(s.features))
	for _, feature := range s.features {
		response = append(response, featureResponse{
			ID:      feature.Manifest.ID,
			Kind:    feature.Manifest.Kind,
			Version: feature.Manifest.Version,
		})
	}
	writeJSON(writer, http.StatusOK, response)
}

func (s *server) logging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		started := time.Now()
		next.ServeHTTP(writer, request)
		s.logger.Info(
			"http request",
			"method", request.Method,
			"path", request.URL.Path,
			"duration_ms", time.Since(started).Milliseconds(),
		)
	})
}

func writeJSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.Header().Set("Cache-Control", "no-store")
	writer.Header().Set("X-Content-Type-Options", "nosniff")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}
