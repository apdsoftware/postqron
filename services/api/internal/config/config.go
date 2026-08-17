// Package config carica e valida la configurazione del servizio a partire
// dalle variabili d'ambiente. Nessun valore di default contiene segreti: le
// credenziali sono sempre fornite dall'ambiente (vedi .env.example).
package config

import (
	"errors"
	"fmt"
	"net"
	"os"
	"slices"
	"strings"
	"time"
)

// Ambienti riconosciuti. `production` impone i vincoli più stretti.
const (
	EnvDevelopment = "development"
	EnvStaging     = "staging"
	EnvProduction  = "production"
)

var validEnvs = []string{EnvDevelopment, EnvStaging, EnvProduction}

// Config è la configurazione effettiva del processo API.
type Config struct {
	// Env è l'ambiente di esecuzione: development, staging o production.
	Env string
	// HTTPAddr è l'indirizzo di ascolto del server HTTP, in forma host:porta.
	HTTPAddr string
	// Postgres descrive la connessione al database.
	Postgres Postgres
	// AllowedOrigins elenca le origin ammesse dal CORS dei frontend statici.
	AllowedOrigins []string
	// ShutdownTimeout è il tempo massimo concesso alle richieste in corso
	// durante l'arresto graceful.
	ShutdownTimeout time.Duration
	// LogLevel è il livello minimo dei log strutturati.
	LogLevel string
}

// IsProduction indica se il processo gira in produzione.
func (c Config) IsProduction() bool { return c.Env == EnvProduction }

// Getenv è la firma di os.Getenv: parametrizzarla rende Load testabile senza
// sporcare l'ambiente del processo di test.
type Getenv func(string) string

// Load legge la configurazione dall'ambiente del processo.
func Load() (Config, error) { return LoadFrom(os.Getenv) }

// LoadFrom legge la configurazione da una qualsiasi sorgente chiave/valore.
func LoadFrom(getenv Getenv) (Config, error) {
	// L'ambiente si risolve per primo: da lui dipende quali default è lecito
	// applicare al resto della configurazione.
	env := stringOr(getenv, "POSTQRON_ENV", EnvDevelopment)
	if !slices.Contains(validEnvs, env) {
		return Config{}, fmt.Errorf("POSTQRON_ENV non valido: %q (ammessi: %v)", env, validEnvs)
	}

	cfg := Config{
		Env:             env,
		HTTPAddr:        stringOr(getenv, "POSTQRON_HTTP_ADDR", ":8080"),
		Postgres:        loadPostgres(getenv, env == EnvProduction),
		AllowedOrigins:  listOr(getenv, "POSTQRON_ALLOWED_ORIGINS", []string{"http://localhost:3000", "http://localhost:3001"}),
		ShutdownTimeout: 15 * time.Second,
		LogLevel:        stringOr(getenv, "POSTQRON_LOG_LEVEL", "info"),
	}

	if raw := getenv("POSTQRON_SHUTDOWN_TIMEOUT"); raw != "" {
		d, err := time.ParseDuration(raw)
		if err != nil {
			return Config{}, fmt.Errorf("POSTQRON_SHUTDOWN_TIMEOUT non è una durata valida: %w", err)
		}
		if d <= 0 {
			return Config{}, errors.New("POSTQRON_SHUTDOWN_TIMEOUT deve essere positivo")
		}
		cfg.ShutdownTimeout = d
	}

	if err := cfg.validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func (c Config) validate() error {
	if _, port, err := net.SplitHostPort(c.HTTPAddr); err != nil || port == "" {
		return fmt.Errorf("POSTQRON_HTTP_ADDR non è nella forma host:porta: %q", c.HTTPAddr)
	}
	if err := c.Postgres.validate(c.IsProduction()); err != nil {
		return err
	}
	return nil
}

// stringOr legge una variabile e ne rimuove gli spazi ai bordi: un valore
// composto di soli spazi equivale a variabile non impostata.
func stringOr(getenv Getenv, key, fallback string) string {
	if v := strings.TrimSpace(getenv(key)); v != "" {
		return v
	}
	return fallback
}

func listOr(getenv Getenv, key string, fallback []string) []string {
	raw := getenv(key)
	if raw == "" {
		return fallback
	}
	var out []string
	for item := range strings.SplitSeq(raw, ",") {
		if trimmed := strings.TrimSpace(item); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	if len(out) == 0 {
		return fallback
	}
	return out
}
