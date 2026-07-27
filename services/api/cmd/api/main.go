package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	featureruntime "github.com/apdsoftware/postqron/packages/runtime"
	"github.com/apdsoftware/postqron/services/api/internal/featurehost"
	"github.com/apdsoftware/postqron/services/api/internal/httpapi"
	_ "github.com/jackc/pgx/v5/stdlib"
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
	database, err := openDatabase(os.Getenv("DATABASE_URL"))
	if err != nil {
		return err
	}
	defer database.Close()

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
	registry := featurehost.NewRegistry()
	if err := registerFeatureFactories(registry); err != nil {
		return err
	}
	host, err := featurehost.New(
		features,
		registry,
		featurehost.Dependencies{
			PostgreSQL: database,
			Config: map[string]string{
				"address":                       address,
				"version":                       version,
				"billing.app_domain":            os.Getenv("APP_DOMAIN"),
				"billing.paddle_environment":    os.Getenv("PADDLE_ENVIRONMENT"),
				"billing.paddle_api_key":        os.Getenv("PADDLE_API_KEY"),
				"billing.paddle_webhook_secret": os.Getenv("PADDLE_WEBHOOK_SECRET"),
				"billing.paddle_catalog_json":   os.Getenv("PADDLE_CATALOG_JSON"),
			},
			Logger: logger,
			Clock:  time.Now,
		},
		featurehost.ValidatedMigrations{},
	)
	if err != nil {
		return err
	}
	if err := host.Start(context.Background()); err != nil {
		return err
	}

	authenticatePrivate, err := httpapi.NewPostgresSessionAuthentication(database, time.Now)
	if err != nil {
		return err
	}
	apiHandler, err := httpapi.NewWithHost(
		host,
		authenticatePrivate,
		version,
		logger,
	)
	if err != nil {
		return err
	}
	server := &http.Server{
		Addr:              address,
		Handler:           apiHandler,
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

	var serverErr error
	select {
	case err := <-errs:
		if !errors.Is(err, http.ErrServerClosed) {
			serverErr = err
		}
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		serverErr = server.Shutdown(shutdownCtx)
		cancel()
	}

	stopCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return errors.Join(serverErr, host.Stop(stopCtx))
}

func openDatabase(databaseURL string) (*sql.DB, error) {
	databaseURL = strings.TrimSpace(databaseURL)
	if databaseURL == "" {
		return nil, errors.New("DATABASE_URL is required by the API feature runtime")
	}
	database, err := sql.Open("pgx", databaseURL)
	if err != nil {
		return nil, fmt.Errorf("open PostgreSQL feature database: %w", err)
	}
	database.SetMaxOpenConns(20)
	database.SetMaxIdleConns(5)
	database.SetConnMaxIdleTime(5 * time.Minute)
	database.SetConnMaxLifetime(30 * time.Minute)
	return database, nil
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
