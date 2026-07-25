package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	featureruntime "github.com/apdsoftware/postqron/packages/runtime"
	"github.com/apdsoftware/postqron/services/api/internal/httpapi"
)

var version = "dev"

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if err := run(logger); err != nil {
		logger.Error("api stopped", "error", err)
		os.Exit(1)
	}
}

func run(logger *slog.Logger) error {
	roots := featureRoots("POSTQRON_FEATURE_ROOTS", defaultFeatureRoots())
	discovered, err := featureruntime.Discover(roots...)
	if err != nil {
		return err
	}
	features, err := featureruntime.FilterKind(discovered, "api")
	if err != nil {
		return err
	}

	address := envOrDefault("API_ADDR", ":8080")
	server := &http.Server{
		Addr:              address,
		Handler:           httpapi.New(features, version, logger),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	errs := make(chan error, 1)
	go func() {
		logger.Info(
			"api listening",
			"address", address,
			"discovered_features", len(discovered),
			"features", len(features),
			"version", version,
		)
		errs <- server.ListenAndServe()
	}()

	select {
	case err := <-errs:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return server.Shutdown(shutdownCtx)
	}
}

func featureRoots(key, fallback string) []string {
	value := envOrDefault(key, fallback)
	return filepath.SplitList(value)
}

func defaultFeatureRoots() string {
	return strings.Join(
		[]string{"services/api/features", "features"},
		string(os.PathListSeparator),
	)
}

func envOrDefault(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
