// Package httpapi espone il router HTTP del servizio.
//
// In questa fase di scaffold contiene solo l'health check: le rotte REST dei
// job, delle esecuzioni e dell'autenticazione arrivano con le issue dedicate.
package httpapi

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/apdsoftware/postqron/services/api/internal/config"
)

// Health è il corpo della risposta di /healthz.
type Health struct {
	Status  string `json:"status"`
	Env     string `json:"env"`
	Version string `json:"version"`
}

// NewRouter costruisce il router del servizio.
func NewRouter(cfg config.Config, version string, logger *slog.Logger) http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, r, logger, http.StatusOK, Health{
			Status:  "ok",
			Env:     cfg.Env,
			Version: version,
		})
	})

	return mux
}

func writeJSON(w http.ResponseWriter, r *http.Request, logger *slog.Logger, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(body); err != nil {
		// La risposta è già iniziata: si può solo tracciare l'errore.
		logger.ErrorContext(r.Context(), "scrittura della risposta JSON fallita",
			slog.String("path", r.URL.Path), slog.Any("error", err))
	}
}
