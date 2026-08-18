package aicreds_test

import (
	"bytes"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/apdsoftware/postqron/services/api/internal/aicreds"
	"github.com/apdsoftware/postqron/services/api/internal/aicredstest"
	"github.com/apdsoftware/postqron/services/api/internal/secretbox"
)

// ---------------------------------------------------------------- impalcatura

// chiaveDiProva è una chiave a 32 byte in base64, la forma che `openssl rand
// -base64 32` produce.
const chiaveDiProva = "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY="

type fixture struct {
	t     *testing.T
	svc   *aicreds.Service
	store *aicredstest.Store
	logs  *bytes.Buffer
	now   time.Time
}

func newFixture(t *testing.T) *fixture {
	t.Helper()

	keyring, err := secretbox.NewKeyring(chiaveDiProva)
	if err != nil {
		t.Fatalf("NewKeyring: %v", err)
	}

	f := &fixture{
		t:     t,
		store: aicredstest.NewStore(),
		logs:  &bytes.Buffer{},
		now:   time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC),
	}
	// Il livello è Debug apposta: il test che cerca la chiave nei log deve
	// guardare **tutto** ciò che il servizio scrive, non solo ciò che passerebbe
	// una soglia di produzione.
	logger := slog.New(slog.NewTextHandler(f.logs, &slog.HandlerOptions{Level: slog.LevelDebug}))

	f.svc, err = aicreds.NewService(aicreds.Options{
		Store:   f.store,
		Keyring: keyring,
		Logger:  logger,
		Now:     func() time.Time { return f.now },
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	return f
}

func (f *fixture) salva(userID string, provider aicreds.Provider) aicreds.Credential {
	f.t.Helper()
	credential, err := f.svc.Save(f.t.Context(), userID, aicreds.SaveInput{
		Provider: string(provider),
		Key:      secretbox.Plaintext(chiave),
		Label:    "chiave di " + string(provider),
	})
	if err != nil {
		f.t.Fatalf("Save(%s): %v", provider, err)
	}
	return credential
}

// ------------------------------------------------- 1. cifrata a riposo

// Ciò che resta scritto non è la chiave, in nessuna delle due colonne, e si
// riapre solo con il keyring e con il legame giusto.
func TestSaveStoresOnlyCiphertext(t *testing.T) {
	f := newFixture(t)
	credential := f.salva("u-1", aicreds.Anthropic)

	sealed := f.store.Sealed(credential.ID)
	if len(sealed.Ciphertext) == 0 || len(sealed.Nonce) == 0 {
		t.Fatal("la scrittura non ha prodotto materiale cifrato")
	}
	for nome, colonna := range map[string][]byte{"ciphertext": sealed.Ciphertext, "nonce": sealed.Nonce} {
		if bytes.Contains(colonna, []byte(chiave)) {
			t.Errorf("la colonna %s contiene la chiave in chiaro", nome)
		}
	}

	// E il giro completo torna: cifrare non è cancellare.
	key, err := f.svc.Reveal(t.Context(), "u-1", aicreds.Anthropic)
	if err != nil {
		t.Fatalf("Reveal: %v", err)
	}
	if key.Reveal() != chiave {
		t.Error("la chiave riletta non è quella scritta")
	}
}

// Il materiale è legato alla riga che lo contiene: spostato su un'altra —
// altro utente, altro provider — **non si apre**. Senza questo legame, chi ha
// accesso in scrittura al database potrebbe farsi usare la chiave di qualcun
// altro copiandone due colonne.
func TestCiphertextIsBoundToItsRow(t *testing.T) {
	f := newFixture(t)
	vittima := f.salva("u-vittima", aicreds.Anthropic)
	attaccante := f.salva("u-attaccante", aicreds.Anthropic)

	rubato := f.store.Sealed(vittima.ID)
	f.store.Corrupt(attaccante.ID, rubato.Ciphertext)

	if _, err := f.svc.Reveal(t.Context(), "u-attaccante", aicreds.Anthropic); err == nil {
		t.Fatal("il materiale di un altro utente è stato aperto")
	}

	// Lo stesso vale fra provider dello stesso utente: il legame porta anche
	// quello, e il dominio in testa lo separa dai segreti del workspace.
	altroProvider := f.salva("u-vittima", aicreds.OpenAI)
	f.store.Corrupt(altroProvider.ID, rubato.Ciphertext)
	if _, err := f.svc.Reveal(t.Context(), "u-vittima", aicreds.OpenAI); err == nil {
		t.Fatal("il materiale di un altro provider è stato aperto")
	}
}

// ------------------------------------------ 2. non esce, nemmeno negli errori

// **Nessun log contiene la chiave, e nemmeno un errore.**
//
// Il test esercita ogni strada del servizio, compresa quella che fallisce, e poi
// cerca la chiave in tutto ciò che è stato scritto e in tutto ciò che è stato
// restituito. È la forma che il requisito ha davvero: «mai loggata» non si
// verifica leggendo il codice una volta, si verifica facendo passare il valore
// da tutte le porte e guardando che non esca da nessuna.
func TestTheKeyNeverReachesTheLogsNorAnError(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	var errori []error

	credential := f.salva("u-1", aicreds.Anthropic)
	if _, err := f.svc.List(ctx, "u-1", true); err != nil {
		t.Fatalf("List: %v", err)
	}
	if _, err := f.svc.Reveal(ctx, "u-1", aicreds.Anthropic); err != nil {
		t.Fatalf("Reveal: %v", err)
	}

	// La validazione rifiuta: l'errore descrive il campo, non il valore.
	_, err := f.svc.Save(ctx, "u-1", aicreds.SaveInput{
		Provider: "anthropic",
		Key:      secretbox.Plaintext(chiave + "\n"),
	})
	errori = append(errori, err)

	// Provider sconosciuto, con la chiave buona nel corpo.
	_, err = f.svc.Save(ctx, "u-1", aicreds.SaveInput{
		Provider: "un-provider-che-non-esiste",
		Key:      secretbox.Plaintext(chiave),
	})
	errori = append(errori, err)

	// La riga manomessa: è il caso in cui sarebbe comodo stampare il materiale
	// per capire cosa non si è aperto.
	f.store.Corrupt(credential.ID, []byte("materiale-manomesso-che-non-si-apre"))
	_, err = f.svc.Reveal(ctx, "u-1", aicreds.Anthropic)
	errori = append(errori, err)

	// Un guasto della persistenza.
	f.store.Fail("LiveByProvider", errors.New("connessione persa"))
	_, err = f.svc.Reveal(ctx, "u-1", aicreds.OpenAI)
	errori = append(errori, err)

	// La revoca di una chiave che non c'è.
	errori = append(errori, f.svc.Revoke(ctx, "u-1", "non-esiste"))

	for indice, err := range errori {
		if err == nil {
			t.Errorf("l'errore %d è nil: il caso non è stato esercitato", indice)
			continue
		}
		if strings.Contains(err.Error(), chiave) {
			t.Errorf("l'errore %d contiene la chiave: %v", indice, err)
		}
		// E nemmeno stampandolo con i verbi che qualcuno userà.
		for _, forma := range []string{fmt.Sprintf("%v", err), fmt.Sprintf("%+v", err), fmt.Sprintf("%q", err)} {
			if strings.Contains(forma, chiave) {
				t.Errorf("l'errore %d stampato contiene la chiave: %s", indice, forma)
			}
		}
	}

	if strings.Contains(f.logs.String(), chiave) {
		t.Fatalf("la chiave è finita nei log:\n%s", f.logs)
	}
	// E i log dicono comunque *di che cosa* stanno parlando: un log che non
	// contiene la chiave perché non contiene niente non prova niente.
	if !strings.Contains(f.logs.String(), "anthropic") {
		t.Errorf("i log non identificano la credenziale:\n%s", f.logs)
	}
}

// L'elenco non porta il materiale cifrato fuori dallo Store: [aicreds.Credential]
// non ha i campi, e questo verifica che il Service non lo aggiri.
func TestListCarriesNoMaterial(t *testing.T) {
	f := newFixture(t)
	f.salva("u-1", aicreds.Anthropic)

	credentials, err := f.svc.List(t.Context(), "u-1", false)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(credentials) != 1 {
		t.Fatalf("credenziali = %d, attesa 1", len(credentials))
	}
	if printed := fmt.Sprintf("%+v", credentials); strings.Contains(printed, chiave) {
		t.Errorf("l'elenco stampato contiene la chiave: %s", printed)
	}
	if credentials[0].Provider != aicreds.Anthropic || credentials[0].Label != "chiave di anthropic" {
		t.Errorf("l'elenco non porta i dati che deve portare: %+v", credentials[0])
	}
}

// ---------------------------------------------------- 3. la revoca cancella

// **Revocare cancella il materiale**, non lo nasconde.
//
// Dopo la revoca non c'è più niente da decifrare: la riga resta come traccia,
// ma nemmeno con `ENCRYPTION_KEY` alla mano si riottiene la chiave. È la
// differenza fra «revocata» come stato e «revocata» come fatto.
func TestRevokeDestroysTheMaterial(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	credential := f.salva("u-1", aicreds.Anthropic)

	if err := f.svc.Revoke(ctx, "u-1", credential.ID); err != nil {
		t.Fatalf("Revoke: %v", err)
	}

	sealed := f.store.Sealed(credential.ID)
	if len(sealed.Ciphertext) != 0 || len(sealed.Nonce) != 0 {
		t.Error("la revoca ha lasciato materiale cifrato a riposo")
	}
	if sealed.RevokedAt == nil {
		t.Error("la revoca non ha datato la riga")
	}
	// La riga resta: è la traccia che spiega perché l'analisi dei log ha smesso
	// di funzionare.
	if f.store.Count() != 1 {
		t.Errorf("righe = %d, attesa 1: la revoca non deve cancellare la traccia", f.store.Count())
	}

	if _, err := f.svc.Reveal(ctx, "u-1", aicreds.Anthropic); !errors.Is(err, aicreds.ErrNoLiveKey) {
		t.Errorf("Reveal dopo la revoca = %v, atteso ErrNoLiveKey", err)
	}

	// Revocare due volte non è un'operazione riuscita: distinguere «non esiste»
	// da «era già revocata» direbbe a chiunque se un identificativo altrui è
	// vivo, quindi i due casi sono lo stesso errore.
	if err := f.svc.Revoke(ctx, "u-1", credential.ID); !errors.Is(err, aicreds.ErrCredentialNotFound) {
		t.Errorf("seconda revoca = %v, atteso ErrCredentialNotFound", err)
	}
}

// La revoca è dell'utente: l'identificativo di una chiave altrui non basta a
// revocarla, e la risposta è la stessa di una chiave inesistente.
func TestRevokeIsScopedToTheUser(t *testing.T) {
	f := newFixture(t)
	credential := f.salva("u-1", aicreds.Anthropic)

	if err := f.svc.Revoke(t.Context(), "u-2", credential.ID); !errors.Is(err, aicreds.ErrCredentialNotFound) {
		t.Fatalf("revoca da un altro utente = %v, atteso ErrCredentialNotFound", err)
	}
	if f.store.Sealed(credential.ID).Revoked() {
		t.Error("la chiave è stata revocata da un altro utente")
	}
}

// Revocata una chiave, il provider torna libero: l'unicità della 0016 vale fra
// le sole chiavi vive. Senza, la prima revoca brucerebbe per sempre lo slot e
// l'utente resterebbe senza BYOK per quel fornitore.
func TestProviderIsFreeAgainAfterRevocation(t *testing.T) {
	f := newFixture(t)
	prima := f.salva("u-1", aicreds.Anthropic)

	if err := f.svc.Revoke(t.Context(), "u-1", prima.ID); err != nil {
		t.Fatalf("Revoke: %v", err)
	}

	f.now = f.now.Add(time.Hour)
	dopo := f.salva("u-1", aicreds.Anthropic)
	if dopo.ID == prima.ID {
		t.Error("la chiave nuova ha riusato la riga revocata invece di crearne una")
	}
	if _, err := f.svc.Reveal(t.Context(), "u-1", aicreds.Anthropic); err != nil {
		t.Errorf("la chiave nuova non si legge: %v", err)
	}
}

// ------------------------------------------------------- 4. una per provider

// Incollare di nuovo la chiave di un provider la **sostituisce**: non è un
// conflitto da segnalare, è la stessa intenzione della prima volta. Il
// materiale viene rigenerato per intero, quindi due scritture della stessa
// chiave non producono lo stesso testo cifrato — il nonce cambia a ogni giro.
func TestSaveReplacesTheLiveKeyOfTheProvider(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()

	prima := f.salva("u-1", aicreds.Anthropic)
	primoMateriale := f.store.Sealed(prima.ID).Ciphertext

	nuova := secretbox.Plaintext("sk-ant-api03-la-seconda-chiave-che-sostituisce-la-prima")
	dopo, err := f.svc.Save(ctx, "u-1", aicreds.SaveInput{
		Provider: "anthropic", Key: nuova, Label: "ruotata",
	})
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	if dopo.ID != prima.ID {
		t.Error("la sostituzione ha creato una seconda riga viva per lo stesso provider")
	}
	if f.store.Count() != 1 {
		t.Errorf("righe = %d, attesa 1", f.store.Count())
	}
	if bytes.Equal(primoMateriale, f.store.Sealed(prima.ID).Ciphertext) {
		t.Error("il materiale non è stato rigenerato")
	}

	key, err := f.svc.Reveal(ctx, "u-1", aicreds.Anthropic)
	if err != nil {
		t.Fatalf("Reveal: %v", err)
	}
	if key.Reveal() != nuova.Reveal() {
		t.Error("Reveal restituisce ancora la chiave vecchia")
	}
	if dopo.Label != "ruotata" {
		t.Errorf("etichetta = %q, attesa \"ruotata\"", dopo.Label)
	}
}

// Provider diversi convivono, e ciascuno vede solo la propria chiave.
func TestProvidersAreIndependent(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()

	f.salva("u-1", aicreds.Anthropic)
	openai, err := f.svc.Save(ctx, "u-1", aicreds.SaveInput{
		Provider: "openai", Key: secretbox.Plaintext("sk-proj-una-chiave-openai-abbastanza-lunga"),
	})
	if err != nil {
		t.Fatalf("Save(openai): %v", err)
	}

	credentials, err := f.svc.List(ctx, "u-1", false)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(credentials) != 2 {
		t.Fatalf("credenziali = %d, attese 2", len(credentials))
	}

	key, err := f.svc.Reveal(ctx, "u-1", aicreds.OpenAI)
	if err != nil {
		t.Fatalf("Reveal(openai): %v", err)
	}
	if key.Reveal() != "sk-proj-una-chiave-openai-abbastanza-lunga" {
		t.Error("Reveal ha restituito la chiave di un altro provider")
	}
	if openai.Provider != aicreds.OpenAI {
		t.Errorf("provider = %q", openai.Provider)
	}

	// E la chiave di un utente non è quella di un altro.
	if _, err := f.svc.Reveal(ctx, "u-2", aicreds.OpenAI); !errors.Is(err, aicreds.ErrNoLiveKey) {
		t.Errorf("Reveal per un altro utente = %v, atteso ErrNoLiveKey", err)
	}
}

// -------------------------------------------------------------- validazione

// La validazione rifiuta ciò che non è una chiave, e lo dice per campo: chi
// compila un form vuole sapere in un giro tutto quello che c'è da correggere.
func TestSaveValidation(t *testing.T) {
	f := newFixture(t)

	casi := map[string]struct {
		in     aicreds.SaveInput
		campo  string
		codice string
	}{
		"provider sconosciuto": {
			aicreds.SaveInput{Provider: "claude", Key: secretbox.Plaintext(chiave)},
			"provider", "invalid_provider",
		},
		"chiave vuota": {
			aicreds.SaveInput{Provider: "anthropic"},
			"key", "required",
		},
		"a capo finale": {
			aicreds.SaveInput{Provider: "anthropic", Key: secretbox.Plaintext(chiave + "\n")},
			"key", "surrounding_whitespace",
		},
		"spazio iniziale": {
			aicreds.SaveInput{Provider: "anthropic", Key: secretbox.Plaintext(" " + chiave)},
			"key", "surrounding_whitespace",
		},
		"riga di .env incollata": {
			aicreds.SaveInput{Provider: "anthropic",
				Key: secretbox.Plaintext("ANTHROPIC_API_KEY=" + chiave + "\nALTRO=1")},
			"key", "control_characters",
		},
		"chiave troncata": {
			aicreds.SaveInput{Provider: "anthropic", Key: secretbox.Plaintext("sk-ant-123")},
			"key", "too_short",
		},
		"chiave enorme": {
			aicreds.SaveInput{Provider: "anthropic",
				Key: secretbox.Plaintext(strings.Repeat("a", aicreds.MaxKeyLength+1))},
			"key", "too_long",
		},
		"etichetta lunga": {
			aicreds.SaveInput{Provider: "anthropic", Key: secretbox.Plaintext(chiave),
				Label: strings.Repeat("e", aicreds.MaxLabelLength+1)},
			"label", "too_long",
		},
	}

	for nome, caso := range casi {
		t.Run(nome, func(t *testing.T) {
			_, err := f.svc.Save(t.Context(), "u-1", caso.in)
			invalid, ok := aicreds.AsValidation(err)
			if !ok {
				t.Fatalf("errore = %v, atteso un ValidationError", err)
			}
			trovato := false
			for _, field := range invalid.Fields {
				if field.Field == caso.campo && field.Code == caso.codice {
					trovato = true
				}
			}
			if !trovato {
				t.Errorf("campi = %+v, atteso %s/%s", invalid.Fields, caso.campo, caso.codice)
			}
			if f.store.Count() != 0 {
				t.Error("una richiesta rifiutata ha scritto qualcosa")
			}
		})
	}
}

// I motivi si raccolgono tutti in un giro.
func TestValidationCollectsEveryReason(t *testing.T) {
	f := newFixture(t)

	_, err := f.svc.Save(t.Context(), "u-1", aicreds.SaveInput{
		Provider: "claude",
		Key:      secretbox.Plaintext("corta"),
		Label:    strings.Repeat("e", aicreds.MaxLabelLength+1),
	})
	invalid, ok := aicreds.AsValidation(err)
	if !ok {
		t.Fatalf("errore = %v, atteso un ValidationError", err)
	}
	if len(invalid.Fields) != 3 {
		t.Errorf("campi = %d, attesi 3: %+v", len(invalid.Fields), invalid.Fields)
	}
}

// ------------------------------------------------------------ last_used_at

// `last_used_at` si aggiorna alla lettura, ma non a ogni lettura: sotto la
// soglia la scrittura si salta. Con il debugging automatico di un job che
// fallisce ogni minuto, una scrittura per lettura sarebbe una scrittura al
// minuto per registrare un fatto che interessa al giorno.
func TestLastUsedAtIsThrottled(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	f.salva("u-1", aicreds.Anthropic)

	if _, err := f.svc.Reveal(ctx, "u-1", aicreds.Anthropic); err != nil {
		t.Fatalf("Reveal: %v", err)
	}
	if f.store.CallCount("TouchCredential") != 1 {
		t.Fatalf("la prima lettura non ha registrato l'uso")
	}

	f.now = f.now.Add(time.Minute)
	if _, err := f.svc.Reveal(ctx, "u-1", aicreds.Anthropic); err != nil {
		t.Fatalf("Reveal: %v", err)
	}
	if f.store.CallCount("TouchCredential") != 1 {
		t.Error("una lettura dopo un minuto ha riscritto last_used_at")
	}

	f.now = f.now.Add(10 * time.Minute)
	if _, err := f.svc.Reveal(ctx, "u-1", aicreds.Anthropic); err != nil {
		t.Fatalf("Reveal: %v", err)
	}
	if f.store.CallCount("TouchCredential") != 2 {
		t.Error("una lettura dopo undici minuti non ha riscritto last_used_at")
	}
}

// Un guasto nella registrazione dell'uso non impedisce di ottenere la chiave:
// non registrare l'uso è un difetto della traccia, non un motivo per non fare
// partire un'analisi già autorizzata.
func TestTouchFailureDoesNotBlockTheRead(t *testing.T) {
	f := newFixture(t)
	f.salva("u-1", aicreds.Anthropic)
	f.store.Fail("TouchCredential", errors.New("scrittura fallita"))

	key, err := f.svc.Reveal(t.Context(), "u-1", aicreds.Anthropic)
	if err != nil {
		t.Fatalf("Reveal: %v", err)
	}
	if key.Reveal() != chiave {
		t.Error("la chiave non è quella scritta")
	}
	if !strings.Contains(f.logs.String(), "last_used_at") {
		t.Error("il guasto non è stato segnalato nei log")
	}
}

// ------------------------------------------------------------ costruzione

// Un keyring non inizializzato è un errore d'avvio, non un servizio che
// funziona a metà: senza chiave non si cifra, e una chiave AI che non si può
// cifrare non va salvata in chiaro «per intanto».
func TestNewServiceRequiresStoreAndKeyring(t *testing.T) {
	keyring, err := secretbox.NewKeyring(chiaveDiProva)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := aicreds.NewService(aicreds.Options{Keyring: keyring}); err == nil {
		t.Error("un servizio senza Store è stato costruito")
	}
	if _, err := aicreds.NewService(aicreds.Options{Store: aicredstest.NewStore()}); err == nil {
		t.Error("un servizio senza keyring è stato costruito")
	}
}

// Una riga cifrata con una chiave che il processo non ha più non è persa: manca
// la chiave, e l'errore lo dice. È il caso di una rotazione fatta a metà, e la
// distinzione conta perché la risposta è «rimetti la vecchia in ENCRYPTION_KEY»
// e non «la chiave dell'utente è andata».
func TestRotationKeepsOldRowsReadable(t *testing.T) {
	f := newFixture(t)
	credential := f.salva("u-1", aicreds.Anthropic)
	sealed := f.store.Sealed(credential.ID)

	if sealed.KeyVersion != 1 {
		t.Fatalf("key_version = %d, attesa 1", sealed.KeyVersion)
	}

	// Un secondo keyring che cifra con la 2 e apre ancora la 1: è la forma che
	// una rotazione produce.
	seconda := "2:" + chiaveDiProva[:len(chiaveDiProva)-2] + "Q=" + ",1:" + chiaveDiProva
	keyring, err := secretbox.NewKeyring(seconda)
	if err != nil {
		t.Fatalf("NewKeyring ruotato: %v", err)
	}
	if !keyring.NeedsRotation(secretbox.Box{
		Ciphertext: sealed.Ciphertext, Nonce: sealed.Nonce, KeyVersion: sealed.KeyVersion,
	}) {
		t.Error("una riga cifrata con la chiave vecchia non risulta da risigillare")
	}

	ruotato, err := aicreds.NewService(aicreds.Options{
		Store: f.store, Keyring: keyring,
		Logger: slog.New(slog.NewTextHandler(f.logs, nil)),
		Now:    func() time.Time { return f.now },
	})
	if err != nil {
		t.Fatalf("NewService ruotato: %v", err)
	}

	key, err := ruotato.Reveal(t.Context(), "u-1", aicreds.Anthropic)
	if err != nil {
		t.Fatalf("Reveal con il keyring ruotato: %v", err)
	}
	if key.Reveal() != chiave {
		t.Error("la chiave scritta prima della rotazione non si rilegge")
	}

	// E riscriverla la porta sulla chiave attiva: un aggiornamento è anche una
	// rotazione della singola riga.
	if _, err := ruotato.Save(t.Context(), "u-1", aicreds.SaveInput{
		Provider: "anthropic", Key: secretbox.Plaintext(chiave),
	}); err != nil {
		t.Fatalf("Save con il keyring ruotato: %v", err)
	}
	if v := f.store.Sealed(credential.ID).KeyVersion; v != 2 {
		t.Errorf("key_version dopo la riscrittura = %d, attesa 2", v)
	}
}
