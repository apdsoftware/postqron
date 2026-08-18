package httpapi_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/apdsoftware/postqron/services/api/internal/auth"
	"github.com/apdsoftware/postqron/services/api/internal/config"
	"github.com/apdsoftware/postqron/services/api/internal/emailrender"
	"github.com/apdsoftware/postqron/services/api/internal/httpapi"
	"github.com/apdsoftware/postqron/services/api/internal/marketing"
)

// ---------------------------------------------------------------- impalcatura

type marketingFixture struct {
	*api
	store  *marketing.MemoryStore
	svc    *marketing.Service
	user   auth.User
	token  string
	userID string
}

func newMarketingFixture(t *testing.T) *marketingFixture {
	t.Helper()

	store := marketing.NewMemoryStore()
	signer, err := marketing.NewSigner(testSecret)
	if err != nil {
		t.Fatalf("NewSigner: %v", err)
	}
	svc, err := marketing.NewService(marketing.Options{
		Store: store, Signer: signer, APIBaseURL: "https://api.postqron.test",
	})
	if err != nil {
		t.Fatalf("marketing.NewService: %v", err)
	}

	site := emailrender.Site{
		ProductName:   "Postqron",
		PublicBaseURL: "https://postqron.test",
		AppBaseURL:    "https://app.postqron.test",
		SupportEmail:  "support@postqron.test",
	}
	dir, err := emailrender.FindDir(".")
	if err != nil {
		t.Fatalf("FindDir: %v", err)
	}
	renderer, err := emailrender.NewFromDir(dir, site)
	if err != nil {
		t.Fatalf("NewFromDir: %v", err)
	}
	page, err := marketing.NewPage(renderer, site)
	if err != nil {
		t.Fatalf("NewPage: %v", err)
	}

	a := newAPI(t, func(_ *config.Config, _ *auth.Options, deps *httpapi.Deps) {
		deps.Marketing = svc
		deps.MarketingPage = page
	})
	user, token := a.registerAndLogin()
	store.WithUser(marketing.Recipient{
		UserID: user.ID, Email: user.Email, Name: "Mario", Language: "en",
	})

	return &marketingFixture{api: a, store: store, svc: svc, user: user, token: token, userID: user.ID}
}

func (f *marketingFixture) state(t *testing.T) marketing.State {
	t.Helper()
	state, err := f.svc.Status(context.Background(), f.userID)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	return state
}

func (f *marketingFixture) unsubscribeToken(t *testing.T) string {
	t.Helper()
	link := f.svc.UnsubscribeURL(f.userID)
	_, token, found := strings.Cut(link, "token=")
	if !found {
		t.Fatalf("il link non porta un token: %q", link)
	}
	decoded, err := url.QueryUnescape(token)
	if err != nil {
		t.Fatalf("token non decodificabile: %v", err)
	}
	return decoded
}

// form esegue una richiesta con corpo `application/x-www-form-urlencoded`.
//
// È l'unica forma che la disiscrizione accetta, perché la manda il form della
// pagina precedente — che deve funzionare senza JavaScript. L'aiutante generale
// [api.do] manda JSON e qui non servirebbe.
func (f *marketingFixture) form(path string, values url.Values, prepare ...func(*http.Request)) *httptest.ResponseRecorder {
	f.t.Helper()

	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(values.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.RemoteAddr = "203.0.113.7:54321"
	for _, fn := range prepare {
		fn(req)
	}

	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, req)
	return rec
}

// ------------------------------------------------------------------ consenso

// Il consenso si presta e si ritira, e la risposta dice sempre lo stato.
func TestIlConsensoSiPrestaESiRitira(t *testing.T) {
	f := newMarketingFixture(t)

	// Chi non ha mai deciso: né consenso né decisione, e nessuna data.
	rec := f.do(http.MethodGet, "/marketing/consent", nil, withCookie(f.token))
	body := decodeConsent(t, rec)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, atteso 200", rec.Code)
	}
	if body.Consented || body.Decided || body.DecidedAt != nil {
		t.Errorf("chi non ha mai scelto risulta %+v: l'assenza di una decisione è un rifiuto", body)
	}

	rec = f.do(http.MethodPost, "/marketing/consent", nil, withCookie(f.token))
	body = decodeConsent(t, rec)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, atteso 200", rec.Code)
	}
	if !body.Consented || !body.Decided {
		t.Errorf("dopo il consenso lo stato è %+v", body)
	}
	if body.DecidedAt == nil || body.DecidedAt.IsZero() {
		t.Error("il consenso è senza data: è la data che il documento promette di conservare")
	}

	rec = f.do(http.MethodDelete, "/marketing/consent", nil, withCookie(f.token))
	body = decodeConsent(t, rec)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, atteso 200", rec.Code)
	}
	if body.Consented {
		t.Error("dopo la revoca il consenso risulta ancora prestato")
	}
	if !body.Decided {
		t.Error("dopo la revoca la decisione risulta mai presa: sono due cose diverse")
	}
}

// Il consenso **non ha un corpo**, e non c'è nessun campo da mandare.
//
// §2.8 dice che il consenso è chiesto per conto proprio. Un corpo sarebbe il
// posto in cui, un giorno, qualcuno infilerebbe un secondo campo: qui si
// verifica che la rotta non ne legga uno, così che quel giorno non arrivi per
// inerzia.
func TestIlConsensoNonViaggiaInsiemeANientAltro(t *testing.T) {
	f := newMarketingFixture(t)

	rec := f.do(http.MethodPost, "/marketing/consent",
		map[string]any{"terms_accepted": true}, withCookie(f.token))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, atteso 200", rec.Code)
	}

	// Il corpo è stato ignorato: ciò che è cambiato è solo il consenso, e la
	// risposta non riporta niente di quello che è stato mandato.
	if strings.Contains(rec.Body.String(), "terms") {
		t.Error("la risposta al consenso parla di qualcosa che non è il consenso")
	}
}

// Le rotte del consenso esigono una sessione.
//
// Una chiave API dimenticata in un file di configurazione non deve poter
// prestare, a nome del suo proprietario, un consenso che vale come base
// giuridica.
func TestIlConsensoEsigeUnaSessione(t *testing.T) {
	f := newMarketingFixture(t)

	for _, metodo := range []string{http.MethodGet, http.MethodPost, http.MethodDelete} {
		t.Run(metodo, func(t *testing.T) {
			if rec := f.do(metodo, "/marketing/consent", nil); rec.Code != http.StatusUnauthorized {
				t.Errorf("senza credenziali lo status è %d, atteso 401", rec.Code)
			}
		})
	}
}

// ------------------------------------------------------------ disiscrizione

// La disiscrizione funziona **senza sessione**: nessun cookie, nessuna chiave.
func TestLaDisiscrizioneFunzionaSenzaAccedere(t *testing.T) {
	f := newMarketingFixture(t)

	if rec := f.do(http.MethodPost, "/marketing/consent", nil, withCookie(f.token)); rec.Code != http.StatusOK {
		t.Fatalf("consenso non prestato: %d", rec.Code)
	}
	token := f.unsubscribeToken(t)

	// Nessuna credenziale su nessuna delle due richieste.
	rec := f.do(http.MethodGet, "/marketing/unsubscribe?token="+url.QueryEscape(token), nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("la pagina di conferma risponde %d senza sessione, atteso 200", rec.Code)
	}
	if tipo := rec.Header().Get("Content-Type"); !strings.HasPrefix(tipo, "text/html") {
		t.Errorf("la pagina risponde %q: la apre un client di posta, non il nostro frontend", tipo)
	}
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Error("la pagina di conferma è memorizzabile in cache, e porta il token nel form")
	}

	rec = f.form("/marketing/unsubscribe", url.Values{"token": {token}})
	if rec.Code != http.StatusOK {
		t.Fatalf("la disiscrizione risponde %d senza sessione, atteso 200", rec.Code)
	}

	if state := f.state(t); state.Consented {
		t.Error("dopo la disiscrizione il consenso risulta ancora prestato")
	}
}

// **Un `GET` non disiscrive nessuno.**
//
// È il controllo più importante di questo file. L'indirizzo arriva dentro
// un'email, e le email le aprono anche gli scanner antivirus dei server di posta
// aziendali, i prefetch del browser e i crawler: se leggerlo bastasse a
// disiscrivere, quelle persone smetterebbero di ricevere le comunicazioni senza
// aver mai cliccato, e per R20.1 non avremmo modo di accorgercene.
func TestUnGetNonDisiscriveNessuno(t *testing.T) {
	f := newMarketingFixture(t)

	if rec := f.do(http.MethodPost, "/marketing/consent", nil, withCookie(f.token)); rec.Code != http.StatusOK {
		t.Fatalf("consenso non prestato: %d", rec.Code)
	}
	token := f.unsubscribeToken(t)
	indirizzo := "/marketing/unsubscribe?token=" + url.QueryEscape(token)

	// Venti letture, come venti scanner che aprono lo stesso messaggio.
	for range 20 {
		if rec := f.do(http.MethodGet, indirizzo, nil); rec.Code != http.StatusOK {
			t.Fatalf("la lettura risponde %d", rec.Code)
		}
	}

	if state := f.state(t); !state.Consented {
		t.Fatal("venti letture hanno revocato il consenso: un GET disiscrive")
	}
	history, err := f.svc.History(context.Background(), f.userID)
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	if len(history) != 1 {
		t.Errorf("le letture hanno scritto nella traccia: %d righe, attesa 1 (il solo consenso)", len(history))
	}
}

// La disiscrizione è idempotente: il secondo clic non è una seconda decisione.
func TestLaDisiscrizioneRipetutaNonEUnaSecondaDecisione(t *testing.T) {
	f := newMarketingFixture(t)

	if rec := f.do(http.MethodPost, "/marketing/consent", nil, withCookie(f.token)); rec.Code != http.StatusOK {
		t.Fatalf("consenso non prestato: %d", rec.Code)
	}
	token := f.unsubscribeToken(t)

	for range 3 {
		if rec := f.form("/marketing/unsubscribe", url.Values{"token": {token}}); rec.Code != http.StatusOK {
			t.Fatalf("la disiscrizione risponde %d", rec.Code)
		}
	}

	history, err := f.svc.History(context.Background(), f.userID)
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	// Consenso più una revoca: le altre due non hanno cambiato niente.
	if len(history) != 2 {
		t.Errorf("tre disiscrizioni hanno lasciato %d righe nella traccia, attese 2", len(history))
	}
}

// Un token che non si verifica non disiscrive nessuno, e non dice perché.
func TestUnTokenNonValidoNonDisiscriveNessuno(t *testing.T) {
	f := newMarketingFixture(t)

	if rec := f.do(http.MethodPost, "/marketing/consent", nil, withCookie(f.token)); rec.Code != http.StatusOK {
		t.Fatalf("consenso non prestato: %d", rec.Code)
	}
	valido := f.unsubscribeToken(t)
	_, firma, _ := strings.Cut(valido, ".")

	casi := map[string]string{
		"assente":          "",
		"senza firma":      f.userID,
		"firma alterata":   f.userID + "." + strings.Repeat("0", len(firma)),
		"utente scambiato": "00000000-0000-0000-0000-000000000000." + firma,
	}

	for nome, token := range casi {
		t.Run(nome, func(t *testing.T) {
			rec := f.do(http.MethodGet, "/marketing/unsubscribe?token="+url.QueryEscape(token), nil)
			if rec.Code != http.StatusNotFound {
				t.Errorf("la lettura di un token non valido risponde %d, atteso 404", rec.Code)
			}
			rec = f.form("/marketing/unsubscribe", url.Values{"token": {token}})
			if rec.Code != http.StatusNotFound {
				t.Errorf("la disiscrizione con un token non valido risponde %d, atteso 404", rec.Code)
			}
		})
	}

	if state := f.state(t); !state.Consented {
		t.Error("un token non valido ha revocato il consenso")
	}
}

// La pagina non rivela l'indirizzo email.
//
// La apre chiunque abbia il link: mostrare l'indirizzo trasformerebbe una revoca
// in un modo di scoprire a chi appartiene.
func TestLaPaginaNonRivelaLIndirizzo(t *testing.T) {
	f := newMarketingFixture(t)

	if rec := f.do(http.MethodPost, "/marketing/consent", nil, withCookie(f.token)); rec.Code != http.StatusOK {
		t.Fatalf("consenso non prestato: %d", rec.Code)
	}
	token := f.unsubscribeToken(t)

	rec := f.do(http.MethodGet, "/marketing/unsubscribe?token="+url.QueryEscape(token), nil)
	if strings.Contains(rec.Body.String(), f.user.Email) {
		t.Error("la pagina di conferma mostra l'indirizzo email del destinatario")
	}

	rec = f.form("/marketing/unsubscribe", url.Values{"token": {token}})
	if strings.Contains(rec.Body.String(), f.user.Email) {
		t.Error("la pagina di conferma avvenuta mostra l'indirizzo email del destinatario")
	}
}

// ------------------------------------------------------------------ supporto

func decodeConsent(t *testing.T, rec *httptest.ResponseRecorder) httpapi.MarketingConsentResponse {
	t.Helper()
	var body httpapi.MarketingConsentResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("risposta illeggibile (%s): %v", rec.Body.String(), err)
	}
	return body
}
