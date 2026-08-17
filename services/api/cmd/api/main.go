// Command api avvia il servizio HTTP di PostQron: API REST e, in seguito,
// motore cron. È l'unica origin dinamica del prodotto — i frontend sono
// statici (vedi docs/SPEC.md §2).
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/apdsoftware/postqron/services/api/internal/config"
	"github.com/apdsoftware/postqron/services/api/internal/httpapi"
)

// version è sovrascrivibile al build: -ldflags "-X main.version=$(git rev-parse --short HEAD)".
var version = "dev"

func main() {
	if err := run(); err != nil {
		slog.Error("avvio del servizio fallito", slog.Any("error", err))
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	logger := newLogger(cfg)
	slog.SetDefault(logger)

	// Il contesto si chiude al primo SIGINT/SIGTERM: da lì parte lo shutdown.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	srv := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           httpapi.NewRouter(cfg, version, logger),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	errs := make(chan error, 1)
	go func() {
		logger.Info("server in ascolto",
			slog.String("addr", cfg.HTTPAddr),
			slog.String("env", cfg.Env),
			slog.String("version", version),
			// Postgres si redige da sé: la password non compare. Vedere all'avvio
			// host e porta effettivi è ciò che rende visibile subito un disallineamento
			// fra il container e la configurazione dell'API.
			slog.Any("postgres", cfg.Postgres))
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errs <- err
			return
		}
		errs <- nil
	}()

	select {
	case err := <-errs:
		return err
	case <-ctx.Done():
		logger.Info("segnale di arresto ricevuto, chiusura graceful",
			slog.Duration("timeout", cfg.ShutdownTimeout))
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return err
	}

	logger.Info("servizio arrestato")
	return nil
}

func newLogger(cfg config.Config) *slog.Logger {
	opts := &slog.HandlerOptions{Level: parseLevel(cfg.LogLevel)}
	// In produzione i log vanno in JSON per essere aggregabili; in sviluppo il
	// testo è più leggibile a terminale.
	if cfg.IsProduction() {
		return slog.New(slog.NewJSONHandler(os.Stdout, opts))
	}
	return slog.New(slog.NewTextHandler(os.Stdout, opts))
}

func parseLevel(name string) slog.Level {
	var level slog.Level
	if err := level.UnmarshalText([]byte(name)); err != nil {
		return slog.LevelInfo
	}
	return level
}
