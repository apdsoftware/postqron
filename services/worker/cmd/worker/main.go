package main

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	featureruntime "github.com/apdsoftware/postqron/packages/runtime"
	"github.com/apdsoftware/postqron/services/worker/internal/runner"
	_ "github.com/jackc/pgx/v5/stdlib"
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
	runOnce := os.Getenv("WORKER_RUN_ONCE") == "1"
	logger.Info(
		"worker started",
		"discovered_features", len(discovered),
		"features", len(features),
		"version", version,
	)
	if shouldSkipRunOnceDatabase(runOnce, os.Getenv("DATABASE_URL")) {
		runner.New(features, interval, logger).Tick(ctx)
		logger.Info(
			"worker run-once skipped database-dependent execution",
			"reason", "DATABASE_URL is not configured",
		)
		return
	}

	database, err := openDatabase(os.Getenv("DATABASE_URL"))
	if err != nil {
		logger.Error("open worker database", "error", err)
		os.Exit(1)
	}
	defer database.Close()

	worker, err := runner.NewRuntime(
		features,
		database,
		os.Getenv("DATABASE_URL"),
		os.Getenv("APP_DOMAIN"),
		interval,
		time.Now,
		logger,
	)
	if err != nil {
		logger.Error("configure worker", "error", err)
		os.Exit(1)
	}
	defer worker.Close()
	if runOnce {
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

func shouldSkipRunOnceDatabase(runOnce bool, databaseURL string) bool {
	return runOnce && strings.TrimSpace(databaseURL) == ""
}

func defaultFeatureRoots() string {
	return strings.Join(
		[]string{"services/worker/features", "services/api/features", "features"},
		string(os.PathListSeparator),
	)
}

func openDatabase(databaseURL string) (*sql.DB, error) {
	databaseURL = strings.TrimSpace(databaseURL)
	if databaseURL == "" {
		return nil, errors.New("DATABASE_URL is required by the worker runtime")
	}
	database, err := sql.Open("pgx", databaseURL)
	if err != nil {
		return nil, err
	}
	database.SetMaxOpenConns(10)
	database.SetMaxIdleConns(2)
	database.SetConnMaxIdleTime(5 * time.Minute)
	database.SetConnMaxLifetime(30 * time.Minute)
	return database, nil
}
