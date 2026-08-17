// Command api avvia il servizio HTTP di Postqron: API REST e, in seguito,
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

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/apdsoftware/postqron/services/api/internal/auth"
	"github.com/apdsoftware/postqron/services/api/internal/authpg"
	"github.com/apdsoftware/postqron/services/api/internal/config"
	"github.com/apdsoftware/postqron/services/api/internal/database"
	"github.com/apdsoftware/postqron/services/api/internal/dotenv"
	"github.com/apdsoftware/postqron/services/api/internal/httpapi"
	"github.com/apdsoftware/postqron/services/api/internal/jobs"
	"github.com/apdsoftware/postqron/services/api/internal/jobspg"
	"github.com/apdsoftware/postqron/services/api/internal/mailronix"
)

// version è sovrascrivibile al build: -ldflags "-X main.version=$(git rev-parse --short HEAD)".
var version = "dev"

// trustedProxiesEnvVar elenca le reti da cui accettare `X-Forwarded-For`.
//
// Non passa da internal/config perché quel package appartiene a un'altra issue;
// il posto giusto, a regime, è lì. Vedi httpapi.ClientIP per il motivo per cui
// serve: senza, dietro un reverse proxy il rate limiting del login mette tutti
// gli utenti nello stesso secchio.
const trustedProxiesEnvVar = "POSTQRON_TRUSTED_PROXIES"

func main() {
	if err := run(); err != nil {
		slog.Error("avvio del servizio fallito", slog.Any("error", err))
		os.Exit(1)
	}
}

func run() error {
	// In sviluppo la configurazione sta nel `.env` del monorepo, lo stesso file
	// con cui `make db-up` ha creato il container (AGENTS.md §7). In produzione
	// il file non esiste e l'ambiente basta: dotenv non sovrascrive mai una
	// variabile già impostata.
	workdir, err := os.Getwd()
	if err != nil {
		return err
	}
	if _, err := dotenv.LoadNearest(workdir); err != nil {
		return err
	}

	cfg, err := config.Load()
	if err != nil {
		return err
	}

	logger := newLogger(cfg)
	slog.SetDefault(logger)

	// Il contesto si chiude al primo SIGINT/SIGTERM: da lì parte lo shutdown.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pool, err := database.Open(ctx, cfg.Postgres, database.Options{
		ApplicationName: "postqron-api",
	})
	if err != nil {
		return err
	}
	defer pool.Close()

	authService, err := newAuthService(pool, workdir, cfg, logger)
	if err != nil {
		return err
	}

	jobsService, err := newJobsService(pool, logger)
	if err != nil {
		return err
	}

	trustedProxies, err := httpapi.ParseTrustedProxies(os.Getenv(trustedProxiesEnvVar))
	if err != nil {
		return err
	}

	srv := &http.Server{
		Addr: cfg.HTTPAddr,
		Handler: httpapi.NewRouter(cfg, version, logger, httpapi.Deps{
			Auth:           authService,
			Jobs:           jobsService,
			TrustedProxies: trustedProxies,
		}),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	errs := make(chan error, 1)
	go func() {
		logger.Info("server in ascolto",
			slog.String("addr", cfg.HTTPAddr),
			slog.String("env", cfg.Env),
			slog.String("version", version),
			slog.Int("trusted_proxies", len(trustedProxies)),
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
	// Le email di conferma e di recupero password partono fuori dal percorso
	// della richiesta (è ciò che rende costante il tempo di risposta): l'arresto
	// le aspetta, altrimenti un riavvio farebbe sparire in silenzio i messaggi
	// delle ultime richieste servite.
	if err := authService.Shutdown(shutdownCtx); err != nil {
		logger.Warn("attività in coda non completate prima dell'arresto", slog.Any("error", err))
	}

	logger.Info("servizio arrestato")
	return nil
}

// newAuthService costruisce l'autenticazione (R14).
func newAuthService(pool *pgxpool.Pool, workdir string, cfg config.Config, logger *slog.Logger) (*auth.Service, error) {
	store, err := authpg.New(pool)
	if err != nil {
		return nil, err
	}
	hasher, err := auth.NewHasher(auth.DefaultParams)
	if err != nil {
		return nil, err
	}
	keyring, err := auth.KeyringFromEnv(os.Getenv)
	if err != nil {
		return nil, err
	}
	mailer, err := newMailer(workdir, cfg, logger)
	if err != nil {
		return nil, err
	}
	return auth.NewService(auth.Options{
		Store:   store,
		Hasher:  hasher,
		Keyring: keyring,
		Mailer:  mailer,
		Logger:  logger,
	})
}

// newMailer costruisce il recapito delle email (R20, issue #419).
//
// Senza MAILRONIX_API_KEY si ripiega su [auth.LogMailer], che registra e non
// recapita: in sviluppo nessuno vuole che `go run ./cmd/api` pretenda la chiave
// di un servizio esterno, e chi ha bisogno di un token di reset lo legge dal
// database. In produzione, invece, la stessa mancanza è un errore d'avvio: un
// servizio che accetta registrazioni senza poter mandare l'email di verifica è
// rotto in un modo che nessuno si accorge di aver causato.
func newMailer(workdir string, cfg config.Config, logger *slog.Logger) (auth.Mailer, error) {
	mailer, err := mailronix.NewMailerFromEnv(os.Getenv, workdir, logger)
	switch {
	case err == nil:
		return mailer, nil
	case errors.Is(err, mailronix.ErrNotConfigured) && !cfg.IsProduction():
		logger.Warn("email non recapitate: "+mailronix.EnvAPIKey+" non impostata",
			slog.String("env", cfg.Env))
		return auth.LogMailer{Logger: logger}, nil
	default:
		return nil, err
	}
}

// newJobsService costruisce le rotte dei job (R8).
//
// Guard e Dispatcher restano nil, ed è il punto in cui due issue si innestano:
// il blocco SSRF di R38 (#455) fornirà un [jobs.TargetGuard], e il worker pool
// (#389) un [jobs.Dispatcher] che esegue le occorrenze manuali. Vedi
// [jobs.Dispatcher] per il motivo per cui la seconda **non** è opzionale come
// sembra: lo scheduler di #388 non raccoglie i trigger manuali, per scelta
// dichiarata.
func newJobsService(pool *pgxpool.Pool, logger *slog.Logger) (*jobs.Service, error) {
	store, err := jobspg.New(pool)
	if err != nil {
		return nil, err
	}
	return jobs.NewService(jobs.Options{Store: store, Logger: logger})
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
