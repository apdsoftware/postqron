// Command api avvia il servizio HTTP di Postqron: API REST e motore cron nello
// stesso processo. È l'unica origin dinamica del prodotto — i frontend sono
// statici (vedi docs/SPEC.md §2).
//
// I due convivono deliberatamente: API, motore e database stanno sulla stessa
// macchina (SPEC §2, §11 R49), e separarli in due binari significherebbe due
// pool di connessioni verso lo stesso PostgreSQL e due unità da tenere allineate
// in cambio di nessun isolamento vero. Il collegamento sta in engine.go.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/apdsoftware/postqron/services/api/internal/apikeypg"
	"github.com/apdsoftware/postqron/services/api/internal/apikeys"
	"github.com/apdsoftware/postqron/services/api/internal/auth"
	"github.com/apdsoftware/postqron/services/api/internal/authpg"
	"github.com/apdsoftware/postqron/services/api/internal/billing"
	"github.com/apdsoftware/postqron/services/api/internal/billingpg"
	"github.com/apdsoftware/postqron/services/api/internal/config"
	"github.com/apdsoftware/postqron/services/api/internal/cronyaml"
	"github.com/apdsoftware/postqron/services/api/internal/database"
	"github.com/apdsoftware/postqron/services/api/internal/dispatch"
	"github.com/apdsoftware/postqron/services/api/internal/dotenv"
	"github.com/apdsoftware/postqron/services/api/internal/emailrender"
	"github.com/apdsoftware/postqron/services/api/internal/githubapp"
	"github.com/apdsoftware/postqron/services/api/internal/githubhook"
	"github.com/apdsoftware/postqron/services/api/internal/githubhookpg"
	"github.com/apdsoftware/postqron/services/api/internal/health"
	"github.com/apdsoftware/postqron/services/api/internal/httpapi"
	"github.com/apdsoftware/postqron/services/api/internal/jobs"
	"github.com/apdsoftware/postqron/services/api/internal/jobspg"
	"github.com/apdsoftware/postqron/services/api/internal/mailronix"
	"github.com/apdsoftware/postqron/services/api/internal/metrics"
	"github.com/apdsoftware/postqron/services/api/internal/netguard"
	"github.com/apdsoftware/postqron/services/api/internal/notify"
	"github.com/apdsoftware/postqron/services/api/internal/notifypg"
	"github.com/apdsoftware/postqron/services/api/internal/paddle"
	"github.com/apdsoftware/postqron/services/api/internal/paddlepg"
	"github.com/apdsoftware/postqron/services/api/internal/reposync"
	"github.com/apdsoftware/postqron/services/api/internal/reposyncpg"
	"github.com/apdsoftware/postqron/services/api/internal/retention"
	"github.com/apdsoftware/postqron/services/api/internal/scheduler"
	"github.com/apdsoftware/postqron/services/api/internal/secretbox"
	"github.com/apdsoftware/postqron/services/api/internal/secrets"
	"github.com/apdsoftware/postqron/services/api/internal/secretspg"
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

	// La coda delle notifiche (R21) si costruisce **prima di tutto ciò che la
	// usa**: autenticazione, chiavi API, motore e fatturazione ci si agganciano
	// tutti, e sono quattro punti che devono vedere la stessa coda.
	//
	// Accodare non richiede Mailronix: serve solo il database. È il motivo per
	// cui questa riga non ha un ramo per la macchina di sviluppo — la coda c'è
	// sempre, ed è il corriere a poter mancare.
	notifyService, err := notifypg.NewService(pool, logger)
	if err != nil {
		return err
	}
	sinks, err := newNotifySinks(notifyService)
	if err != nil {
		return err
	}

	authService, err := newAuthService(pool, cfg, logger, sinks.auth)
	if err != nil {
		return err
	}

	// Un solo guard per tutto il processo: lo stesso che rifiuta un URL alla
	// creazione del job, che anticipa il rifiuto prima di occupare un worker, e
	// il cui trasporto è l'unico da cui esce traffico verso i bersagli (R38).
	// Costruirne uno per punto d'innesto significherebbe tre politiche libere di
	// divergere, e nessun posto in cui accorgersene.
	guard := netguard.New(netguard.Options{Logger: logger})

	secretsService, err := newSecretsService(pool, logger)
	if err != nil {
		return err
	}

	// L'osservabilità (R7) si costruisce **prima** del motore, perché è il motore
	// a essere osservato: un registro innestato dopo perderebbe le passate e i
	// fallimenti dei primi istanti di vita del processo, che sono quelli in cui
	// un deploy andato male si vede.
	registry := metrics.New(metrics.Options{
		Version:   version,
		Env:       cfg.Env,
		Tolerance: scheduler.DefaultTolerance,
	})

	eng, err := newEngine(engineOptions{
		Pool:    pool,
		Logger:  logger,
		Clients: guard,
		Targets: guard,
		Secrets: secretsService,
		Alerter: sinks.failures,
		Metrics: registry,
	})
	if err != nil {
		return err
	}
	registry.UsePool(eng.Workers())

	// Le sonde di prontezza. `Engine` è il registro perché il battito dello
	// scheduler — l'istante dell'ultima passata **riuscita** — lo conosce chi
	// osserva le passate, non lo scheduler, che le espone e va avanti.
	healthSvc, err := health.New(health.Options{
		Pool:   pool,
		Engine: registry,
		Logger: logger,
	})
	if err != nil {
		return err
	}
	registry.UseHealth(healthSvc)

	jobsService, err := newJobsService(pool, logger, guard, eng.Manual(), eng.Concurrency())
	if err != nil {
		return err
	}

	apiKeysService, err := newAPIKeysService(pool, logger, sinks.keys)
	if err != nil {
		return err
	}

	trustedProxies, err := httpapi.ParseTrustedProxies(os.Getenv(trustedProxiesEnvVar))
	if err != nil {
		return err
	}

	// Il sink delle push: la riconciliazione di `cron.yaml` (R13, issue #423).
	// nil se la GitHub App non è configurata — vedi newRepoSyncService.
	repoSync, err := newRepoSyncService(pool, logger, guard, secretsService)
	if err != nil {
		return err
	}

	// Il webhook GitHub (R11, issue #421). Il residuo di cablaggio è qui perché
	// #421 aveva cmd/api fra i propri percorsi vietati.
	//
	// **Senza `GITHUB_APP_WEBHOOK_SECRET` il servizio è nil**, e la rotta non
	// viene registrata affatto: una macchina senza GitHub App non deve ottenere
	// un endpoint pubblico che accetta qualunque corpo, deve non ottenere
	// l'endpoint. Per questo l'assenza del segreto non è un errore d'avvio.
	//
	// Il sink può essere nil a sua volta, e i due nil sono indipendenti: il
	// segreto del webhook e la chiave privata dell'App si configurano con
	// variabili diverse, e chi ha impostato solo la prima ottiene un endpoint
	// che verifica e registra le push senza sincronizzarle — che è ciò che
	// [githubhook.Service] fa quando il sink manca, registrandole come
	// `ignored` invece che come lavorate.
	gitHubWebhook, err := githubhookpg.NewService(pool, os.Getenv, logger, sinkOrNil(repoSync))
	if err != nil {
		return err
	}

	// La fatturazione (R16, R58). Il servizio si costruisce sempre: senza
	// catalogo Paddle non si vende — e `CanSell` lo dice — ma il piano in forza
	// va comunque letto, e un evento che arrivasse su una macchina senza catalogo
	// deve fallire rumorosamente invece di essere ignorato.
	billingService, err := billingpg.NewService(pool, os.Getenv, logger, sinks.plans)
	if err != nil {
		return err
	}
	if !billingService.CanSell() {
		logger.Warn("checkout non disponibile: catalogo Paddle o token del checkout non configurati",
			slog.String("env", "PADDLE_PRICE_*, PADDLE_CLIENT_TOKEN"))
	}

	// Il webhook Paddle (R16). **Senza `PADDLE_WEBHOOK_SECRET` il servizio è
	// nil**, e la rotta non viene registrata affatto: vale la stessa regola del
	// webhook GitHub, con la posta in gioco più alta. Un endpoint di
	// fatturazione che accetta corpi non verificati è un modo per farsi regalare
	// un piano a pagamento da chiunque ne conosca l'indirizzo, quindi l'assenza
	// del segreto non è un errore d'avvio ma la rinuncia alla rotta.
	paddleWebhook, err := paddlepg.NewService(pool, os.Getenv, logger, sinkOrNilEntitlement(billingService))
	if err != nil {
		return err
	}

	srv := &http.Server{
		Addr: cfg.HTTPAddr,
		Handler: httpapi.NewRouter(cfg, version, logger, httpapi.Deps{
			Auth:    authService,
			Jobs:    jobsService,
			APIKeys: apiKeysService,
			// Le rotte `/secrets` (R42) sono lo stesso servizio che il motore usa per
			// risolvere: un segreto che l'utente non può creare è un segreto che il
			// motore non risolverà mai.
			Secrets:        secretsService,
			GitHubWebhook:  gitHubWebhook,
			PaddleWebhook:  paddleWebhook,
			Billing:        billingService,
			TrustedProxies: trustedProxies,

			// R7. Il token è la credenziale di chi gestisce il servizio, non di un
			// utente del prodotto: senza, `/metrics` non viene registrata affatto e
			// `/readyz` dice il solo stato complessivo. Vedi httpapi/observability.go.
			Readiness:    healthSvc,
			Metrics:      registry,
			MetricsToken: os.Getenv(httpapi.MetricsTokenEnvVar),
		}),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	// La retention dei log (R6, issue #393) parte **prima** del motore.
	//
	// Non è una preferenza estetica: la sua prima passata prepara le partizioni
	// giornaliere di `job_executions`, e senza una partizione disponibile
	// l'inserimento di un'esecuzione fallisce (`23514`). La 0006 ne crea
	// quattordici in avanti al momento della migrazione e poi non le crea più
	// nessuno — un processo che riparte dopo un fermo lungo troverebbe una
	// tabella su cui non si può scrivere. L'ordine non è comunque una garanzia,
	// e non ha bisogno di esserlo: un inserimento fallito lascia l'occorrenza non
	// accodata, e la passata successiva dello scheduler la riprende.
	//
	// Questo residuo di cablaggio sta qui perché #393 aveva `cmd/api` fra i
	// percorsi di altri: le funzioni della 0006 esistevano dalla prima
	// migrazione e non le invocava nessuno, che è esattamente il modo in cui una
	// promessa scritta nella privacy policy resta non mantenuta.
	retentionSvc, err := retention.New(retention.Options{
		Pool:     pool,
		Logger:   logger,
		Observer: registry,
	})
	if err != nil {
		return err
	}
	retentionStopped := make(chan struct{})
	go func() {
		defer close(retentionStopped)
		runLoop(ctx, logger, "retention", retentionSvc.Run)
	}()

	// Le sonde di prontezza (R7). Girano accanto al motore e non dentro di esso:
	// sono un ticker che interroga il database, e non deve poter occupare un
	// worker.
	//
	// Partono **prima** del motore per una ragione precisa: `/readyz`
	// risponde «non pronto» finché non hanno guardato, e farle partire dopo
	// significherebbe tenere il servizio dichiarato non pronto per tutto il tempo
	// dell'avvio del motore.
	observabilityStopped := make(chan struct{})
	go func() {
		defer close(observabilityStopped)
		runLoop(ctx, logger, "prontezza", healthSvc.Run)
	}()

	// Il corriere delle email (R20, R21). Il costruttore va chiamato **prima**
	// che il server parta anche quando non c'è niente da avviare: senza
	// MAILRONIX_API_KEY in produzione è un errore d'avvio, e scoprirlo mentre il
	// servizio già accetta registrazioni è troppo tardi.
	courier, err := newCourier(pool, workdir, cfg, logger)
	if err != nil {
		return err
	}
	courierStopped := make(chan struct{})
	if courier == nil {
		close(courierStopped)
	} else {
		go func() {
			defer close(courierStopped)
			if err := courier.Run(ctx, notify.DefaultInterval); err != nil && !errors.Is(err, context.Canceled) {
				logger.Error("notifiche: ciclo del corriere interrotto", slog.Any("error", err))
			}
		}()
	}

	// Il motore parte prima del server: le occorrenze rimaste in sospeso da una
	// vita precedente del processo vengono riprese subito (scheduler.Engine.Recover),
	// e un job creato al primo secondo di vita dell'API trova il motore già in
	// piedi invece di aspettare la passata successiva.
	eng.Start(ctx)

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

	var listenErr error
	select {
	case listenErr = <-errs:
		// Il server non è partito, o è caduto. Il motore invece sta girando: va
		// fermato lo stesso, altrimenti resterebbero esecuzioni `running` che
		// nessuno chiude più. `stop` chiude il contesto del processo, che è il
		// segnale con cui lo scheduler esce dal proprio ciclo.
		stop()
	case <-ctx.Done():
		logger.Info("segnale di arresto ricevuto, chiusura graceful",
			slog.Duration("timeout", cfg.ShutdownTimeout))
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return errors.Join(listenErr, err)
	}
	// Il motore si ferma **dopo** il server, e l'ordine non è arbitrario: finché
	// l'API accetta richieste, un trigger manuale può ancora arrivare al worker
	// pool, e un pool già chiuso lo rifiuterebbe lasciando la riga `pending` per
	// sempre — nessuno riprende un «esegui adesso» (vedi manualDispatcher).
	if err := eng.Shutdown(shutdownCtx); err != nil {
		// Non è un guasto: le esecuzioni interrotte sono tornate `pending` e il
		// recupero le riprenderà al riavvio. È il fatto da lasciare scritto,
		// invece di doverlo dedurre dalle esecuzioni che ricompaiono.
		logger.Warn("esecuzioni interrotte dall'arresto del motore", slog.Any("error", err))
	}
	// Le email di conferma e di recupero password partono fuori dal percorso
	// della richiesta (è ciò che rende costante il tempo di risposta): l'arresto
	// le aspetta, altrimenti un riavvio farebbe sparire in silenzio i messaggi
	// delle ultime richieste servite.
	if err := authService.Shutdown(shutdownCtx); err != nil {
		logger.Warn("attività in coda non completate prima dell'arresto", slog.Any("error", err))
	}
	// La retention si aspetta perché usa lo stesso pool, che viene chiuso
	// all'uscita di run: un lotto ancora in volo su un pool chiuso produrrebbe
	// un errore che non descrive niente. Ciò che il contesto ha interrotto a
	// metà non è perso — sono righe scadute che restano scadute, e le porta via
	// la prima passata dopo il riavvio.
	select {
	case <-retentionStopped:
	case <-shutdownCtx.Done():
		logger.Warn("retention: passata non conclusa entro il tempo dell'arresto")
	}
	// Il corriere si aspetta per la stessa ragione, con una in più: una notifica
	// consegnata a Mailronix e non ancora chiusa in coda tornerebbe disponibile
	// alla scadenza del contratto e partirebbe una seconda volta. Aspettare la
	// passata in corso è ciò che rende quel doppione raro invece che sistematico
	// a ogni riavvio.
	select {
	case <-courierStopped:
	case <-shutdownCtx.Done():
		logger.Warn("notifiche: passata non conclusa entro il tempo dell'arresto")
	}
	// Le sonde di prontezza usano lo stesso pool, e vale lo stesso discorso.
	select {
	case <-observabilityStopped:
	case <-shutdownCtx.Done():
		logger.Warn("prontezza: sonde non concluse entro il tempo dell'arresto")
	}

	logger.Info("servizio arrestato")
	return listenErr
}

// runLoop fa girare un ciclo di servizio e ne registra l'uscita anomala.
//
// La chiusura del contesto non è anomala: è l'arresto del processo, ed è così
// che ogni ciclo esce. Scriverla nei log come un errore renderebbe illeggibile
// l'arresto pulito, che è il momento in cui si guardano i log per capire se
// qualcosa non ha fatto in tempo.
func runLoop(ctx context.Context, logger *slog.Logger, name string, run func(context.Context) error) {
	if err := run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		logger.Error(name+": ciclo interrotto", slog.Any("error", err))
	}
}

// newAuthService costruisce l'autenticazione (R14).
func newAuthService(
	pool *pgxpool.Pool,
	cfg config.Config,
	logger *slog.Logger,
	mailer auth.Mailer,
) (*auth.Service, error) {
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
	return auth.NewService(auth.Options{
		Store:   store,
		Hasher:  hasher,
		Keyring: keyring,
		Mailer:  mailer,
		Logger:  logger,
	})
}

// notifySinks sono i quattro punti in cui i domini si attaccano alla coda (R21).
//
// Stanno insieme perché sono la stessa decisione presa quattro volte: chi
// produce il fatto dichiara l'interfaccia, internal/notify la implementa, e
// `cmd/api` è l'unico posto in cui i due si incontrano. Nessun package di
// dominio importa internal/notify.
type notifySinks struct {
	auth     auth.Mailer
	keys     apikeys.SecurityNotifier
	plans    billing.Notifier
	failures dispatch.Alerter
}

func newNotifySinks(service *notify.Service) (notifySinks, error) {
	var sinks notifySinks
	var err error
	if sinks.auth, err = notify.NewAuthMailer(service); err != nil {
		return notifySinks{}, err
	}
	if sinks.keys, err = notify.NewKeySink(service); err != nil {
		return notifySinks{}, err
	}
	if sinks.plans, err = notify.NewPlanSink(service); err != nil {
		return notifySinks{}, err
	}
	if sinks.failures, err = notify.NewFailureSink(service); err != nil {
		return notifySinks{}, err
	}
	return sinks, nil
}

// newCourier costruisce chi svuota la coda e recapita (R20, R21).
//
// **Restituisce (nil, nil) senza MAILRONIX_API_KEY fuori dalla produzione.** È
// la stessa scelta che c'era prima con [auth.LogMailer], spostata di un livello
// e migliorata: in sviluppo nessuno vuole che `go run ./cmd/api` pretenda la
// chiave di un servizio esterno, e adesso ciò che *sarebbe* stato mandato non si
// perde in una riga di log — resta in `notifications`, leggibile con una SELECT.
//
// In produzione la stessa mancanza resta un errore d'avvio: un servizio che
// accetta registrazioni senza poter mandare l'email di verifica è rotto in un
// modo che nessuno si accorge di aver causato.
func newCourier(
	pool *pgxpool.Pool,
	workdir string,
	cfg config.Config,
	logger *slog.Logger,
) (*notify.Courier, error) {
	cfgMailronix, err := mailronix.LoadConfig(os.Getenv)
	switch {
	case errors.Is(err, mailronix.ErrNotConfigured) && !cfg.IsProduction():
		logger.Warn("email non recapitate: "+mailronix.EnvAPIKey+" non impostata. "+
			"Le notifiche restano in coda nella tabella `notifications`",
			slog.String("env", cfg.Env))
		return nil, nil
	case err != nil:
		return nil, err
	}

	client, err := mailronix.New(cfgMailronix, mailronix.WithLogger(logger))
	if err != nil {
		return nil, err
	}

	dir, err := emailrender.FindDir(workdir)
	if err != nil {
		return nil, fmt.Errorf("template delle email: %w", err)
	}
	renderer, err := emailrender.NewFromDir(dir, mailronix.SiteFromEnv(os.Getenv))
	if err != nil {
		return nil, fmt.Errorf("template delle email: %w", err)
	}

	store, err := notifypg.New(pool)
	if err != nil {
		return nil, err
	}
	return notify.NewCourier(notify.CourierOptions{
		Queue:    store,
		Renderer: renderer,
		Sender:   client,
		Logger:   logger,
	})
}

// newAPIKeysService costruisce le chiavi API (R9).
//
// Il keyring è lo stesso dell'autenticazione, ricavato dallo stesso
// SESSION_SECRET: le impronte delle chiavi API e quelle dei token di sessione
// sono HMAC sotto chiavi HKDF diverse derivate da lì (vedi
// [auth.Keyring.APIKeyHash]). La conseguenza operativa va conosciuta:
// **cambiare SESSION_SECRET invalida anche tutte le chiavi API**, non solo le
// sessioni. È la stessa leva d'emergenza, con un costo più alto — le chiavi le
// devono rigenerare gli utenti — e per questo va usata sapendolo.
//
// `Users` è lo store dell'autenticazione: risolvere il proprietario di una
// chiave è leggere una riga di `users`, e duplicare quella query qui farebbe
// divergere le due letture al primo cambio di `deleted_at`.
func newAPIKeysService(
	pool *pgxpool.Pool,
	logger *slog.Logger,
	notifier apikeys.SecurityNotifier,
) (*apikeys.Service, error) {
	store, err := apikeypg.New(pool)
	if err != nil {
		return nil, err
	}
	users, err := authpg.New(pool)
	if err != nil {
		return nil, err
	}
	keyring, err := auth.KeyringFromEnv(os.Getenv)
	if err != nil {
		return nil, err
	}
	return apikeys.NewService(apikeys.Options{
		Store:    store,
		Users:    users,
		Keyring:  keyring,
		Notifier: notifier,
		Logger:   logger,
	})
}

// newJobsService costruisce le rotte dei job (R8).
//
// Il guard arriva da fuori invece di lasciare che [jobs.NewService] si costruisca
// il proprio: nil significherebbe comunque il blocco predefinito (#455), ma
// sarebbe un *secondo* guard, con una politica libera di divergere da quella con
// cui il motore esegue davvero le chiamate. Uno solo, quello di run.
//
// Dispatcher è l'adattatore del motore (#486): da qui in poi un trigger manuale
// non è più una riga che nessuno raccoglie. Vedi [jobs.Dispatcher] per il motivo
// per cui **non** è opzionale come sembra — lo scheduler di #388 non raccoglie i
// trigger manuali, per scelta dichiarata.
//
// Concurrency è il tetto tecnico sulle esecuzioni contemporanee (R10): lo
// conosce il worker pool, che è l'unico a sapere cosa è in volo, e serve al
// trigger manuale per rifiutare invece di promettere un «adesso» che sarebbe una
// coda.
func newJobsService(
	pool *pgxpool.Pool,
	logger *slog.Logger,
	guard jobs.TargetGuard,
	dispatcher jobs.Dispatcher,
	concurrency jobs.Concurrency,
) (*jobs.Service, error) {
	store, err := jobspg.New(pool)
	if err != nil {
		return nil, err
	}
	return jobs.NewService(jobs.Options{
		Store:       store,
		Logger:      logger,
		Guard:       guard,
		Dispatcher:  dispatcher,
		Concurrency: concurrency,
	})
}

// newRepoSyncService costruisce la riconciliazione di `cron.yaml` (R13).
//
// **Restituisce (nil, nil) se la GitHub App non è configurata**, come già fa
// [githubhookpg.NewService] per il segreto del webhook: leggere un file da un
// repository richiede la chiave privata dell'App, e senza non c'è un modo
// degradato di farlo. La macchina di sviluppo di chi non ha l'App si avvia lo
// stesso, e il log lo dice.
//
// Le tre dipendenze che non sono lo store vengono da fuori, e nessuna delle tre
// per comodità:
//
//   - il **piano** è [jobspg.Store], che è la stessa query da cui l'API legge i
//     limiti: due letture del listino divergerebbero, e la differenza si
//     noterebbe come un `every: 1s` accettato dal sync e rifiutato dal form;
//   - i **segreti** sono lo stesso servizio delle rotte `/secrets` e del motore,
//     per la ragione scritta in newSecretsService — qui serve solo l'elenco dei
//     nomi, che non decifra niente;
//   - il **guard** è quello del processo (R38), lo stesso che apre le
//     connessioni verso i bersagli.
func newRepoSyncService(
	pool *pgxpool.Pool,
	logger *slog.Logger,
	guard jobs.TargetGuard,
	secretsService *secrets.Service,
) (*reposync.Service, error) {
	// Il tetto è quello del parser: scaricare più byte di quanti `cron.yaml`
	// possa averne significherebbe leggere memoria per poi rifiutarla.
	contents, err := githubapp.LoadFromEnv(os.Getenv, githubapp.Options{
		MaxBytes: cronyaml.MaxFileSize,
		Logger:   logger,
	})
	if err != nil {
		return nil, err
	}
	if contents == nil {
		logger.Warn("sync di cron.yaml non attivo: GitHub App non configurata",
			slog.String("env", githubapp.AppIDEnvVar+", "+githubapp.PrivateKeyPathEnvVar))
		return nil, nil
	}

	store, err := reposyncpg.New(pool)
	if err != nil {
		return nil, err
	}
	plans, err := jobspg.New(pool)
	if err != nil {
		return nil, err
	}

	return reposync.NewService(reposync.Options{
		Store:    store,
		Plans:    plans,
		Secrets:  secretsService,
		Contents: contents,
		Guard:    guard,
		Logger:   logger,
	})
}

// sinkOrNil converte un servizio assente in un'interfaccia **davvero** nil.
//
// Non è una precauzione teorica: un `(*reposync.Service)(nil)` assegnato a
// [githubhook.PushSink] produce un'interfaccia non nil con dentro un puntatore
// nil, il controllo `sink == nil` di quel package non scatta, e la prima push
// che arriva chiama un metodo su un ricevitore nil. Il caso è esattamente
// quello di una macchina senza GitHub App, cioè quello in cui il difetto non si
// noterebbe fino al primo cliente collegato.
func sinkOrNil(svc *reposync.Service) githubhook.PushSink {
	if svc == nil {
		return nil
	}
	return svc
}

// sinkOrNilEntitlement converte un servizio assente in un'interfaccia **davvero**
// nil, per la stessa ragione di [sinkOrNil]: un `(*billing.Service)(nil)`
// assegnato a [paddle.EntitlementSink] produce un'interfaccia non nil con dentro
// un puntatore nil, e il primo evento che arriva chiamerebbe un metodo su un
// ricevitore nil.
//
// Oggi il servizio non è mai nil — si costruisce sempre — ma la conversione
// resta esplicita: il giorno in cui qualcuno lo rendesse opzionale, il difetto
// si manifesterebbe alla prima consegna di un cliente vero.
func sinkOrNilEntitlement(svc *billing.Service) paddle.EntitlementSink {
	if svc == nil {
		return nil
	}
	return svc
}

// newSecretsService costruisce i segreti del workspace (R42, R43).
//
// Il residuo di cablaggio è qui perché #458 aveva cmd/api fra i propri percorsi
// vietati, e la issue #496 — che collega la risoluzione all'esecuzione — non può
// farne a meno: senza questo servizio l'esecutore non parte affatto, per la
// ragione scritta in [httpexec.Options].
//
// Il servizio è **uno solo** per il processo, e serve due utenti diversi: le
// rotte `/secrets`, da cui l'utente li crea, e il motore, che li risolve al
// momento di eseguire. Due istanze sarebbero due keyring liberi di divergere,
// cioè un segreto creato con una chiave e illeggibile con l'altra.
//
// `ENCRYPTION_KEY` diventa così obbligatoria all'avvio (vedi [secretbox.EnvVar]).
// Non c'è un modo sensato di degradare: senza chiave i segreti non si decifrano,
// e un motore che eseguisse lo stesso manderebbe al bersaglio dell'utente la
// stringa `${TOKEN}` al posto della sua credenziale.
func newSecretsService(pool *pgxpool.Pool, logger *slog.Logger) (*secrets.Service, error) {
	store, err := secretspg.New(pool)
	if err != nil {
		return nil, err
	}
	keyring, err := secretbox.KeyringFromEnv(os.Getenv)
	if err != nil {
		return nil, err
	}
	return secrets.NewService(secrets.Options{
		Store:   store,
		Keyring: keyring,
		Logger:  logger,
	})
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
