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

	"github.com/apdsoftware/postqron/services/api/internal/auth"
	"github.com/apdsoftware/postqron/services/api/internal/config"
	"github.com/apdsoftware/postqron/services/api/internal/httpapi"
	"github.com/apdsoftware/postqron/services/api/internal/jobs"
	"github.com/apdsoftware/postqron/services/api/internal/jobstest"
	"github.com/apdsoftware/postqron/services/api/internal/ratelimit"
)

// ---------------------------------------------------------------- impalcatura

type quotaFixture struct {
	*keysFixture
	store *jobstest.Store
	clock *clockDiProva
}

// newQuotaFixture costruisce il router con i limiti di R10 agganciati
// all'orologio del test. Con `limits` a zero valgono i tetti predefiniti.
func newQuotaFixture(t *testing.T, limits httpapi.RateLimits) *quotaFixture {
	t.Helper()

	clock := &clockDiProva{now: time.Date(2026, 8, 17, 10, 0, 0, 0, time.UTC)}
	store := jobstest.NewStore()
	store.Now = clock.Now

	a := newAPI(t, func(_ *config.Config, _ *auth.Options, deps *httpapi.Deps) {
		svc, err := jobs.NewService(jobs.Options{
			Store:  store,
			Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
			Now:    clock.Now,
		})
		if err != nil {
			t.Fatalf("jobs.NewService: %v", err)
		}
		deps.Jobs = svc
		deps.RateLimits = limits
		deps.Now = clock.Now
	})

	user, token := a.registerAndLogin()
	return &quotaFixture{
		keysFixture: &keysFixture{api: a, user: user, token: token},
		store:       store,
		clock:       clock,
	}
}

func (f *quotaFixture) conSessione(method, path string, body any) *httptest.ResponseRecorder {
	f.t.Helper()
	return f.do(method, path, body, withCookie(f.token))
}

// pianoStretto è un piano la cui portata sta in poche richieste: la quota è
// derivata da `max_jobs` per `min_interval` (SPEC §8), e provarla con i venti al
// minuto veri renderebbe il test lento senza dimostrare niente di più.
func pianoStretto(t *testing.T, f *quotaFixture, maxJobs int) {
	t.Helper()
	max := maxJobs
	f.store.SetPlan(f.user.ID, jobs.Plan{
		Code: "free", Name: "Free",
		MaxJobs:      &max,
		MinInterval:  time.Minute,
		LogRetention: 3 * 24 * time.Hour,
	})
}

// ------------------------------------------------------------- tetto tecnico

// TestIlTettoTecnicoRifiutaSenzaPromettereUnUpgrade è la metà di R10 che difende
// il servizio. Il rifiuto non nomina nessun piano, e non è una dimenticanza:
// nessun piano ne concede di più, e suggerire un upgrade che non servirebbe
// sarebbe una bugia commerciale.
func TestIlTettoTecnicoRifiutaSenzaPromettereUnUpgrade(t *testing.T) {
	f := newQuotaFixture(t, httpapi.RateLimits{
		Requests: ratelimit.Rule{Burst: 2, Window: time.Minute},
	})

	for i := range 2 {
		if rec := f.conSessione(http.MethodGet, "/jobs", nil); rec.Code != http.StatusOK {
			t.Fatalf("richiesta %d: status = %d, corpo = %s", i+1, rec.Code, rec.Body)
		}
	}

	rec := f.conSessione(http.MethodGet, "/jobs", nil)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, atteso 429 (corpo: %s)", rec.Code, rec.Body)
	}

	detail := errorDetail(t, rec)
	if detail.Code != "rate_limited" {
		t.Errorf("codice = %q, atteso \"rate_limited\"", detail.Code)
	}
	if detail.Plan != "" || detail.Limit != "" {
		t.Errorf("il tetto tecnico si è travestito da limite di piano: plan = %q, limit = %q",
			detail.Plan, detail.Limit)
	}
	if strings.Contains(detail.Message, "piano superiore") {
		t.Errorf("il messaggio promette un upgrade che non servirebbe: %q", detail.Message)
	}
	if !strings.Contains(detail.Message, "tetto tecnico") {
		t.Errorf("il messaggio non dice che tipo di limite è: %q", detail.Message)
	}
	if detail.RetryAfter < 1 {
		t.Errorf("retry_after = %d, atteso almeno 1", detail.RetryAfter)
	}
	if header := rec.Header().Get("Retry-After"); header == "" {
		t.Error("manca la testata Retry-After")
	}

	// La promessa di `Retry-After` va mantenuta: passata la finestra si riprende.
	f.clock.avanza(time.Minute)
	if rec := f.conSessione(http.MethodGet, "/jobs", nil); rec.Code != http.StatusOK {
		t.Errorf("dopo la finestra: status = %d, corpo = %s", rec.Code, rec.Body)
	}
}

// TestIlTettoTecnicoNonDistingueLeChiaviVereDaQuelleInventate è la difesa
// dall'enumerazione che #396 ha costruito e che #398 non deve rompere.
//
// Il limitatore conta l'impronta della credenziale **presentata**, prima di
// sapere se esiste: una chiave vera e una fabbricata sul momento consumano lo
// stesso credito e ricevono lo stesso 429 allo stesso tentativo. La differenza
// fra 200 e 401 la fa l'autenticazione, che è il suo mestiere; il limitatore non
// aggiunge un secondo segnale da cui dedurre se una chiave esiste.
func TestIlTettoTecnicoNonDistingueLeChiaviVereDaQuelleInventate(t *testing.T) {
	f := newQuotaFixture(t, httpapi.RateLimits{
		Requests: ratelimit.Rule{Burst: 2, Window: time.Minute},
	})
	vera, _ := f.creaChiave("vera", "jobs:read")
	// Stessa forma di una chiave viva — il prefisso è ciò che il router usa per
	// riconoscerne una — ma non è mai esistita.
	inventata := "pq_live_" + strings.Repeat("a", 40)

	tentativi := func(secret string) (codici []int, ultimo *httptest.ResponseRecorder) {
		for range 3 {
			rec := f.do(http.MethodGet, "/jobs", nil, withKey(secret))
			codici = append(codici, rec.Code)
			ultimo = rec
		}
		return codici, ultimo
	}

	codiciVeri, rifiutoVero := tentativi(vera)
	codiciFinti, rifiutoFinto := tentativi(inventata)

	if codiciVeri[2] != http.StatusTooManyRequests || codiciFinti[2] != http.StatusTooManyRequests {
		t.Fatalf("il tetto non è scattato allo stesso tentativo: vera = %v, inventata = %v",
			codiciVeri, codiciFinti)
	}
	if rifiutoVero.Body.String() != rifiutoFinto.Body.String() {
		t.Errorf("i due rifiuti si distinguono:\nvera:      %s\ninventata: %s",
			rifiutoVero.Body, rifiutoFinto.Body)
	}
	if rifiutoVero.Header().Get("Retry-After") != rifiutoFinto.Header().Get("Retry-After") {
		t.Error("le due attese dichiarate si distinguono")
	}
}

// ---------------------------------------------------------- quota di piano

// TestLaQuotaDiScritturaVieneDalPiano è l'altra metà di R10: la capacità che
// l'utente ha comprato, applicata lato server (R15).
func TestLaQuotaDiScritturaVieneDalPiano(t *testing.T) {
	f := newQuotaFixture(t, httpapi.RateLimits{})
	// Due job al minuto di portata, e un catalogo che ne accetta due: il job
	// creato qui sotto resta l'unico, così che ciò che ferma le scritture
	// successive possa essere solo la quota e non il tetto al catalogo.
	pianoStretto(t, f, 2)

	creato := f.creaJobConSessione("primo")
	if rec := f.conSessione(http.MethodPatch, "/jobs/"+creato, map[string]any{
		"name": "rinominato",
	}); rec.Code != http.StatusOK {
		t.Fatalf("seconda scrittura: status = %d, corpo = %s", rec.Code, rec.Body)
	}

	rec := f.conSessione(http.MethodPatch, "/jobs/"+creato, map[string]any{"name": "terzo-nome"})
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, atteso 429 (corpo: %s)", rec.Code, rec.Body)
	}

	detail := errorDetail(t, rec)
	if detail.Code != "plan_limit_write_rate" || detail.Limit != "write_rate" {
		t.Errorf("codice = %q, limite = %q", detail.Code, detail.Limit)
	}
	if detail.Plan != "free" {
		t.Errorf("piano = %q, atteso \"free\"", detail.Plan)
	}
	if detail.RetryAfter < 1 || rec.Header().Get("Retry-After") == "" {
		t.Errorf("attesa dichiarata assente: retry_after = %d, testata = %q",
			detail.RetryAfter, rec.Header().Get("Retry-After"))
	}
	// Il messaggio deve dire quale piano concede quanto: è l'unico punto in cui
	// un limite tecnico è anche un'informazione commerciale, e un 429 muto
	// costringerebbe l'utente a indovinare se ha sbagliato o se deve pagare.
	for _, atteso := range []string{"Free", "2", "scritture", "piano superiore"} {
		if !strings.Contains(detail.Message, atteso) {
			t.Errorf("il messaggio non contiene %q: %q", atteso, detail.Message)
		}
	}

	// Il rifiuto è arrivato **prima** del lavoro: il job non è stato toccato.
	rimasti := f.store.Jobs()
	if len(rimasti) != 1 {
		t.Fatalf("job in archivio = %d, atteso 1", len(rimasti))
	}
	if rimasti[0].Name != "rinominato" {
		t.Errorf("il gestore è stato eseguito comunque: nome = %q", rimasti[0].Name)
	}
}

// TestLeLettureNonConsumanoLaQuotaDelPiano: un rate limit è una difesa del
// servizio, non una leva commerciale. Far pagare le letture alla portata del
// piano renderebbe la dashboard inutilizzabile sul piano Free, cioè punirebbe
// l'uso normale per proteggersi dall'abuso.
func TestLeLettureNonConsumanoLaQuotaDelPiano(t *testing.T) {
	f := newQuotaFixture(t, httpapi.RateLimits{})
	pianoStretto(t, f, 1)

	creato := f.creaJobConSessione("primo")
	if rec := f.conSessione(http.MethodPatch, "/jobs/"+creato, map[string]any{
		"name": "rinominato",
	}); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("la quota di scrittura non è scattata: status = %d", rec.Code)
	}

	for _, path := range []string{"/jobs", "/jobs/" + creato, "/jobs/" + creato + "/executions"} {
		if rec := f.conSessione(http.MethodGet, path, nil); rec.Code != http.StatusOK {
			t.Errorf("GET %s = %d, atteso 200 (corpo: %s)", path, rec.Code, rec.Body)
		}
	}
}

// TestIlTriggerNonPagaDueVolte: il trigger manuale ha già il proprio tetto —
// quello di #395, con la stessa portata e in più la casella per singolo job —
// e contarlo anche fra le scritture gli farebbe consumare due contatori per la
// stessa operazione, con un messaggio che nomina il numero sbagliato.
func TestIlTriggerNonPagaDueVolte(t *testing.T) {
	f := newQuotaFixture(t, httpapi.RateLimits{})
	pianoStretto(t, f, 1)

	creato := f.creaJobConSessione("primo")
	if rec := f.conSessione(http.MethodPatch, "/jobs/"+creato, map[string]any{
		"name": "rinominato",
	}); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("la quota di scrittura non è scattata: status = %d", rec.Code)
	}

	rec := f.conSessione(http.MethodPost, "/jobs/"+creato+"/executions", nil)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("trigger = %d, atteso 202: il trigger sta pagando la quota delle scritture (corpo: %s)",
			rec.Code, rec.Body)
	}
}

// TestLaQuotaDiPianoSiRiempie: la portata è una finestra, non un tetto
// definitivo. Senza questo, `Retry-After` sarebbe una promessa non mantenuta.
func TestLaQuotaDiPianoSiRiempie(t *testing.T) {
	f := newQuotaFixture(t, httpapi.RateLimits{})
	pianoStretto(t, f, 1)

	creato := f.creaJobConSessione("primo")
	if rec := f.conSessione(http.MethodPatch, "/jobs/"+creato, map[string]any{
		"name": "rinominato",
	}); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("la quota di scrittura non è scattata: status = %d", rec.Code)
	}

	f.clock.avanza(time.Minute)
	if rec := f.conSessione(http.MethodPatch, "/jobs/"+creato, map[string]any{
		"name": "rinominato-davvero",
	}); rec.Code != http.StatusOK {
		t.Errorf("dopo la finestra: status = %d, corpo = %s", rec.Code, rec.Body)
	}
}

// creaJobConSessione crea un job e restituisce il suo identificativo.
func (f *quotaFixture) creaJobConSessione(nome string) string {
	f.t.Helper()
	rec := f.conSessione(http.MethodPost, "/jobs", jobPayload(nome))
	if rec.Code != http.StatusCreated {
		f.t.Fatalf("creazione: status = %d, corpo = %s", rec.Code, rec.Body)
	}
	var body httpapi.JobResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		f.t.Fatalf("corpo della creazione: %v", err)
	}
	return body.ID
}
