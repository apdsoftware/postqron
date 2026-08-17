package config_test

import (
	"bytes"
	"log/slog"
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
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.with(base).DSN(); got != tc.want {
				t.Errorf("DSN() = %q, atteso %q", got, tc.want)
			}
		})
	}
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
