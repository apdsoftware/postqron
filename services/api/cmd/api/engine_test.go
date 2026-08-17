package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/apdsoftware/postqron/services/api/internal/auth"
	"github.com/apdsoftware/postqron/services/api/internal/authpg"
	"github.com/apdsoftware/postqron/services/api/internal/config"
	"github.com/apdsoftware/postqron/services/api/internal/httpapi"
	"github.com/apdsoftware/postqron/services/api/internal/jobs"
	"github.com/apdsoftware/postqron/services/api/internal/jobspg"
	"github.com/apdsoftware/postqron/services/api/internal/netguard"
)

// Queste sono le prove che chiudono #486, e sono l'unico posto del repository
// in cui il prodotto fa la cosa per cui esiste: un job creato via API viene
// eseguito davvero, all'orario dovuto, contro un bersaglio HTTP vero, e il suo
// esito compare nel registro.
//
// Niente è finto tranne due cose, ed entrambe per necessità:
//
//   - **Il client HTTP e il controllo sulla destinazione.** Il bersaglio della
//     prova è un `httptest.Server`, che vive su 127.0.0.1 — cioè esattamente
//     l'indirizzo che netguard ha il compito di rifiutare (R38). La deroga sta
//     qui, nel chiamante, ed è la stessa scelta che netguard fa per i propri
//     test con `AllowForTest`. Che la deroga sia l'unica differenza dal percorso
//     d'esercizio lo garantisce [newEngine], che è la funzione di composizione
//     vera: la prova non ne ricostruisce una versione parallela.
//   - **I parametri di Argon2id**, che altrove costano 150 ms per login.
//
// Il database è PostgreSQL vero con le migrazioni vere, il motore è quello che
// gira in produzione, e il registro si rilegge dall'API.

const segretoDiProva = "un-segreto-di-prova-abbastanza-lungo-da-essere-accettato"

// parametriEconomici: Argon2id ai minimi accettati. Qui la robustezza dell'hash
// non è ciò che si sta verificando, e il costo vero sarebbe pagato due volte per
// ogni prova.
var parametriEconomici = auth.Argon2idParams{
	Memory: 8 * 1024, Time: 1, Parallelism: 1, SaltLength: 16, KeyLength: 32,
}

// sorgenteAperta è la sorgente del client HTTP nella prova: un client che
// raggiunge 127.0.0.1. Vedi la nota in testa al file.
type sorgenteAperta struct{ client *http.Client }

func (s sorgenteAperta) Client() *http.Client { return s.client }

// destinazioniAperte accetta qualunque destinazione.
type destinazioniAperte struct{}

func (destinazioniAperte) CheckTarget(context.Context, *url.URL) error { return nil }

// ------------------------------------------------------------------- banco

// banco è il servizio intero in piedi: database, API, motore e bersaglio.
type banco struct {
	t       *testing.T
	pool    *pgxpool.Pool
	handler http.Handler
	target  *bersaglio
	eng     *engine

	cookie string
	userID string
}

// nuovoBanco monta il servizio come lo monta `run`, con un utente già collegato
// e il motore avviato.
func nuovoBanco(t *testing.T, tune ...func(*engineOptions)) *banco {
	t.Helper()

	pool := newTestDatabase(t)
	log := testLogger(t)
	target := nuovoBersaglio(t)

	opts := engineOptions{
		Pool:    pool,
		Logger:  log,
		Clients: sorgenteAperta{client: target.Client()},
		Targets: destinazioniAperte{},
	}
	for _, f := range tune {
		f(&opts)
	}
	eng, err := newEngine(opts)
	if err != nil {
		t.Fatalf("newEngine: %v", err)
	}

	jobStore, err := jobspg.New(pool)
	if err != nil {
		t.Fatalf("jobspg.New: %v", err)
	}
	jobsSvc, err := jobs.NewService(jobs.Options{
		Store:      jobStore,
		Logger:     log,
		Guard:      destinazioniAperte{},
		Dispatcher: eng.Manual(),
	})
	if err != nil {
		t.Fatalf("jobs.NewService: %v", err)
	}

	authStore, err := authpg.New(pool)
	if err != nil {
		t.Fatalf("authpg.New: %v", err)
	}
	hasher, err := auth.NewHasher(parametriEconomici)
	if err != nil {
		t.Fatalf("auth.NewHasher: %v", err)
	}
	keyring, err := auth.NewKeyring(segretoDiProva)
	if err != nil {
		t.Fatalf("auth.NewKeyring: %v", err)
	}
	authSvc, err := auth.NewService(auth.Options{
		Store:   authStore,
		Hasher:  hasher,
		Keyring: keyring,
		Mailer:  &auth.MemoryMailer{},
		Logger:  log,
	})
	if err != nil {
		t.Fatalf("auth.NewService: %v", err)
	}

	cfg, err := config.LoadFrom(func(string) string { return "" })
	if err != nil {
		t.Fatalf("configurazione di default non valida: %v", err)
	}

	b := &banco{
		t:       t,
		pool:    pool,
		handler: httpapi.NewRouter(cfg, "prova", log, httpapi.Deps{Auth: authSvc, Jobs: jobsSvc}),
		target:  target,
		eng:     eng,
	}
	b.registraEAccedi()

	// Il motore parte per ultimo, come in `run`: la sua vita è il contesto del
	// processo, e l'arresto è quello vero — drenaggio compreso.
	ctx, stop := context.WithCancel(context.Background())
	eng.Start(ctx)
	t.Cleanup(func() {
		stop()
		arresto, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := eng.Shutdown(arresto); err != nil {
			t.Logf("arresto del motore: %v", err)
		}
	})
	return b
}

// chiama esegue una richiesta sull'API, autenticata se c'è una sessione aperta.
func (b *banco) chiama(method, path string, body any) *httptest.ResponseRecorder {
	b.t.Helper()

	var corpo io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			b.t.Fatalf("serializzazione della richiesta: %v", err)
		}
		corpo = bytes.NewReader(raw)
	}
	req := httptest.NewRequest(method, path, corpo)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if b.cookie != "" {
		req.AddCookie(&http.Cookie{Name: httpapi.SessionCookieName, Value: b.cookie})
	}

	rec := httptest.NewRecorder()
	b.handler.ServeHTTP(rec, req)
	return rec
}

// registraEAccedi apre un account e una sessione passando dalle rotte vere.
func (b *banco) registraEAccedi() {
	b.t.Helper()

	email := fmt.Sprintf("mario.rossi+%d@example.com", databaseCounter.Add(1))
	const password = "una-password-lunga-abbastanza"

	if rec := b.chiama(http.MethodPost, "/auth/register", map[string]any{
		"email": email, "password": password, "full_name": "Mario Rossi",
	}); rec.Code != http.StatusAccepted {
		b.t.Fatalf("registrazione: status %d, corpo %s", rec.Code, rec.Body)
	}

	rec := b.chiama(http.MethodPost, "/auth/login", map[string]any{
		"email": email, "password": password,
	})
	if rec.Code != http.StatusOK {
		b.t.Fatalf("login: status %d, corpo %s", rec.Code, rec.Body)
	}
	for _, cookie := range rec.Result().Cookies() {
		if cookie.Name == httpapi.SessionCookieName {
			b.cookie = cookie.Value
		}
	}
	if b.cookie == "" {
		b.t.Fatal("il login non ha impostato il cookie di sessione")
	}

	if err := b.pool.QueryRow(b.t.Context(),
		`SELECT id::text FROM users WHERE email = $1`, email).Scan(&b.userID); err != nil {
		b.t.Fatalf("lettura dell'utente: %v", err)
	}
}

// pianoTeam porta l'utente sul piano con risoluzione di un secondo (SPEC §8).
//
// Serve alla prova, non al prodotto: su Free la risoluzione minima è un minuto,
// e verificare che un job parta all'orario dovuto vorrebbe dire aspettarne uno.
// Il percorso esercitato è lo stesso — è la stessa colonna che decide, e i
// limiti restano applicati lato server (R15).
func (b *banco) pianoTeam() {
	b.t.Helper()
	if _, err := b.pool.Exec(b.t.Context(),
		`INSERT INTO subscriptions (user_id, plan_code, status) VALUES ($1::uuid, 'team', 'active')`,
		b.userID); err != nil {
		b.t.Fatalf("attivazione del piano team: %v", err)
	}
}

// creaJob crea un job dall'API e ne restituisce l'identificativo.
func (b *banco) creaJob(payload map[string]any) string {
	b.t.Helper()

	rec := b.chiama(http.MethodPost, "/jobs", payload)
	if rec.Code != http.StatusCreated {
		b.t.Fatalf("creazione del job: status %d, corpo %s", rec.Code, rec.Body)
	}
	var risposta httpapi.JobResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &risposta); err != nil {
		b.t.Fatalf("lettura della risposta di creazione: %v", err)
	}
	return risposta.ID
}

// esecuzioni rilegge il registro dall'API, che è il modo in cui l'utente lo
// vede (R6).
func (b *banco) esecuzioni(jobID, trigger string) []httpapi.ExecutionResponse {
	b.t.Helper()

	path := "/jobs/" + jobID + "/executions"
	if trigger != "" {
		path += "?trigger=" + trigger
	}
	rec := b.chiama(http.MethodGet, path, nil)
	if rec.Code != http.StatusOK {
		b.t.Fatalf("lettura del registro: status %d, corpo %s", rec.Code, rec.Body)
	}
	var risposta httpapi.ExecutionListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &risposta); err != nil {
		b.t.Fatalf("lettura del registro: %v", err)
	}
	return risposta.Executions
}

// statiTerminali sono gli stati in cui un'esecuzione è finita (0001).
var statiTerminali = []string{"succeeded", "failed", "timed_out", "skipped"}

// attendiEsecuzione aspetta la prima esecuzione conclusa del job.
//
// L'attesa è su un fatto osservabile dall'API — una riga in stato terminale —
// e non su una durata fissa: è la differenza fra una prova che verifica il
// comportamento e una che verifica la pazienza di chi la guarda.
func (b *banco) attendiEsecuzione(jobID, trigger string, entro time.Duration) httpapi.ExecutionResponse {
	b.t.Helper()

	scadenza := time.Now().Add(entro)
	for {
		for _, e := range b.esecuzioni(jobID, trigger) {
			if slices.Contains(statiTerminali, e.Status) {
				return e
			}
		}
		if time.Now().After(scadenza) {
			b.t.Fatalf("nessuna esecuzione conclusa per il job %s entro %s: registro = %s",
				jobID, entro, b.registroLeggibile(jobID, trigger))
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func (b *banco) registroLeggibile(jobID, trigger string) string {
	var out []string
	for _, e := range b.esecuzioni(jobID, trigger) {
		out = append(out, fmt.Sprintf("%s/%s@%s err=%q",
			e.Status, e.TriggeredBy, e.ScheduledFor.Format(time.RFC3339), e.Error))
	}
	if len(out) == 0 {
		return "(vuoto)"
	}
	return strings.Join(out, "; ")
}

// ------------------------------------------------------------------- prove

// TestUnJobCreatoViaAPIVieneEseguitoAllOrarioDovuto è la prova che chiude #390 e
// #486 insieme: fino a qui Postqron non eseguiva nessun cronjob.
func TestUnJobCreatoViaAPIVieneEseguitoAllOrarioDovuto(t *testing.T) {
	b := nuovoBanco(t)
	b.pianoTeam()

	jobID := b.creaJob(map[string]any{
		"name":  "eco-schedulato",
		"every": "1s",
		"request": map[string]any{
			"url":     b.target.URL + "/schedulato",
			"method":  "POST",
			"headers": map[string]string{"X-Postqron-Prova": "schedulato"},
			"body":    "ciao",
		},
		"timeout": "5s",
	})

	esecuzione := b.attendiEsecuzione(jobID, "schedule", 20*time.Second)

	// --- l'esito, che è ciò che l'utente vede
	if esecuzione.Status != "succeeded" {
		t.Fatalf("stato = %q, atteso succeeded (errore: %q)", esecuzione.Status, esecuzione.Error)
	}
	if esecuzione.TriggeredBy != "schedule" {
		t.Errorf("triggered_by = %q, atteso schedule", esecuzione.TriggeredBy)
	}
	if esecuzione.ResponseStatus == nil || *esecuzione.ResponseStatus != http.StatusOK {
		t.Errorf("response_status = %v, atteso 200", esecuzione.ResponseStatus)
	}
	if esecuzione.ResponseExcerpt != "eco:ciao" {
		t.Errorf("response_excerpt = %q, atteso %q", esecuzione.ResponseExcerpt, "eco:ciao")
	}
	if esecuzione.Error != "" {
		t.Errorf("error = %q, atteso vuoto", esecuzione.Error)
	}
	if esecuzione.StartedAt == nil || esecuzione.FinishedAt == nil {
		t.Fatalf("istanti di inizio e fine mancanti: %+v", esecuzione)
	}
	if esecuzione.DurationMS == nil {
		t.Error("duration_ms mancante: è la colonna generata dalla differenza fra i due istanti")
	}

	// --- l'orario dovuto (R47)
	//
	// La tolleranza dichiarata è di un secondo a coda scarica; qui il margine è
	// largo di proposito, perché la prova gira insieme alle altre e sotto
	// `-race`. Ciò che deve restare vero è che l'esecuzione parta *dopo*
	// l'orario dovuto e non ore dopo.
	ritardo := esecuzione.StartedAt.Sub(esecuzione.ScheduledFor)
	if ritardo < 0 {
		t.Errorf("l'esecuzione è partita %s prima dell'orario dovuto", -ritardo)
	}
	if ritardo > 10*time.Second {
		t.Errorf("l'esecuzione è partita con %s di ritardo sull'orario dovuto", ritardo)
	}

	// --- il bersaglio è stato chiamato davvero, e con ciò che il job diceva
	chiamate := b.target.chiamateSu("/schedulato")
	if len(chiamate) == 0 {
		t.Fatal("il bersaglio non ha ricevuto nessuna richiesta")
	}
	prima := chiamate[0]
	if prima.Method != http.MethodPost {
		t.Errorf("metodo ricevuto = %q, atteso POST", prima.Method)
	}
	if prima.Body != "ciao" {
		t.Errorf("corpo ricevuto = %q, atteso %q", prima.Body, "ciao")
	}
	if v := prima.Headers.Get("X-Postqron-Prova"); v != "schedulato" {
		t.Errorf("header del job non arrivato: X-Postqron-Prova = %q", v)
	}
	if v := prima.Headers.Get("User-Agent"); !strings.HasPrefix(v, "Postqron/") {
		t.Errorf("User-Agent = %q, atteso quello del servizio", v)
	}
}

// TestUnTriggerManualeParteSubito verifica l'altro confine: la riga che l'API
// registra (#395) e che finora nessuno raccoglieva.
//
// Il job ha una schedulazione notturna proprio per questo: se durante la prova
// partisse anche da solo, non si potrebbe dire se a farlo partire è stato il
// trigger o il calendario.
func TestUnTriggerManualeParteSubito(t *testing.T) {
	b := nuovoBanco(t)

	jobID := b.creaJob(map[string]any{
		"name":     "eco-manuale",
		"schedule": "0 3 * * *",
		"request": map[string]any{
			"url":    b.target.URL + "/manuale",
			"method": "POST",
			"body":   "adesso",
		},
		"timeout": "5s",
	})

	inizio := time.Now()
	rec := b.chiama(http.MethodPost, "/jobs/"+jobID+"/executions", map[string]any{
		"environment": "production",
	})
	if rec.Code != http.StatusAccepted {
		t.Fatalf("trigger manuale: status %d, corpo %s", rec.Code, rec.Body)
	}

	esecuzione := b.attendiEsecuzione(jobID, "manual", 20*time.Second)
	attesa := time.Since(inizio)

	if esecuzione.Status != "succeeded" {
		t.Fatalf("stato = %q, atteso succeeded (errore: %q)", esecuzione.Status, esecuzione.Error)
	}
	if esecuzione.TriggeredBy != "manual" {
		t.Errorf("triggered_by = %q, atteso manual", esecuzione.TriggeredBy)
	}
	if esecuzione.ResponseExcerpt != "eco:adesso" {
		t.Errorf("response_excerpt = %q, atteso %q", esecuzione.ResponseExcerpt, "eco:adesso")
	}
	// «Subito» significa che non si aspetta la schedulazione, che qui sarebbe
	// alle tre di notte. Il margine è largo: ciò che si vuole escludere è
	// l'ordine di grandezza sbagliato.
	if attesa > 10*time.Second {
		t.Errorf("il trigger manuale ha impiegato %s a partire", attesa)
	}

	if n := len(b.target.chiamateSu("/manuale")); n != 1 {
		t.Errorf("chiamate al bersaglio = %d, attesa 1", n)
	}
	// Nessuna occorrenza schedulata: il job scatta alle tre.
	if programmate := b.esecuzioni(jobID, "schedule"); len(programmate) != 0 {
		t.Errorf("esecuzioni schedulate = %d, attese 0", len(programmate))
	}
}

// TestUnaDestinazioneNonPubblicaLasciaUnEsecuzioneFallita verifica che il guard
// di #455 sia davvero innestato nel motore (R38).
//
// Il job viene inserito direttamente sul database perché dall'API non ci si
// arriva: la stessa politica lo rifiuterebbe alla creazione. È la via da cui un
// bersaglio interno può comparire davvero — un nome che risolveva pubblico
// quando il job è stato creato e che oggi risolve all'interno.
//
// L'asserzione è sul prefisso del messaggio, e non è un dettaglio: «destinazione
// rifiutata» è ciò che scrive il worker pool quando il guard lo ferma **prima**
// di aprire una connessione. Se quel controllo non fosse collegato, il client di
// questa prova — che raggiunge il loopback — proverebbe a connettersi davvero, e
// l'errore sulla riga sarebbe un altro.
func TestUnaDestinazioneNonPubblicaLasciaUnEsecuzioneFallita(t *testing.T) {
	b := nuovoBanco(t, func(o *engineOptions) {
		o.Targets = netguard.New(netguard.Options{})
	})

	var jobID string
	if err := b.pool.QueryRow(b.t.Context(),
		`INSERT INTO jobs (user_id, name, every_seconds, timezone, environments, url, method)
		 VALUES ($1::uuid, 'bersaglio-interno', 1, 'UTC', ARRAY['production']::environment[],
		         'http://127.0.0.1:9/interno', 'POST')
		 RETURNING id::text`, b.userID).Scan(&jobID); err != nil {
		t.Fatalf("inserimento del job: %v", err)
	}

	esecuzione := b.attendiEsecuzione(jobID, "schedule", 20*time.Second)

	if esecuzione.Status != "failed" {
		t.Fatalf("stato = %q, atteso failed (errore: %q)", esecuzione.Status, esecuzione.Error)
	}
	if !strings.HasPrefix(esecuzione.Error, "destinazione rifiutata:") {
		t.Errorf("errore = %q, atteso il rifiuto del guard prima della connessione", esecuzione.Error)
	}
	// Il motivo del rifiuto non è dell'utente: il messaggio è sempre lo stesso
	// (netguard.ErrNotAllowed) e non dice quale indirizzo, né perché.
	if strings.Contains(esecuzione.Error, "loopback") || strings.Contains(esecuzione.Error, "127.0.0.1") {
		t.Errorf("l'errore rivela la ragione del rifiuto: %q", esecuzione.Error)
	}
	if n := len(b.target.chiamate()); n != 0 {
		t.Errorf("il bersaglio ha ricevuto %d richieste: la connessione non doveva aprirsi", n)
	}
}
