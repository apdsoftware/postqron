package httpapi_test

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"github.com/apdsoftware/postqron/services/api/internal/auth"
	"github.com/apdsoftware/postqron/services/api/internal/config"
	"github.com/apdsoftware/postqron/services/api/internal/httpapi"
	"github.com/apdsoftware/postqron/services/api/internal/secretbox"
	"github.com/apdsoftware/postqron/services/api/internal/secrets"
	"github.com/apdsoftware/postqron/services/api/internal/secretstest"
)

// segretissimo è il valore che nessuna risposta e nessun log devono contenere.
// È volutamente lungo e riconoscibile: se sfugge, si vede.
const segretissimo = "finto-valore-che-non-deve-uscire"

// ---------------------------------------------------------------- impalcatura

type secretsFixture struct {
	*api
	store *secretstest.Store
	user  auth.User
	token string
}

func newSecretsFixture(t *testing.T) *secretsFixture {
	t.Helper()

	store := secretstest.NewStore()
	a := newAPI(t, func(_ *config.Config, _ *auth.Options, deps *httpapi.Deps) {
		key := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0x11}, 32))
		keyring, err := secretbox.NewKeyring(key)
		if err != nil {
			t.Fatalf("secretbox.NewKeyring: %v", err)
		}
		// Il log del servizio è già verificato in internal/secrets; qui interessa
		// quello del router, che [api] raccoglie per conto suo.
		svc, err := secrets.NewService(secrets.Options{
			Store:   store,
			Keyring: keyring,
			Logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
		})
		if err != nil {
			t.Fatalf("secrets.NewService: %v", err)
		}
		deps.Secrets = svc
	})

	user, token := a.registerAndLogin()
	return &secretsFixture{api: a, store: store, user: user, token: token}
}

// creaChiave emette una chiave API di scrittura piena. Serve a provare che
// nemmeno con quella si tocchino i segreti.
func (f *secretsFixture) creaChiave() string {
	f.t.Helper()

	rec := f.do(http.MethodPost, "/keys", map[string]any{
		"name":   "chiave-di-prova",
		"scopes": []string{"jobs:write", "jobs:read"},
	}, withCookie(f.token))
	if rec.Code != http.StatusCreated {
		f.t.Fatalf("POST /keys = %d: %s", rec.Code, rec.Body.String())
	}
	var body httpapi.APIKeyCreatedResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		f.t.Fatalf("decodifica: %v", err)
	}
	return body.Secret
}

// crea registra un segreto via API e restituisce la risposta.
func (f *secretsFixture) crea(name, value string) httpapi.SecretResponse {
	f.t.Helper()

	rec := f.do(http.MethodPost, "/secrets", map[string]any{
		"name": name, "value": value, "description": "nota di prova",
	}, withCookie(f.token))
	if rec.Code != http.StatusCreated {
		f.t.Fatalf("POST /secrets = %d, atteso 201: %s", rec.Code, rec.Body.String())
	}

	var body httpapi.SecretResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		f.t.Fatalf("decodifica della risposta: %v", err)
	}
	return body
}

// ------------------------------------------------- il valore non esce mai

// **Il test che la issue chiede.** Il valore entra una volta e non compare in
// nessuna risposta dell'API, nemmeno in quella della creazione.
//
// Il controllo è sul corpo **grezzo** e non sui campi noti: un campo aggiunto
// per sbaglio a una risposta futura deve far fallire questo test, e controllare
// solo i campi che si conoscono non lo farebbe.
func TestSecretValueNeverLeavesTheAPI(t *testing.T) {
	f := newSecretsFixture(t)

	risposte := map[string]string{}

	rec := f.do(http.MethodPost, "/secrets", map[string]any{
		"name": "DIGEST_TOKEN", "value": segretissimo, "description": "token del digest",
	}, withCookie(f.token))
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST /secrets = %d: %s", rec.Code, rec.Body.String())
	}
	risposte["POST /secrets"] = rec.Body.String()

	var creato httpapi.SecretResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &creato); err != nil {
		t.Fatalf("decodifica: %v", err)
	}

	risposte["GET /secrets"] = f.do(http.MethodGet, "/secrets", nil, withCookie(f.token)).Body.String()
	risposte["GET /secrets?include_revoked"] = f.do(http.MethodGet,
		"/secrets?include_revoked=true", nil, withCookie(f.token)).Body.String()
	risposte["PATCH /secrets/{id}"] = f.do(http.MethodPatch, "/secrets/"+creato.ID,
		map[string]any{"value": segretissimo + "-aggiornato"}, withCookie(f.token)).Body.String()
	risposte["DELETE /secrets/{id}"] = f.do(http.MethodDelete, "/secrets/"+creato.ID,
		nil, withCookie(f.token)).Body.String()
	risposte["GET /secrets dopo la revoca"] = f.do(http.MethodGet,
		"/secrets?include_revoked=true", nil, withCookie(f.token)).Body.String()

	for rotta, corpo := range risposte {
		if strings.Contains(corpo, segretissimo) {
			t.Errorf("%s ha restituito il valore del segreto: %s", rotta, corpo)
		}
	}

	// E il valore non è finito nemmeno nei log del router.
	if strings.Contains(f.logs.String(), segretissimo) {
		t.Errorf("il valore è finito nei log:\n%s", f.logs)
	}
}

// Non esiste una rotta che restituisca un valore, e non esiste nemmeno una
// `GET /secrets/{id}`: ogni rotta di lettura in più è un altro posto in cui la
// proprietà «il valore non si rilegge» deve continuare a valere.
func TestNoRouteReadsASingleSecret(t *testing.T) {
	f := newSecretsFixture(t)
	creato := f.crea("DIGEST_TOKEN", segretissimo)

	for _, percorso := range []string{
		"/secrets/" + creato.ID,
		"/secrets/" + creato.ID + "/value",
		"/secrets/" + creato.ID + "?reveal=true",
	} {
		rec := f.do(http.MethodGet, percorso, nil, withCookie(f.token))
		if rec.Code != http.StatusNotFound && rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("GET %s = %d, attesi 404 o 405: %s", percorso, rec.Code, rec.Body.String())
		}
		if strings.Contains(rec.Body.String(), segretissimo) {
			t.Errorf("GET %s ha restituito il valore", percorso)
		}
	}
}

// La risposta che porta il nome del segreto porta anche il testo da incollare
// nel `cron.yaml`: senza, l'utente deve sapere a memoria che la sintassi è
// `${...}`.
func TestCreateReturnsTheReference(t *testing.T) {
	f := newSecretsFixture(t)
	creato := f.crea("DIGEST_TOKEN", segretissimo)

	if creato.Reference != "${DIGEST_TOKEN}" {
		t.Errorf("Reference = %q", creato.Reference)
	}
	if creato.Revoked || creato.RevokedAt != nil {
		t.Error("un segreto appena creato non è revocato")
	}
	if creato.Description != "nota di prova" {
		t.Errorf("Description = %q", creato.Description)
	}
}

// La risposta alla creazione non è memorizzabile: la *richiesta* portava un
// segreto, e un intermediario che memorizzasse lo scambio ne conserverebbe una
// copia.
func TestSecretResponsesAreNotCacheable(t *testing.T) {
	f := newSecretsFixture(t)

	rec := f.do(http.MethodPost, "/secrets", map[string]any{
		"name": "DIGEST_TOKEN", "value": segretissimo,
	}, withCookie(f.token))
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q, atteso no-store", got)
	}
}

// Il messaggio di un corpo JSON illeggibile non viene rimandato al client così
// com'è: `json.Decoder` cita il testo che non è riuscito a leggere, e su queste
// rotte quel testo può essere il segreto.
func TestMalformedBodyDoesNotEchoTheSecret(t *testing.T) {
	f := newSecretsFixture(t)

	// Un campo sconosciuto: il decodificatore lo nomina, e se il corpo fosse
	// rimandato indietro nominerebbe anche ciò che gli sta accanto.
	rec := f.do(http.MethodPost, "/secrets", map[string]any{
		"name": "DIGEST_TOKEN", "value": segretissimo, "sconosciuto": segretissimo,
	}, withCookie(f.token))

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, atteso 400: %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), segretissimo) {
		t.Errorf("la risposta d'errore contiene il segreto: %s", rec.Body.String())
	}
}

// ------------------------------------------------------------ autenticazione

// Le rotte esigono una sessione. Una chiave API non tocca i segreti: potrebbe
// *sostituirne* il valore, dirottando le credenziali dei job dell'utente verso
// un bersaglio scelto da chi ha la chiave, senza che nessun log mostri un valore
// cambiato.
func TestSecretRoutesRequireASession(t *testing.T) {
	f := newSecretsFixture(t)
	creato := f.crea("DIGEST_TOKEN", segretissimo)

	chiave := f.creaChiave()

	richieste := []struct {
		method, path string
		body         any
	}{
		{http.MethodGet, "/secrets", nil},
		{http.MethodPost, "/secrets", map[string]any{"name": "ALTRO", "value": segretissimo}},
		{http.MethodPatch, "/secrets/" + creato.ID, map[string]any{"value": segretissimo}},
		{http.MethodDelete, "/secrets/" + creato.ID, nil},
	}

	for _, r := range richieste {
		// Senza credenziali.
		if rec := f.do(r.method, r.path, r.body); rec.Code != http.StatusUnauthorized {
			t.Errorf("%s %s senza sessione = %d, atteso 401", r.method, r.path, rec.Code)
		}
		// Con una chiave API di scrittura piena.
		rec := f.do(r.method, r.path, r.body, withKey(chiave))
		if rec.Code == http.StatusOK || rec.Code == http.StatusCreated || rec.Code == http.StatusNoContent {
			t.Errorf("%s %s con una chiave API = %d: le chiavi non toccano i segreti",
				r.method, r.path, rec.Code)
		}
		if strings.Contains(rec.Body.String(), segretissimo) {
			t.Errorf("%s %s con una chiave API ha restituito il valore", r.method, r.path)
		}
	}
}

// Il segreto di un altro workspace non si legge, non si aggiorna e non si
// revoca — e i tre casi non si distinguono da «non esiste».
func TestSecretsAreScopedToTheWorkspace(t *testing.T) {
	f := newSecretsFixture(t)
	creato := f.crea("DIGEST_TOKEN", segretissimo)

	_, altroToken := f.registerAndLoginAs("altro-workspace@example.com")

	if rec := f.do(http.MethodGet, "/secrets", nil, withCookie(altroToken)); rec.Code == http.StatusOK {
		var body httpapi.SecretListResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("decodifica: %v", err)
		}
		if len(body.Secrets) != 0 {
			t.Errorf("un altro workspace vede %d segreti", len(body.Secrets))
		}
	}

	for _, r := range []struct {
		method string
		body   any
	}{
		{http.MethodPatch, map[string]any{"value": "valore-di-un-altro"}},
		{http.MethodDelete, nil},
	} {
		rec := f.do(r.method, "/secrets/"+creato.ID, r.body, withCookie(altroToken))
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s da un altro workspace = %d, atteso 404: %s", r.method, rec.Code, rec.Body.String())
		}
	}
}

// ------------------------------------------------------------------- errori

func TestSecretErrorCodes(t *testing.T) {
	f := newSecretsFixture(t)
	f.crea("DIGEST_TOKEN", segretissimo)

	tests := []struct {
		name         string
		method, path string
		body         any
		status       int
		code         string
	}{
		{
			"nome già in uso", http.MethodPost, "/secrets",
			map[string]any{"name": "DIGEST_TOKEN", "value": segretissimo},
			http.StatusConflict, "secret_name_taken",
		},
		{
			"nome non valido", http.MethodPost, "/secrets",
			map[string]any{"name": "digest-token", "value": segretissimo},
			http.StatusBadRequest, "validation_failed",
		},
		{
			"valore troppo corto", http.MethodPost, "/secrets",
			map[string]any{"name": "CORTO", "value": "abc"},
			http.StatusBadRequest, "validation_failed",
		},
		{
			"segreto inesistente", http.MethodPatch, "/secrets/00000000-0000-0000-0000-000000000000",
			map[string]any{"value": segretissimo},
			http.StatusNotFound, "secret_not_found",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			rec := f.do(test.method, test.path, test.body, withCookie(f.token))
			if rec.Code != test.status {
				t.Fatalf("status = %d, atteso %d: %s", rec.Code, test.status, rec.Body.String())
			}
			var body httpapi.ErrorBody
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("decodifica: %v", err)
			}
			if body.Error.Code != test.code {
				t.Errorf("code = %q, atteso %q", body.Error.Code, test.code)
			}
			if strings.Contains(rec.Body.String(), segretissimo) {
				t.Errorf("la risposta d'errore contiene il valore: %s", rec.Body.String())
			}
		})
	}

	// La validazione ancora i motivi ai campi, così che un form li evidenzi senza
	// interpretare un messaggio.
	rec := f.do(http.MethodPost, "/secrets",
		map[string]any{"name": "digest-token", "value": "abc"}, withCookie(f.token))
	var body httpapi.ErrorBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decodifica: %v", err)
	}
	campi := map[string]bool{}
	for _, dettaglio := range body.Error.Details {
		campi[dettaglio.Field] = true
	}
	if !campi["name"] || !campi["value"] {
		t.Errorf("details = %+v: attesi entrambi i campi", body.Error.Details)
	}

}

// Senza servizio dei segreti le rotte non esistono, e la mancanza si nota nel
// log all'avvio invece di diventare un 500 alla prima richiesta.
func TestRouterWithoutSecretsService(t *testing.T) {
	a := newAPI(t)
	_, token := a.registerAndLogin()

	if rec := a.do(http.MethodGet, "/secrets", nil, withCookie(token)); rec.Code != http.StatusNotFound {
		t.Errorf("GET /secrets = %d, atteso 404", rec.Code)
	}
	if !strings.Contains(a.logs.String(), "segreti del workspace non registrate") {
		t.Errorf("l'avvio non ha segnalato la mancanza:\n%s", a.logs)
	}
}
