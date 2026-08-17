package secrets_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/apdsoftware/postqron/services/api/internal/secrets"
)

// segreto è il valore che nessun test deve mai trovare stampato da nessuna
// parte. È volutamente lungo e riconoscibile: se compare, si vede.
const segreto = "valore-che-non-deve-comparire-da-nessuna-parte-1234567890"

// ------------------------------------------------- il valore non si stampa

// Il valore non si stampa **con nessun verbo**.
//
// È il test che giustifica [secrets.Value] come tipo invece che come `string`:
// non è che ci si ricorda di non stamparlo, è che stamparlo non produce il
// valore. Il caso `%#v` è quello che una `fmt.Stringer` da sola non copre, ed è
// anche quello che una struttura stampata con `%+v` produce sui suoi campi.
func TestValueNeverPrints(t *testing.T) {
	value := secrets.Value(segreto)

	forms := map[string]string{
		"%s":            fmt.Sprintf("%s", value),
		"%q":            fmt.Sprintf("%q", value),
		"%v":            fmt.Sprintf("%v", value),
		"%+v":           fmt.Sprintf("%+v", value),
		"%#v":           fmt.Sprintf("%#v", value),
		"%d":            fmt.Sprintf("%d", value),
		"String()":      value.String(),
		"Sprint":        fmt.Sprint(value),
		"in una strut.": fmt.Sprintf("%+v", struct{ V secrets.Value }{value}),
		"in una slice":  fmt.Sprintf("%v", []secrets.Value{value}),
		"in una mappa":  fmt.Sprintf("%v", map[string]secrets.Value{"k": value}),
	}
	for form, printed := range forms {
		if strings.Contains(printed, segreto) {
			t.Errorf("%s ha stampato il valore: %s", form, printed)
		}
	}

	// E il valore vero si ottiene, ma bisogna chiederlo per nome.
	if value.Reveal() != segreto {
		t.Error("Reveal non restituisce il valore")
	}
}

// Nei log strutturati vale lo stesso, per tutte e tre le strade con cui un
// valore ci finisce.
func TestValueNeverReachesTheLog(t *testing.T) {
	logs := &bytes.Buffer{}
	logger := slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	value := secrets.Value(segreto)

	logger.Info("prova", slog.Any("valore", value))
	logger.Info("prova", slog.String("valore", value.String()))
	logger.Info("prova", slog.Any("input", secrets.CreateInput{Name: "TOKEN", Value: value}))

	if strings.Contains(logs.String(), segreto) {
		t.Fatalf("il valore è finito nei log:\n%s", logs)
	}
	if !strings.Contains(logs.String(), "segreto") {
		t.Errorf("la maschera non compare nei log:\n%s", logs)
	}
}

// Serializzato in JSON — una risposta HTTP, un file di stato, un dump di
// diagnostica — il valore resta mascherato.
func TestValueNeverSerializes(t *testing.T) {
	encoded, err := json.Marshal(struct {
		Value secrets.Value `json:"value"`
	}{secrets.Value(segreto)})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if strings.Contains(string(encoded), segreto) {
		t.Fatalf("il valore è finito nel JSON: %s", encoded)
	}
}

// ------------------------------------------------ il campo che non esiste

// [secrets.Secret] non ha un campo per il valore, e non ne ha uno che *possa*
// contenerlo.
//
// Il test guarda i campi con la reflection invece di controllare una risposta
// serializzata: una risposta si controlla per il caso che si è pensato a
// controllare, i campi della struttura ci sono tutti. Se qualcuno aggiunge
// `Value`, `Plaintext` o `Ciphertext` a questa struttura — che è quella che
// l'API restituisce e che i log stampano — questo test si accorge subito, e non
// alla prima risposta che lo mostra.
func TestSecretHasNoFieldForTheValue(t *testing.T) {
	vietati := []string{"value", "plaintext", "secret", "ciphertext", "nonce", "plain", "preview"}

	typ := reflect.TypeOf(secrets.Secret{})
	for i := range typ.NumField() {
		name := strings.ToLower(typ.Field(i).Name)
		for _, vietato := range vietati {
			if strings.Contains(name, vietato) {
				t.Errorf("secrets.Secret ha il campo %q: il valore non deve poterci stare",
					typ.Field(i).Name)
			}
		}
	}
}

// Il materiale cifrato esiste in un tipo solo, [secrets.Sealed], e nemmeno lui
// lo stampa: la sua forma nei log è quella del segreto senza le colonne cifrate.
func TestSealedDoesNotLogItsCiphertext(t *testing.T) {
	logs := &bytes.Buffer{}
	logger := slog.New(slog.NewTextHandler(logs, nil))

	sealed := secrets.Sealed{
		Secret:     secrets.Secret{ID: "s-1", UserID: "u-1", Name: "DIGEST_TOKEN"},
		Ciphertext: []byte("materiale-cifrato-riconoscibile"),
		Nonce:      []byte("nonce-riconoscibile"),
	}
	logger.Info("prova", slog.Any("secret", sealed))

	printed := logs.String()
	for _, vietato := range []string{"materiale-cifrato", "nonce-riconoscibile"} {
		if strings.Contains(printed, vietato) {
			t.Errorf("i log contengono %q:\n%s", vietato, printed)
		}
	}
	if !strings.Contains(printed, "DIGEST_TOKEN") {
		t.Errorf("i log non dicono di quale segreto si tratta:\n%s", printed)
	}
}

// [secrets.Secret.LogValue] scrive ciò che serve a capire quale segreto ha fatto
// cosa, e niente di più.
func TestSecretLogValue(t *testing.T) {
	now := time.Now()
	secret := secrets.Secret{
		ID: "s-1", UserID: "u-1", Name: "DIGEST_TOKEN", RevokedAt: &now,
	}
	if !secret.Revoked() || secret.Live() {
		t.Fatal("un segreto con revoked_at è revocato e non è vivo")
	}

	logs := &bytes.Buffer{}
	slog.New(slog.NewTextHandler(logs, nil)).Info("prova", slog.Any("secret", secret))
	for _, atteso := range []string{"s-1", "u-1", "DIGEST_TOKEN", "revoked=true"} {
		if !strings.Contains(logs.String(), atteso) {
			t.Errorf("i log non contengono %q:\n%s", atteso, logs)
		}
	}
}
