package aicreds_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/apdsoftware/postqron/services/api/internal/aicreds"
	"github.com/apdsoftware/postqron/services/api/internal/secretbox"
)

// chiave è il valore che nessun test deve mai trovare stampato da nessuna
// parte. È volutamente lungo e riconoscibile: se compare, si vede.
const chiave = "sk-ant-api03-chiave-che-non-deve-comparire-da-nessuna-parte-1234567890"

// ------------------------------------------------- la chiave non si stampa

// La chiave non si stampa **con nessun verbo**.
//
// Il tipo è [secretbox.Plaintext], lo stesso dei segreti del workspace, e il
// test è qui e non solo là perché è la proprietà che R18 chiede a *questa*
// funzionalità: se un giorno qualcuno cambiasse il tipo del campo in una
// `string`, questo test se ne accorgerebbe dal lato delle chiavi AI senza
// aspettare che se ne accorga quello dei segreti.
func TestKeyNeverPrints(t *testing.T) {
	key := secretbox.Plaintext(chiave)

	forms := map[string]string{
		"%s":            fmt.Sprintf("%s", key),
		"%q":            fmt.Sprintf("%q", key),
		"%v":            fmt.Sprintf("%v", key),
		"%+v":           fmt.Sprintf("%+v", key),
		"%#v":           fmt.Sprintf("%#v", key),
		"String()":      key.String(),
		"Sprint":        fmt.Sprint(key),
		"in un input":   fmt.Sprintf("%+v", aicreds.SaveInput{Provider: "anthropic", Key: key}),
		"in una slice":  fmt.Sprintf("%v", []secretbox.Plaintext{key}),
		"in una mappa":  fmt.Sprintf("%v", map[string]secretbox.Plaintext{"k": key}),
		"in una strut.": fmt.Sprintf("%+v", struct{ K secretbox.Plaintext }{key}),
	}
	for form, printed := range forms {
		if strings.Contains(printed, chiave) {
			t.Errorf("%s ha stampato la chiave: %s", form, printed)
		}
	}

	if key.Reveal() != chiave {
		t.Error("Reveal non restituisce la chiave")
	}
}

// Serializzata in JSON — una risposta HTTP, un dump di diagnostica — la chiave
// resta mascherata anche dentro il corpo di una richiesta di scrittura, che è
// l'unico tipo che la contiene per forza.
func TestSaveInputNeverSerializesTheKey(t *testing.T) {
	encoded, err := json.Marshal(aicreds.SaveInput{
		Provider: "anthropic",
		Key:      secretbox.Plaintext(chiave),
		Label:    "piano team",
	})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if strings.Contains(string(encoded), chiave) {
		t.Fatalf("la chiave è finita nel JSON: %s", encoded)
	}
}

// ------------------------------------------------ il campo che non esiste

// [aicreds.Credential] non ha un campo per la chiave, e non ne ha uno che
// *possa* contenerne un pezzo.
//
// `last_four` è nell'elenco dei nomi vietati insieme a `value` e `ciphertext`:
// la 0007 quella colonna ce l'aveva, la 0016 l'ha tolta, e questo test è ciò
// che impedisce che torni per comodità dal lato Go — dove reintrodurla sarebbe
// una riga sola e nessuno la noterebbe.
func TestCredentialHasNoFieldForTheKey(t *testing.T) {
	vietati := []string{
		"key" /* KeyVersion è escluso sotto */, "plaintext", "secret", "ciphertext",
		"nonce", "plain", "preview", "lastfour", "hint", "suffix", "prefix",
	}

	typ := reflect.TypeOf(aicreds.Credential{})
	for i := range typ.NumField() {
		name := typ.Field(i).Name
		// KeyVersion è la versione di ENCRYPTION_KEY, non la chiave: non dice
		// niente sul valore e serve alla rotazione.
		if name == "KeyVersion" {
			continue
		}
		lower := strings.ToLower(name)
		for _, vietato := range vietati {
			if strings.Contains(lower, vietato) {
				t.Errorf("aicreds.Credential ha il campo %q: la chiave non deve poterci stare", name)
			}
		}
	}
}

// Il materiale cifrato esiste in un tipo solo, [aicreds.Sealed], e nemmeno lui
// lo stampa: la sua forma nei log è quella della credenziale senza le colonne
// cifrate.
func TestSealedDoesNotLogItsCiphertext(t *testing.T) {
	logs := &bytes.Buffer{}
	logger := slog.New(slog.NewTextHandler(logs, nil))

	sealed := aicreds.Sealed{
		Credential: aicreds.Credential{ID: "c-1", UserID: "u-1", Provider: aicreds.Anthropic},
		Ciphertext: []byte("materiale-cifrato-riconoscibile"),
		Nonce:      []byte("nonce-riconoscibile"),
	}
	logger.Info("prova", slog.Any("credential", sealed))

	printed := logs.String()
	for _, vietato := range []string{"materiale-cifrato", "nonce-riconoscibile"} {
		if strings.Contains(printed, vietato) {
			t.Errorf("i log contengono %q:\n%s", vietato, printed)
		}
	}
	if !strings.Contains(printed, "anthropic") {
		t.Errorf("i log non dicono di quale chiave si tratta:\n%s", printed)
	}
}

// [aicreds.Credential.LogValue] scrive ciò che serve a capire quale chiave ha
// fatto cosa, e niente di più.
func TestCredentialLogValue(t *testing.T) {
	now := time.Now()
	credential := aicreds.Credential{
		ID: "c-1", UserID: "u-1", Provider: aicreds.OpenAI, RevokedAt: &now,
	}
	if !credential.Revoked() || credential.Live() {
		t.Fatal("una credenziale con revoked_at è revocata e non è viva")
	}

	logs := &bytes.Buffer{}
	slog.New(slog.NewTextHandler(logs, nil)).Info("prova", slog.Any("credential", credential))
	for _, atteso := range []string{"c-1", "u-1", "openai", "revoked=true"} {
		if !strings.Contains(logs.String(), atteso) {
			t.Errorf("i log non contengono %q:\n%s", atteso, logs)
		}
	}
}

// ------------------------------------------------------------- i provider

// L'insieme dei provider è chiuso, e coincide con l'enumerato `ai_provider`
// della 0001: un valore che il database rifiuterebbe va rifiutato qui, con un
// messaggio, invece di diventare un 500 alla scrittura.
func TestParseProvider(t *testing.T) {
	casi := map[string]struct {
		atteso aicreds.Provider
		valido bool
	}{
		"anthropic":   {aicreds.Anthropic, true},
		"  openai  ":  {aicreds.OpenAI, true},
		"Google":      {aicreds.Google, true},
		"ANTHROPIC":   {aicreds.Anthropic, true},
		"claude":      {"claude", false},
		"":            {"", false},
		"anthropic ;": {"anthropic ;", false},
	}
	for raw, atteso := range casi {
		provider, ok := aicreds.ParseProvider(raw)
		if ok != atteso.valido {
			t.Errorf("ParseProvider(%q) valido = %v, atteso %v", raw, ok, atteso.valido)
		}
		if ok && provider != atteso.atteso {
			t.Errorf("ParseProvider(%q) = %q, atteso %q", raw, provider, atteso.atteso)
		}
	}
}
