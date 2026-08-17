package database_test

import (
	"strings"
	"testing"

	"github.com/apdsoftware/postqron/services/api/internal/config"
	"github.com/apdsoftware/postqron/services/api/internal/database"
)

// Un errore di connessione finisce nei log: la password non deve poterci
// arrivare per questa strada (SPEC §5).
func TestOpenErrorDoesNotLeakThePassword(t *testing.T) {
	const password = "password-che-non-deve-comparire"

	pg := config.Postgres{
		// Una porta libera sul loopback fallisce subito e senza dipendere dalla
		// rete: è l'errore che serve a questo test.
		Host:     "127.0.0.1",
		Port:     "1",
		Database: "postqron",
		User:     "postqron",
		Password: password,
		SSLMode:  "disable",
	}

	pool, err := database.Open(t.Context(), pg, database.Options{MaxConns: 1})
	if err == nil {
		pool.Close()
		t.Fatal("atteso un errore di connessione sulla porta 1")
	}
	if strings.Contains(err.Error(), password) {
		t.Errorf("la password compare nell'errore: %v", err)
	}
	// L'errore deve comunque dire dove ha provato a collegarsi, altrimenti non
	// serve a diagnosticare nulla.
	if !strings.Contains(err.Error(), "127.0.0.1") {
		t.Errorf("l'errore non indica l'host: %v", err)
	}
}

func TestOpenRejectsInvalidConfiguration(t *testing.T) {
	pg := config.Postgres{
		Host:     "127.0.0.1",
		Port:     "non-un-numero",
		Database: "postqron",
		User:     "postqron",
		SSLMode:  "disable",
	}
	pool, err := database.Open(t.Context(), pg, database.Options{})
	if err == nil {
		pool.Close()
		t.Fatal("atteso un errore su una porta non numerica")
	}
}
