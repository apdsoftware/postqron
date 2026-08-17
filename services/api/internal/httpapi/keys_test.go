package httpapi_test

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/apdsoftware/postqron/services/api/internal/apikeys"
	"github.com/apdsoftware/postqron/services/api/internal/auth"
	"github.com/apdsoftware/postqron/services/api/internal/config"
	"github.com/apdsoftware/postqron/services/api/internal/httpapi"
	"github.com/apdsoftware/postqron/services/api/internal/jobs"
	"github.com/apdsoftware/postqron/services/api/internal/jobstest"
)

// ---------------------------------------------------------------- impalcatura

type keysFixture struct {
	*api
	user  auth.User
	token string
}

// newKeysFixture costruisce il router con autenticazione, job e chiavi API, e ci
// apre già una sessione.
//
// I job ci sono perché è su quelle rotte che gli scope si applicano: una suite
// delle chiavi senza rotte da proteggere verificherebbe solo la contabilità.
func newKeysFixture(t *testing.T) *keysFixture {
	t.Helper()

	a := newAPI(t, func(_ *config.Config, _ *auth.Options, deps *httpapi.Deps) {
		svc, err := jobs.NewService(jobs.Options{
			Store:  jobstest.NewStore(),
			Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		})
		if err != nil {
			t.Fatalf("jobs.NewService: %v", err)
		}
		deps.Jobs = svc
	})

	user, token := a.registerAndLogin()
	return &keysFixture{api: a, user: user, token: token}
}

// registerAndLoginAs apre una sessione su un secondo account. Serve ai test
// sull'ambito: «la chiave di un altro utente» ha bisogno di un altro utente.
func (a *api) registerAndLoginAs(email string) (auth.User, string) {
	a.t.Helper()

	if rec := a.do(http.MethodPost, "/auth/register", map[string]string{
		"email": email, "password": testPassword, "full_name": "Altro Utente",
	}); rec.Code != http.StatusAccepted {
		a.t.Fatalf("registrazione di %s: status = %d, corpo = %s", email, rec.Code, rec.Body)
	}
	a.svc.Wait()

	rec := a.do(http.MethodPost, "/auth/login", map[string]string{
		"email": email, "password": testPassword,
	})
	if rec.Code != http.StatusOK {
		a.t.Fatalf("login di %s: status = %d, corpo = %s", email, rec.Code, rec.Body)
	}
	cookie := sessionCookie(a.t, rec)
	if cookie == nil {
		a.t.Fatal("il login non ha impostato il cookie di sessione")
	}
	user, err := a.store.UserByEmail(a.t.Context(), email)
	if err != nil {
		a.t.Fatalf("l'account non esiste: %v", err)
	}
	return user, cookie.Value
}

// creaChiave crea una chiave con gli scope indicati e ne restituisce il valore in
// chiaro insieme all'identificativo.
func (f *keysFixture) creaChiave(nome string, scopes ...string) (secret, id string) {
	f.t.Helper()

	rec := f.do(http.MethodPost, "/keys", map[string]any{
		"name":   nome,
		"scopes": scopes,
	}, withCookie(f.token))
	if rec.Code != http.StatusCreated {
		f.t.Fatalf("POST /keys = %d, atteso 201: %s", rec.Code, rec.Body.String())
	}

	var body httpapi.APIKeyCreatedResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		f.t.Fatalf("decodifica della risposta: %v", err)
	}
	return body.Secret, body.Key.ID
}

// withKey aggiunge la chiave API alla testata Authorization.
func withKey(secret string) func(*http.Request) {
	return func(r *http.Request) {
		r.Header.Set("Authorization", "Bearer "+secret)
	}
}

// jobPayload è [corpoValido] con un nome scelto: i test degli scope creano più
// job nello stesso archivio, e il nome è l'identità del job.
func jobPayload(name string) map[string]any {
	body := corpoValido()
	body["name"] = name
	return body
}

// ------------------------------------------------------- la chiave una volta

// La chiave si vede una volta sola: la creazione la mostra, e nessuna lettura
// successiva la contiene. Il test controlla il corpo grezzo e non i soli campi
// noti, così che un campo aggiunto per sbaglio a una risposta futura lo faccia
// fallire.
func TestLaChiaveSiVedeUnaVoltaSola(t *testing.T) {
	f := newKeysFixture(t)

	rec := f.do(http.MethodPost, "/keys", map[string]any{
		"name":   "produzione",
		"scopes": []string{"jobs:read"},
	}, withCookie(f.token))
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST /keys = %d, atteso 201: %s", rec.Code, rec.Body.String())
	}

	var created httpapi.APIKeyCreatedResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decodifica: %v", err)
	}
	if created.Secret == "" {
		t.Fatal("la creazione non ha mostrato la chiave")
	}
	if !strings.HasPrefix(created.Secret, apikeys.TokenPrefix) {
		t.Errorf("la chiave non ha il prefisso atteso: %q", created.Key.Prefix)
	}
	if created.Warning == "" {
		t.Error("la risposta non avverte che la chiave non sarà più leggibile")
	}
	// La sola risposta che porta un segreto non deve finire in nessuna cache.
	if store := rec.Header().Get("Cache-Control"); !strings.Contains(store, "no-store") {
		t.Errorf("Cache-Control = %q, atteso no-store", store)
	}

	// Da qui in poi: nessuna lettura la contiene.
	for _, path := range []string{"/keys", "/keys?include_revoked=true"} {
		lettura := f.do(http.MethodGet, path, nil, withCookie(f.token))
		if lettura.Code != http.StatusOK {
			t.Fatalf("GET %s = %d, atteso 200: %s", path, lettura.Code, lettura.Body.String())
		}
		corpo := lettura.Body.String()
		if strings.Contains(corpo, created.Secret) {
			t.Errorf("GET %s contiene la chiave in chiaro", path)
		}
		if coda := strings.TrimPrefix(created.Secret, created.Key.Prefix); strings.Contains(corpo, coda) {
			t.Errorf("GET %s contiene la coda della chiave", path)
		}
		// Nemmeno l'impronta: non è un segreto invertibile, ma non serve a nessun
		// client e non ha ragione di uscire.
		if strings.Contains(corpo, f.keyring.APIKeyHash(created.Secret)) {
			t.Errorf("GET %s contiene l'impronta della chiave", path)
		}
		// Il prefisso invece sì: è il modo di riconoscere quale chiave revocare.
		if !strings.Contains(corpo, created.Key.Prefix) {
			t.Errorf("GET %s non contiene il prefisso della chiave", path)
		}
	}
}

// Non esiste nessuna rotta che restituisca la chiave dopo la creazione, nemmeno
// tentando le forme che un'API del genere avrebbe di solito.
func TestNessunaRottaRestituisceLaChiaveDopoLaCreazione(t *testing.T) {
	f := newKeysFixture(t)
	secret, id := f.creaChiave("produzione", "jobs:read")

	for _, path := range []string{
		"/keys/" + id,
		"/keys/" + id + "/secret",
		"/keys/" + id + "/reveal",
	} {
		rec := f.do(http.MethodGet, path, nil, withCookie(f.token))
		if rec.Code == http.StatusOK {
			t.Errorf("GET %s risponde 200: esiste un percorso di rilettura", path)
		}
		if strings.Contains(rec.Body.String(), secret) {
			t.Errorf("GET %s contiene la chiave in chiaro", path)
		}
	}
}

// Nessuna chiave nei log delle rotte: né in creazione, né in uso, né nei rifiuti
// (SPEC §5).
func TestNessunaChiaveNeiLogDelleRotte(t *testing.T) {
	f := newKeysFixture(t)
	secret, id := f.creaChiave("produzione", "jobs:read")

	// Uso riuscito, uso vietato, revoca, uso dopo la revoca, chiave inventata:
	// tutti i percorsi che scrivono un log.
	f.do(http.MethodGet, "/jobs", nil, withKey(secret))
	f.do(http.MethodPost, "/jobs", jobPayload("vietato"), withKey(secret))
	f.do(http.MethodDelete, "/keys/"+id, nil, withCookie(f.token))
	f.do(http.MethodGet, "/jobs", nil, withKey(secret))
	f.do(http.MethodGet, "/jobs", nil, withKey(apikeys.TokenPrefix+"chiave-inventata"))

	logs := f.logs.String()
	if logs == "" {
		t.Fatal("nessun log prodotto: il test non sta verificando niente")
	}
	if strings.Contains(logs, secret) {
		t.Error("i log contengono la chiave in chiaro")
	}
	if coda := strings.TrimPrefix(secret, secret[:len(apikeys.TokenPrefix)+4]); strings.Contains(logs, coda) {
		t.Error("i log contengono la coda della chiave")
	}
	if strings.Contains(logs, f.keyring.APIKeyHash(secret)) {
		t.Error("i log contengono l'impronta della chiave")
	}
}

// ----------------------------------------------------- autenticazione e scope

// Una chiave con lo scope giusto autentica le rotte che quello scope copre.
func TestUnaChiaveAutenticaLeRotteDelSuoScope(t *testing.T) {
	f := newKeysFixture(t)
	secret, _ := f.creaChiave("lettura", "jobs:read")

	rec := f.do(http.MethodGet, "/jobs", nil, withKey(secret))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /jobs con la chiave = %d, atteso 200: %s", rec.Code, rec.Body.String())
	}
}

// **Il test dell'operazione vietata.** Una chiave di sola lettura non crea job,
// non li modifica, non li elimina e non li fa partire. Se ci riuscisse, lo scope
// sarebbe decorazione.
func TestUnaChiaveDiSolaLetturaNonScrive(t *testing.T) {
	f := newKeysFixture(t)
	secret, _ := f.creaChiave("sola lettura", "jobs:read")

	// Un job creato con la sessione, su cui provare le operazioni vietate.
	creazione := f.do(http.MethodPost, "/jobs", jobPayload("esistente"), withCookie(f.token))
	if creazione.Code != http.StatusCreated {
		t.Fatalf("preparazione del job = %d: %s", creazione.Code, creazione.Body.String())
	}
	var job struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(creazione.Body.Bytes(), &job); err != nil {
		t.Fatalf("decodifica del job: %v", err)
	}

	vietate := []struct {
		nome, method, path, scope string
		body                      any
	}{
		{"creazione", http.MethodPost, "/jobs", "jobs:write", jobPayload("nuovo")},
		{"modifica", http.MethodPatch, "/jobs/" + job.ID, "jobs:write", map[string]any{"enabled": false}},
		{"eliminazione", http.MethodDelete, "/jobs/" + job.ID, "jobs:write", nil},
		{"registro", http.MethodGet, "/jobs/" + job.ID + "/executions", "executions:read", nil},
		{"trigger", http.MethodPost, "/jobs/" + job.ID + "/executions", "executions:trigger", nil},
	}
	for _, tc := range vietate {
		t.Run(tc.nome, func(t *testing.T) {
			rec := f.do(tc.method, tc.path, tc.body, withKey(secret))
			if rec.Code != http.StatusForbidden {
				t.Fatalf("%s %s = %d, atteso 403: %s", tc.method, tc.path, rec.Code, rec.Body.String())
			}

			detail := errorDetail(t, rec)
			if detail.Code != "insufficient_scope" {
				t.Errorf("code = %q, atteso insufficient_scope", detail.Code)
			}
			// Il client deve poter sapere *quale* permesso gli serviva, senza
			// interpretare un messaggio in italiano.
			if detail.Scope != tc.scope {
				t.Errorf("scope = %q, atteso %q", detail.Scope, tc.scope)
			}
			if header := rec.Header().Get("WWW-Authenticate"); !strings.Contains(header, "insufficient_scope") {
				t.Errorf("WWW-Authenticate = %q, atteso insufficient_scope (RFC 6750)", header)
			}
		})
	}

	// Controprova: il job non è stato toccato da nessuno dei tentativi.
	lettura := f.do(http.MethodGet, "/jobs/"+job.ID, nil, withCookie(f.token))
	if lettura.Code != http.StatusOK {
		t.Errorf("il job è stato modificato o eliminato da una chiave di sola lettura: %d", lettura.Code)
	}
}

// Ogni scope apre esattamente le rotte che gli corrispondono, e non le altre: la
// tabella è la mappa fra permessi e rotte, e un `scoped` sbagliato in
// jobsAPI.routes la fa fallire.
func TestOgniScopeApreSoloLeSueRotte(t *testing.T) {
	f := newKeysFixture(t)

	creazione := f.do(http.MethodPost, "/jobs", jobPayload("bersaglio"), withCookie(f.token))
	if creazione.Code != http.StatusCreated {
		t.Fatalf("preparazione del job = %d: %s", creazione.Code, creazione.Body.String())
	}
	var job struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(creazione.Body.Bytes(), &job); err != nil {
		t.Fatalf("decodifica del job: %v", err)
	}

	tests := []struct {
		scope   string
		method  string
		path    string
		ammessa bool
	}{
		{"jobs:read", http.MethodGet, "/jobs", true},
		{"jobs:read", http.MethodGet, "/jobs/" + job.ID, true},
		{"jobs:read", http.MethodGet, "/jobs/" + job.ID + "/executions", false},
		{"executions:read", http.MethodGet, "/jobs/" + job.ID + "/executions", true},
		{"executions:read", http.MethodGet, "/jobs", false},
		{"executions:trigger", http.MethodPost, "/jobs/" + job.ID + "/executions", true},
		{"executions:trigger", http.MethodPost, "/jobs", false},
	}
	for _, tc := range tests {
		t.Run(tc.scope+" "+tc.method+" "+tc.path, func(t *testing.T) {
			secret, _ := f.creaChiave("chiave "+tc.scope, tc.scope)
			rec := f.do(tc.method, tc.path, nil, withKey(secret))

			vietata := rec.Code == http.StatusForbidden &&
				errorDetail(t, rec).Code == "insufficient_scope"
			if tc.ammessa && vietata {
				t.Errorf("%s %s con %s è stata rifiutata per scope: %s",
					tc.method, tc.path, tc.scope, rec.Body.String())
			}
			if !tc.ammessa && !vietata {
				t.Errorf("%s %s con %s = %d, atteso 403 insufficient_scope: %s",
					tc.method, tc.path, tc.scope, rec.Code, rec.Body.String())
			}
		})
	}
}

// La sessione non è soggetta agli scope: quelli limitano le deleghe, non il
// titolare. Se una sessione incontrasse un `insufficient_scope`, la dashboard
// smetterebbe di funzionare.
func TestLaSessioneNonESoggettaAgliScope(t *testing.T) {
	f := newKeysFixture(t)

	for _, path := range []string{"/jobs"} {
		rec := f.do(http.MethodGet, path, nil, withCookie(f.token))
		if rec.Code != http.StatusOK {
			t.Errorf("GET %s con la sessione = %d, atteso 200: %s", path, rec.Code, rec.Body.String())
		}
	}
	rec := f.do(http.MethodPost, "/jobs", jobPayload("dalla-sessione"), withCookie(f.token))
	if rec.Code != http.StatusCreated {
		t.Errorf("POST /jobs con la sessione = %d, atteso 201: %s", rec.Code, rec.Body.String())
	}
}

// --------------------------------------------------------------- la revoca

// **La revoca è immediata.** La stessa chiave che ha appena risposto 200 riceve
// 401 alla richiesta successiva, senza attese e senza scadenze di cache.
func TestLaRevocaHaEffettoAllaRichiestaSuccessiva(t *testing.T) {
	f := newKeysFixture(t)
	secret, id := f.creaChiave("da revocare", "jobs:read")

	if rec := f.do(http.MethodGet, "/jobs", nil, withKey(secret)); rec.Code != http.StatusOK {
		t.Fatalf("prima della revoca = %d, atteso 200: %s", rec.Code, rec.Body.String())
	}

	revoca := f.do(http.MethodDelete, "/keys/"+id, nil, withCookie(f.token))
	if revoca.Code != http.StatusNoContent {
		t.Fatalf("DELETE /keys/%s = %d, atteso 204: %s", id, revoca.Code, revoca.Body.String())
	}

	rec := f.do(http.MethodGet, "/jobs", nil, withKey(secret))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("dopo la revoca = %d, atteso 401: %s", rec.Code, rec.Body.String())
	}
	if detail := errorDetail(t, rec); detail.Code != "invalid_api_key" {
		t.Errorf("code = %q, atteso invalid_api_key", detail.Code)
	}
}

// La chiave revocata sparisce dall'elenco vivo ma resta consultabile come traccia
// storica: chi indaga su un incidente vuole vedere che c'era e quando è stata
// spenta.
func TestUnaChiaveRevocataRestaComeTraccia(t *testing.T) {
	f := newKeysFixture(t)
	_, id := f.creaChiave("da revocare", "jobs:read")

	if rec := f.do(http.MethodDelete, "/keys/"+id, nil, withCookie(f.token)); rec.Code != http.StatusNoContent {
		t.Fatalf("DELETE /keys = %d: %s", rec.Code, rec.Body.String())
	}

	vive := chiavi(t, f.do(http.MethodGet, "/keys", nil, withCookie(f.token)))
	if len(vive) != 0 {
		t.Errorf("chiavi vive = %d, attesa 0", len(vive))
	}

	tutte := chiavi(t, f.do(http.MethodGet, "/keys?include_revoked=true", nil, withCookie(f.token)))
	if len(tutte) != 1 {
		t.Fatalf("chiavi con le revocate = %d, attesa 1", len(tutte))
	}
	if !tutte[0].Revoked || tutte[0].RevokedAt == nil {
		t.Error("la chiave revocata non è segnata come tale")
	}
}

// Una chiave altrui non si revoca, e non si distingue da una inesistente.
func TestNonSiRevocaLaChiaveDiUnAltroUtente(t *testing.T) {
	f := newKeysFixture(t)
	secret, id := f.creaChiave("della vittima", "jobs:read")

	_, altroToken := f.registerAndLoginAs("altro@example.com")
	rec := f.do(http.MethodDelete, "/keys/"+id, nil, withCookie(altroToken))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("DELETE di una chiave altrui = %d, atteso 404: %s", rec.Code, rec.Body.String())
	}

	// E la chiave deve funzionare ancora.
	if uso := f.do(http.MethodGet, "/jobs", nil, withKey(secret)); uso.Code != http.StatusOK {
		t.Errorf("la chiave è stata revocata da un altro utente: %d", uso.Code)
	}
}

// -------------------------------------------------- chi può gestire le chiavi

// **Una chiave API non gestisce chiavi API.** Se potesse, una chiave di sola
// lettura sarebbe a una richiesta di distanza dall'emetterne una di scrittura, e
// gli scope sarebbero una formalità aggirabile.
func TestUnaChiaveNonPuoGestireLeChiavi(t *testing.T) {
	f := newKeysFixture(t)
	// Deliberatamente una chiave con *tutti* gli scope: la negazione non dipende
	// da quali permessi ha, ma dal fatto che è una chiave.
	secret, id := f.creaChiave("onnipotente",
		"jobs:read", "jobs:write", "executions:read", "executions:trigger")

	tests := []struct{ nome, method, path string }{
		{"elenco", http.MethodGet, "/keys"},
		{"creazione", http.MethodPost, "/keys"},
		{"revoca", http.MethodDelete, "/keys/" + id},
		{"scope", http.MethodGet, "/keys/scopes"},
	}
	for _, tc := range tests {
		t.Run(tc.nome, func(t *testing.T) {
			var body any
			if tc.method == http.MethodPost {
				body = map[string]any{"name": "figlia", "scopes": []string{"jobs:write"}}
			}
			rec := f.do(tc.method, tc.path, body, withKey(secret))
			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("%s %s con una chiave = %d, atteso 401: %s",
					tc.method, tc.path, rec.Code, rec.Body.String())
			}
		})
	}

	// Controprova: nessuna chiave nuova è stata creata, e quella esistente c'è
	// ancora.
	elenco := chiavi(t, f.do(http.MethodGet, "/keys", nil, withCookie(f.token)))
	if len(elenco) != 1 {
		t.Errorf("chiavi = %d, attesa 1: la chiave ha modificato il proprio insieme", len(elenco))
	}
}

// La chiave nella testata `Authorization` ha la precedenza sul cookie: dare la
// precedenza al cookie eseguirebbe l'operazione con i poteri pieni dell'utente,
// ignorando in silenzio i limiti della chiave che il chiamante ha dichiarato di
// voler usare.
func TestLaChiaveHaLaPrecedenzaSulCookie(t *testing.T) {
	f := newKeysFixture(t)
	secret, _ := f.creaChiave("sola lettura", "jobs:read")

	rec := f.do(http.MethodPost, "/jobs", jobPayload("con-entrambi"),
		withCookie(f.token), withKey(secret))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("con cookie e chiave insieme = %d, atteso 403: %s", rec.Code, rec.Body.String())
	}
	if detail := errorDetail(t, rec); detail.Code != "insufficient_scope" {
		t.Errorf("code = %q, atteso insufficient_scope: il cookie ha prevalso sulla chiave", detail.Code)
	}
}

// Un token di sessione mandato come Bearer continua a funzionare: non ha il
// prefisso delle chiavi, quindi il guard lo riconosce per quello che è.
func TestIlTokenDiSessioneComeBearerFunzionaAncora(t *testing.T) {
	f := newKeysFixture(t)

	rec := f.do(http.MethodGet, "/jobs", nil, func(r *http.Request) {
		r.Header.Set("Authorization", "Bearer "+f.token)
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /jobs con il token di sessione = %d, atteso 200: %s", rec.Code, rec.Body.String())
	}
}

// Una chiave malformata o inesistente non autentica, e la risposta non distingue
// i casi: la differenza fra «non esiste», «revocata» e «scaduta» è ciò che
// trasformerebbe l'API in un oracolo su una chiave trovata in giro.
func TestUnaChiaveNonValidaNonDistingueILeCause(t *testing.T) {
	f := newKeysFixture(t)

	scaduta := time.Now().Add(-time.Hour)
	f.keysStore.Seed(apikeys.Key{
		UserID:    f.user.ID,
		Name:      "scaduta",
		Prefix:    apikeys.TokenPrefix + "scad",
		Hash:      f.keyring.APIKeyHash(apikeys.TokenPrefix + "chiave-scaduta"),
		Scopes:    []apikeys.Scope{apikeys.ScopeJobsRead},
		ExpiresAt: &scaduta,
		CreatedAt: time.Now().Add(-2 * time.Hour),
	})
	revocata := time.Now().Add(-time.Minute)
	f.keysStore.Seed(apikeys.Key{
		UserID:    f.user.ID,
		Name:      "revocata",
		Prefix:    apikeys.TokenPrefix + "revo",
		Hash:      f.keyring.APIKeyHash(apikeys.TokenPrefix + "chiave-revocata"),
		Scopes:    []apikeys.Scope{apikeys.ScopeJobsRead},
		RevokedAt: &revocata,
		CreatedAt: time.Now().Add(-2 * time.Hour),
	})

	corpi := map[string]string{}
	for nome, token := range map[string]string{
		"inesistente": apikeys.TokenPrefix + "chiave-mai-emessa",
		"scaduta":     apikeys.TokenPrefix + "chiave-scaduta",
		"revocata":    apikeys.TokenPrefix + "chiave-revocata",
	} {
		rec := f.do(http.MethodGet, "/jobs", nil, withKey(token))
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("%s = %d, atteso 401: %s", nome, rec.Code, rec.Body.String())
		}
		corpi[nome] = rec.Body.String()
	}

	riferimento := corpi["inesistente"]
	for nome, corpo := range corpi {
		if corpo != riferimento {
			t.Errorf("la risposta per %q è diversa da quella per una chiave inesistente:\n%s\n%s",
				nome, corpo, riferimento)
		}
	}
}

// ------------------------------------------------------------- validazione

// I motivi di rifiuto sono ancorati ai campi, così che un form possa
// evidenziarli senza interpretare il messaggio.
func TestLaCreazioneRifiutaICampiNonValidi(t *testing.T) {
	f := newKeysFixture(t)

	tests := map[string]struct {
		body  map[string]any
		campo string
		code  string
	}{
		"senza nome": {
			map[string]any{"scopes": []string{"jobs:read"}}, "name", "required",
		},
		"senza scope": {
			map[string]any{"name": "chiave"}, "scopes", "required",
		},
		"scope inventato": {
			map[string]any{"name": "chiave", "scopes": []string{"jobs:tutto"}}, "scopes", "unknown_scope",
		},
		"scadenza nel passato": {
			map[string]any{
				"name":       "chiave",
				"scopes":     []string{"jobs:read"},
				"expires_at": time.Now().Add(-time.Hour).Format(time.RFC3339),
			}, "expires_at", "in_the_past",
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			rec := f.do(http.MethodPost, "/keys", tc.body, withCookie(f.token))
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("POST /keys = %d, atteso 400: %s", rec.Code, rec.Body.String())
			}
			detail := errorDetail(t, rec)
			if detail.Code != "validation_failed" {
				t.Fatalf("code = %q, atteso validation_failed", detail.Code)
			}
			if got := fieldCodes(t, rec)[tc.campo]; got != tc.code {
				t.Errorf("codice sul campo %q = %q, atteso %q (details: %+v)",
					tc.campo, got, tc.code, detail.Details)
			}
		})
	}
}

// Le rotte delle chiavi esigono una sessione: senza credenziali è 401.
func TestLeRotteDelleChiaviEsigonoLAutenticazione(t *testing.T) {
	f := newKeysFixture(t)

	tests := []struct{ method, path string }{
		{http.MethodGet, "/keys"},
		{http.MethodPost, "/keys"},
		{http.MethodDelete, "/keys/qualsiasi"},
		{http.MethodGet, "/keys/scopes"},
	}
	for _, tc := range tests {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			var body any
			if tc.method == http.MethodPost {
				body = map[string]any{"name": "x", "scopes": []string{"jobs:read"}}
			}
			rec := f.do(tc.method, tc.path, body)
			if rec.Code != http.StatusUnauthorized {
				t.Errorf("%s %s senza credenziali = %d, atteso 401", tc.method, tc.path, rec.Code)
			}
		})
	}
}

// L'elenco degli scope assegnabili è servito dal backend: il form che crea una
// chiave lo legge da qui invece di duplicarlo.
func TestGliScopeAssegnabiliSonoEsposti(t *testing.T) {
	f := newKeysFixture(t)

	rec := f.do(http.MethodGet, "/keys/scopes", nil, withCookie(f.token))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /keys/scopes = %d, atteso 200: %s", rec.Code, rec.Body.String())
	}
	var body httpapi.ScopesResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decodifica: %v", err)
	}
	if len(body.Scopes) != len(apikeys.Scopes()) {
		t.Errorf("scope = %v, attesi %v", body.Scopes, apikeys.Scopes())
	}
}

// L'elenco è ambito sull'utente: le chiavi di un altro account non compaiono.
func TestLElencoMostraSoloLeProprieChiavi(t *testing.T) {
	f := newKeysFixture(t)
	f.creaChiave("mia", "jobs:read")

	_, altroToken := f.registerAndLoginAs("altro@example.com")
	elenco := chiavi(t, f.do(http.MethodGet, "/keys", nil, withCookie(altroToken)))
	if len(elenco) != 0 {
		t.Errorf("chiavi viste dall'altro utente = %d, attesa 0", len(elenco))
	}
}

// Senza il servizio delle chiavi le rotte non ci sono, e una chiave presentata a
// quel servizio non autentica per sbaglio con la sessione.
func TestSenzaIlServizioDelleChiaviLeRotteNonEsistono(t *testing.T) {
	a := newAPI(t, func(_ *config.Config, _ *auth.Options, deps *httpapi.Deps) {
		svc, err := jobs.NewService(jobs.Options{
			Store:  jobstest.NewStore(),
			Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		})
		if err != nil {
			t.Fatalf("jobs.NewService: %v", err)
		}
		deps.Jobs = svc
		// Il router va costruito con Deps.APIKeys esplicitamente assente: è la
		// configurazione dei test dell'health check.
		deps.APIKeys = nil
	})
	_, token := a.registerAndLogin()

	// Le rotte non registrate rispondono 404, non 401: non esistono.
	if rec := a.do(http.MethodGet, "/keys", nil, withCookie(token)); rec.Code != http.StatusNotFound {
		t.Errorf("GET /keys senza il servizio = %d, atteso 404: %s", rec.Code, rec.Body.String())
	}
	// E una chiave presentata alle rotte dei job non diventa un token di sessione.
	rec := a.do(http.MethodGet, "/jobs", nil, withKey(apikeys.TokenPrefix+"qualcosa"))
	if rec.Code == http.StatusOK {
		t.Error("una chiave ha autenticato senza il servizio delle chiavi")
	}
}

// ------------------------------------------------------------------ supporto

// chiavi decodifica l'elenco delle chiavi da una risposta.
func chiavi(t *testing.T, rec *httptest.ResponseRecorder) []httpapi.APIKeyResponse {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /keys = %d, atteso 200: %s", rec.Code, rec.Body.String())
	}
	var body httpapi.APIKeyListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decodifica dell'elenco: %v", err)
	}
	return body.Keys
}
