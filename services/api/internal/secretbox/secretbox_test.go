package secretbox_test

import (
	"bytes"
	"encoding/base64"
	"errors"
	"strings"
	"testing"

	"github.com/apdsoftware/postqron/services/api/internal/secretbox"
)

// Chiavi di prova. Sono trentadue byte costanti e riconoscibili: non proteggono
// niente, e non devono somigliare a una chiave vera per non finire in una
// segnalazione di gitleaks.
var (
	keyOne = base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0x11}, 32))
	keyTwo = base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0x22}, 32))
)

func newKeyring(t *testing.T, raw string) secretbox.Keyring {
	t.Helper()
	keyring, err := secretbox.NewKeyring(raw)
	if err != nil {
		t.Fatalf("NewKeyring(%q): %v", raw, err)
	}
	return keyring
}

// Il giro completo: ciò che si chiude si riapre, e il testo cifrato non contiene
// il testo in chiaro.
func TestSealOpenRoundTrip(t *testing.T) {
	keyring := newKeyring(t, keyOne)
	binding := []byte("utente-1\x00DIGEST_TOKEN")
	plaintext := []byte("valore-del-segreto-molto-riconoscibile")

	box, err := keyring.Seal(plaintext, binding)
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	if bytes.Contains(box.Ciphertext, plaintext) {
		t.Fatal("il testo in chiaro compare dentro al testo cifrato")
	}
	if box.KeyVersion != 1 {
		t.Errorf("KeyVersion = %d, attesa 1", box.KeyVersion)
	}

	opened, err := keyring.Open(box, binding)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if !bytes.Equal(opened, plaintext) {
		t.Errorf("Open = %q, atteso %q", opened, plaintext)
	}
}

// Il nonce cambia a ogni scrittura: due sigilli dello stesso valore non
// producono lo stesso testo cifrato. Senza, due righe con lo stesso valore
// sarebbero riconoscibili come tali guardando solo il database.
func TestSealUsesAFreshNonce(t *testing.T) {
	keyring := newKeyring(t, keyOne)
	binding := []byte("utente-1\x00TOKEN")

	first, err := keyring.Seal([]byte("stesso-valore"), binding)
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	second, err := keyring.Seal([]byte("stesso-valore"), binding)
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}

	if bytes.Equal(first.Nonce, second.Nonce) {
		t.Fatal("due sigilli hanno usato lo stesso nonce")
	}
	if bytes.Equal(first.Ciphertext, second.Ciphertext) {
		t.Fatal("due sigilli dello stesso valore hanno prodotto lo stesso testo cifrato")
	}
}

// Il legame con la riga: un testo cifrato copiato su un'altra riga non si apre.
// È la difesa contro chi ha accesso in scrittura al database e prova a farsi
// risolvere il segreto di un altro spostandogli sotto la colonna.
func TestOpenRejectsAForeignBinding(t *testing.T) {
	keyring := newKeyring(t, keyOne)

	box, err := keyring.Seal([]byte("segreto-di-alice"), []byte("alice\x00TOKEN"))
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}

	for _, binding := range []string{"bob\x00TOKEN", "alice\x00ALTRO", ""} {
		if _, err := keyring.Open(box, []byte(binding)); !errors.Is(err, secretbox.ErrNotAuthenticated) {
			t.Errorf("Open con binding %q: errore = %v, atteso ErrNotAuthenticated", binding, err)
		}
	}
}

// Un byte cambiato nel testo cifrato non produce un valore diverso: produce un
// errore. È il motivo per cui il cifrario è autenticato — il testo in chiaro
// finisce in una testata `Authorization` che parte verso l'esterno.
func TestOpenDetectsTampering(t *testing.T) {
	keyring := newKeyring(t, keyOne)
	binding := []byte("utente\x00TOKEN")

	box, err := keyring.Seal([]byte("valore-integro"), binding)
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	box.Ciphertext[0] ^= 0x01

	if _, err := keyring.Open(box, binding); !errors.Is(err, secretbox.ErrNotAuthenticated) {
		t.Errorf("errore = %v, atteso ErrNotAuthenticated", err)
	}
}

// ------------------------------------------------------------------ rotazione

// La rotazione: si cifra con la chiave attiva e si aprono anche le righe scritte
// con quelle precedenti. È la proprietà che rende possibile ruotare
// ENCRYPTION_KEY senza un istante in cui metà delle righe è illeggibile.
func TestRotationOpensOlderVersions(t *testing.T) {
	before := newKeyring(t, keyOne)
	binding := []byte("utente\x00TOKEN")

	old, err := before.Seal([]byte("scritto-prima-della-rotazione"), binding)
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}

	// Dopo la rotazione la variabile porta la chiave nuova per prima, e la
	// vecchia resta per aprire ciò che c'è già.
	after := newKeyring(t, "2:"+keyTwo+",1:"+keyOne)

	if after.ActiveVersion() != 2 {
		t.Fatalf("ActiveVersion = %d, attesa 2", after.ActiveVersion())
	}
	if !after.NeedsRotation(old) {
		t.Error("la riga vecchia dovrebbe risultare da risigillare")
	}

	opened, err := after.Open(old, binding)
	if err != nil {
		t.Fatalf("Open della riga vecchia: %v", err)
	}
	if string(opened) != "scritto-prima-della-rotazione" {
		t.Errorf("Open = %q", opened)
	}

	// Il risigillo: stesso valore, chiave attiva, e da lì in poi niente da
	// ruotare.
	fresh, err := after.Seal(opened, binding)
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	if fresh.KeyVersion != 2 {
		t.Errorf("KeyVersion dopo il risigillo = %d, attesa 2", fresh.KeyVersion)
	}
	if after.NeedsRotation(fresh) {
		t.Error("una riga appena risigillata non ha niente da ruotare")
	}
}

// Tolta la chiave vecchia dalla variabile, le righe che la usavano non sono
// perse: manca la chiave, e l'errore lo dice. È la differenza fra un guasto
// diagnosticabile e un dato corrotto.
func TestOpenWithoutTheKeySaysSo(t *testing.T) {
	before := newKeyring(t, keyOne)
	binding := []byte("utente\x00TOKEN")

	box, err := before.Seal([]byte("valore-di-una-vita-precedente"), binding)
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}

	only2 := newKeyring(t, "2:"+keyTwo)
	_, err = only2.Open(box, binding)
	if !errors.Is(err, secretbox.ErrUnknownVersion) {
		t.Fatalf("errore = %v, atteso ErrUnknownVersion", err)
	}
	if !strings.Contains(err.Error(), "versione 1") {
		t.Errorf("l'errore non dice quale versione manca: %v", err)
	}
}

// ------------------------------------------------------------- configurazione

func TestNewKeyringAcceptsTheDocumentedForms(t *testing.T) {
	raw := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{0x33}, 32))

	tests := []struct {
		name   string
		value  string
		active uint16
		count  int
	}{
		{"chiave sola come in .env.example", keyOne, 1, 1},
		{"base64 url senza padding", raw, 1, 1},
		{"rotazione in corso", "2:" + keyTwo + ",1:" + keyOne, 2, 2},
		{"spazi attorno alle voci", " 3:" + keyTwo + " , 1:" + keyOne + " ", 3, 2},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			keyring := newKeyring(t, test.value)
			if !keyring.Valid() {
				t.Fatal("keyring non valido")
			}
			if keyring.ActiveVersion() != test.active {
				t.Errorf("ActiveVersion = %d, attesa %d", keyring.ActiveVersion(), test.active)
			}
			if got := len(keyring.Versions()); got != test.count {
				t.Errorf("versioni = %d, attese %d", got, test.count)
			}
		})
	}
}

func TestNewKeyringRejectsWhatCannotWork(t *testing.T) {
	short := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0x44}, 16))

	tests := []struct{ name, value string }{
		{"vuota", "   "},
		{"non è base64", "non-una-chiave!!"},
		{"chiave di 16 byte", short},
		{"versione zero", "0:" + keyOne},
		{"versione non numerica", "prima:" + keyOne},
		{"stessa versione due volte", "1:" + keyOne + ",1:" + keyTwo},
		{"seconda voce senza versione", "1:" + keyOne + "," + keyTwo},
		{"voce vuota", "1:" + keyOne + ",,2:" + keyTwo},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := secretbox.NewKeyring(test.value); err == nil {
				t.Fatal("accettata una configurazione che non può funzionare")
			}
		})
	}
}

// Il valore zero non cifra niente. Un keyring dimenticato non deve poter
// diventare un segreto salvato in chiaro.
func TestZeroKeyringRefusesToWork(t *testing.T) {
	var keyring secretbox.Keyring

	if keyring.Valid() {
		t.Fatal("il keyring zero si dichiara valido")
	}
	if _, err := keyring.Seal([]byte("valore"), nil); err == nil {
		t.Error("il keyring zero ha cifrato qualcosa")
	}
	if _, err := keyring.Open(secretbox.Box{Ciphertext: []byte{1}, Nonce: []byte{2}}, nil); err == nil {
		t.Error("il keyring zero ha decifrato qualcosa")
	}
}

// Una riga revocata ha le colonne vuote: aprirla è un difetto del chiamante e va
// detto, non inghiottito restituendo una stringa vuota che finirebbe in una
// testata `Authorization`.
func TestOpenRefusesAnEmptyBox(t *testing.T) {
	keyring := newKeyring(t, keyOne)

	var empty secretbox.Box
	if !empty.Empty() {
		t.Fatal("il Box zero dovrebbe essere vuoto")
	}
	if keyring.NeedsRotation(empty) {
		t.Error("una riga revocata non ha niente da ruotare")
	}
	if _, err := keyring.Open(empty, nil); err == nil {
		t.Error("Open su un Box vuoto non ha segnalato niente")
	}
}

func TestKeyringFromEnv(t *testing.T) {
	env := map[string]string{secretbox.EnvVar: keyOne}
	keyring, err := secretbox.KeyringFromEnv(func(k string) string { return env[k] })
	if err != nil {
		t.Fatalf("KeyringFromEnv: %v", err)
	}
	if !keyring.Valid() {
		t.Error("keyring non valido")
	}

	_, err = secretbox.KeyringFromEnv(func(string) string { return "" })
	if err == nil {
		t.Fatal("una variabile assente dev'essere un errore d'avvio")
	}
	if !strings.Contains(err.Error(), secretbox.EnvVar) {
		t.Errorf("l'errore non nomina la variabile: %v", err)
	}
}
