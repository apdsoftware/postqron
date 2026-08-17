package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"

	"golang.org/x/crypto/hkdf"
)

// tokenBytes è l'entropia di un token opaco, in byte.
//
// 32 byte da CSPRNG sono 256 bit: un attaccante che tirasse a indovinare un
// token di sessione non arriverebbe a niente nemmeno con tutta la banda del
// pianeta, ed è la ragione per cui i token *non* passano da Argon2 — non c'è
// nessuna debolezza di entropia da compensare, e un KDF lento andrebbe pagato a
// ogni richiesta autenticata invece di una volta per login.
const tokenBytes = 32

// minSecretLength è la lunghezza minima di SESSION_SECRET, in byte.
//
// Il segreto non è una password d'uso umano: è materiale di chiave, e sotto i 32
// byte non ha più entropia della chiave che deve produrre.
const minSecretLength = 32

// SecretEnvVar è la variabile che porta il segreto di firma delle sessioni.
//
// La lettura non passa da internal/config perché quel package appartiene a
// un'altra issue e le due strade non devono divergere a metà di una PR: il
// posto giusto per questa variabile, a regime, è lì accanto alle POSTGRES_*.
const SecretEnvVar = "SESSION_SECRET"

// newToken genera un token opaco in base64url senza padding.
//
// Base64url e non esadecimale perché il token finisce in un cookie e in un link
// via email: 43 caratteri invece di 64, senza caratteri che richiedano
// escaping.
func newToken() (string, error) {
	buf := make([]byte, tokenBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generazione del token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// ------------------------------------------------------------------- keyring

// Keyring contiene le chiavi derivate da SESSION_SECRET.
//
// I token conservati nel database non sono i token in chiaro ma il loro
// HMAC-SHA256 sotto una di queste chiavi. Perché un HMAC e non uno SHA-256
// semplice, dato che 256 bit di entropia non sono invertibili in nessun caso:
//
//   - **rotazione.** Cambiare SESSION_SECRET invalida in un colpo tutte le
//     sessioni e tutti i token pendenti, senza toccare il database. È la leva
//     che serve dopo un incidente, e senza chiave non esisterebbe.
//   - **difesa in profondità.** Il segreto sta nell'ambiente, gli hash nel
//     database: chi ottiene solo il secondo — un backup, una replica, un dump —
//     non ha il materiale per verificare alcuna ipotesi sui token, nemmeno se
//     in futuro qualcuno accorciasse [tokenBytes].
//
// Le due chiavi sono derivate con HKDF da segreti di dominio diversi invece di
// usare SESSION_SECRET direttamente per entrambe: un token di recupero password
// e un token di sessione non devono poter essere scambiati l'uno per l'altro
// nemmeno in presenza di un bug che confonda le due tabelle.
type Keyring struct {
	sessionKey   []byte
	userTokenKey []byte
}

// NewKeyring deriva le chiavi da un segreto.
func NewKeyring(secret string) (Keyring, error) {
	if len(secret) < minSecretLength {
		return Keyring{}, fmt.Errorf(
			"%s deve essere lungo almeno %d byte (attuali: %d); generane uno con `openssl rand -base64 48`",
			SecretEnvVar, minSecretLength, len(secret))
	}
	sessionKey, err := deriveKey(secret, "postqron/v1/session-token")
	if err != nil {
		return Keyring{}, err
	}
	userTokenKey, err := deriveKey(secret, "postqron/v1/user-token")
	if err != nil {
		return Keyring{}, err
	}
	return Keyring{sessionKey: sessionKey, userTokenKey: userTokenKey}, nil
}

// KeyringFromEnv legge SESSION_SECRET e ne deriva le chiavi.
//
// Non esiste un valore di default: un segreto generato all'avvio farebbe
// scadere tutte le sessioni a ogni riavvio, e un segreto costante scritto nel
// codice sarebbe un segreto nel codice (SPEC §5). Se la variabile manca, il
// servizio non parte.
func KeyringFromEnv(getenv func(string) string) (Keyring, error) {
	secret := strings.TrimSpace(getenv(SecretEnvVar))
	if secret == "" {
		return Keyring{}, fmt.Errorf("%s non è impostata: vedi .env.example", SecretEnvVar)
	}
	return NewKeyring(secret)
}

// SessionHash è l'impronta con cui una sessione è cercata nel database.
func (k Keyring) SessionHash(token string) string { return mac(k.sessionKey, token) }

// UserTokenHash è l'impronta di un token monouso (verifica email, recupero
// password).
func (k Keyring) UserTokenHash(token string) string { return mac(k.userTokenKey, token) }

// valid indica se il keyring è stato inizializzato.
func (k Keyring) valid() bool { return len(k.sessionKey) > 0 && len(k.userTokenKey) > 0 }

func mac(key []byte, token string) string {
	m := hmac.New(sha256.New, key)
	m.Write([]byte(token))
	return hex.EncodeToString(m.Sum(nil))
}

func deriveKey(secret, info string) ([]byte, error) {
	// Nessun salt: HKDF senza salt è definito e sicuro, e un salt costante nel
	// codice non aggiungerebbe niente. La separazione dei domini la fa `info`.
	key := make([]byte, sha256.Size)
	if _, err := io.ReadFull(hkdf.New(sha256.New, []byte(secret), nil, []byte(info)), key); err != nil {
		return nil, fmt.Errorf("derivazione della chiave %q: %w", info, err)
	}
	if len(key) != sha256.Size {
		return nil, errors.New("derivazione della chiave: lunghezza inattesa")
	}
	return key, nil
}
