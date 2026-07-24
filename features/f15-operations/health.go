package operations

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"time"
)

type ReadinessCheck func(context.Context) error

type HealthHandler struct {
	checks  map[string]ReadinessCheck
	metrics *Metrics
	timeout time.Duration
	version string
}

func NewHealthHandler(
	version string,
	checks map[string]ReadinessCheck,
	timeout time.Duration,
	metrics *Metrics,
) *HealthHandler {
	if timeout <= 0 {
		timeout = 2 * time.Second
	}
	if metrics == nil {
		metrics = &Metrics{}
	}
	cloned := make(map[string]ReadinessCheck, len(checks))
	for name, check := range checks {
		cloned[name] = check
	}
	return &HealthHandler{checks: cloned, metrics: metrics, timeout: timeout, version: version}
}

// Register owns only the F15 endpoints and can be mounted by any discovered API
// host without a central feature registry.
func (handler *HealthHandler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /healthz", handler.Live)
	mux.HandleFunc("GET /readyz", handler.Ready)
	mux.Handle("GET /metrics", handler.metrics)
}

func (handler *HealthHandler) Live(writer http.ResponseWriter, _ *http.Request) {
	writeOperationalJSON(writer, http.StatusOK, map[string]string{
		"status":  "ok",
		"service": "operations",
		"version": handler.version,
	})
}

func (handler *HealthHandler) Ready(writer http.ResponseWriter, request *http.Request) {
	type result struct {
		name string
		err  error
	}

	ctx, cancel := context.WithTimeout(request.Context(), handler.timeout)
	defer cancel()
	results := make(chan result, len(handler.checks))

	for name, check := range handler.checks {
		go func() {
			if check == nil {
				results <- result{name: name, err: context.Canceled}
				return
			}
			results <- result{name: name, err: check(ctx)}
		}()
	}

	statuses := make(map[string]string, len(handler.checks))
	remaining := len(handler.checks)
	for remaining > 0 {
		select {
		case checked := <-results:
			status := "ready"
			if checked.err != nil {
				status = "unavailable"
			}
			statuses[checked.name] = status
			remaining--
		case <-ctx.Done():
			remaining = 0
		}
	}

	for name := range handler.checks {
		if _, exists := statuses[name]; !exists {
			statuses[name] = "unavailable"
		}
	}
	names := make([]string, 0, len(statuses))
	ready := true
	for name, status := range statuses {
		names = append(names, name)
		if status != "ready" {
			ready = false
		}
	}
	sort.Strings(names)
	dependencies := make([]map[string]string, 0, len(names))
	for _, name := range names {
		dependencies = append(dependencies, map[string]string{
			"name":   name,
			"status": statuses[name],
		})
	}

	handler.metrics.SetReady(ready)
	statusCode := http.StatusOK
	status := "ready"
	if !ready {
		statusCode = http.StatusServiceUnavailable
		status = "unavailable"
	}
	writeOperationalJSON(writer, statusCode, map[string]any{
		"status":       status,
		"dependencies": dependencies,
	})
}

func writeOperationalJSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.Header().Set("Cache-Control", "no-store")
	writer.Header().Set("X-Content-Type-Options", "nosniff")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}
