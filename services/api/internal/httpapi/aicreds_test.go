package httpapi_test

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/apdsoftware/postqron/services/api/internal/aicreds"
	"github.com/apdsoftware/postqron/services/api/internal/aicredstest"
	"github.com/apdsoftware/postqron/services/api/internal/auth"
	"github.com/apdsoftware/postqron/services/api/internal/config"
	"github.com/apdsoftware/postqron/services/api/internal/httpapi"
	"github.com/apdsoftware/postqron/services/api/internal/secretbox"
)

// chiaveAI è la chiave che nessuna risposta e nessun log devono contenere. È
// volutamente lunga e riconoscibile: se sfugge, si vede.
const chiaveAI = "sk-ant-api03-finta-chiave-che-non-deve-uscire-mai"

// ---------------------------------------------------------------- impalcatura

type aiKeysFixture struct {
	*api
	store *aicredstest.Store
	user  auth.User
	token string
}

func newAIKeysFixture(t *testing.T) *aiKeysFixture {
	t.Helper()

	store := aicredstest.NewStore()
	a := newAPI(t, func(_ *config.Config, _ *auth.Options, deps *httpapi.Deps) {
		key := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0x22}, 32))
		keyring, err := secretbox.NewKeyring(key)
		if err != nil {
			t.Fatalf("secretbox.NewKeyring: %v", err)
		}
		// Il log del servizio è già verificato in internal/aicreds; qui interessa
		// quello del router, che [api] raccoglie per conto suo.
		svc, err := aicreds.NewService(aicreds.Options{
			Store:   store,
			Keyring: keyring,
			Logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
		})
		if err != nil {
			t.Fatalf("aicreds.NewService: %v", err)
		}
		deps.AIKeys = svc
	})

	user, token := a.registerAndLogin()
	return &aiKeysFixture{api: a, store: store, user: user, token: token}
}

// salva registra una chiave AI via API e restituisce la risposta.
func (f *aiKeysFixture) salva(provider, key string) httpapi.AIKeyResponse {
	f.t.Helper()

	rec := f.do(http.MethodPost, "/ai/keys", map[string]any{
		"provider": provider, "key": key, "label": "chiave di prova",
	}, withCookie(f.token))
	if rec.Code != http.StatusCreated {
		f.t.Fatalf("POST /ai/keys = %d, atteso 201: %s", rec.Code, rec.Body.String())
	}

	var body httpapi.AIKeyResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		f.t.Fatalf("decodifica della risposta: %v", err)
	}
	return body
}

// ------------------------------------------------- la chiave non esce mai

// **Il test che R18 chiede.** La chiave entra una volta e non compare in nessuna
// risposta dell'API, nemmeno in quella della scrittura, e nemmeno per un
// frammento.
//
// Il controllo è sul corpo **grezzo** e non sui campi noti: un campo aggiunto
// per sbaglio a una risposta futura deve far fallire questo test, e controllare
// solo i campi che si conoscono non lo farebbe.
func TestAIKeyNeverLeavesTheAPI(t *testing.T) {
	f := newAIKeysFixture(t)

	risposte := map[string]string{}

	rec := f.do(http.MethodPost, "/ai/keys", map[string]any{
		"provider": "anthropic", "key": chiaveAI, "label": "piano team",
	}, withCookie(f.token))
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST /ai/keys = %d: %s", rec.Code, rec.Body.String())
	}
	risposte["POST /ai/keys"] = rec.Body.String()

	var creata httpapi.AIKeyResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &creata); err != nil {
		t.Fatalf("decodifica: %v", err)
	}

	risposte["GET /ai/keys"] = f.do(http.MethodGet, "/ai/keys", nil, withCookie(f.token)).Body.String()
	risposte["GET /ai/keys?include_revoked"] = f.do(http.MethodGet,
		"/ai/keys?include_revoked=true", nil, withCookie(f.token)).Body.String()
	risposte["POST /ai/keys (sostituzione)"] = f.do(http.MethodPost, "/ai/keys",
		map[string]any{"provider": "anthropic", "key": chiaveAI + "-ruotata"},
		withCookie(f.token)).Body.String()
	risposte["DELETE /ai/keys/{id}"] = f.do(http.MethodDelete, "/ai/keys/"+creata.ID,
		nil, withCookie(f.token)).Body.String()
	risposte["GET /ai/keys dopo la revoca"] = f.do(http.MethodGet,
		"/ai/keys?include_revoked=true", nil, withCookie(f.token)).Body.String()

	for rotta, corpo := range risposte {
		if strings.Contains(corpo, chiaveAI) {
			t.Errorf("%s ha restituito la chiave: %s", rotta, corpo)
		}
		// E nemmeno la sua coda: `last_four` non esiste più (migrazione 0016), e
		// questo è ciò che impedisce che torni sotto un altro nome nella risposta.
		if coda := chiaveAI[len(chiaveAI)-4:]; strings.Contains(corpo, coda) {
			t.Errorf("%s ha restituito la coda della chiave (%q): %s", rotta, coda, corpo)
		}
	}

	// E la chiave non è finita nemmeno nei log del router.
	if strings.Contains(f.logs.String(), chiaveAI) {
		t.Errorf("la chiave è finita nei log:\n%s", f.logs)
	}
}

// Non esiste una rotta che restituisca una chiave, e non esiste nemmeno una
// `GET /ai/keys/{id}`: ogni rotta di lettura in più è un altro posto in cui la
// proprietà «la chiave non si rilegge» deve continuare a valere.
func TestNoRouteReadsASingleAIKey(t *testing.T) {
	f := newAIKeysFixture(t)
	creata := f.salva("anthropic", chiaveAI)

	for _, percorso := range []string{
		"/ai/keys/" + creata.ID,
		"/ai/keys/" + creata.ID + "/key",
		"/ai/keys/" + creata.ID + "?reveal=true",
		"/ai/keys/anthropic",
	} {
		rec := f.do(http.MethodGet, percorso, nil, withCookie(f.token))
		if rec.Code != http.StatusNotFound && rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("GET %s = %d, attesi 404 o 405: %s", percorso, rec.Code, rec.Body.String())
		}
		if strings.Contains(rec.Body.String(), chiaveAI) {
			t.Errorf("GET %s ha restituito la chiave", percorso)
		}
	}
}

// [httpapi.AIKeyResponse] non ha un campo che possa contenere la chiave o un suo
// frammento. Il test guarda i campi serializzati invece di un corpo di esempio:
// un corpo si controlla per il caso a cui si è pensato, i campi ci sono tutti.
func TestAIKeyResponseHasNoFieldForTheKey(t *testing.T) {
	encoded, err := json.Marshal(httpapi.AIKeyResponse{ID: "c-1", Provider: "anthropic"})
	if err != nil {
		t.Fatal(err)
	}
	var campi map[string]any
	if err := json.Unmarshal(encoded, &campi); err != nil {
		t.Fatal(err)
	}

	vietati := []string{"key", "value", "secret", "plaintext", "ciphertext",
		"nonce", "last_four", "preview", "hint", "suffix", "prefix"}
	for campo := range campi {
		for _, vietato := range vietati {
			if strings.Contains(campo, vietato) {
				t.Errorf("AIKeyResponse ha il campo %q: la chiave non deve poterci stare", campo)
			}
		}
	}
}

// La risposta alla scrittura non è memorizzabile: la *richiesta* portava la
// chiave, e un intermediario che memorizzasse lo scambio ne conserverebbe una
// copia.
func TestAIKeyResponsesAreNotCacheable(t *testing.T) {
	f := newAIKeysFixture(t)

	rec := f.do(http.MethodPost, "/ai/keys", map[string]any{
		"provider": "anthropic", "key": chiaveAI,
	}, withCookie(f.token))
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q, atteso no-store", got)
	}
}

// Il messaggio di un corpo JSON illeggibile non viene rimandato al client così
// com'è: `json.Decoder` cita il testo che non è riuscito a leggere, e su questa
// rotta quel testo può essere la chiave.
func TestMalformedBodyDoesNotEchoTheKey(t *testing.T) {
	f := newAIKeysFixture(t)

	// Un corpo con un campo sconosciuto che porta la chiave: è il caso in cui il
	// decodificatore la citerebbe nel proprio messaggio.
	rec := f.do(http.MethodPost, "/ai/keys", map[string]any{
		"provider": "anthropic", "key": chiaveAI, "chiave": chiaveAI,
	}, withCookie(f.token))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("POST con un campo sconosciuto = %d, atteso 400: %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), chiaveAI) {
		t.Errorf("la risposta cita la chiave: %s", rec.Body.String())
	}
	if strings.Contains(f.logs.String(), chiaveAI) {
		t.Errorf("i log citano la chiave:\n%s", f.logs)
	}
}

// ------------------------------------------------------------ comportamento

// L'elenco dice quali fornitori sono ammessi: senza, la dashboard ne terrebbe
// una copia, e le due liste divergerebbero il giorno in cui l'enumerato della
// 0001 cambia.
func TestListAnnouncesTheProviders(t *testing.T) {
	f := newAIKeysFixture(t)
	f.salva("anthropic", chiaveAI)

	rec := f.do(http.MethodGet, "/ai/keys", nil, withCookie(f.token))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /ai/keys = %d: %s", rec.Code, rec.Body.String())
	}
	var body httpapi.AIKeyListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decodifica: %v", err)
	}

	if len(body.Keys) != 1 || body.Keys[0].Provider != "anthropic" {
		t.Errorf("chiavi = %+v", body.Keys)
	}
	if !slices.Equal(body.Providers, []string{"anthropic", "openai", "google"}) {
		t.Errorf("providers = %v", body.Providers)
	}
	if body.Keys[0].Label != "chiave di prova" || body.Keys[0].Revoked {
		t.Errorf("la risposta non porta i dati che deve portare: %+v", body.Keys[0])
	}
}

// Incollare di nuovo la chiave di un provider la sostituisce: non è un
// conflitto, è la stessa intenzione della prima volta. Rispondere 409
// costringerebbe a revocare prima di aggiornare.
func TestSecondPostReplacesInsteadOfConflicting(t *testing.T) {
	f := newAIKeysFixture(t)
	prima := f.salva("anthropic", chiaveAI)
	dopo := f.salva("anthropic", chiaveAI+"-ruotata")

	if dopo.ID != prima.ID {
		t.Error("la sostituzione ha creato una seconda chiave viva per lo stesso provider")
	}
	if f.store.Count() != 1 {
		t.Errorf("righe = %d, attesa 1", f.store.Count())
	}
}

// La revoca è un 204, e la chiave revocata resta visibile come traccia solo se
// la si chiede.
func TestRevokeAndTheTrace(t *testing.T) {
	f := newAIKeysFixture(t)
	creata := f.salva("anthropic", chiaveAI)

	rec := f.do(http.MethodDelete, "/ai/keys/"+creata.ID, nil, withCookie(f.token))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("DELETE = %d, atteso 204: %s", rec.Code, rec.Body.String())
	}

	var vive httpapi.AIKeyListResponse
	if err := json.Unmarshal(
		f.do(http.MethodGet, "/ai/keys", nil, withCookie(f.token)).Body.Bytes(), &vive); err != nil {
		t.Fatal(err)
	}
	if len(vive.Keys) != 0 {
		t.Errorf("chiavi vive dopo la revoca = %d, attese 0", len(vive.Keys))
	}

	var tutte httpapi.AIKeyListResponse
	if err := json.Unmarshal(
		f.do(http.MethodGet, "/ai/keys?include_revoked=true", nil,
			withCookie(f.token)).Body.Bytes(), &tutte); err != nil {
		t.Fatal(err)
	}
	if len(tutte.Keys) != 1 || !tutte.Keys[0].Revoked || tutte.Keys[0].RevokedAt == nil {
		t.Errorf("la traccia della revoca non c'è: %+v", tutte.Keys)
	}

	// E revocarla di nuovo è un 404: distinguere «non esiste» da «era già
	// revocata» direbbe a chiunque se un identificativo altrui è vivo.
	if rec := f.do(http.MethodDelete, "/ai/keys/"+creata.ID, nil,
		withCookie(f.token)); rec.Code != http.StatusNotFound {
		t.Errorf("seconda revoca = %d, atteso 404", rec.Code)
	}
}

// L'ambito è sull'utente: la chiave di un altro non si revoca, e la risposta è
// la stessa di una chiave inesistente.
func TestAIKeysAreScopedToTheUser(t *testing.T) {
	f := newAIKeysFixture(t)
	creata := f.salva("anthropic", chiaveAI)

	_, altroToken := f.registerAndLoginAs("bob@example.com")

	var elenco httpapi.AIKeyListResponse
	if err := json.Unmarshal(
		f.do(http.MethodGet, "/ai/keys?include_revoked=true", nil,
			withCookie(altroToken)).Body.Bytes(), &elenco); err != nil {
		t.Fatal(err)
	}
	if len(elenco.Keys) != 0 {
		t.Errorf("un altro utente vede %d chiavi", len(elenco.Keys))
	}

	if rec := f.do(http.MethodDelete, "/ai/keys/"+creata.ID, nil,
		withCookie(altroToken)); rec.Code != http.StatusNotFound {
		t.Errorf("revoca da un altro utente = %d, atteso 404", rec.Code)
	}
	if f.store.Sealed(creata.ID).Revoked() {
		t.Error("la chiave è stata revocata da un altro utente")
	}
}

// **Nemmeno una chiave API di Postqron tocca le chiavi AI.** Le rotte esigono la
// sessione, non una Identity: una chiave API che potesse sostituire la chiave AI
// dell'utente dirotterebbe verso un bersaglio scelto da chi ha la chiave le
// richieste che facciamo per suo conto, e gliele farebbe pagare sul conto del
// fornitore.
func TestAPIKeyCannotTouchAIKeys(t *testing.T) {
	f := newAIKeysFixture(t)

	rec := f.do(http.MethodPost, "/keys", map[string]any{
		"name": "chiave-di-prova", "scopes": []string{"jobs:write", "jobs:read"},
	}, withCookie(f.token))
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST /keys = %d: %s", rec.Code, rec.Body.String())
	}
	var creata httpapi.APIKeyCreatedResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &creata); err != nil {
		t.Fatal(err)
	}

	casi := []struct {
		metodo, percorso string
		corpo            any
	}{
		{http.MethodGet, "/ai/keys", nil},
		{http.MethodPost, "/ai/keys", map[string]any{"provider": "anthropic", "key": chiaveAI}},
		{http.MethodDelete, "/ai/keys/qualsiasi", nil},
	}
	for _, caso := range casi {
		rec := f.do(caso.metodo, caso.percorso, caso.corpo, withKey(creata.Secret))
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s %s con una chiave API = %d, atteso 401: %s",
				caso.metodo, caso.percorso, rec.Code, rec.Body.String())
		}
	}
	if f.store.Count() != 0 {
		t.Error("una chiave API ha scritto una chiave AI")
	}
}

// Senza sessione non si tocca niente.
func TestAIKeysRequireASession(t *testing.T) {
	f := newAIKeysFixture(t)

	for _, caso := range []struct{ metodo, percorso string }{
		{http.MethodGet, "/ai/keys"},
		{http.MethodPost, "/ai/keys"},
		{http.MethodDelete, "/ai/keys/qualsiasi"},
	} {
		rec := f.do(caso.metodo, caso.percorso, nil)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s %s senza sessione = %d, atteso 401", caso.metodo, caso.percorso, rec.Code)
		}
	}
}

// La validazione risponde 400 con i campi ancorati, così che un form possa
// evidenziarli senza interpretare un messaggio.
func TestAIKeyValidationErrors(t *testing.T) {
	f := newAIKeysFixture(t)

	casi := map[string]struct {
		corpo  map[string]any
		campo  string
		codice string
	}{
		"provider sconosciuto": {
			map[string]any{"provider": "claude", "key": chiaveAI}, "provider", "invalid_provider",
		},
		"chiave assente": {
			map[string]any{"provider": "anthropic"}, "key", "required",
		},
		"chiave con a capo": {
			map[string]any{"provider": "anthropic", "key": chiaveAI + "\n"},
			"key", "surrounding_whitespace",
		},
		"chiave troncata": {
			map[string]any{"provider": "anthropic", "key": "sk-ant-1"}, "key", "too_short",
		},
	}

	for nome, caso := range casi {
		t.Run(nome, func(t *testing.T) {
			rec := f.do(http.MethodPost, "/ai/keys", caso.corpo, withCookie(f.token))
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, atteso 400: %s", rec.Code, rec.Body.String())
			}

			var body httpapi.ErrorBody
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("decodifica: %v", err)
			}
			if body.Error.Code != "validation_failed" {
				t.Errorf("code = %q, atteso validation_failed", body.Error.Code)
			}
			trovato := false
			for _, dettaglio := range body.Error.Details {
				if dettaglio.Field == caso.campo && dettaglio.Code == caso.codice {
					trovato = true
				}
			}
			if !trovato {
				t.Errorf("dettagli = %+v, atteso %s/%s", body.Error.Details, caso.campo, caso.codice)
			}
			// E il messaggio di errore non porta con sé la chiave.
			if strings.Contains(rec.Body.String(), chiaveAI) {
				t.Errorf("la risposta di errore contiene la chiave: %s", rec.Body.String())
			}
		})
	}
}

// Senza servizio configurato le rotte non esistono affatto: rispondono 404 e non
// un 500 che dice che manca qualcosa.
func TestAIKeyRoutesAreAbsentWithoutTheService(t *testing.T) {
	a := newAPI(t)
	_, token := a.registerAndLogin()

	rec := a.do(http.MethodGet, "/ai/keys", nil, withCookie(token))
	if rec.Code != http.StatusNotFound {
		t.Errorf("GET /ai/keys senza servizio = %d, atteso 404: %s", rec.Code, rec.Body.String())
	}
}
