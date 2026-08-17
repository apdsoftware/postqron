// Package database apre e chiude la connessione a PostgreSQL.
//
// Il DSN non si compone qui: arriva da config.Postgres.DSN(), che è l'unico
// punto in cui esiste (AGENTS.md §7). Questo package si occupa del pool, dei
// timeout e di non far uscire la password negli errori.
package database

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/apdsoftware/postqron/services/api/internal/config"
)

// Valori di partenza del pool. Sono conservativi di proposito: in produzione
// PostgreSQL sta sulla stessa VPS dell'API (SPEC §2), dove le connessioni
// costano poco ma la memoria è condivisa con tutto il resto.
const (
	defaultMaxConns          = 10
	defaultMinConns          = 2
	defaultConnectTimeout    = 10 * time.Second
	defaultMaxConnLifetime   = time.Hour
	defaultMaxConnIdleTime   = 30 * time.Minute
	defaultHealthCheckPeriod = time.Minute
)

// Options permette a un chiamante con esigenze diverse dal servizio HTTP di
// dimensionare il pool. Il tool di migrazione, per esempio, usa una sola
// connessione: i lock consultivi sono legati alla sessione, e una seconda
// connessione non ne vedrebbe nemmeno l'esistenza.
type Options struct {
	// MaxConns è il numero massimo di connessioni del pool; 0 usa il default.
	MaxConns int32
	// MinConns è il numero di connessioni tenute aperte a riposo.
	MinConns int32
	// ApplicationName compare in `pg_stat_activity`: distinguere l'API dalle
	// migrazioni è ciò che rende leggibile quella vista quando qualcosa blocca.
	ApplicationName string
}

// Open apre il pool e verifica subito che il database risponda.
//
// La verifica immediata è deliberata: senza, il primo errore di connessione
// arriverebbe alla prima query utile, cioè a servizio già dichiarato pronto.
func Open(ctx context.Context, pg config.Postgres, opts Options) (*pgxpool.Pool, error) {
	poolCfg, err := pgxpool.ParseConfig(pg.DSN())
	if err != nil {
		return nil, fmt.Errorf("configurazione della connessione non valida: %w", redact(err, pg.Password))
	}

	poolCfg.MaxConns = defaultMaxConns
	if opts.MaxConns > 0 {
		poolCfg.MaxConns = opts.MaxConns
	}
	poolCfg.MinConns = defaultMinConns
	if opts.MinConns > 0 {
		poolCfg.MinConns = opts.MinConns
	}
	// Un minimo superiore al massimo farebbe rifiutare la configurazione dal
	// driver: chi chiede un pool da una connessione sta chiedendo anche questo.
	if poolCfg.MinConns > poolCfg.MaxConns {
		poolCfg.MinConns = poolCfg.MaxConns
	}

	poolCfg.MaxConnLifetime = defaultMaxConnLifetime
	poolCfg.MaxConnIdleTime = defaultMaxConnIdleTime
	poolCfg.HealthCheckPeriod = defaultHealthCheckPeriod
	poolCfg.ConnConfig.ConnectTimeout = defaultConnectTimeout

	if opts.ApplicationName != "" {
		if poolCfg.ConnConfig.RuntimeParams == nil {
			poolCfg.ConnConfig.RuntimeParams = map[string]string{}
		}
		poolCfg.ConnConfig.RuntimeParams["application_name"] = opts.ApplicationName
	}

	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		return nil, fmt.Errorf("apertura del pool fallita: %w", redact(err, pg.Password))
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("il database non risponde su %s:%s: %w",
			pg.Host, pg.Port, redact(err, pg.Password))
	}
	return pool, nil
}

// redact toglie la password dal testo di un errore.
//
// Gli errori del driver possono contenere la stringa di connessione, e un
// errore finisce nei log: la password non deve poterci arrivare, nemmeno per
// questa strada (SPEC §5).
func redact(err error, password string) error {
	if err == nil || password == "" {
		return err
	}
	text := err.Error()
	if !strings.Contains(text, password) {
		return err
	}
	return errors.New(strings.ReplaceAll(text, password, "xxxxx"))
}
