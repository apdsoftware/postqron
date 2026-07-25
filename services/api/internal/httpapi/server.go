package httpapi

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	featureruntime "github.com/apdsoftware/postqron/packages/runtime"
	"github.com/apdsoftware/postqron/services/api/internal/featurehost"
)

type featureResponse struct {
	ID      string `json:"id"`
	Kind    string `json:"kind"`
	Version string `json:"version"`
	Status  string `json:"status"`
	Error   string `json:"error,omitempty"`
}

type server struct {
	authenticate func(http.Handler) http.Handler
	features     []featureruntime.Feature
	host         *featurehost.Host
	logger       *slog.Logger
	version      string
}

func New(features []featureruntime.Feature, version string, logger *slog.Logger) http.Handler {
	api := &server{features: features, version: version, logger: logger}
	handler, err := api.handler()
	if err != nil {
		panic(err)
	}
	return handler
}

func NewWithHost(
	host *featurehost.Host,
	authenticate func(http.Handler) http.Handler,
	version string,
	logger *slog.Logger,
) (http.Handler, error) {
	api := &server{
		authenticate: authenticate,
		host:         host,
		version:      version,
		logger:       logger,
	}
	return api.handler()
}

func (s *server) handler() (http.Handler, error) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.health)
	mux.HandleFunc("GET /readyz", s.ready)
	mux.HandleFunc("GET /api/v1/features", s.listFeatures)
	if s.host != nil {
		if err := s.host.MountAuthenticatedRoutes(mux, s.authenticate); err != nil {
			return nil, err
		}
		mux.Handle("/api/v1/", s.host.PublicHandler())
	}
	return s.logging(mux), nil
}

func (s *server) health(writer http.ResponseWriter, _ *http.Request) {
	writeJSON(writer, http.StatusOK, map[string]string{
		"status":  "ok",
		"service": "api",
		"version": s.version,
	})
}

func (s *server) ready(writer http.ResponseWriter, request *http.Request) {
	if s.host != nil {
		if err := s.host.Ready(request.Context()); err != nil {
			writeJSON(writer, http.StatusServiceUnavailable, map[string]any{
				"status": "not_ready",
				"error":  err.Error(),
			})
			return
		}
	}
	writeJSON(writer, http.StatusOK, map[string]any{
		"status":   "ready",
		"features": len(s.featureStatuses()),
	})
}

func (s *server) listFeatures(writer http.ResponseWriter, _ *http.Request) {
	statuses := s.featureStatuses()
	response := make([]featureResponse, 0, len(statuses))
	for _, status := range statuses {
		response = append(response, featureResponse{
			ID:      status.ID,
			Kind:    status.Kind,
			Version: status.Version,
			Status:  string(status.State),
			Error:   status.Error,
		})
	}
	writeJSON(writer, http.StatusOK, response)
}

func (s *server) featureStatuses() []featurehost.Status {
	if s.host != nil {
		return s.host.PublicStatuses()
	}
	statuses := make([]featurehost.Status, 0, len(s.features))
	for _, feature := range s.features {
		statuses = append(statuses, featurehost.Status{
			ID:      feature.Manifest.ID,
			Kind:    feature.Manifest.Kind,
			Version: feature.Manifest.Version,
			State:   featurehost.StateActive,
		})
	}
	return statuses
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
