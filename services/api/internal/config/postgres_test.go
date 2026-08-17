package config_test

import (
	"bytes"
	"log/slog"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/apdsoftware/postqron/services/api/internal/config"
)

func TestDSN(t *testing.T) {
	base := config.Postgres{
		Host:     "127.0.0.1",
		Port:     "5432",
		Database: "postqron",
		User:     "postqron",
		Password: "postqron_local_dev",
		SSLMode:  "disable",
	}

	cases := []struct {
		name string
		with func(config.Postgres) config.Postgres
		want string
	}{
		{
			name: "connessione locale",
			with: func(p config.Postgres) config.Postgres { return p },
			want: "postgres://postqron:postqron_local_dev@127.0.0.1:5432/postqron?sslmode=disable",
		},
		{
			// Senza password url.UserPassword lascerebbe un "utente:" pendente.
			name: "senza password",
			with: func(p config.Postgres) config.Postgres { p.Password = ""; return p },
			want: "postgres://postqron@127.0.0.1:5432/postqron?sslmode=disable",
		},
		{
			// Una password con caratteri riservati non deve rompere il DSN: senza
			// escaping "@" e "/" verrebbero letti come separatori.
			name: "password con caratteri riservati",
			with: func(p config.Postgres) config.Postgres { p.Password = "p@ss/w:rd?#"; return p },
			want: "postgres://postqron:p%40ss%2Fw%3Ard%3F%23@127.0.0.1:5432/postqron?sslmode=disable",
		},
		{
			name: "host IPv6",
			with: func(p config.Postgres) config.Postgres { p.Host = "::1"; return p },
			want: "postgres://postqron:postqron_local_dev@[::1]:5432/postqron?sslmode=disable",
		},
		{
			name: "TLS obbligatorio",
			with: func(p config.Postgres) config.Postgres { p.SSLMode = "verify-full"; return p },
			want: "postgres://postqron:postqron_local_dev@127.0.0.1:5432/postqron?sslmode=verify-full",
		},
		{
			// Su socket Unix libpq vuole l'authority vuota e la directory nel
			// parametro `host`: dentro l'authority gli slash sarebbero separatori.
			name: "socket Unix",
			with: func(p config.Postgres) config.Postgres { p.Host = "/var/run/postgresql"; return p },
			want: "postgres://postqron:postqron_local_dev@/postqron" +
				"?host=%2Fvar%2Frun%2Fpostgresql&port=5432&sslmode=disable",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.with(base).DSN(); got != tc.want {
				t.Errorf("DSN() = %q, atteso %q", got, tc.want)
			}
		})
	}
}

var (
	localHosts  = []string{"localhost", "127.0.0.1", "127.0.0.53", "::1", "/var/run/postgresql"}
	remoteHosts = []string{"db.internal", "10.0.0.5", "203.0.113.7", "2001:db8::1"}

	// Non garantiscono la cifratura: disable non cifra, allow e prefer ripiegano
	// sul chiaro in silenzio se il server non offre TLS.
	unsafeSSLModes = []string{"disable", "allow", "prefer"}
	safeSSLModes   = []string{"require", "verify-ca", "verify-full"}
)

// In produzione la modalità TLS ammessa dipende dall'host. PostgreSQL gira sulla
// stessa VPS dell'API (SPEC §2), quindi la connessione locale in chiaro è quella
// che useremo davvero e non va vietata; su host remoto invece serve una modalità
// che garantisca la cifratura, non una che la negozi e ripieghi senza dirlo.
func TestSSLModeInProduzione(t *testing.T) {
	load := func(t *testing.T, env, host, sslmode string) error {
		t.Helper()
		_, err := config.LoadFrom(envMap(map[string]string{
			"POSTQRON_ENV":      env,
			"POSTGRES_HOST":     host,
			"POSTGRES_PORT":     "5432",
			"POSTGRES_DB":       "postqron",
			"POSTGRES_USER":     "api",
			"POSTGRES_PASSWORD": "s3gr3to",
			"POSTGRES_SSLMODE":  sslmode,
		}))
		return err
	}

	// Sull'host locale la regola non entra: la connessione non esce dalla macchina,
	// quindi ogni modalità resta lecita, `disable` compresa.
	t.Run("su host locale ogni modalità è ammessa", func(t *testing.T) {
		for _, host := range localHosts {
			for _, sslmode := range slices.Concat(unsafeSSLModes, safeSSLModes) {
				t.Run(host+"/"+sslmode, func(t *testing.T) {
					if err := load(t, config.EnvProduction, host, sslmode); err != nil {
						t.Errorf("host locale %q con sslmode=%s rifiutato in produzione: %v", host, sslmode, err)
					}
				})
			}
		}
	})

	t.Run("su host remoto le modalità non cifrate sono rifiutate", func(t *testing.T) {
		for _, host := range remoteHosts {
			for _, sslmode := range unsafeSSLModes {
				t.Run(host+"/"+sslmode, func(t *testing.T) {
					err := load(t, config.EnvProduction, host, sslmode)
					if err == nil {
						t.Fatalf("host remoto %q con sslmode=%s accettato in produzione", host, sslmode)
					}
					// Il messaggio deve dire *questo* problema, non un generico
					// "sslmode non valido": è ciò che rende l'errore azionabile.
					// Deve anche indicare la via d'uscita preferibile.
					want := []string{
						"POSTGRES_SSLMODE=" + strconv.Quote(sslmode),
						host,
						"in chiaro",
						"verify-full",
					}
					// Per allow e prefer il punto non è l'assenza di TLS ma il
					// ripiegamento muto: se il messaggio non lo dice, chi lo legge
					// non capisce perché una modalità che «usa TLS» sia rifiutata.
					if sslmode != "disable" {
						want = append(want, "senza segnalarlo")
					}
					for _, w := range want {
						if !strings.Contains(err.Error(), w) {
							t.Errorf("messaggio privo di %q: %v", w, err)
						}
					}
				})
			}
		}
	})

	t.Run("su host remoto le modalità cifrate sono ammesse", func(t *testing.T) {
		for _, host := range remoteHosts {
			for _, sslmode := range safeSSLModes {
				t.Run(host+"/"+sslmode, func(t *testing.T) {
					if err := load(t, config.EnvProduction, host, sslmode); err != nil {
						t.Errorf("host remoto %q con sslmode=%s rifiutato: %v", host, sslmode, err)
					}
				})
			}
		}
	})

	t.Run("nessun vincolo fuori dalla produzione", func(t *testing.T) {
		for _, env := range []string{config.EnvDevelopment, config.EnvStaging} {
			for _, sslmode := range unsafeSSLModes {
				t.Run(env+"/"+sslmode, func(t *testing.T) {
					if err := load(t, env, "db.internal", sslmode); err != nil {
						t.Errorf("host remoto con sslmode=%s rifiutato in %s: %v", sslmode, env, err)
					}
				})
			}
		}
	})
}

// La password non deve poter finire nei log nemmeno per distrazione (SPEC §5).
func TestLogValueRedigeLaPassword(t *testing.T) {
	const password = "una-password-riconoscibile"
	p := config.Postgres{
		Host:     "db.internal",
		Port:     "5432",
		Database: "postqron",
		User:     "api",
		Password: password,
		SSLMode:  "require",
	}

	var buf bytes.Buffer
	slog.New(slog.NewTextHandler(&buf, nil)).Info("connessione", slog.Any("postgres", p))

	logged := buf.String()
	if strings.Contains(logged, password) {
		t.Fatalf("la password è finita nel log: %s", logged)
	}
	// La redazione non deve però rendere il log inutile per il debug.
	for _, want := range []string{"db.internal", "5432", "postqron", "api", "require"} {
		if !strings.Contains(logged, want) {
			t.Errorf("log privo di %q, utile per il debug: %s", want, logged)
		}
	}
}
