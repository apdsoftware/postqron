package config_test

import (
	"maps"
	"slices"
	"testing"
	"time"

	"github.com/apdsoftware/postqron/services/api/internal/config"
)

// envMap costruisce una Getenv da una mappa, così i test non toccano
// l'ambiente reale del processo.
func envMap(vars map[string]string) config.Getenv {
	return func(key string) string { return vars[key] }
}

func TestLoadFromDefaults(t *testing.T) {
	cfg, err := config.LoadFrom(envMap(nil))
	if err != nil {
		t.Fatalf("LoadFrom con ambiente vuoto: %v", err)
	}

	if cfg.Env != config.EnvDevelopment {
		t.Errorf("Env = %q, atteso %q", cfg.Env, config.EnvDevelopment)
	}
	if cfg.HTTPAddr != ":8080" {
		t.Errorf("HTTPAddr = %q, atteso \":8080\"", cfg.HTTPAddr)
	}
	if cfg.ShutdownTimeout != 15*time.Second {
		t.Errorf("ShutdownTimeout = %v, atteso 15s", cfg.ShutdownTimeout)
	}
	if cfg.IsProduction() {
		t.Error("IsProduction() = true con ambiente di default")
	}
	if len(cfg.AllowedOrigins) == 0 {
		t.Error("AllowedOrigins vuoto: atteso almeno il localhost di sviluppo")
	}

	// Fuori dalla produzione i default devono coincidere con quelli di
	// .env.example, così `make db-up` e l'API si trovano senza configurazione.
	want := config.Postgres{
		Host:     "127.0.0.1",
		Port:     "5432",
		Database: "postqron",
		User:     "postqron",
		SSLMode:  "disable",
	}
	if cfg.Postgres != want {
		t.Errorf("Postgres = %+v, atteso %+v", cfg.Postgres, want)
	}
}

func TestLoadFromOverrides(t *testing.T) {
	cfg, err := config.LoadFrom(envMap(map[string]string{
		"POSTQRON_ENV":              config.EnvProduction,
		"POSTQRON_HTTP_ADDR":        "127.0.0.1:9000",
		"POSTGRES_HOST":             "db.internal",
		"POSTGRES_PORT":             "5433",
		"POSTGRES_DB":               "postqron",
		"POSTGRES_USER":             "api",
		"POSTGRES_PASSWORD":         "s3gr3to",
		"POSTGRES_SSLMODE":          "require",
		"POSTQRON_ALLOWED_ORIGINS":  "https://postqron.com, https://app.postqron.com ,",
		"POSTQRON_SHUTDOWN_TIMEOUT": "30s",
		"POSTQRON_LOG_LEVEL":        "warn",
	}))
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}

	if !cfg.IsProduction() {
		t.Error("IsProduction() = false con POSTQRON_ENV=production")
	}
	if cfg.HTTPAddr != "127.0.0.1:9000" {
		t.Errorf("HTTPAddr = %q", cfg.HTTPAddr)
	}
	if cfg.ShutdownTimeout != 30*time.Second {
		t.Errorf("ShutdownTimeout = %v, atteso 30s", cfg.ShutdownTimeout)
	}
	if cfg.LogLevel != "warn" {
		t.Errorf("LogLevel = %q, atteso \"warn\"", cfg.LogLevel)
	}

	want := []string{"https://postqron.com", "https://app.postqron.com"}
	if !slices.Equal(cfg.AllowedOrigins, want) {
		t.Errorf("AllowedOrigins = %v, atteso %v (spazi e voci vuote da scartare)", cfg.AllowedOrigins, want)
	}

	const wantDSN = "postgres://api:s3gr3to@db.internal:5433/postqron?sslmode=require"
	if got := cfg.Postgres.DSN(); got != wantDSN {
		t.Errorf("DSN() = %q, atteso %q", got, wantDSN)
	}
}

// In produzione nessun default: un deploy che dimentica una variabile deve
// fallire all'avvio, non collegarsi al localhost della VPS.
func TestProduzioneRichiedeTutteLeVariabiliPostgres(t *testing.T) {
	complete := map[string]string{
		"POSTQRON_ENV":      config.EnvProduction,
		"POSTGRES_HOST":     "db.internal",
		"POSTGRES_PORT":     "5432",
		"POSTGRES_DB":       "postqron",
		"POSTGRES_USER":     "api",
		"POSTGRES_PASSWORD": "s3gr3to",
		"POSTGRES_SSLMODE":  "require",
	}
	if _, err := config.LoadFrom(envMap(complete)); err != nil {
		t.Fatalf("configurazione di produzione completa rifiutata: %v", err)
	}

	for _, missing := range []string{
		"POSTGRES_HOST", "POSTGRES_PORT", "POSTGRES_DB",
		"POSTGRES_USER", "POSTGRES_PASSWORD", "POSTGRES_SSLMODE",
	} {
		t.Run("senza "+missing, func(t *testing.T) {
			vars := maps.Clone(complete)
			delete(vars, missing)
			if _, err := config.LoadFrom(envMap(vars)); err == nil {
				t.Fatalf("LoadFrom senza %s non ha restituito errore", missing)
			}
		})
	}
}

func TestLoadFromErrors(t *testing.T) {
	cases := map[string]map[string]string{
		"ambiente sconosciuto": {
			"POSTQRON_ENV": "collaudo",
		},
		"indirizzo di ascolto senza porta": {
			"POSTQRON_HTTP_ADDR": "9000",
		},
		"produzione senza variabili POSTGRES_*": {
			"POSTQRON_ENV": config.EnvProduction,
		},
		"porta del database non numerica": {
			"POSTGRES_PORT": "cinquemilaquattrocentotrentadue",
		},
		"porta del database fuori intervallo": {
			"POSTGRES_PORT": "70000",
		},
		"sslmode inesistente": {
			"POSTGRES_SSLMODE": "diable",
		},
		"timeout non parsabile": {
			"POSTQRON_SHUTDOWN_TIMEOUT": "presto",
		},
		"timeout non positivo": {
			"POSTQRON_SHUTDOWN_TIMEOUT": "0s",
		},
	}

	for name, vars := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := config.LoadFrom(envMap(vars)); err == nil {
				t.Fatalf("LoadFrom(%v) non ha restituito errore", vars)
			}
		})
	}
}
