package secrets_test

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/apdsoftware/postqron/services/api/internal/secretbox"
	"github.com/apdsoftware/postqron/services/api/internal/secrets"
	"github.com/apdsoftware/postqron/services/api/internal/secretstest"
)

const (
	utente = "utente-1"
	altro  = "utente-2"
)

// testKey è una chiave di prova: trentadue byte costanti, che non proteggono
// niente e non somigliano a una chiave vera.
var testKey = base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0x11}, 32))

// ---------------------------------------------------------------- impalcatura

type fixture struct {
	t     *testing.T
	svc   *secrets.Service
	store *secretstest.Store
	logs  *bytes.Buffer
	now   time.Time
}

func newFixture(t *testing.T, keyring ...string) *fixture {
	t.Helper()

	raw := testKey
	if len(keyring) > 0 {
		raw = keyring[0]
	}
	keys, err := secretbox.NewKeyring(raw)
	if err != nil {
		t.Fatalf("NewKeyring: %v", err)
	}

	f := &fixture{
		t:     t,
		store: secretstest.NewStore(),
		logs:  &bytes.Buffer{},
		now:   time.Date(2026, 8, 17, 3, 0, 0, 0, time.UTC),
	}
	f.svc, err = secrets.NewService(secrets.Options{
		Store:   f.store,
		Keyring: keys,
		Logger:  slog.New(slog.NewTextHandler(f.logs, &slog.HandlerOptions{Level: slog.LevelDebug})),
		Now:     func() time.Time { return f.now },
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	return f
}

// crea registra un segreto e restituisce la riga.
func (f *fixture) crea(name, value string) secrets.Secret {
	f.t.Helper()
	secret, err := f.svc.Create(f.t.Context(), utente, secrets.CreateInput{
		Name:  name,
		Value: secrets.Value(value),
	})
	if err != nil {
		f.t.Fatalf("Create(%s): %v", name, err)
	}
	return secret
}

// resolveWith risolve una richiesta contro i valori indicati. È l'impalcatura
// dei test di resolve_test.go, che del servizio hanno bisogno solo per arrivare
// a un [secrets.Resolved] vero.
func resolveWith(t *testing.T, req secrets.Request, values map[string]string) secrets.Resolved {
	t.Helper()

	f := newFixture(t)
	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}
	// In ordine, così che l'ordine di creazione — e quindi gli identificativi —
	// non dipenda dall'iterazione casuale delle mappe di Go.
	slices.Sort(names)
	for _, name := range names {
		f.crea(name, values[name])
	}

	resolved, err := f.svc.Resolve(t.Context(), utente, req)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	return resolved
}

// logged restituisce ciò che un logger scrive di un valore.
func logged(t *testing.T, value any) string {
	t.Helper()
	buf := &bytes.Buffer{}
	slog.New(slog.NewTextHandler(buf, nil)).Info("prova", slog.Any("valore", value))
	return buf.String()
}

// ------------------------------------------------------------------ creazione

// Il valore entra cifrato e non esce: né dalla riga a riposo, né dal log della
// creazione, né dalla struttura restituita.
func TestCreateStoresOnlyCiphertext(t *testing.T) {
	f := newFixture(t)
	const valore = "finto-valore-riconoscibile"

	secret := f.crea("DIGEST_TOKEN", valore)

	if secret.Name != "DIGEST_TOKEN" || secret.ID == "" {
		t.Fatalf("segreto = %+v", secret)
	}

	sealed := f.store.Sealed(secret.ID)
	if len(sealed.Ciphertext) == 0 || len(sealed.Nonce) == 0 {
		t.Fatal("la riga non ha materiale cifrato")
	}
	if bytes.Contains(sealed.Ciphertext, []byte(valore)) {
		t.Fatal("il valore in chiaro è dentro al testo cifrato")
	}
	if sealed.KeyVersion != 1 {
		t.Errorf("KeyVersion = %d", sealed.KeyVersion)
	}

	// Il log della creazione dice quale segreto, non quale valore.
	if strings.Contains(f.logs.String(), valore) {
		t.Fatalf("il valore è finito nei log:\n%s", f.logs)
	}
	if !strings.Contains(f.logs.String(), "DIGEST_TOKEN") {
		t.Errorf("i log non dicono quale segreto è stato creato:\n%s", f.logs)
	}

	// E la struttura restituita, stampata in ogni forma, non lo contiene.
	for _, printed := range []string{fmt.Sprintf("%+v", secret), logged(t, secret)} {
		if strings.Contains(printed, valore) {
			t.Errorf("il valore compare in una forma stampata: %s", printed)
		}
	}
}

func TestCreateRejectsWhatCannotWork(t *testing.T) {
	f := newFixture(t)

	tests := []struct {
		name  string
		in    secrets.CreateInput
		campo string
	}{
		{"nome vuoto", secrets.CreateInput{Value: "valore-abbastanza-lungo"}, "name/required"},
		{"nome minuscolo", secrets.CreateInput{Name: "digest", Value: "valore-abbastanza-lungo"}, "name/invalid_format"},
		{"nome con trattino", secrets.CreateInput{Name: "A-B", Value: "valore-abbastanza-lungo"}, "name/invalid_format"},
		{"nome troppo lungo", secrets.CreateInput{Name: strings.Repeat("A", 65), Value: "valore-abbastanza-lungo"}, "name/too_long"},
		{"valore vuoto", secrets.CreateInput{Name: "TOKEN"}, "value/required"},
		{"valore troppo corto", secrets.CreateInput{Name: "TOKEN", Value: "abc"}, "value/too_short"},
		{"valore troppo lungo", secrets.CreateInput{Name: "TOKEN", Value: secrets.Value(strings.Repeat("x", 4097))}, "value/too_long"},
		{"nota troppo lunga", secrets.CreateInput{Name: "TOKEN", Value: "valore-abbastanza-lungo", Description: strings.Repeat("n", 201)}, "description/too_long"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := f.svc.Create(t.Context(), utente, test.in)
			if got := campi(t, err); !slices.Contains(got, test.campo) {
				t.Errorf("campi = %v, atteso %q", got, test.campo)
			}
		})
	}

	if f.store.Count() != 0 {
		t.Errorf("una creazione rifiutata ha scritto %d righe", f.store.Count())
	}
}

// Il messaggio del nome minuscolo indica la correzione esatta: è l'errore più
// frequente e l'unico in cui si può fare di meglio che ripetere la regola.
func TestCreateSuggestsTheUppercaseName(t *testing.T) {
	f := newFixture(t)
	_, err := f.svc.Create(t.Context(), utente, secrets.CreateInput{
		Name: "digest_token", Value: "valore-abbastanza-lungo",
	})
	invalid, ok := secrets.AsValidation(err)
	if !ok {
		t.Fatalf("errore = %v", err)
	}
	if !strings.Contains(invalid.Fields[0].Message, "DIGEST_TOKEN") {
		t.Errorf("messaggio = %s", invalid.Fields[0].Message)
	}
}

// Il valore **non** viene ripulito dagli spazi ai bordi: toglierli
// significherebbe salvare un segreto diverso da quello incollato, e il
// fallimento che ne seguirebbe è fra i più difficili da capire che esistano.
func TestCreateKeepsSurroundingSpaces(t *testing.T) {
	f := newFixture(t)
	const valore = "  valore-con-spazi-ai-bordi  "

	f.crea("TOKEN", valore)
	resolved, err := f.svc.Resolve(t.Context(), utente, secrets.Request{Body: "${TOKEN}"})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if resolved.Body() != valore {
		t.Errorf("corpo = %q, atteso %q", resolved.Body(), valore)
	}
}

// Un secondo segreto con lo stesso nome è un conflitto, non un aggiornamento
// silenzioso: sovrascrivere cambierebbe la credenziale che altri job stanno già
// usando.
func TestCreateRejectsADuplicateName(t *testing.T) {
	f := newFixture(t)
	f.crea("DIGEST_TOKEN", "primo-valore-lungo")

	_, err := f.svc.Create(t.Context(), utente, secrets.CreateInput{
		Name: "DIGEST_TOKEN", Value: "secondo-valore-lungo",
	})
	if !errors.Is(err, secrets.ErrDuplicateName) {
		t.Fatalf("errore = %v, atteso ErrDuplicateName", err)
	}

	// Ma lo stesso nome in un altro workspace è legittimo.
	if _, err := f.svc.Create(t.Context(), altro, secrets.CreateInput{
		Name: "DIGEST_TOKEN", Value: "valore-di-un-altro",
	}); err != nil {
		t.Fatalf("lo stesso nome in un altro workspace: %v", err)
	}
}

func TestCreateEnforcesTheCeiling(t *testing.T) {
	f := newFixture(t)
	for i := range secrets.MaxSecretsPerWorkspace {
		f.crea(fmt.Sprintf("SEGRETO_%d", i), "valore-abbastanza-lungo")
	}
	_, err := f.svc.Create(t.Context(), utente, secrets.CreateInput{
		Name: "UNO_DI_TROPPO", Value: "valore-abbastanza-lungo",
	})
	if !errors.Is(err, secrets.ErrTooManySecrets) {
		t.Fatalf("errore = %v, atteso ErrTooManySecrets", err)
	}
}

// --------------------------------------------------------------- aggiornamento

// L'aggiornamento sostituisce il valore e risigilla con la chiave attiva: è
// anche una rotazione della singola riga.
func TestUpdateReplacesTheValue(t *testing.T) {
	f := newFixture(t)
	secret := f.crea("DIGEST_TOKEN", "valore-vecchio-lungo")
	prima := f.store.Sealed(secret.ID).Ciphertext

	nota := "ruotato dopo l'incidente"
	if _, err := f.svc.Update(t.Context(), utente, secret.ID, secrets.UpdateInput{
		Value:       "valore-nuovo-riconoscibile",
		Description: &nota,
	}); err != nil {
		t.Fatalf("Update: %v", err)
	}

	dopo := f.store.Sealed(secret.ID)
	if bytes.Equal(prima, dopo.Ciphertext) {
		t.Fatal("il testo cifrato non è cambiato")
	}
	if bytes.Contains(dopo.Ciphertext, []byte("valore-nuovo")) {
		t.Fatal("il valore in chiaro è dentro al testo cifrato")
	}
	if dopo.Description != nota {
		t.Errorf("nota = %q", dopo.Description)
	}
	if strings.Contains(f.logs.String(), "valore-nuovo") {
		t.Fatalf("il valore è finito nei log:\n%s", f.logs)
	}

	resolved, err := f.svc.Resolve(t.Context(), utente, secrets.Request{Body: "${DIGEST_TOKEN}"})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if resolved.Body() != "valore-nuovo-riconoscibile" {
		t.Errorf("corpo = %q", resolved.Body())
	}
}

// Il segreto di un altro workspace non si tocca, e non si distingue da uno
// inesistente.
func TestUpdateIsScopedToTheWorkspace(t *testing.T) {
	f := newFixture(t)
	secret := f.crea("DIGEST_TOKEN", "valore-di-alice-lungo")

	_, err := f.svc.Update(t.Context(), altro, secret.ID, secrets.UpdateInput{
		Value: "valore-di-bob-lungo",
	})
	if !errors.Is(err, secrets.ErrSecretNotFound) {
		t.Fatalf("errore = %v, atteso ErrSecretNotFound", err)
	}
}

func TestUpdateValidatesTheNewValue(t *testing.T) {
	f := newFixture(t)
	secret := f.crea("DIGEST_TOKEN", "valore-abbastanza-lungo")

	_, err := f.svc.Update(t.Context(), utente, secret.ID, secrets.UpdateInput{Value: "corto"})
	if got := campi(t, err); !slices.Contains(got, "value/too_short") {
		t.Errorf("campi = %v", got)
	}
}

// ------------------------------------------------------------------ elenco

// L'elenco non contiene il valore, in nessuna variante e per nessun chiamante.
func TestListNeverCarriesTheValue(t *testing.T) {
	f := newFixture(t)
	const valore = "finto-valore-da-non-elencare"
	f.crea("DIGEST_TOKEN", valore)

	for _, includeRevoked := range []bool{false, true} {
		found, err := f.svc.List(t.Context(), utente, includeRevoked)
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if len(found) != 1 {
			t.Fatalf("segreti = %d", len(found))
		}
		if printed := fmt.Sprintf("%+v", found); strings.Contains(printed, valore) {
			t.Errorf("l'elenco contiene il valore: %s", printed)
		}
	}

	// E l'elenco è per workspace.
	found, err := f.svc.List(t.Context(), altro, true)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(found) != 0 {
		t.Errorf("l'elenco di un altro workspace ha %d segreti", len(found))
	}
}

// ------------------------------------------------------------------ revoca

// La revoca cancella il valore: non lo rende inaccessibile, lo fa smettere di
// esistere. Ed è immediata — il segreto non si risolve già alla richiesta
// successiva, senza cache da invalidare.
func TestRevokeDestroysTheValue(t *testing.T) {
	f := newFixture(t)
	secret := f.crea("DIGEST_TOKEN", "valore-da-distruggere-lungo")

	if err := f.svc.Revoke(t.Context(), utente, secret.ID); err != nil {
		t.Fatalf("Revoke: %v", err)
	}

	sealed := f.store.Sealed(secret.ID)
	if len(sealed.Ciphertext) != 0 || len(sealed.Nonce) != 0 {
		t.Fatal("la revoca ha lasciato il testo cifrato in tabella")
	}
	if sealed.RevokedAt == nil {
		t.Fatal("la revoca non ha datato la riga")
	}

	// Non si risolve più.
	_, err := f.svc.Resolve(t.Context(), utente, secrets.Request{Body: "${DIGEST_TOKEN}"})
	if got := campi(t, err); !slices.Contains(got, "request.body/unknown_secret") {
		t.Errorf("campi = %v: un segreto revocato non deve risolversi", got)
	}

	// Una seconda revoca non sposta la data.
	if err := f.svc.Revoke(t.Context(), utente, secret.ID); !errors.Is(err, secrets.ErrSecretNotFound) {
		t.Errorf("seconda revoca: errore = %v", err)
	}
}

// Il nome di un segreto revocato torna disponibile: senza, la revoca brucerebbe
// per sempre il riferimento scritto nel `cron.yaml`.
func TestRevokeFreesTheName(t *testing.T) {
	f := newFixture(t)
	secret := f.crea("DIGEST_TOKEN", "primo-valore-lungo")
	if err := f.svc.Revoke(t.Context(), utente, secret.ID); err != nil {
		t.Fatalf("Revoke: %v", err)
	}

	rinato := f.crea("DIGEST_TOKEN", "secondo-valore-lungo")
	if rinato.ID == secret.ID {
		t.Fatal("il segreto rinato è la stessa riga")
	}

	resolved, err := f.svc.Resolve(t.Context(), utente, secrets.Request{Body: "${DIGEST_TOKEN}"})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if resolved.Body() != "secondo-valore-lungo" {
		t.Errorf("corpo = %q", resolved.Body())
	}
}

func TestRevokeIsScopedToTheWorkspace(t *testing.T) {
	f := newFixture(t)
	secret := f.crea("DIGEST_TOKEN", "valore-di-alice-lungo")

	if err := f.svc.Revoke(t.Context(), altro, secret.ID); !errors.Is(err, secrets.ErrSecretNotFound) {
		t.Fatalf("errore = %v, atteso ErrSecretNotFound", err)
	}
	if f.store.Sealed(secret.ID).Revoked() {
		t.Fatal("un altro workspace ha revocato il segreto")
	}
}

// ------------------------------------------------------- validazione al sync

// La validazione al sync non decifra niente: per dire che un nome non c'è basta
// sapere che non c'è.
func TestValidateDoesNotDecrypt(t *testing.T) {
	f := newFixture(t)
	f.crea("DIGEST_TOKEN", "valore-abbastanza-lungo")

	req := secrets.Request{Headers: map[string]string{"Authorization": "Bearer ${DIGEST_TOKEN}"}}
	if err := f.svc.Validate(t.Context(), utente, req); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if f.store.CallCount("LiveByNames") != 0 {
		t.Error("la validazione ha letto il materiale cifrato")
	}
	if f.store.CallCount("LiveNames") == 0 {
		t.Error("la validazione non ha letto i nomi")
	}
}

// [Service.Available] è la lettura unica che il parser di `cron.yaml` fa per
// validare tutte le voci di un file.
func TestAvailableIsReadOnce(t *testing.T) {
	f := newFixture(t)
	f.crea("DIGEST_TOKEN", "valore-abbastanza-lungo")
	f.crea("API_KEY", "un-altro-valore-lungo")

	set, err := f.svc.Available(t.Context(), utente)
	if err != nil {
		t.Fatalf("Available: %v", err)
	}
	if names := set.Names(); !slices.Equal(names, []string{"API_KEY", "DIGEST_TOKEN"}) {
		t.Fatalf("Names = %v", names)
	}

	// Cinquanta job validati con una lettura sola.
	for i := range 50 {
		req := secrets.Request{URL: fmt.Sprintf("https://x.test/%d?k=${API_KEY}", i)}
		if err := set.Validate(req); err != nil {
			t.Fatalf("Validate: %v", err)
		}
	}
	if got := f.store.CallCount("LiveNames"); got != 1 {
		t.Errorf("letture = %d, attesa 1", got)
	}
}

// ------------------------------------------------------------- risoluzione

// Una richiesta senza riferimenti non legge niente: è la maggioranza dei job, e
// non deve pagare una query per esecuzione.
func TestResolveWithoutReferencesReadsNothing(t *testing.T) {
	f := newFixture(t)
	f.crea("DIGEST_TOKEN", "valore-abbastanza-lungo")

	req := secrets.Request{
		URL:     "https://api.example.com/health",
		Headers: map[string]string{"Accept": "application/json"},
	}
	resolved, err := f.svc.Resolve(t.Context(), utente, req)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if resolved.URL() != req.URL {
		t.Errorf("URL = %q", resolved.URL())
	}
	if f.store.CallCount("LiveByNames") != 0 {
		t.Error("una richiesta senza riferimenti ha letto i segreti")
	}
	// E il redattore c'è comunque, così che chi esegue produca l'estratto allo
	// stesso modo in tutti i casi.
	if got := resolved.Redactor().Excerpt([]byte("risposta"), 0).String(); got != "risposta" {
		t.Errorf("estratto = %q", got)
	}
}

// La risoluzione chiede i nomi che le servono, non tutti i segreti del
// workspace: la query gira a ogni occorrenza.
func TestResolveAsksOnlyForWhatItNeeds(t *testing.T) {
	f := newFixture(t)
	f.crea("DIGEST_TOKEN", "valore-che-serve-lungo")
	f.crea("ALTRO_SEGRETO", "valore-che-non-serve")

	resolved, err := f.svc.Resolve(t.Context(), utente, secrets.Request{
		Headers: map[string]string{"Authorization": "Bearer ${DIGEST_TOKEN}"},
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if resolved.Redactor().Len() != 1 {
		t.Errorf("segreti risolti = %d, atteso 1", resolved.Redactor().Len())
	}
	// Il valore che non serviva non entra nel redattore, e quindi non compare in
	// nessuna forma dentro al processo che esegue.
	excerpt := resolved.Redactor().Excerpt([]byte("valore-che-non-serve"), 0)
	if excerpt.String() != "valore-che-non-serve" {
		t.Errorf("un segreto non riferito è stato redatto: %s", excerpt)
	}
}

// Il segreto revocato fra il sync e l'esecuzione fa fallire l'esecuzione con lo
// **stesso messaggio** della validazione. Non fa partire una richiesta con un
// `${TOKEN}` letterale dentro alla testata `Authorization`: quella sarebbe una
// credenziale sbagliata spedita a un bersaglio, cioè il modo di farsi bloccare
// l'account.
func TestResolveFailsOnAMissingSecret(t *testing.T) {
	f := newFixture(t)

	req := secrets.Request{Headers: map[string]string{"Authorization": "Bearer ${DIGEST_TOKEN}"}}
	_, err := f.svc.Resolve(t.Context(), utente, req)
	if got := campi(t, err); !slices.Equal(got, []string{"request.headers.Authorization/unknown_secret"}) {
		t.Fatalf("campi = %v", got)
	}
}

// `last_used_at` si aggiorna con la stessa granularità di cinque minuti delle
// chiavi API: senza, ogni occorrenza sarebbe anche una scrittura, e con la
// risoluzione al secondo sono 86.400 al giorno per job.
func TestResolveTouchesLastUsedAtSparingly(t *testing.T) {
	f := newFixture(t)
	secret := f.crea("DIGEST_TOKEN", "valore-abbastanza-lungo")
	req := secrets.Request{Body: "${DIGEST_TOKEN}"}

	if _, err := f.svc.Resolve(t.Context(), utente, req); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if f.store.Sealed(secret.ID).LastUsedAt == nil {
		t.Fatal("la prima risoluzione non ha registrato l'uso")
	}

	// Un minuto dopo non si riscrive.
	f.now = f.now.Add(time.Minute)
	if _, err := f.svc.Resolve(t.Context(), utente, req); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got := f.store.CallCount("TouchSecrets"); got != 1 {
		t.Errorf("scritture = %d, attesa 1", got)
	}

	// Passata la soglia, sì.
	f.now = f.now.Add(6 * time.Minute)
	if _, err := f.svc.Resolve(t.Context(), utente, req); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got := f.store.CallCount("TouchSecrets"); got != 2 {
		t.Errorf("scritture = %d, attese 2", got)
	}
}

// Un guasto nell'aggiornamento di `last_used_at` non ferma l'esecuzione: non
// registrare l'uso è un difetto della traccia, non un motivo per non far partire
// una richiesta già composta.
func TestResolveSurvivesATouchFailure(t *testing.T) {
	f := newFixture(t)
	f.crea("DIGEST_TOKEN", "valore-abbastanza-lungo")
	f.store.Fail("TouchSecrets", errors.New("database in fiamme"))

	resolved, err := f.svc.Resolve(t.Context(), utente, secrets.Request{Body: "${DIGEST_TOKEN}"})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if resolved.Body() != "valore-abbastanza-lungo" {
		t.Errorf("corpo = %q", resolved.Body())
	}
	if !strings.Contains(f.logs.String(), "last_used_at") {
		t.Errorf("il guasto non è stato segnalato:\n%s", f.logs)
	}
}

// Un guasto in lettura **ferma** l'esecuzione: al contrario, una richiesta
// partirebbe con un riferimento non risolto al posto della credenziale.
func TestResolveStopsOnAReadFailure(t *testing.T) {
	f := newFixture(t)
	f.crea("DIGEST_TOKEN", "valore-abbastanza-lungo")
	f.store.Fail("LiveByNames", errors.New("database in fiamme"))

	if _, err := f.svc.Resolve(t.Context(), utente, secrets.Request{Body: "${DIGEST_TOKEN}"}); err == nil {
		t.Fatal("un guasto in lettura non ha fermato la risoluzione")
	}
}

// Una riga che non si decifra — chiave tolta dall'ambiente, colonna manomessa,
// testo cifrato copiato da un'altra riga — ferma l'esecuzione, e nel log finisce
// il nome del segreto, mai il materiale cifrato.
func TestResolveStopsOnAnUndecryptableRow(t *testing.T) {
	f := newFixture(t)
	f.store.Seed(secrets.Sealed{
		Secret:     secrets.Secret{ID: "s-rotto", UserID: utente, Name: "DIGEST_TOKEN"},
		Ciphertext: []byte("materiale-cifrato-che-non-si-apre"),
		Nonce:      bytes.Repeat([]byte{0x01}, 12),
	})

	_, err := f.svc.Resolve(t.Context(), utente, secrets.Request{Body: "${DIGEST_TOKEN}"})
	if err == nil {
		t.Fatal("una riga illeggibile non ha fermato la risoluzione")
	}
	if !strings.Contains(err.Error(), "DIGEST_TOKEN") {
		t.Errorf("l'errore non dice quale segreto: %v", err)
	}
	if strings.Contains(f.logs.String(), "materiale-cifrato") {
		t.Errorf("il materiale cifrato è finito nei log:\n%s", f.logs)
	}
}

// Un testo cifrato copiato dalla riga di un altro workspace non si apre: il
// legame crittografico con proprietario e nome è ciò che lo impedisce.
func TestResolveRejectsATransplantedCiphertext(t *testing.T) {
	f := newFixture(t)
	di_alice := f.crea("DIGEST_TOKEN", "valore-di-alice-lungo")
	rubato := f.store.Sealed(di_alice.ID)

	f.store.Seed(secrets.Sealed{
		Secret:     secrets.Secret{ID: "s-rubato", UserID: altro, Name: "DIGEST_TOKEN"},
		Ciphertext: rubato.Ciphertext,
		Nonce:      rubato.Nonce,
	})

	_, err := f.svc.Resolve(t.Context(), altro, secrets.Request{Body: "${DIGEST_TOKEN}"})
	if err == nil {
		t.Fatal("il testo cifrato di un altro workspace si è aperto")
	}
	if strings.Contains(fmt.Sprint(err), "valore-di-alice") {
		t.Errorf("l'errore contiene il valore: %v", err)
	}
}

// ------------------------------------------------------------------ rotazione

// Una riga cifrata con la chiave precedente si risolve ancora dopo la rotazione:
// è la proprietà che rende possibile ruotare ENCRYPTION_KEY senza un istante in
// cui metà dei job smette di funzionare.
func TestResolveWorksAcrossAKeyRotation(t *testing.T) {
	prima := newFixture(t)
	secret := prima.crea("DIGEST_TOKEN", "valore-scritto-prima-lungo")
	riga := prima.store.Sealed(secret.ID)

	// Lo stesso archivio, un processo con la chiave nuova per prima e la vecchia
	// dietro.
	nuova := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0x22}, 32))
	dopo := newFixture(t, "2:"+nuova+",1:"+testKey)
	dopo.store.Seed(riga)

	resolved, err := dopo.svc.Resolve(t.Context(), utente, secrets.Request{Body: "${DIGEST_TOKEN}"})
	if err != nil {
		t.Fatalf("Resolve dopo la rotazione: %v", err)
	}
	if resolved.Body() != "valore-scritto-prima-lungo" {
		t.Errorf("corpo = %q", resolved.Body())
	}

	// E un aggiornamento risigilla con la chiave attiva.
	if _, err := dopo.svc.Update(t.Context(), utente, riga.ID, secrets.UpdateInput{
		Value: "valore-riscritto-lungo",
	}); err != nil {
		t.Fatalf("Update: %v", err)
	}
	if got := dopo.store.Sealed(riga.ID).KeyVersion; got != 2 {
		t.Errorf("KeyVersion dopo l'aggiornamento = %d, attesa 2", got)
	}
}

// ------------------------------------------------------------- costruzione

// Senza chiave il servizio non nasce. Un segreto che non si può cifrare non va
// salvato in chiaro «per intanto».
func TestNewServiceRefusesToStartWithoutAKey(t *testing.T) {
	_, err := secrets.NewService(secrets.Options{Store: secretstest.NewStore()})
	if err == nil {
		t.Fatal("il servizio è nato senza chiave di cifratura")
	}
	if !strings.Contains(err.Error(), secretbox.EnvVar) {
		t.Errorf("l'errore non dice quale variabile manca: %v", err)
	}

	keys, err := secretbox.NewKeyring(testKey)
	if err != nil {
		t.Fatalf("NewKeyring: %v", err)
	}
	if _, err := secrets.NewService(secrets.Options{Keyring: keys}); err == nil {
		t.Error("il servizio è nato senza archivio")
	}
}

// ------------------------------------------- il contratto con chi esegue

// Il contratto, in una riga, per chi arriva qui da un test rosso.
const contratto = `Il contratto con l'esecutore HTTP (internal/httpexec, #390):

  resolved, err := secretsSvc.Resolve(ctx, occ.Job.UserID, secrets.Request{
      URL: occ.Job.URL, Headers: headers, Body: body,
  })
  // la richiesta si compone da resolved.URL(), .Headers(), .Body()

  result.ResponseExcerpt = resolved.Redactor().Excerpt(raw, maxBytes).String()
  record.Error           = resolved.Redactor().ErrorText(err, maxBytes).String()
  logger.Info("...", slog.Any("request", resolved))   // NON resolved.URL()

Perché: l'estratto della risposta e il testo dell'errore finiscono in
job_executions, che l'utente rilegge dall'API e che restiamo a conservare
(privacy policy §2.2). Sono due testi che non controlliamo — il bersaglio
riflette la credenziale nei propri messaggi d'errore — e senza la redazione R43
sarebbe rispettata da noi e violata da loro.`

// Il giro completo che l'esecutore HTTP (#390) deve fare.
//
// # Perché questo test esiste, e perché è scritto così
//
// L'esecutore è stato mergiato prima di questa PR, quindi **la sequenza qui
// sotto non è ancora quella che gira in produzione**: `internal/httpexec`
// compone la richiesta dai campi grezzi del job e non passa da
// [secrets.Service.Resolve]. Finché quel collegamento non c'è, questo test è
// l'unico documento eseguibile del contratto — è il posto in cui chi lo
// implementerà viene a leggere che cosa deve chiamare.
//
// Da qui la forma: ogni clausola è un sottotest che si chiama come la clausola,
// così che un `--- FAIL` dica **quale** promessa è saltata prima ancora di
// leggere il messaggio; ogni messaggio d'errore stampa [contratto], così che
// chi lo vede non debba cercare la documentazione; e ogni clausola comincia
// verificando che il caso stia davvero esercitando ciò che dichiara — un test
// di non-perdita che passa perché il segreto non c'era è peggio di un test
// assente, perché autorizza a non guardare.
func TestExecutorContract(t *testing.T) {
	const token = "finta-credenziale-del-cliente"

	f := newFixture(t)
	f.crea("DIGEST_TOKEN", token)

	richiesta := secrets.Request{
		URL:     "https://api.example.com/tasks/digest?tenant=acme",
		Headers: map[string]string{"Authorization": "Bearer ${DIGEST_TOKEN}"},
		Body:    `{"kind":"daily"}`,
	}

	// --------------------------------------------------------------- clausola 1
	//
	// La risoluzione. I valori si prendono con i metodi, non con dei campi: una
	// struttura con `URL string` esportato finisce in un `%+v` alla prima
	// diagnostica, e con lei il token che l'espansione ha appena messo dentro.
	resolved, err := f.svc.Resolve(t.Context(), utente, richiesta)
	if err != nil {
		t.Fatalf("Resolve ha rifiutato una richiesta valida: %v\n\n%s", err, contratto)
	}

	t.Run("1. Resolve espande i riferimenti nella richiesta", func(t *testing.T) {
		if got := resolved.Headers()["Authorization"]; got != "Bearer "+token {
			t.Fatalf("la testata non è stata risolta: %q, atteso %q\n\n%s",
				got, "Bearer "+token, contratto)
		}
		if resolved.Redactor().Len() != 1 {
			t.Fatalf("il redattore conosce %d segreti, atteso 1: senza, non redige niente\n\n%s",
				resolved.Redactor().Len(), contratto)
		}
	})

	// --------------------------------------------------------------- clausola 2
	//
	// L'estratto della risposta. Il bersaglio riflette la nostra credenziale nel
	// proprio messaggio d'errore, come fanno molte API.
	t.Run("2. l'estratto della risposta passa dal redattore", func(t *testing.T) {
		grezzo := []byte(`{"error":"la credenziale ` + token + ` è scaduta"}`)

		// Il caso esercita davvero ciò che dichiara: senza questo controllo, un
		// domani in cui il corpo di prova non contiene più il token renderebbe la
		// verifica seguente vera per vuoto.
		if !bytes.Contains(grezzo, []byte(token)) {
			t.Fatalf("il corpo di prova non contiene la credenziale: questo caso non prova niente")
		}

		excerpt := resolved.Redactor().Excerpt(grezzo, 8<<10)
		if strings.Contains(excerpt.String(), token) {
			t.Errorf(
				"l'estratto conserva la credenziale che il bersaglio ha rimandato indietro:\n"+
					"  %s\nQuesto testo finisce in job_executions.response_excerpt, che l'utente "+
					"rilegge dall'API.\n\n%s", excerpt, contratto)
		}
		if !strings.Contains(excerpt.String(), "${DIGEST_TOKEN}") {
			t.Errorf("l'estratto non dice che cosa è stato tolto: %s\n"+
				"Al posto del valore ci va il riferimento, altrimenti chi legge il registro "+
				"non capisce perché il testo non torna.\n\n%s", excerpt, contratto)
		}
	})

	// --------------------------------------------------------------- clausola 3
	//
	// Il testo dell'errore. È la via di fuga meno evidente delle due:
	// `http.Client` avvolge ogni errore in un `*url.Error` che stampa l'URL
	// completo, query compresa — quindi un segreto in querystring finirebbe nel
	// registro **anche se la risposta non è mai arrivata**.
	//
	// `internal/httpexec` toglie già quell'involucro per conto suo, ed è la difesa
	// giusta al posto giusto. Questa è la seconda, e serve perché togliere
	// l'involucro protegge dal caso noto: la causa che resta è un errore di
	// qualcun altro — un driver, una libreria TLS, un redirect — e nessuno di loro
	// ha promesso di non citare l'indirizzo a cui stava andando.
	t.Run("3. il testo dell'errore passa dal redattore", func(t *testing.T) {
		guasto := fmt.Errorf(`Post %q: EOF`, resolved.URL()+"&t="+token)

		if !strings.Contains(guasto.Error(), token) {
			t.Fatalf("l'errore di prova non contiene la credenziale: questo caso non prova niente")
		}

		errText := resolved.Redactor().ErrorText(guasto, 8<<10)
		if strings.Contains(errText.String(), token) {
			t.Errorf(
				"il testo dell'errore conserva la credenziale:\n  %s\n"+
					"Questo testo finisce in job_executions.error, che l'utente rilegge "+
					"dall'API.\n\n%s", errText, contratto)
		}
		if errText.Empty() {
			t.Errorf("l'errore è stato cancellato invece che redatto: chi legge il registro " +
				"ha bisogno di sapere che cosa è andato storto")
		}
	})

	// --------------------------------------------------------------- clausola 4
	//
	// Il log. Ciò che si scrive è il [secrets.Resolved], che stampa la richiesta
	// **non** risolta. `slog.String("url", resolved.URL())` è la riga sbagliata, e
	// somiglia molto a quella giusta.
	t.Run("4. nel log va il Resolved, non i suoi valori", func(t *testing.T) {
		riga := logged(t, resolved)

		if strings.Contains(riga, token) {
			t.Errorf("la riga di log contiene la credenziale:\n  %s\n\n%s", riga, contratto)
		}
		if !strings.Contains(riga, "api.example.com/tasks/digest") {
			t.Errorf("la riga di log non dice quale richiesta è partita:\n  %s\n"+
				"Il Resolved stampa la richiesta non risolta, che è esattamente ciò che "+
				"serve a capire quale job ha fatto cosa.\n\n%s", riga, contratto)
		}

		// E la riga sbagliata sarebbe stata davvero sbagliata: se un giorno
		// `resolved.URL()` smettesse di contenere il segreto, la clausola qui sopra
		// diventerebbe vera per vuoto e questo test smetterebbe di difendere
		// qualcosa.
		if !strings.Contains(resolved.URL()+resolved.Headers()["Authorization"], token) {
			t.Fatal("i valori risolti non contengono la credenziale: questo caso non prova niente")
		}
	})

	// --------------------------------------------------------------- clausola 5
	//
	// La garanzia sta nel tipo, non nella diligenza. [secrets.Excerpt] non ha
	// campi esportati e non ha un costruttore pubblico: l'unico modo di ottenerne
	// uno con del testo dentro è chiamare uno dei due metodi del redattore.
	//
	// Il giorno in cui qualcuno aggiungesse un campo esportato o un
	// `NewExcerpt(string)` «per comodità nei test», la promessa smetterebbe di
	// essere strutturale e tornerebbe a essere una cosa da ricordarsi. Questo
	// sottotest è il guardiano di quella differenza.
	t.Run("5. Excerpt non è costruibile senza passare dalla redazione", func(t *testing.T) {
		typ := reflect.TypeOf(secrets.Excerpt{})
		for i := range typ.NumField() {
			if field := typ.Field(i); field.IsExported() {
				t.Errorf(
					"secrets.Excerpt ha il campo esportato %q: con un campo pubblico chiunque "+
						"può costruire un estratto senza redigerlo, e il tipo smette di essere "+
						"una garanzia.\n\n%s", field.Name, contratto)
			}
		}

		// Il valore zero è ammesso — un'esecuzione senza segreti deve poter
		// produrre un estratto — ma è vuoto: non trasporta testo non redatto.
		var zero secrets.Excerpt
		if !zero.Empty() || zero.String() != "" {
			t.Errorf("il valore zero di Excerpt non è vuoto: %q", zero)
		}
	})

	// --------------------------------------------------------------- clausola 6
	//
	// Il caso senza segreti deve costare come prima. La maggioranza dei job non
	// riferisce niente, e se il contratto rendesse più laborioso quel caso
	// verrebbe aggirato proprio lì.
	t.Run("6. una richiesta senza segreti segue la stessa strada", func(t *testing.T) {
		senza, err := f.svc.Resolve(t.Context(), utente, secrets.Request{
			URL: "https://api.example.com/health",
		})
		if err != nil {
			t.Fatalf("Resolve: %v", err)
		}
		if got := senza.Redactor().Excerpt([]byte("ok"), 8<<10).String(); got != "ok" {
			t.Errorf("estratto = %q, atteso %q: il redattore vuoto non deve toccare niente\n\n%s",
				got, "ok", contratto)
		}
	})
}
