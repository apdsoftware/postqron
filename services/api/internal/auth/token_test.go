package auth_test

import (
	"strings"
	"testing"

	"github.com/apdsoftware/postqron/services/api/internal/auth"
)

const testSecret = "un-segreto-di-prova-abbastanza-lungo-da-essere-accettato"

func newTestKeyring(t testing.TB) auth.Keyring {
	t.Helper()
	keyring, err := auth.NewKeyring(testSecret)
	if err != nil {
		t.Fatalf("costruzione del Keyring: %v", err)
	}
	return keyring
}

// Un segreto corto non è un segreto: sotto i 32 byte non ha l'entropia della
// chiave che deve produrre, e il servizio non deve partire.
func TestNewKeyringRifiutaUnSegretoCorto(t *testing.T) {
	if _, err := auth.NewKeyring(strings.Repeat("a", 31)); err == nil {
		t.Fatal("atteso un errore su un segreto di 31 byte")
	}
	if _, err := auth.NewKeyring(strings.Repeat("a", 32)); err != nil {
		t.Fatalf("un segreto di 32 byte deve essere accettato: %v", err)
	}
}

// Senza SESSION_SECRET il servizio non parte: un default generato all'avvio
// farebbe scadere tutte le sessioni a ogni riavvio, e uno costante nel codice
// sarebbe un segreto nel codice (SPEC §5).
func TestKeyringFromEnvEsigeLaVariabile(t *testing.T) {
	tests := map[string]string{
		"assente":      "",
		"soli spazi":   "   ",
		"troppo corto": "corto",
	}
	for name, value := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := auth.KeyringFromEnv(func(key string) string {
				if key == auth.SecretEnvVar {
					return value
				}
				return ""
			})
			if err == nil {
				t.Fatal("atteso un errore")
			}
			// Il messaggio deve dire quale variabile manca, altrimenti non serve
			// a chi legge il log di avvio.
			if !strings.Contains(err.Error(), auth.SecretEnvVar) {
				t.Errorf("il messaggio non nomina %s: %v", auth.SecretEnvVar, err)
			}
			// E non deve contenere il valore, nemmeno quello sbagliato.
			if trimmed := strings.TrimSpace(value); trimmed != "" && strings.Contains(err.Error(), trimmed) {
				t.Errorf("il messaggio contiene il valore del segreto: %v", err)
			}
		})
	}

	keyring, err := auth.KeyringFromEnv(func(string) string { return testSecret })
	if err != nil {
		t.Fatalf("con il segreto impostato: %v", err)
	}
	if keyring.SessionHash("x") == "" {
		t.Error("il keyring non è utilizzabile")
	}
}

// Le due chiavi sono derivate con domini separati: un token di recupero password
// e un token di sessione non devono poter essere scambiati l'uno per l'altro
// nemmeno per effetto di un bug che confonda le due tabelle.
func TestLeImpronteDeiDueDominiSonoDistinte(t *testing.T) {
	keyring := newTestKeyring(t)
	const token = "lo-stesso-token"

	if keyring.SessionHash(token) == keyring.UserTokenHash(token) {
		t.Fatal("le due impronte coincidono: le chiavi non sono separate")
	}
}

// L'impronta è deterministica — altrimenti nessuna sessione sarebbe ritrovabile —
// e dipende dal segreto: cambiarlo invalida tutto, che è la leva operativa dopo
// un incidente.
func TestLImprontaDipendeDalSegreto(t *testing.T) {
	first := newTestKeyring(t)
	second, err := auth.NewKeyring(testSecret + "-diverso")
	if err != nil {
		t.Fatalf("NewKeyring: %v", err)
	}

	const token = "token-di-sessione"
	if first.SessionHash(token) != first.SessionHash(token) {
		t.Error("l'impronta non è deterministica")
	}
	if first.SessionHash(token) == second.SessionHash(token) {
		t.Error("l'impronta non cambia al cambio di segreto: la rotazione non avrebbe effetto")
	}
}

// L'impronta ha la forma che il vincolo `sessions_token_hash_format_check` della
// migrazione 0009 impone: 64 cifre esadecimali minuscole. Se qui cambiasse la
// codifica, l'INSERT fallirebbe in produzione e non in questo test.
func TestLImprontaRispettaIlVincoloDelloSchema(t *testing.T) {
	keyring := newTestKeyring(t)
	for name, hash := range map[string]string{
		"sessione": keyring.SessionHash("token"),
		"monouso":  keyring.UserTokenHash("token"),
	} {
		t.Run(name, func(t *testing.T) {
			if len(hash) != 64 {
				t.Fatalf("lunghezza = %d, attesa 64: %q", len(hash), hash)
			}
			if strings.Trim(hash, "0123456789abcdef") != "" {
				t.Errorf("l'impronta non è esadecimale minuscola: %q", hash)
			}
		})
	}
}

// Il token in chiaro non deve essere ricostruibile dall'impronta: è ciò che rende
// un dump del database inutile per impersonare qualcuno.
func TestLImprontaNonContieneIlToken(t *testing.T) {
	keyring := newTestKeyring(t)
	// Il valore è parlante e non esadecimale di proposito: una stringa di cifre
	// esadecimali qui è indistinguibile da una chiave vera per lo scanner di
	// segreti della CI, e un rilevamento da escludere costa più di quanto valga.
	const token = "token-in-chiaro-di-prova"
	if strings.Contains(keyring.SessionHash(token), token) {
		t.Error("l'impronta contiene il token")
	}
}
