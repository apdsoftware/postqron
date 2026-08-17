package httpapi_test

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/apdsoftware/postqron/services/api/internal/auth"
	"github.com/apdsoftware/postqron/services/api/internal/config"
	"github.com/apdsoftware/postqron/services/api/internal/httpapi"
	"github.com/apdsoftware/postqron/services/api/internal/jobs"
	"github.com/apdsoftware/postqron/services/api/internal/jobstest"
)

// ---------------------------------------------------------------- impalcatura

// clockDiProva è un orologio che i test fanno avanzare a mano: il tetto al
// trigger manuale è una griglia temporale, e aspettarla davvero renderebbe la
// suite lenta.
type clockDiProva struct {
	mu  sync.Mutex
	now time.Time
}

func (c *clockDiProva) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *clockDiProva) avanza(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

type jobsFixture struct {
	*api
	store *jobstest.Store
	clock *clockDiProva
	user  auth.User
	token string
}

// newJobsFixture costruisce il router con autenticazione e job agganciati ad
// archivi in memoria, e ci apre già una sessione.
func newJobsFixture(t *testing.T) *jobsFixture {
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
	})

	user, token := a.registerAndLogin()
	return &jobsFixture{api: a, store: store, clock: clock, user: user, token: token}
}

// call esegue una richiesta autenticata.
func (f *jobsFixture) call(method, path string, body any) *httptest.ResponseRecorder {
	f.t.Helper()
	return f.do(method, path, body, withCookie(f.token))
}

// corpoValido è un job che l'API accetta sul piano Free.
func corpoValido() map[string]any {
	return map[string]any{
		"name":     "daily-digest",
		"schedule": "0 9 * * *",
		"timezone": "Europe/Rome",
		"request": map[string]any{
			"url":    "https://api.example.com/tasks/digest",
			"method": "POST",
		},
	}
}

func (f *jobsFixture) creaJob(body map[string]any) httpapi.JobResponse {
	f.t.Helper()
	rec := f.call(http.MethodPost, "/jobs", body)
	if rec.Code != http.StatusCreated {
		f.t.Fatalf("creazione: status = %d, corpo = %s", rec.Code, rec.Body)
	}
	var job httpapi.JobResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &job); err != nil {
		f.t.Fatalf("corpo della creazione: %v", err)
	}
	return job
}

// errorDetail legge l'intero dettaglio dell'errore, non solo il codice.
func errorDetail(t *testing.T, rec *httptest.ResponseRecorder) httpapi.ErrorDetail {
	t.Helper()
	var body httpapi.ErrorBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("corpo non decodificabile (%q): %v", rec.Body.String(), err)
	}
	return body.Error
}

// fieldCodes indicizza i motivi di rifiuto per campo.
func fieldCodes(t *testing.T, rec *httptest.ResponseRecorder) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, field := range errorDetail(t, rec).Details {
		out[field.Field] = field.Code
	}
	return out
}

// ------------------------------------------------------------ autenticazione

func TestLeRotteDeiJobEsigonoUnaSessione(t *testing.T) {
	f := newJobsFixture(t)
	job := f.creaJob(corpoValido())

	rotte := []struct {
		method string
		path   string
		body   any
	}{
		{http.MethodGet, "/jobs", nil},
		{http.MethodPost, "/jobs", corpoValido()},
		{http.MethodGet, "/jobs/" + job.ID, nil},
		{http.MethodPatch, "/jobs/" + job.ID, map[string]any{"enabled": false}},
		{http.MethodDelete, "/jobs/" + job.ID, nil},
		{http.MethodGet, "/jobs/" + job.ID + "/executions", nil},
		{http.MethodPost, "/jobs/" + job.ID + "/executions", nil},
	}

	for _, rotta := range rotte {
		// Senza cookie: nessuna sessione.
		rec := f.do(rotta.method, rotta.path, rotta.body)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s %s: status = %d, atteso 401", rotta.method, rotta.path, rec.Code)
		}
		if code := errorCode(t, rec); code != "unauthenticated" {
			t.Errorf("%s %s: codice = %q, atteso \"unauthenticated\"", rotta.method, rotta.path, code)
		}
	}
}

// TestIJobDiUnAltroUtenteNonEsistono: la differenza fra «non c'è» e «non è tuo»
// direbbe a chiunque se un identificativo altrui è vivo.
func TestIJobDiUnAltroUtenteNonEsistono(t *testing.T) {
	f := newJobsFixture(t)
	job := f.creaJob(corpoValido())

	// Un job registrato a nome di un altro utente non compare né si legge.
	f.store.Seed(jobs.Job{
		UserID: "un-altro-utente", Name: "altrui",
		Schedule: "0 9 * * *", Timezone: "UTC",
		Environments: []jobs.Environment{jobs.EnvironmentProduction},
		URL:          "https://example.com/", Method: jobs.MethodPOST,
		Timeout: 30 * time.Second, RetryBackoff: jobs.BackoffExponential, Enabled: true,
	})

	rec := f.call(http.MethodGet, "/jobs", nil)
	var page httpapi.JobListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil {
		t.Fatalf("corpo dell'elenco: %v", err)
	}
	if len(page.Jobs) != 1 || page.Jobs[0].ID != job.ID {
		t.Fatalf("l'elenco contiene job di altri utenti: %+v", page.Jobs)
	}
}

// ------------------------------------------------------------------ contratto

func TestContrattoDelJob(t *testing.T) {
	f := newJobsFixture(t)

	body := corpoValido()
	body["description"] = "Digest quotidiano"
	body["timeout"] = "45s"
	body["retries"] = map[string]any{"max": 5, "backoff": "linear"}
	body["alerts"] = map[string]any{"on_failure": []string{"email"}}
	body["request"] = map[string]any{
		"url":     "https://api.example.com/tasks/digest",
		"method":  "POST",
		"headers": map[string]string{"Authorization": "Bearer ${DIGEST_TOKEN}"},
		"body":    `{"kind":"daily"}`,
	}

	rec := f.call(http.MethodPost, "/jobs", body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, corpo = %s", rec.Code, rec.Body)
	}
	if location := rec.Header().Get("Location"); !strings.HasPrefix(location, "/jobs/") {
		t.Errorf("Location = %q", location)
	}

	var job httpapi.JobResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &job); err != nil {
		t.Fatalf("corpo: %v", err)
	}

	if job.Schedule == nil || *job.Schedule != "0 9 * * *" {
		t.Errorf("schedule = %v", job.Schedule)
	}
	// L'altra modalità è esplicitamente nulla, non assente: il client non deve
	// distinguere «campo mancante» da «modalità non in uso».
	if job.Every != nil {
		t.Errorf("every = %v, atteso null su un job cron", *job.Every)
	}
	// Le durate sono stringhe nella forma di `cron.yaml`, così che ciò che si
	// legge si possa rimandare indietro senza conversioni.
	if job.Timeout != "45s" {
		t.Errorf("timeout = %q, atteso \"45s\"", job.Timeout)
	}
	if job.Retries.Max != 5 || job.Retries.Backoff != "linear" {
		t.Errorf("retries = %+v", job.Retries)
	}
	if job.Request.Headers["Authorization"] != "Bearer ${DIGEST_TOKEN}" {
		t.Errorf("headers = %+v: i riferimenti ai segreti restano non risolti a riposo", job.Request.Headers)
	}
	if job.Request.Body == nil || *job.Request.Body != `{"kind":"daily"}` {
		t.Errorf("body = %v", job.Request.Body)
	}
	// `next_run_at` nasce nulla: la colonna è dello scheduler, anche il primo
	// valore (migrazione 0010). Il client la vede comparire alla prima passata
	// del motore, ed è il campo che gli dice quando aspettarsi l'esecuzione.
	if job.NextRunAt != nil {
		t.Errorf("next_run_at = %s alla creazione: la calcola lo scheduler", job.NextRunAt)
	}
	// I default della 0005 compaiono nella risposta invece di essere sostituiti in
	// silenzio dal database.
	if len(job.Environments) != 1 || job.Environments[0] != "production" {
		t.Errorf("environments = %v", job.Environments)
	}
	if !job.Enabled {
		t.Error("il job è nato in pausa")
	}
}

func TestModalitaAIntervalloNelContratto(t *testing.T) {
	f := newJobsFixture(t)
	f.store.SetPlan(f.user.ID, jobs.Plan{
		Code: "pro", Name: "Pro", MinInterval: 10 * time.Second, EnvironmentsEnabled: true,
	})

	body := corpoValido()
	delete(body, "schedule")
	body["every"] = "10s"

	job := f.creaJob(body)
	if job.Schedule != nil {
		t.Errorf("schedule = %v, atteso null su un job a intervallo", *job.Schedule)
	}
	if job.Every == nil || *job.Every != "10s" {
		t.Errorf("every = %v, atteso \"10s\"", job.Every)
	}
}

// ------------------------------------------------------------------- rifiuti

func TestCreazioneRifiutata(t *testing.T) {
	cases := []struct {
		nome   string
		tune   func(map[string]any)
		status int
		code   string
		campi  map[string]string
	}{
		{
			nome:   "senza schedulazione",
			tune:   func(b map[string]any) { delete(b, "schedule") },
			status: http.StatusBadRequest, code: "validation_failed",
			campi: map[string]string{"schedule": "schedule_required"},
		},
		{
			// Il vincolo XOR del database, applicato prima della query: se
			// arrivasse fin là sarebbe un 500.
			nome:   "entrambe le modalità",
			tune:   func(b map[string]any) { b["every"] = "30s" },
			status: http.StatusBadRequest, code: "validation_failed",
			campi: map[string]string{"schedule": "schedule_conflict"},
		},
		{
			nome:   "espressione cron non valida",
			tune:   func(b map[string]any) { b["schedule"] = "0 99 * * *" },
			status: http.StatusBadRequest, code: "validation_failed",
			campi: map[string]string{"schedule": "invalid_schedule"},
		},
		{
			nome:   "fuso sconosciuto",
			tune:   func(b map[string]any) { b["timezone"] = "Europe/Atlantide" },
			status: http.StatusBadRequest, code: "validation_failed",
			campi: map[string]string{"timezone": "invalid_schedule"},
		},
		{
			nome:   "nome non valido",
			tune:   func(b map[string]any) { b["name"] = "daily digest" },
			status: http.StatusBadRequest, code: "validation_failed",
			campi: map[string]string{"name": "invalid_format"},
		},
		{
			nome: "schema non HTTP",
			tune: func(b map[string]any) {
				b["request"] = map[string]any{"url": "file:///etc/passwd"}
			},
			status: http.StatusBadRequest, code: "validation_failed",
			campi: map[string]string{"request.url": "unsupported_scheme"},
		},
		{
			nome: "header riservato",
			tune: func(b map[string]any) {
				b["request"] = map[string]any{
					"url":     "https://example.com/",
					"headers": map[string]string{"Host": "interno"},
				}
			},
			status: http.StatusBadRequest, code: "validation_failed",
			campi: map[string]string{"request.headers": "reserved_name"},
		},
		{
			nome:   "timeout oltre il tetto",
			tune:   func(b map[string]any) { b["timeout"] = "10m" },
			status: http.StatusBadRequest, code: "validation_failed",
			campi: map[string]string{"timeout": "out_of_range"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.nome, func(t *testing.T) {
			f := newJobsFixture(t)
			body := corpoValido()
			tc.tune(body)

			rec := f.call(http.MethodPost, "/jobs", body)
			if rec.Code != tc.status {
				t.Fatalf("status = %d, atteso %d (corpo: %s)", rec.Code, tc.status, rec.Body)
			}
			if code := errorCode(t, rec); code != tc.code {
				t.Fatalf("codice = %q, atteso %q", code, tc.code)
			}
			got := fieldCodes(t, rec)
			for campo, code := range tc.campi {
				if got[campo] != code {
					t.Errorf("campo %q: codice = %q, atteso %q (dettagli: %v)", campo, got[campo], code, got)
				}
			}
			if len(f.store.Jobs()) != 0 {
				t.Error("una richiesta rifiutata ha comunque scritto un job")
			}
		})
	}
}

// TestUnCampoScrittoMaleVieneRifiutato: `requst` accettato in silenzio
// significherebbe un job creato con i default al posto di ciò che l'utente
// intendeva, e nessun modo di accorgersene.
func TestUnCampoScrittoMaleVieneRifiutato(t *testing.T) {
	f := newJobsFixture(t)

	t.Run("al primo livello", func(t *testing.T) {
		body := corpoValido()
		body["timezon"] = "Europe/Rome"
		rec := f.call(http.MethodPost, "/jobs", body)
		if rec.Code != http.StatusBadRequest || errorCode(t, rec) != "invalid_request" {
			t.Fatalf("status = %d, codice = %q", rec.Code, errorCode(t, rec))
		}
	})

	t.Run("dentro un oggetto annidato", func(t *testing.T) {
		body := corpoValido()
		body["request"] = map[string]any{"url": "https://example.com/", "metod": "GET"}
		rec := f.call(http.MethodPost, "/jobs", body)
		if rec.Code != http.StatusBadRequest || errorCode(t, rec) != "invalid_request" {
			t.Fatalf("status = %d, codice = %q", rec.Code, errorCode(t, rec))
		}
	})
}

func TestDurataScrittaMale(t *testing.T) {
	f := newJobsFixture(t)

	body := corpoValido()
	body["timeout"] = 30 // un numero, non una durata
	rec := f.call(http.MethodPost, "/jobs", body)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, atteso 400 (corpo: %s)", rec.Code, rec.Body)
	}
	// Il messaggio deve dire come si scrive, non solo che è sbagliato.
	if msg := errorDetail(t, rec).Message; !strings.Contains(msg, "10s") {
		t.Errorf("il messaggio non mostra la forma attesa: %s", msg)
	}
}

func TestNomeGiaUsato(t *testing.T) {
	f := newJobsFixture(t)
	f.creaJob(corpoValido())

	rec := f.call(http.MethodPost, "/jobs", corpoValido())
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, atteso 409 (corpo: %s)", rec.Code, rec.Body)
	}
	if code := errorCode(t, rec); code != "job_name_taken" {
		t.Errorf("codice = %q", code)
	}
}

// ------------------------------------------------------------- limiti di piano

// TestEveryUnSecondoSuFreeRifiutatoDallAPI è il caso di SPEC §9 visto dal
// client: un codice su cui fare branching, il piano, e il campo responsabile.
func TestEveryUnSecondoSuFreeRifiutatoDallAPI(t *testing.T) {
	f := newJobsFixture(t)

	body := corpoValido()
	delete(body, "schedule")
	body["every"] = "1s"

	rec := f.call(http.MethodPost, "/jobs", body)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, atteso 403 (corpo: %s)", rec.Code, rec.Body)
	}

	detail := errorDetail(t, rec)
	if detail.Code != "plan_limit_resolution" {
		t.Errorf("codice = %q, atteso \"plan_limit_resolution\"", detail.Code)
	}
	if detail.Limit != "resolution" || detail.Plan != "free" {
		t.Errorf("limite = %q, piano = %q", detail.Limit, detail.Plan)
	}
	if len(detail.Details) != 1 || detail.Details[0].Field != "every" {
		t.Errorf("dettagli = %+v, atteso il campo `every`", detail.Details)
	}
	if len(f.store.Jobs()) != 0 {
		t.Error("il job è stato creato: il limite è stato degradato invece che applicato")
	}
}

func TestTettoAiJobDallAPI(t *testing.T) {
	f := newJobsFixture(t)
	max := 1
	f.store.SetPlan(f.user.ID, jobs.Plan{
		Code: "free", Name: "Free", MaxJobs: &max, MinInterval: time.Minute,
	})
	f.creaJob(corpoValido())

	body := corpoValido()
	body["name"] = "secondo"
	rec := f.call(http.MethodPost, "/jobs", body)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, atteso 403 (corpo: %s)", rec.Code, rec.Body)
	}
	detail := errorDetail(t, rec)
	if detail.Code != "plan_limit_jobs" || detail.Limit != "jobs" {
		t.Errorf("codice = %q, limite = %q", detail.Code, detail.Limit)
	}
}

func TestAmbienteStagingRifiutatoSuFree(t *testing.T) {
	f := newJobsFixture(t)

	body := corpoValido()
	body["environments"] = []string{"staging"}
	rec := f.call(http.MethodPost, "/jobs", body)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, atteso 403 (corpo: %s)", rec.Code, rec.Body)
	}
	if code := errorCode(t, rec); code != "plan_limit_environments" {
		t.Errorf("codice = %q", code)
	}
}

// -------------------------------------------------------------- lettura e modifica

func TestLetturaEModifica(t *testing.T) {
	f := newJobsFixture(t)
	created := f.creaJob(corpoValido())

	rec := f.call(http.MethodGet, "/jobs/"+created.ID, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("lettura: status = %d", rec.Code)
	}

	rec = f.call(http.MethodPatch, "/jobs/"+created.ID, map[string]any{
		"description": "aggiornata",
		"retries":     map[string]any{"max": 1},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("modifica: status = %d, corpo = %s", rec.Code, rec.Body)
	}

	var updated httpapi.JobResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &updated); err != nil {
		t.Fatalf("corpo: %v", err)
	}
	if updated.Description != "aggiornata" || updated.Retries.Max != 1 {
		t.Errorf("modifica non applicata: %+v", updated)
	}
	// Il backoff non era nella patch: `retries` è un oggetto parziale come tutto
	// il resto, non una sostituzione.
	if updated.Retries.Backoff != created.Retries.Backoff {
		t.Errorf("backoff = %q, atteso %q: un PATCH non azzera ciò che non riceve",
			updated.Retries.Backoff, created.Retries.Backoff)
	}
	if updated.Request.URL != created.Request.URL {
		t.Errorf("url = %q, atteso invariato", updated.Request.URL)
	}
}

func TestModificaVuota(t *testing.T) {
	f := newJobsFixture(t)
	created := f.creaJob(corpoValido())

	rec := f.call(http.MethodPatch, "/jobs/"+created.ID, map[string]any{})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, atteso 400", rec.Code)
	}
	if code := errorCode(t, rec); code != "empty_patch" {
		t.Errorf("codice = %q", code)
	}
}

func TestJobInesistente(t *testing.T) {
	f := newJobsFixture(t)

	for _, id := range []string{
		"00000000-0000-4000-8000-000000009999", // uuid ben formato ma inesistente
		"non-un-uuid",                          // e uno che non è nemmeno un uuid
	} {
		rec := f.call(http.MethodGet, "/jobs/"+id, nil)
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s: status = %d, atteso 404", id, rec.Code)
		}
		if code := errorCode(t, rec); code != "job_not_found" {
			t.Errorf("%s: codice = %q", id, code)
		}
	}
}

func TestJobGestitoDaRepository(t *testing.T) {
	f := newJobsFixture(t)
	gestito := f.store.Seed(jobs.Job{
		UserID: f.user.ID, RepositoryID: "33333333-3333-4333-8333-333333333333",
		Name: "da-repository", Schedule: "0 9 * * *", Timezone: "UTC",
		Environments: []jobs.Environment{jobs.EnvironmentProduction},
		URL:          "https://example.com/", Method: jobs.MethodPOST,
		Timeout: 30 * time.Second, MaxRetries: 3, RetryBackoff: jobs.BackoffExponential,
		Enabled: true,
	})

	rec := f.call(http.MethodPatch, "/jobs/"+gestito.ID, map[string]any{"name": "rinominato"})
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, atteso 409 (corpo: %s)", rec.Code, rec.Body)
	}
	if code := errorCode(t, rec); code != "job_managed_by_repository" {
		t.Errorf("codice = %q", code)
	}

	// La pausa resta ammessa: la 0005 la tiene distinta da `archived_at` proprio
	// perché sopravviva alla riconciliazione.
	rec = f.call(http.MethodPatch, "/jobs/"+gestito.ID, map[string]any{"enabled": false})
	if rec.Code != http.StatusOK {
		t.Fatalf("pausa: status = %d, corpo = %s", rec.Code, rec.Body)
	}

	// Il client lo sa in anticipo, dal campo nella risposta, invece di scoprirlo
	// da un 409 dopo aver compilato un form.
	var job httpapi.JobResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &job); err != nil {
		t.Fatalf("corpo: %v", err)
	}
	if job.RepositoryID == "" {
		t.Error("la risposta non dichiara che il job è gestito da un repository")
	}
}

func TestEliminazione(t *testing.T) {
	f := newJobsFixture(t)
	created := f.creaJob(corpoValido())

	rec := f.call(http.MethodDelete, "/jobs/"+created.ID, nil)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, atteso 204 (corpo: %s)", rec.Code, rec.Body)
	}
	if rec.Body.Len() != 0 {
		t.Errorf("il 204 ha un corpo: %q", rec.Body.String())
	}
	if rec := f.call(http.MethodGet, "/jobs/"+created.ID, nil); rec.Code != http.StatusNotFound {
		t.Errorf("dopo l'eliminazione: status = %d, atteso 404", rec.Code)
	}
}

// ---------------------------------------------------------------- paginazione

func TestPaginazioneDellElencoDeiJob(t *testing.T) {
	f := newJobsFixture(t)
	f.store.SetPlan(f.user.ID, jobs.Plan{Code: "team", MinInterval: time.Second, EnvironmentsEnabled: true})

	for i := 0; i < 5; i++ {
		body := corpoValido()
		body["name"] = fmt.Sprintf("job-%02d", i)
		f.creaJob(body)
		f.clock.avanza(time.Second)
	}

	rec := f.call(http.MethodGet, "/jobs?limit=2", nil)
	var prima httpapi.JobListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &prima); err != nil {
		t.Fatalf("corpo: %v", err)
	}
	if len(prima.Jobs) != 2 || prima.Page.Limit != 2 {
		t.Fatalf("prima pagina = %d job, limit = %d", len(prima.Jobs), prima.Page.Limit)
	}
	if prima.Page.NextCursor == nil {
		t.Fatal("manca il cursore della pagina successiva")
	}

	rec = f.call(http.MethodGet, "/jobs?limit=2&cursor="+*prima.Page.NextCursor, nil)
	var seconda httpapi.JobListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &seconda); err != nil {
		t.Fatalf("corpo: %v", err)
	}
	for _, job := range seconda.Jobs {
		for _, precedente := range prima.Jobs {
			if job.ID == precedente.ID {
				t.Errorf("il job %q compare in due pagine", job.Name)
			}
		}
	}
}

func TestLimiteDiPaginaOltreIlMassimo(t *testing.T) {
	f := newJobsFixture(t)

	// Non è un errore: è una richiesta ridimensionata, e il client se ne accorge
	// dal `page.limit` della risposta invece di contare le righe.
	rec := f.call(http.MethodGet, "/jobs?limit=100000", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, atteso 200", rec.Code)
	}
	var page httpapi.JobListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil {
		t.Fatalf("corpo: %v", err)
	}
	if page.Page.Limit != jobs.MaxPageSize {
		t.Errorf("limit = %d, atteso %d", page.Page.Limit, jobs.MaxPageSize)
	}
}

func TestParametriDiQueryNonValidi(t *testing.T) {
	f := newJobsFixture(t)
	created := f.creaJob(corpoValido())

	cases := []struct {
		path  string
		campo string
	}{
		{"/jobs?limit=abc", "limit"},
		{"/jobs?limit=-1", "limit"},
		{"/jobs?enabled=forse", "enabled"},
		{"/jobs/" + created.ID + "/executions?since=ieri", "since"},
	}

	for _, tc := range cases {
		rec := f.call(http.MethodGet, tc.path, nil)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status = %d, atteso 400 (corpo: %s)", tc.path, rec.Code, rec.Body)
			continue
		}
		if got := fieldCodes(t, rec); got[tc.campo] == "" {
			t.Errorf("%s: nessun errore sul campo %q (dettagli: %v)", tc.path, tc.campo, got)
		}
	}
}

func TestCursoreNonValidoDallAPI(t *testing.T) {
	f := newJobsFixture(t)

	rec := f.call(http.MethodGet, "/jobs?cursor=xxx", nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, atteso 400", rec.Code)
	}
	if code := errorCode(t, rec); code != "invalid_cursor" {
		t.Errorf("codice = %q", code)
	}
}

// ------------------------------------------------------------ trigger manuale

func TestTriggerManualeDallAPI(t *testing.T) {
	f := newJobsFixture(t)
	created := f.creaJob(corpoValido())

	// Senza corpo: l'unico campo è facoltativo, e pretendere `{}` per dire
	// «esegui adesso» sarebbe attrito senza contropartita.
	rec := f.call(http.MethodPost, "/jobs/"+created.ID+"/executions", nil)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, atteso 202 (corpo: %s)", rec.Code, rec.Body)
	}

	var exec httpapi.ExecutionResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &exec); err != nil {
		t.Fatalf("corpo: %v", err)
	}
	if exec.TriggeredBy != "manual" {
		t.Errorf("triggered_by = %q, atteso \"manual\"", exec.TriggeredBy)
	}
	if exec.Status != "pending" {
		t.Errorf("status = %q, atteso \"pending\"", exec.Status)
	}
	if exec.JobID != created.ID {
		t.Errorf("job_id = %q", exec.JobID)
	}
}

// TestIlTriggerManualeERateLimitato è il punto che impedisce di trasformare un
// piano Free in esecuzioni illimitate con un ciclo.
func TestIlTriggerManualeERateLimitato(t *testing.T) {
	f := newJobsFixture(t)
	created := f.creaJob(corpoValido())

	if rec := f.call(http.MethodPost, "/jobs/"+created.ID+"/executions", nil); rec.Code != http.StatusAccepted {
		t.Fatalf("primo trigger: status = %d", rec.Code)
	}

	rec := f.call(http.MethodPost, "/jobs/"+created.ID+"/executions", nil)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("secondo trigger: status = %d, atteso 429 (corpo: %s)", rec.Code, rec.Body)
	}

	detail := errorDetail(t, rec)
	if detail.Code != "plan_limit_manual_trigger" {
		t.Errorf("codice = %q", detail.Code)
	}
	if detail.RetryAfter <= 0 {
		t.Errorf("retry_after = %d, atteso positivo", detail.RetryAfter)
	}
	// La testata dice la stessa cosa del corpo: i client HTTP la leggono da soli.
	header := rec.Header().Get("Retry-After")
	seconds, err := strconv.Atoi(header)
	if err != nil || seconds != detail.RetryAfter {
		t.Errorf("Retry-After = %q, retry_after = %d", header, detail.RetryAfter)
	}

	// Passata la casella, il trigger torna possibile.
	f.clock.avanza(time.Minute)
	if rec := f.call(http.MethodPost, "/jobs/"+created.ID+"/executions", nil); rec.Code != http.StatusAccepted {
		t.Fatalf("terzo trigger: status = %d, corpo = %s", rec.Code, rec.Body)
	}
}

func TestTriggerSuJobInPausa(t *testing.T) {
	f := newJobsFixture(t)
	created := f.creaJob(corpoValido())

	if rec := f.call(http.MethodPatch, "/jobs/"+created.ID, map[string]any{"enabled": false}); rec.Code != http.StatusOK {
		t.Fatalf("pausa: status = %d", rec.Code)
	}

	rec := f.call(http.MethodPost, "/jobs/"+created.ID+"/executions", nil)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, atteso 409 (corpo: %s)", rec.Code, rec.Body)
	}
	if code := errorCode(t, rec); code != "job_disabled" {
		t.Errorf("codice = %q", code)
	}
}

// TestRegistroDelleEsecuzioni: limitare i trigger manuali senza poterli
// rileggere renderebbe impossibile accorgersi di un abuso sotto soglia.
func TestRegistroDelleEsecuzioni(t *testing.T) {
	f := newJobsFixture(t)
	created := f.creaJob(corpoValido())

	if rec := f.call(http.MethodPost, "/jobs/"+created.ID+"/executions", nil); rec.Code != http.StatusAccepted {
		t.Fatalf("trigger: status = %d", rec.Code)
	}
	f.store.SeedExecution(jobs.Execution{
		JobID:        created.ID,
		ScheduledFor: f.clock.Now().Add(time.Hour),
		Environment:  jobs.EnvironmentProduction,
		Attempt:      1,
		Status:       jobs.StatusFailed,
		TriggeredBy:  jobs.TriggerSchedule,
	})

	rec := f.call(http.MethodGet, "/jobs/"+created.ID+"/executions", nil)
	var page httpapi.ExecutionListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil {
		t.Fatalf("corpo: %v", err)
	}
	if len(page.Executions) != 2 {
		t.Fatalf("esecuzioni = %d, attese 2", len(page.Executions))
	}

	rec = f.call(http.MethodGet, "/jobs/"+created.ID+"/executions?trigger=manual", nil)
	if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil {
		t.Fatalf("corpo: %v", err)
	}
	if len(page.Executions) != 1 || page.Executions[0].TriggeredBy != "manual" {
		t.Fatalf("filtro per origine: %+v", page.Executions)
	}

	rec = f.call(http.MethodGet, "/jobs/"+created.ID+"/executions?status=failed,skipped", nil)
	if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil {
		t.Fatalf("corpo: %v", err)
	}
	if len(page.Executions) != 1 || page.Executions[0].Status != "failed" {
		t.Fatalf("filtro per stato: %+v", page.Executions)
	}
}

// --------------------------------------------------------------------- limiti

func TestCorpoTroppoGrande(t *testing.T) {
	f := newJobsFixture(t)

	body := corpoValido()
	body["request"] = map[string]any{
		"url":  "https://example.com/",
		"body": strings.Repeat("x", 128<<10),
	}

	rec := f.call(http.MethodPost, "/jobs", body)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, atteso 413 (corpo: %s)", rec.Code, rec.Body)
	}
	if code := errorCode(t, rec); code != "body_too_large" {
		t.Errorf("codice = %q", code)
	}
}

// TestUnCorpoAmmessoMaGrandeVienAccettato: il tetto delle rotte dei job è più
// alto di quello dell'autenticazione proprio perché un job porta con sé il corpo
// della richiesta che invierà.
func TestUnCorpoAmmessoMaGrandeVienAccettato(t *testing.T) {
	f := newJobsFixture(t)

	body := corpoValido()
	body["request"] = map[string]any{
		"url":  "https://example.com/",
		"body": strings.Repeat("x", jobs.MaxBodyLength),
	}

	if rec := f.call(http.MethodPost, "/jobs", body); rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, atteso 201 (corpo: %s)", rec.Code, rec.Body)
	}
}
