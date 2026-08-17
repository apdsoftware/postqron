package main

import (
	"strings"
	"testing"

	"github.com/apdsoftware/postqron/services/api/internal/dotenv"
)

// `make migrate` verifica l'identità del container leggendo il `.env`
// (scripts/db-guard.sh), mentre il tool darebbe la precedenza all'ambiente: se i
// due divergono, la guardia controlla un server e la migrazione ne tocca un
// altro. Il comando deve fermarsi, non scegliere.
func TestConnectionConflictsStopTheCommand(t *testing.T) {
	err := refuseConnectionConflicts([]dotenv.Conflict{
		{Key: "POSTGRES_PORT", Environment: "15432", File: "5433"},
	})
	if err == nil {
		t.Fatal("atteso un rifiuto su POSTGRES_PORT divergente")
	}
	for _, fragment := range []string{"POSTGRES_PORT", "15432", "5433"} {
		if !strings.Contains(err.Error(), fragment) {
			t.Errorf("l'errore non riporta %q: %v", fragment, err)
		}
	}
}

// Solo le variabili che decidono su quale database si scrive giustificano un
// blocco: il resto è configurazione applicativa, e sovrascriverla dall'ambiente
// è legittimo.
func TestOnlyConnectionVariablesStopTheCommand(t *testing.T) {
	tests := map[string][]dotenv.Conflict{
		"nessun conflitto": nil,
		"solo applicative": {
			{Key: "POSTQRON_LOG_LEVEL", Environment: "debug", File: "info"},
			{Key: "NUXT_PUBLIC_SITE_URL", Environment: "http://a", File: "http://b"},
		},
	}
	for name, conflicts := range tests {
		t.Run(name, func(t *testing.T) {
			if err := refuseConnectionConflicts(conflicts); err != nil {
				t.Errorf("blocco inatteso: %v", err)
			}
		})
	}
}

func TestConnectionConflictsAreAllReported(t *testing.T) {
	err := refuseConnectionConflicts([]dotenv.Conflict{
		{Key: "POSTGRES_HOST", Environment: "10.0.0.1", File: "127.0.0.1"},
		{Key: "POSTQRON_LOG_LEVEL", Environment: "debug", File: "info"},
		{Key: "POSTGRES_DB", Environment: "altro", File: "postqron"},
	})
	if err == nil {
		t.Fatal("atteso un rifiuto")
	}
	if !strings.Contains(err.Error(), "POSTGRES_HOST") || !strings.Contains(err.Error(), "POSTGRES_DB") {
		t.Errorf("l'errore non elenca tutte le variabili in conflitto: %v", err)
	}
	if strings.Contains(err.Error(), "POSTQRON_LOG_LEVEL") {
		t.Errorf("l'errore include una variabile non pertinente: %v", err)
	}
}

func TestParseCount(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		want    int
		wantErr bool
	}{
		// `up` senza numero significa «tutte», `down` senza numero significa
		// una sola: annullare tutto non deve poter succedere per distrazione.
		{name: "up senza numero", args: []string{"up"}, want: 0},
		{name: "down senza numero", args: []string{"down"}, want: 1},
		{name: "up con numero", args: []string{"up", "3"}, want: 3},
		{name: "down con numero", args: []string{"down", "2"}, want: 2},
		{name: "numero non intero", args: []string{"down", "molte"}, wantErr: true},
		{name: "numero nullo", args: []string{"down", "0"}, wantErr: true},
		{name: "numero negativo", args: []string{"up", "-1"}, wantErr: true},
		{name: "argomenti in eccesso", args: []string{"up", "1", "2"}, wantErr: true},
		{name: "status senza argomenti", args: []string{"status"}, want: 0},
		{name: "status con argomento", args: []string{"status", "3"}, wantErr: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := parseCount(test.args[0], test.args)
			if test.wantErr {
				if err == nil {
					t.Fatalf("atteso un errore, ottenuto %d", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseCount: %v", err)
			}
			if got != test.want {
				t.Errorf("parseCount = %d, atteso %d", got, test.want)
			}
		})
	}
}
