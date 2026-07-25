package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	featureruntime "github.com/apdsoftware/postqron/packages/runtime"
	"github.com/apdsoftware/postqron/services/worker/internal/runner"
)

var version = "dev"

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	roots := filepath.SplitList(envOrDefault("POSTQRON_FEATURE_ROOTS", defaultFeatureRoots()))
	discovered, err := featureruntime.Discover(roots...)
	if err != nil {
		logger.Error("discover worker features", "error", err)
		os.Exit(1)
	}
	features, err := featureruntime.FilterKind(discovered, "worker")
	if err != nil {
		logger.Error("filter worker features", "error", err)
		os.Exit(1)
	}

	interval, err := time.ParseDuration(envOrDefault("WORKER_POLL_INTERVAL", "5s"))
	if err != nil || interval <= 0 {
		logger.Error("invalid WORKER_POLL_INTERVAL", "value", os.Getenv("WORKER_POLL_INTERVAL"))
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	logger.Info(
		"worker started",
		"discovered_features", len(discovered),
		"features", len(features),
		"version", version,
	)

	worker := runner.New(features, interval, logger)
	if os.Getenv("WORKER_RUN_ONCE") == "1" {
		worker.Tick(ctx)
		return
	}
	worker.Run(ctx)
	logger.Info("worker stopped")
}

func envOrDefault(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func defaultFeatureRoots() string {
	return strings.Join(
		[]string{"services/worker/features", "services/api/features", "features"},
		string(os.PathListSeparator),
	)
}
