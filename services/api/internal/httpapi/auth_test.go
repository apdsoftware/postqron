package httpapi_test

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/apdsoftware/postqron/services/api/internal/apikeys"
	"github.com/apdsoftware/postqron/services/api/internal/apikeystest"
	"github.com/apdsoftware/postqron/services/api/internal/auth"
	"github.com/apdsoftware/postqron/services/api/internal/authtest"
	"github.com/apdsoftware/postqron/services/api/internal/config"
	"github.com/apdsoftware/postqron/services/api/internal/httpapi"
	"github.com/apdsoftware/postqron/services/api/internal/ratelimit"
)

// ---------------------------------------------------------------- impalcatura

const (
	testEmail    = "mario.rossi@example.com"
	testPassword = "una-password-lunga-abbastanza"
	testSecret   = "un-segreto-di-prova-abbastanza-lungo-da-essere-accettato"
)

// cheapParams: Argon2id ai minimi accettati, per non pagare 150 ms per richiesta
// in una suite che ne fa molte. La proprietà che i test verificano è il
// *rapporto* fra i tempi dei due percorsi, non il loro valore assoluto.
var cheapParams = auth.Argon2idParams{
	Memory: 8 * 1024, Time: 1, Parallelism: 1, SaltLength: 16, KeyLength: 32,
}

type api struct {
	t       *testing.T
	handler http.Handler
	store   *authtest.Store
	mailer  *auth.MemoryMailer
	svc     *auth.Service
	logs    *bytes.Buffer

	// Le chiavi API (R9) sono agganciate a ogni router costruito qui, e non solo a
	// quello dei loro test: è ciò che rende vero, per tutte le suite insieme, che
	// aggiungere il riconoscimento delle chiavi non ha cambiato il comportamento
	// delle rotte autenticate con la sessione.
	keys      *apikeys.Service
	keysStore *apikeystest.Store
	keyring   auth.Keyring
}

// newAPI costruisce il router con l'autenticazione agganciata a un archivio in
// memoria.
func newAPI(t *testing.T, tune ...func(*config.Config, *auth.Options, *httpapi.Deps)) *api {
	t.Helper()

	cfg, err := config.LoadFrom(func(string) string { return "" })
	if err != nil {
		t.Fatalf("configurazione di default non valida: %v", err)
	}

	store := authtest.NewStore()
	mailer := &auth.MemoryMailer{}
	hasher, err := auth.NewHasher(cheapParams)
	if err != nil {
		t.Fatalf("NewHasher: %v", err)
	}
	keyring, err := auth.NewKeyring(testSecret)
	if err != nil {
		t.Fatalf("NewKeyring: %v", err)
	}

	logs := &bytes.Buffer{}
	logger := slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug}))

	opts := auth.Options{
		Store: store, Hasher: hasher, Keyring: keyring, Mailer: mailer, Logger: logger,
	}

	// Le chiavi API (R9) usano lo stesso keyring dell'autenticazione e lo stesso
	// archivio degli utenti: è la configurazione d'esercizio, dove risolvere il
	// proprietario di una chiave è leggere una riga di `users`.
	//
	// Sono agganciate *prima* di `tune`, così che un test possa azzerarle per
	// verificare come si comporta un router senza il servizio delle chiavi.
	keysStore := apikeystest.NewStore()
	keysSvc, err := apikeys.NewService(apikeys.Options{
		Store:   keysStore,
		Users:   store,
		Keyring: keyring,
		Logger:  logger,
	})
	if err != nil {
		t.Fatalf("apikeys.NewService: %v", err)
	}

	deps := httpapi.Deps{APIKeys: keysSvc}
	for _, fn := range tune {
		fn(&cfg, &opts, &deps)
	}

	svc, err := auth.NewService(opts)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	t.Cleanup(svc.Wait)
	deps.Auth = svc

	return &api{
		t: t, handler: httpapi.NewRouter(cfg, "test", logger, deps),
		store: store, mailer: mailer, svc: svc, logs: logs,
		keys: keysSvc, keysStore: keysStore, keyring: keyring,
	}
}

// do esegue una richiesta JSON e restituisce la risposta registrata.
func (a *api) do(method, path string, body any, prepare ...func(*http.Request)) *httptest.ResponseRecorder {
	a.t.Helper()

	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			a.t.Fatalf("codifica del corpo: %v", err)
		}
		reader = bytes.NewReader(encoded)
	}

	req := httptest.NewRequest(method, path, reader)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.RemoteAddr = "203.0.113.7:54321"
	req.Header.Set("User-Agent", "Postqron-Test/1.0")
	for _, fn := range prepare {
		fn(req)
	}

	rec := httptest.NewRecorder()
	a.handler.ServeHTTP(rec, req)
	return rec
}

// withCookie aggiunge il cookie di sessione a una richiesta.
func withCookie(token string) func(*http.Request) {
	return func(r *http.Request) {
		r.AddCookie(&http.Cookie{Name: httpapi.SessionCookieName, Value: token})
	}
}

// sessionCookie estrae il cookie di sessione da una risposta.
func sessionCookie(t *testing.T, rec *httptest.ResponseRecorder) *http.Cookie {
	t.Helper()
	for _, cookie := range rec.Result().Cookies() {
		if cookie.Name == httpapi.SessionCookieName {
			return cookie
		}
	}
	return nil
}

// registerAndLogin crea un account, ne conferma la password e restituisce il
// token di sessione.
func (a *api) registerAndLogin() (auth.User, string) {
	a.t.Helper()
	if rec := a.do(http.MethodPost, "/auth/register", map[string]string{
		"email": testEmail, "password": testPassword, "full_name": "Mario Rossi",
	}); rec.Code != http.StatusAccepted {
		a.t.Fatalf("registrazione: status = %d, corpo = %s", rec.Code, rec.Body)
	}
	a.svc.Wait()

	rec := a.do(http.MethodPost, "/auth/login", map[string]string{
		"email": testEmail, "password": testPassword,
	})
	if rec.Code != http.StatusOK {
		a.t.Fatalf("login: status = %d, corpo = %s", rec.Code, rec.Body)
	}
	cookie := sessionCookie(a.t, rec)
	if cookie == nil {
		a.t.Fatal("il login non ha impostato il cookie di sessione")
	}
	user, err := a.store.UserByEmail(a.t.Context(), testEmail)
	if err != nil {
		a.t.Fatalf("l'account non esiste: %v", err)
	}
	return user, cookie.Value
}

// errorCode legge il codice di errore dal corpo.
func errorCode(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var body httpapi.ErrorBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("corpo non decodificabile (%q): %v", rec.Body.String(), err)
	}
	return body.Error.Code
}

// ------------------------------------------------------- flusso completo

func TestFlussoCompletoDiAutenticazione(t *testing.T) {
	a := newAPI(t)

	// Registrazione.
	rec := a.do(http.MethodPost, "/auth/register", map[string]string{
		"email": testEmail, "password": testPassword, "full_name": "Mario Rossi",
	})
	if rec.Code != http.StatusAccepted {
		t.Fatalf("register: status = %d, corpo = %s", rec.Code, rec.Body)
	}
	a.svc.Wait()

	// Login.
	rec = a.do(http.MethodPost, "/auth/login", map[string]string{
		"email": testEmail, "password": testPassword,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("login: status = %d, corpo = %s", rec.Code, rec.Body)
	}
	var envelope httpapi.SessionEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("corpo del login: %v", err)
	}
	if envelope.User.Email != testEmail {
		t.Errorf("email = %q", envelope.User.Email)
	}
	if !envelope.Session.Current {
		t.Error("la sessione restituita dal login non è marcata come corrente")
	}
	// Il corpo non contiene il token: quello sta nel cookie, HttpOnly, fuori
	// dalla portata di JavaScript.
	token := sessionCookie(t, rec).Value
	if strings.Contains(rec.Body.String(), token) {
		t.Error("il token di sessione compare nel corpo della risposta")
	}

	// Sessione corrente.
	rec = a.do(http.MethodGet, "/auth/session", nil, withCookie(token))
	if rec.Code != http.StatusOK {
		t.Fatalf("session: status = %d, corpo = %s", rec.Code, rec.Body)
	}

	// Conferma dell'indirizzo.
	msg, found := a.mailer.Last(auth.KindEmailVerification)
	if !found {
		t.Fatal("nessuna email di conferma")
	}
	rec = a.do(http.MethodPost, "/auth/email/verify", map[string]string{"token": msg.Token})
	if rec.Code != http.StatusNoContent {
		t.Fatalf("verify: status = %d, corpo = %s", rec.Code, rec.Body)
	}
	rec = a.do(http.MethodGet, "/auth/session", nil, withCookie(token))
	if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("corpo: %v", err)
	}
	if !envelope.User.EmailVerified {
		t.Error("l'indirizzo non risulta confermato")
	}

	// Logout.
	rec = a.do(http.MethodPost, "/auth/logout", nil, withCookie(token))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("logout: status = %d", rec.Code)
	}
	if cookie := sessionCookie(t, rec); cookie == nil || cookie.MaxAge >= 0 {
		t.Error("il logout non ha cancellato il cookie")
	}
	if rec := a.do(http.MethodGet, "/auth/session", nil, withCookie(token)); rec.Code != http.StatusUnauthorized {
		t.Errorf("dopo il logout: status = %d, atteso 401", rec.Code)
	}
}

// ------------------------------------------------- enumerazione degli account

// La risposta alla registrazione è identica byte per byte, indirizzo libero o
// preso: status, testate significative e corpo.
func TestRegisterRispondeInModoIdenticoSuIndirizzoLiberoEPreso(t *testing.T) {
	a := newAPI(t, func(_ *config.Config, o *auth.Options, _ *httpapi.Deps) {
		o.Limits.RegisterPerIP = ratelimit.Rule{Burst: 100, Window: time.Hour}
	})

	libero := a.do(http.MethodPost, "/auth/register", map[string]string{
		"email": testEmail, "password": testPassword,
	})
	a.svc.Wait()
	preso := a.do(http.MethodPost, "/auth/register", map[string]string{
		"email": testEmail, "password": "un-altra-password-diversa",
	})
	a.svc.Wait()

	if libero.Code != http.StatusAccepted || preso.Code != http.StatusAccepted {
		t.Fatalf("status: libero = %d, preso = %d, attesi entrambi 202", libero.Code, preso.Code)
	}
	if libero.Body.String() != preso.Body.String() {
		t.Errorf("corpi diversi:\nlibero: %s\npreso:  %s", libero.Body, preso.Body)
	}
	if got, want := preso.Header().Get("Content-Type"), libero.Header().Get("Content-Type"); got != want {
		t.Errorf("Content-Type = %q, atteso %q", got, want)
	}
}

// Password sbagliata e indirizzo inesistente producono la stessa risposta: stesso
// status, stesso codice, stesso corpo.
func TestLoginRispondeInModoIdenticoQualunqueSiaLaCausa(t *testing.T) {
	a := newAPI(t, func(_ *config.Config, o *auth.Options, _ *httpapi.Deps) {
		o.Limits.LoginPerIP = ratelimit.Rule{Burst: 1000, Window: time.Hour}
		o.Limits.LoginPerAccount = ratelimit.Rule{Burst: 1000, Window: time.Hour}
	})
	a.registerAndLogin()

	casi := map[string]map[string]string{
		"password sbagliata":    {"email": testEmail, "password": "password-completamente-altra"},
		"indirizzo inesistente": {"email": "nessuno@example.com", "password": testPassword},
		"indirizzo malformato":  {"email": "non-una-email", "password": testPassword},
	}

	var bodies []string
	for name, body := range casi {
		rec := a.do(http.MethodPost, "/auth/login", body)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s: status = %d, atteso 401", name, rec.Code)
		}
		if code := errorCode(t, rec); code != "invalid_credentials" {
			t.Errorf("%s: codice = %q, atteso invalid_credentials", name, code)
		}
		if sessionCookie(t, rec) != nil {
			t.Errorf("%s: è stato impostato un cookie di sessione", name)
		}
		bodies = append(bodies, rec.Body.String())
	}
	for i := 1; i < len(bodies); i++ {
		if bodies[i] != bodies[0] {
			t.Errorf("corpi diversi fra i casi di rifiuto:\n%s\n%s", bodies[0], bodies[i])
		}
	}
}

// La stessa proprietà misurata sul tempo di risposta HTTP, che è ciò che un
// attaccante osserva davvero. La soglia è larga (fattore due) perché la misura è
// rumorosa; serve a distinguere «uguali» da «uno dei due non calcola l'hash»,
// che è un fattore cento.
func TestLoginNonRivelaLEsistenzaDellAccountDalTempoDiRisposta(t *testing.T) {
	a := newAPI(t, func(_ *config.Config, o *auth.Options, _ *httpapi.Deps) {
		o.Limits.LoginPerIP = ratelimit.Rule{Burst: 10_000, Window: time.Hour}
		o.Limits.LoginPerAccount = ratelimit.Rule{Burst: 10_000, Window: time.Hour}
	})
	a.registerAndLogin()

	measure := func(email string) time.Duration {
		start := time.Now()
		a.do(http.MethodPost, "/auth/login", map[string]string{
			"email": email, "password": "password-sbagliata-ma-lunga",
		})
		return time.Since(start)
	}

	const samples = 15
	esistente := make([]time.Duration, 0, samples)
	inesistente := make([]time.Duration, 0, samples)
	// Alternati: se la macchina rallenta a metà, rallenta per entrambi.
	for range samples {
		esistente = append(esistente, measure(testEmail))
		inesistente = append(inesistente, measure("nessuno@example.com"))
	}

	conAccount, senzaAccount := median(esistente), median(inesistente)
	t.Logf("mediana con account = %s, senza account = %s", conAccount, senzaAccount)
	if conAccount <= 0 || senzaAccount <= 0 {
		t.Fatal("misura non utilizzabile")
	}
	if ratio := float64(conAccount) / float64(senzaAccount); ratio < 0.5 || ratio > 2 {
		t.Errorf("i tempi differiscono di un fattore %.2f (con account %s, senza %s)",
			ratio, conAccount, senzaAccount)
	}
}

// Il recupero password risponde allo stesso modo, e nello stesso tempo, per un
// indirizzo registrato e per uno che non lo è.
func TestForgotPasswordRispondeInModoIdentico(t *testing.T) {
	a := newAPI(t, func(_ *config.Config, o *auth.Options, _ *httpapi.Deps) {
		o.Limits.PasswordResetPerIP = ratelimit.Rule{Burst: 1000, Window: time.Hour}
		o.Limits.PasswordResetPerAccount = ratelimit.Rule{Burst: 1000, Window: time.Hour}
	})
	a.registerAndLogin()

	esistente := a.do(http.MethodPost, "/auth/password/forgot", map[string]string{"email": testEmail})
	inesistente := a.do(http.MethodPost, "/auth/password/forgot", map[string]string{"email": "nessuno@example.com"})
	malformato := a.do(http.MethodPost, "/auth/password/forgot", map[string]string{"email": "non-una-email"})
	a.svc.Wait()

	for name, rec := range map[string]*httptest.ResponseRecorder{
		"esistente": esistente, "inesistente": inesistente, "malformato": malformato,
	} {
		if rec.Code != http.StatusAccepted {
			t.Errorf("%s: status = %d, atteso 202", name, rec.Code)
		}
		if rec.Body.String() != esistente.Body.String() {
			t.Errorf("%s: corpo diverso da quello dell'indirizzo esistente:\n%s", name, rec.Body)
		}
	}

	measure := func(email string) time.Duration {
		start := time.Now()
		a.do(http.MethodPost, "/auth/password/forgot", map[string]string{"email": email})
		return time.Since(start)
	}
	const samples = 21
	con := make([]time.Duration, 0, samples)
	senza := make([]time.Duration, 0, samples)
	for range samples {
		con = append(con, measure(testEmail))
		senza = append(senza, measure("nessuno@example.com"))
	}
	a.svc.Wait()

	// Qui non c'è un Argon2id che domini il tempo: la costanza viene dal fatto
	// che ricerca, emissione del token e invio avvengono dopo la risposta. La
	// soglia è in valore absoluto perché entrambe le mediane sono microsecondi,
	// dove un rapporto è tutto rumore.
	conMedian, senzaMedian := median(con), median(senza)
	t.Logf("mediana con account = %s, senza account = %s", conMedian, senzaMedian)
	if diff := absDuration(conMedian - senzaMedian); diff > 2*time.Millisecond {
		t.Errorf("differenza di %s fra i due percorsi: il lavoro non è tutto fuori dal percorso della risposta "+
			"(con account %s, senza %s)", diff, conMedian, senzaMedian)
	}
}

func median(values []time.Duration) time.Duration {
	if len(values) == 0 {
		return 0
	}
	sorted := slices.Clone(values)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	return sorted[len(sorted)/2]
}

func absDuration(d time.Duration) time.Duration {
	if d < 0 {
		return -d
	}
	return d
}

// -------------------------------------------------------------- rate limiting

func TestLoginRispondeConUn429EIlRetryAfter(t *testing.T) {
	a := newAPI(t, func(_ *config.Config, o *auth.Options, _ *httpapi.Deps) {
		o.Limits.LoginPerAccount = ratelimit.Rule{Burst: 3, Window: 15 * time.Minute}
	})
	a.registerAndLogin()

	for i := range 3 {
		rec := a.do(http.MethodPost, "/auth/login", map[string]string{
			"email": testEmail, "password": "password-sbagliata-ma-lunga",
		})
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("tentativo %d: status = %d, atteso 401", i, rec.Code)
		}
	}

	rec := a.do(http.MethodPost, "/auth/login", map[string]string{
		"email": testEmail, "password": testPassword,
	})
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, atteso 429", rec.Code)
	}
	if code := errorCode(t, rec); code != "rate_limited" {
		t.Errorf("codice = %q, atteso rate_limited", code)
	}
	retryAfter := rec.Header().Get("Retry-After")
	seconds, err := strconv.Atoi(retryAfter)
	if err != nil || seconds <= 0 {
		t.Errorf("Retry-After = %q, atteso un numero di secondi positivo", retryAfter)
	}
	// Il 429 non deve aprire una sessione, nemmeno con la password giusta.
	if sessionCookie(t, rec) != nil {
		t.Error("una risposta 429 ha impostato il cookie di sessione")
	}
}

// Il 429 non dice se l'account esiste: il contatore vale su qualunque stringa
// scritta nel campo email.
func TestIl429NonDipendeDallEsistenzaDellAccount(t *testing.T) {
	a := newAPI(t, func(_ *config.Config, o *auth.Options, _ *httpapi.Deps) {
		o.Limits.LoginPerAccount = ratelimit.Rule{Burst: 1, Window: 15 * time.Minute}
		o.Limits.LoginPerIP = ratelimit.Rule{Burst: 1000, Window: 15 * time.Minute}
	})
	a.registerAndLogin()

	var bodies []string
	for _, email := range []string{testEmail, "nessuno@example.com"} {
		a.do(http.MethodPost, "/auth/login", map[string]string{
			"email": email, "password": "password-sbagliata-ma-lunga",
		})
		rec := a.do(http.MethodPost, "/auth/login", map[string]string{
			"email": email, "password": "password-sbagliata-ma-lunga",
		})
		if rec.Code != http.StatusTooManyRequests {
			t.Fatalf("%s: status = %d, atteso 429", email, rec.Code)
		}
		bodies = append(bodies, rec.Body.String())
	}
	if bodies[0] != bodies[1] {
		t.Errorf("corpi diversi:\n%s\n%s", bodies[0], bodies[1])
	}
}

func TestForgotPasswordELimitato(t *testing.T) {
	a := newAPI(t, func(_ *config.Config, o *auth.Options, _ *httpapi.Deps) {
		o.Limits.PasswordResetPerAccount = ratelimit.Rule{Burst: 2, Window: time.Hour}
	})
	a.registerAndLogin()

	for i := range 2 {
		if rec := a.do(http.MethodPost, "/auth/password/forgot",
			map[string]string{"email": testEmail}); rec.Code != http.StatusAccepted {
			t.Fatalf("richiesta %d: status = %d", i, rec.Code)
		}
	}
	a.svc.Wait()

	rec := a.do(http.MethodPost, "/auth/password/forgot", map[string]string{"email": testEmail})
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, atteso 429", rec.Code)
	}
}

// ------------------------------------------------------------------- cookie

func TestIlCookieDiSessioneEProtetto(t *testing.T) {
	tests := map[string]struct {
		env        string
		wantSecure bool
	}{
		// In sviluppo il flag Secure impedirebbe al browser di memorizzare il
		// cookie su http://localhost: il risultato sarebbe uno sviluppatore che
		// lo disattiva, cioè che lo disattiva anche in produzione.
		"sviluppo":   {config.EnvDevelopment, false},
		"staging":    {config.EnvStaging, true},
		"produzione": {config.EnvProduction, true},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			a := newAPI(t, func(c *config.Config, _ *auth.Options, _ *httpapi.Deps) {
				c.Env = tc.env
			})
			a.registerAndLogin()

			rec := a.do(http.MethodPost, "/auth/login", map[string]string{
				"email": testEmail, "password": testPassword,
			})
			cookie := sessionCookie(t, rec)
			if cookie == nil {
				t.Fatal("nessun cookie di sessione")
			}
			if !cookie.HttpOnly {
				t.Error("il cookie non è HttpOnly: una XSS sulla dashboard porterebbe via la sessione")
			}
			if cookie.Secure != tc.wantSecure {
				t.Errorf("Secure = %v, atteso %v", cookie.Secure, tc.wantSecure)
			}
			if cookie.SameSite != http.SameSiteLaxMode {
				t.Errorf("SameSite = %v, atteso Lax", cookie.SameSite)
			}
			if cookie.Path != "/" {
				t.Errorf("Path = %q, atteso /", cookie.Path)
			}
			if cookie.MaxAge <= 0 {
				t.Errorf("MaxAge = %d, atteso positivo", cookie.MaxAge)
			}
		})
	}
}

// I client che non sono browser non hanno un cookie jar: la testata Authorization
// è la loro strada.
func TestLaSessioneSiPuoPresentareComeBearer(t *testing.T) {
	a := newAPI(t)
	_, token := a.registerAndLogin()

	rec := a.do(http.MethodGet, "/auth/session", nil, func(r *http.Request) {
		r.Header.Set("Authorization", "Bearer "+token)
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, atteso 200", rec.Code)
	}

	// Uno schema diverso, o un token inventato, non passano.
	for name, prepare := range map[string]func(*http.Request){
		"schema sbagliato": func(r *http.Request) { r.Header.Set("Authorization", "Basic "+token) },
		"token inventato":  func(r *http.Request) { r.Header.Set("Authorization", "Bearer inventato") },
		"testata vuota":    func(r *http.Request) { r.Header.Set("Authorization", "") },
	} {
		if rec := a.do(http.MethodGet, "/auth/session", nil, prepare); rec.Code != http.StatusUnauthorized {
			t.Errorf("%s: status = %d, atteso 401", name, rec.Code)
		}
	}
}

// --------------------------------------------------------------- protezioni

// Il controllo sul Content-Type è ciò che rende impossibile il CSRF via form: un
// `<form>` di una pagina di terzi può inviare solo tre tipi di media, e nessuno
// dei tre è application/json.
func TestLeRotteRifiutanoIFormEQuindiIlCSRF(t *testing.T) {
	a := newAPI(t)
	a.registerAndLogin()

	body := strings.NewReader(`email=` + testEmail + `&password=` + testPassword)
	req := httptest.NewRequest(http.MethodPost, "/auth/login", body)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.RemoteAddr = "203.0.113.7:1234"

	rec := httptest.NewRecorder()
	a.handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, atteso 400", rec.Code)
	}
	if sessionCookie(t, rec) != nil {
		t.Error("una richiesta con Content-Type di form ha aperto una sessione")
	}

	// Anche senza Content-Type, che è l'altra forma che un form può prendere.
	req = httptest.NewRequest(http.MethodPost, "/auth/login", strings.NewReader(`{}`))
	req.RemoteAddr = "203.0.113.7:1234"
	rec = httptest.NewRecorder()
	a.handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("senza Content-Type: status = %d, atteso 400", rec.Code)
	}
}

func TestIlCorpoDellaRichiestaEValidato(t *testing.T) {
	a := newAPI(t)

	tests := map[string]struct {
		body     string
		wantCode int
		wantErr  string
	}{
		"JSON non valido":     {`{`, http.StatusBadRequest, "invalid_request"},
		"campo sconosciuto":   {`{"email":"a@b.co","password":"x","admin":true}`, http.StatusBadRequest, "invalid_request"},
		"due oggetti":         {`{"email":"a@b.co"}{"email":"c@d.co"}`, http.StatusBadRequest, "invalid_request"},
		"corpo troppo grande": {`{"password":"` + strings.Repeat("a", 9000) + `"}`, http.StatusRequestEntityTooLarge, "body_too_large"},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/auth/login", strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			req.RemoteAddr = "203.0.113.7:1234"
			rec := httptest.NewRecorder()
			a.handler.ServeHTTP(rec, req)

			if rec.Code != tc.wantCode {
				t.Fatalf("status = %d, atteso %d (corpo: %s)", rec.Code, tc.wantCode, rec.Body)
			}
			if code := errorCode(t, rec); code != tc.wantErr {
				t.Errorf("codice = %q, atteso %q", code, tc.wantErr)
			}
		})
	}
}

// Le rotte autenticate rifiutano chi non ha una sessione, e non sono aggirabili
// con un metodo diverso.
func TestLeRotteAutenticateEsigonoUnaSessione(t *testing.T) {
	a := newAPI(t)
	a.registerAndLogin()

	protette := []struct {
		method string
		path   string
		body   any
	}{
		{http.MethodGet, "/auth/session", nil},
		{http.MethodGet, "/auth/sessions", nil},
		{http.MethodDelete, "/auth/sessions", nil},
		{http.MethodDelete, "/auth/sessions/qualsiasi", nil},
		{http.MethodPost, "/auth/password/change", map[string]string{
			"current_password": testPassword, "new_password": "una-password-nuova-e-lunga",
		}},
		{http.MethodPost, "/auth/email/verify/resend", nil},
	}
	for _, route := range protette {
		t.Run(route.method+" "+route.path, func(t *testing.T) {
			rec := a.do(route.method, route.path, route.body)
			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, atteso 401", rec.Code)
			}
			if code := errorCode(t, rec); code != "unauthenticated" {
				t.Errorf("codice = %q, atteso unauthenticated", code)
			}
		})
	}

	// Nessuna password è cambiata per effetto dei tentativi.
	rec := a.do(http.MethodPost, "/auth/login", map[string]string{
		"email": testEmail, "password": testPassword,
	})
	if rec.Code != http.StatusOK {
		t.Errorf("la password è cambiata durante i tentativi non autenticati: status = %d", rec.Code)
	}
}

// --------------------------------------------------------- gestione sessioni

func TestGestioneDelleSessioniViaHTTP(t *testing.T) {
	a := newAPI(t)
	_, primo := a.registerAndLogin()

	// Una seconda sessione, da un altro dispositivo.
	rec := a.do(http.MethodPost, "/auth/login", map[string]string{
		"email": testEmail, "password": testPassword,
	}, func(r *http.Request) {
		r.RemoteAddr = "198.51.100.22:9999"
		r.Header.Set("User-Agent", "AltroDispositivo/2.0")
	})
	secondo := sessionCookie(t, rec).Value

	// L'elenco mostra entrambe, e marca quella in uso.
	rec = a.do(http.MethodGet, "/auth/sessions", nil, withCookie(primo))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var listed struct {
		Sessions []httpapi.SessionResponse `json:"sessions"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &listed); err != nil {
		t.Fatalf("corpo: %v", err)
	}
	if len(listed.Sessions) != 2 {
		t.Fatalf("sessioni = %d, attese 2", len(listed.Sessions))
	}
	corrente := 0
	for _, session := range listed.Sessions {
		if session.Current {
			corrente++
		}
		// L'elenco non deve contenere né il token né la sua impronta.
		if strings.Contains(rec.Body.String(), primo) || strings.Contains(rec.Body.String(), secondo) {
			t.Fatal("l'elenco delle sessioni contiene un token")
		}
	}
	if corrente != 1 {
		t.Errorf("sessioni marcate come correnti = %d, attesa 1", corrente)
	}

	// Revoca puntuale della seconda.
	var secondoID string
	for _, session := range listed.Sessions {
		if !session.Current {
			secondoID = session.ID
		}
	}
	rec = a.do(http.MethodDelete, "/auth/sessions/"+secondoID, nil, withCookie(primo))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("revoca: status = %d, corpo = %s", rec.Code, rec.Body)
	}
	if rec := a.do(http.MethodGet, "/auth/session", nil, withCookie(secondo)); rec.Code != http.StatusUnauthorized {
		t.Errorf("la sessione revocata funziona ancora: status = %d", rec.Code)
	}
	// Una sessione inesistente è un 404, non un 204.
	if rec := a.do(http.MethodDelete, "/auth/sessions/"+secondoID, nil, withCookie(primo)); rec.Code != http.StatusNotFound {
		t.Errorf("seconda revoca: status = %d, atteso 404", rec.Code)
	}
}

// Chiudere la propria sessione dal pannello equivale a un logout: il cookie va
// cancellato, altrimenti il browser continuerebbe a mandarlo.
func TestRevocareLaPropriaSessioneCancellaIlCookie(t *testing.T) {
	a := newAPI(t)
	_, token := a.registerAndLogin()

	rec := a.do(http.MethodGet, "/auth/session", nil, withCookie(token))
	var envelope httpapi.SessionEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("corpo: %v", err)
	}

	rec = a.do(http.MethodDelete, "/auth/sessions/"+envelope.Session.ID, nil, withCookie(token))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d", rec.Code)
	}
	if cookie := sessionCookie(t, rec); cookie == nil || cookie.MaxAge >= 0 {
		t.Error("il cookie non è stato cancellato")
	}
}

func TestChiudiLeAltreSessioni(t *testing.T) {
	a := newAPI(t)
	_, corrente := a.registerAndLogin()

	rec := a.do(http.MethodPost, "/auth/login", map[string]string{
		"email": testEmail, "password": testPassword,
	})
	altra := sessionCookie(t, rec).Value

	rec = a.do(http.MethodDelete, "/auth/sessions", nil, withCookie(corrente))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, corpo = %s", rec.Code, rec.Body)
	}
	var result struct {
		Revoked int `json:"revoked"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatalf("corpo: %v", err)
	}
	if result.Revoked != 1 {
		t.Errorf("revocate = %d, attesa 1", result.Revoked)
	}
	if rec := a.do(http.MethodGet, "/auth/session", nil, withCookie(altra)); rec.Code != http.StatusUnauthorized {
		t.Errorf("l'altra sessione è ancora valida: status = %d", rec.Code)
	}
	if rec := a.do(http.MethodGet, "/auth/session", nil, withCookie(corrente)); rec.Code != http.StatusOK {
		t.Errorf("la sessione corrente è stata chiusa: status = %d", rec.Code)
	}
}

// --------------------------------------------------- recupero e cambio password

func TestRecuperoPasswordViaHTTP(t *testing.T) {
	a := newAPI(t)
	_, token := a.registerAndLogin()

	if rec := a.do(http.MethodPost, "/auth/password/forgot",
		map[string]string{"email": testEmail}); rec.Code != http.StatusAccepted {
		t.Fatalf("forgot: status = %d", rec.Code)
	}
	a.svc.Wait()

	msg, found := a.mailer.Last(auth.KindPasswordReset)
	if !found {
		t.Fatal("nessun link di recupero")
	}

	const nuova = "una-password-nuova-e-lunga"
	rec := a.do(http.MethodPost, "/auth/password/reset",
		map[string]string{"token": msg.Token, "password": nuova})
	if rec.Code != http.StatusNoContent {
		t.Fatalf("reset: status = %d, corpo = %s", rec.Code, rec.Body)
	}
	// Il cookie viene cancellato: tutte le sessioni sono state revocate.
	if cookie := sessionCookie(t, rec); cookie == nil || cookie.MaxAge >= 0 {
		t.Error("il cookie non è stato cancellato dopo la reimpostazione")
	}
	if rec := a.do(http.MethodGet, "/auth/session", nil, withCookie(token)); rec.Code != http.StatusUnauthorized {
		t.Errorf("una sessione precedente è sopravvissuta: status = %d", rec.Code)
	}

	// La password nuova funziona, la vecchia no.
	if rec := a.do(http.MethodPost, "/auth/login",
		map[string]string{"email": testEmail, "password": nuova}); rec.Code != http.StatusOK {
		t.Errorf("login con la password nuova: status = %d", rec.Code)
	}
	if rec := a.do(http.MethodPost, "/auth/login",
		map[string]string{"email": testEmail, "password": testPassword}); rec.Code != http.StatusUnauthorized {
		t.Errorf("login con la password vecchia: status = %d, atteso 401", rec.Code)
	}

	// Il token non vale una seconda volta.
	rec = a.do(http.MethodPost, "/auth/password/reset",
		map[string]string{"token": msg.Token, "password": "un-altra-password-ancora"})
	if rec.Code != http.StatusBadRequest {
		t.Errorf("secondo uso del token: status = %d, atteso 400", rec.Code)
	}
	if code := errorCode(t, rec); code != "invalid_token" {
		t.Errorf("codice = %q, atteso invalid_token", code)
	}
}

func TestCambioPasswordViaHTTP(t *testing.T) {
	a := newAPI(t)
	_, token := a.registerAndLogin()

	const nuova = "una-password-nuova-e-lunga"
	rec := a.do(http.MethodPost, "/auth/password/change", map[string]string{
		"current_password": testPassword, "new_password": nuova,
	}, withCookie(token))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, corpo = %s", rec.Code, rec.Body)
	}

	// Il cookie porta un token nuovo, e il vecchio non vale più.
	cookie := sessionCookie(t, rec)
	if cookie == nil || cookie.Value == token {
		t.Fatal("il token della sessione corrente non è stato rinnovato")
	}
	if rec := a.do(http.MethodGet, "/auth/session", nil, withCookie(token)); rec.Code != http.StatusUnauthorized {
		t.Errorf("il token precedente è ancora valido: status = %d", rec.Code)
	}
	if rec := a.do(http.MethodGet, "/auth/session", nil, withCookie(cookie.Value)); rec.Code != http.StatusOK {
		t.Errorf("il token nuovo non funziona: status = %d", rec.Code)
	}

	// Con la password corrente sbagliata: 401 e niente cambia.
	rec = a.do(http.MethodPost, "/auth/password/change", map[string]string{
		"current_password": "password-sbagliata-ma-lunga", "new_password": "un-altra-ancora-lunga",
	}, withCookie(cookie.Value))
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, atteso 401", rec.Code)
	}
	if rec := a.do(http.MethodPost, "/auth/login",
		map[string]string{"email": testEmail, "password": nuova}); rec.Code != http.StatusOK {
		t.Errorf("la password è cambiata nonostante il tentativo fallito: status = %d", rec.Code)
	}
}

func TestPasswordDeboleRifiutataConUnCodiceRiconoscibile(t *testing.T) {
	a := newAPI(t)

	rec := a.do(http.MethodPost, "/auth/register", map[string]string{
		"email": testEmail, "password": "corta",
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, atteso 400", rec.Code)
	}
	if code := errorCode(t, rec); code != "weak_password" {
		t.Errorf("codice = %q, atteso weak_password", code)
	}
	// Il messaggio dice qual è il minimo: senza, l'utente prova a caso.
	var body httpapi.ErrorBody
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if !strings.Contains(body.Error.Message, strconv.Itoa(auth.MinPasswordLength)) {
		t.Errorf("il messaggio non indica la lunghezza minima: %q", body.Error.Message)
	}
}

func TestAccountSospesoRifiutatoConUnCodiceRiconoscibile(t *testing.T) {
	a := newAPI(t)
	user, _ := a.registerAndLogin()
	a.store.Suspend(user.ID, time.Now())

	rec := a.do(http.MethodPost, "/auth/login", map[string]string{
		"email": testEmail, "password": testPassword,
	})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, atteso 403", rec.Code)
	}
	if code := errorCode(t, rec); code != "account_suspended" {
		t.Errorf("codice = %q, atteso account_suspended", code)
	}
}

// ----------------------------------------------------------------------- log

// SPEC §5: nessun segreto e nessun dato personale superfluo nei log. Qui si guarda
// l'output reale dell'handler slog, non un doppio.
func TestNessunSegretoNeiLogHTTP(t *testing.T) {
	a := newAPI(t)
	_, token := a.registerAndLogin()

	a.do(http.MethodPost, "/auth/login", map[string]string{
		"email": testEmail, "password": "password-sbagliata-ma-lunga",
	})
	a.do(http.MethodPost, "/auth/password/forgot", map[string]string{"email": testEmail})
	a.svc.Wait()

	msg, found := a.mailer.Last(auth.KindPasswordReset)
	if !found {
		t.Fatal("nessun token di recupero")
	}
	a.do(http.MethodPost, "/auth/password/reset",
		map[string]string{"token": msg.Token, "password": "una-password-nuova-e-lunga"})
	a.svc.Wait()

	logged := a.logs.String()
	if logged == "" {
		t.Fatal("nessun log prodotto: il test non prova niente")
	}
	for name, secret := range map[string]string{
		"la password in chiaro":  testPassword,
		"il token di sessione":   token,
		"il token di recupero":   msg.Token,
		"l'indirizzo email":      testEmail,
		"la password nuova":      "una-password-nuova-e-lunga",
		"il segreto delle firme": testSecret,
	} {
		if strings.Contains(logged, secret) {
			t.Errorf("%s compare nei log", name)
		}
	}
}

// ------------------------------------------------------------------ client IP

// Il rate limiting usa l'indirizzo del chiamante come chiave: sbagliarlo
// significa o mettere tutti nello stesso secchio (dietro un proxy) o accettare un
// valore che il client sceglie (e che quindi non limita nulla).
func TestClientIP(t *testing.T) {
	tests := []struct {
		name       string
		remoteAddr string
		forwarded  []string
		trusted    string
		want       string
	}{
		{
			name:       "senza proxy fidati si usa RemoteAddr",
			remoteAddr: "203.0.113.7:1234",
			forwarded:  []string{"1.2.3.4"},
			trusted:    "",
			want:       "203.0.113.7",
		},
		{
			name:       "la testata di un client non fidato è ignorata",
			remoteAddr: "203.0.113.7:1234",
			forwarded:  []string{"1.2.3.4"},
			trusted:    "10.0.0.0/8",
			want:       "203.0.113.7",
		},
		{
			name:       "dietro un proxy fidato si legge la testata",
			remoteAddr: "10.0.0.1:1234",
			forwarded:  []string{"203.0.113.7"},
			trusted:    "10.0.0.0/8",
			want:       "203.0.113.7",
		},
		{
			name:       "gli indirizzi a sinistra, scritti dal client, sono scartati",
			remoteAddr: "10.0.0.1:1234",
			forwarded:  []string{"1.2.3.4, 203.0.113.7"},
			trusted:    "10.0.0.0/8",
			want:       "203.0.113.7",
		},
		{
			name:       "si scartano tutti i proxy fidati in catena",
			remoteAddr: "10.0.0.1:1234",
			forwarded:  []string{"203.0.113.7, 10.0.0.5", "10.0.0.9"},
			trusted:    "10.0.0.0/8",
			want:       "203.0.113.7",
		},
		{
			name:       "un valore illeggibile interrompe la catena",
			remoteAddr: "10.0.0.1:1234",
			forwarded:  []string{"203.0.113.7, non-un-indirizzo"},
			trusted:    "10.0.0.0/8",
			want:       "10.0.0.1",
		},
		{
			name:       "una catena di soli proxy fidati ricade su RemoteAddr",
			remoteAddr: "10.0.0.1:1234",
			forwarded:  []string{"10.0.0.5"},
			trusted:    "10.0.0.0/8",
			want:       "10.0.0.1",
		},
		{
			name:       "un indirizzo singolo vale come prefisso pieno",
			remoteAddr: "127.0.0.1:1234",
			forwarded:  []string{"203.0.113.7"},
			trusted:    "127.0.0.1",
			want:       "203.0.113.7",
		},
		{
			name:       "IPv6 dietro proxy",
			remoteAddr: "[::1]:1234",
			forwarded:  []string{"2001:db8::1"},
			trusted:    "::1/128",
			want:       "2001:db8::1",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			trusted, err := httpapi.ParseTrustedProxies(tc.trusted)
			if err != nil {
				t.Fatalf("ParseTrustedProxies: %v", err)
			}
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.RemoteAddr = tc.remoteAddr
			for _, value := range tc.forwarded {
				req.Header.Add("X-Forwarded-For", value)
			}
			if got := httpapi.ClientIP(req, trusted); got.String() != tc.want {
				t.Errorf("ClientIP = %s, atteso %s", got, tc.want)
			}
		})
	}
}

func TestParseTrustedProxies(t *testing.T) {
	got, err := httpapi.ParseTrustedProxies(" 10.0.0.0/8 , 127.0.0.1 ,, ::1/128 ")
	if err != nil {
		t.Fatalf("errore: %v", err)
	}
	want := []netip.Prefix{
		netip.MustParsePrefix("10.0.0.0/8"),
		netip.MustParsePrefix("127.0.0.1/32"),
		netip.MustParsePrefix("::1/128"),
	}
	if !slices.Equal(got, want) {
		t.Errorf("got %v, atteso %v", got, want)
	}

	if _, err := httpapi.ParseTrustedProxies("non-un-indirizzo"); err == nil {
		t.Error("atteso un errore su un valore non valido")
	}
	if got, err := httpapi.ParseTrustedProxies(""); err != nil || got != nil {
		t.Errorf("stringa vuota: got %v, err %v", got, err)
	}
}

// Il limite per indirizzo IP deve valere sull'indirizzo vero: dietro un proxy, con
// RemoteAddr sempre uguale, il primo attaccante bloccherebbe il login a tutti.
func TestIlLimitePerIPUsaLIndirizzoDietroIlProxy(t *testing.T) {
	a := newAPI(t, func(_ *config.Config, o *auth.Options, d *httpapi.Deps) {
		o.Limits.LoginPerIP = ratelimit.Rule{Burst: 2, Window: 15 * time.Minute}
		o.Limits.LoginPerAccount = ratelimit.Rule{Burst: 1000, Window: 15 * time.Minute}
		trusted, err := httpapi.ParseTrustedProxies("10.0.0.0/8")
		if err != nil {
			t.Fatalf("ParseTrustedProxies: %v", err)
		}
		d.TrustedProxies = trusted
	})

	viaProxy := func(clientIP string) *httptest.ResponseRecorder {
		return a.do(http.MethodPost, "/auth/login", map[string]string{
			"email": "chiunque@example.com", "password": "password-sbagliata-ma-lunga",
		}, func(r *http.Request) {
			r.RemoteAddr = "10.0.0.1:5555"
			r.Header.Set("X-Forwarded-For", clientIP)
		})
	}

	for i := range 2 {
		if rec := viaProxy("203.0.113.7"); rec.Code != http.StatusUnauthorized {
			t.Fatalf("tentativo %d: status = %d", i, rec.Code)
		}
	}
	if rec := viaProxy("203.0.113.7"); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, atteso 429", rec.Code)
	}
	// Un altro utente dietro lo stesso proxy non è coinvolto.
	if rec := viaProxy("198.51.100.9"); rec.Code != http.StatusUnauthorized {
		t.Errorf("un altro cliente dietro lo stesso proxy è stato bloccato: status = %d", rec.Code)
	}
}

// ----------------------------------------------------------------------- CORS

// I frontend sono statici e vivono su un'altra origin (SPEC §2), e la sessione è
// un cookie: senza le testate CORS con le credenziali il browser non lo
// manderebbe. Il rovescio è che il preflight è anche la difesa contro il CSRF via
// XHR, e per questo l'origin si riflette solo se è fra quelle configurate.
func TestCORS(t *testing.T) {
	a := newAPI(t, func(c *config.Config, _ *auth.Options, _ *httpapi.Deps) {
		c.AllowedOrigins = []string{"https://app.postqron.com"}
	})

	t.Run("origin ammessa", func(t *testing.T) {
		rec := a.do(http.MethodGet, "/healthz", nil, func(r *http.Request) {
			r.Header.Set("Origin", "https://app.postqron.com")
		})
		if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "https://app.postqron.com" {
			t.Errorf("Access-Control-Allow-Origin = %q", got)
		}
		if got := rec.Header().Get("Access-Control-Allow-Credentials"); got != "true" {
			t.Errorf("Access-Control-Allow-Credentials = %q, atteso true", got)
		}
		// Senza Vary, una cache condivisa servirebbe a un'origin la risposta
		// preparata per un'altra.
		if !slices.Contains(rec.Header().Values("Vary"), "Origin") {
			t.Error("manca Vary: Origin")
		}
	})

	t.Run("origin non ammessa", func(t *testing.T) {
		rec := a.do(http.MethodGet, "/healthz", nil, func(r *http.Request) {
			r.Header.Set("Origin", "https://sito-malevolo.example")
		})
		if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
			t.Errorf("Access-Control-Allow-Origin = %q, atteso vuoto", got)
		}
		// Il carattere jolly non è mai ammesso con le credenziali: se comparisse,
		// il browser rifiuterebbe la risposta e la sessione non funzionerebbe.
		if strings.Contains(rec.Header().Get("Access-Control-Allow-Origin"), "*") {
			t.Error("il carattere jolly non è compatibile con le credenziali")
		}
	})

	t.Run("preflight", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodOptions, "/auth/login", nil)
		req.Header.Set("Origin", "https://app.postqron.com")
		req.Header.Set("Access-Control-Request-Method", "POST")
		rec := httptest.NewRecorder()
		a.handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusNoContent {
			t.Errorf("status = %d, atteso 204", rec.Code)
		}
		if !strings.Contains(rec.Header().Get("Access-Control-Allow-Methods"), "POST") {
			t.Errorf("Access-Control-Allow-Methods = %q", rec.Header().Get("Access-Control-Allow-Methods"))
		}
	})
}

// -------------------------------------------------------- router senza auth

// Senza servizio di autenticazione le rotte non esistono: è la configurazione dei
// test dell'health check, e non deve degradare in un 500 o in un accesso libero.
func TestSenzaServizioLeRotteDiAutenticazioneNonEsistono(t *testing.T) {
	cfg, err := config.LoadFrom(func(string) string { return "" })
	if err != nil {
		t.Fatalf("configurazione: %v", err)
	}
	handler := httpapi.NewRouter(cfg, "test",
		slog.New(slog.NewTextHandler(io.Discard, nil)), httpapi.Deps{})

	for _, path := range []string{"/auth/login", "/auth/register", "/auth/session"} {
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader("{}"))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s: status = %d, atteso 404", path, rec.Code)
		}
	}

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rec.Code != http.StatusOK {
		t.Errorf("healthz: status = %d", rec.Code)
	}
}

func TestIlMetodoSbagliatoNonEUn404(t *testing.T) {
	a := newAPI(t)
	for _, path := range []string{"/auth/login", "/auth/register", "/auth/password/forgot"} {
		rec := a.do(http.MethodGet, path, nil)
		if rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("GET %s: status = %d, atteso 405", path, rec.Code)
		}
	}
}
