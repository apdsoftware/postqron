package httpapi_test

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/apdsoftware/postqron/services/api/internal/auth"
	"github.com/apdsoftware/postqron/services/api/internal/config"
	"github.com/apdsoftware/postqron/services/api/internal/execstream"
	"github.com/apdsoftware/postqron/services/api/internal/httpapi"
	"github.com/apdsoftware/postqron/services/api/internal/jobs"
	"github.com/apdsoftware/postqron/services/api/internal/jobstest"
)

// Le prove dello streaming girano contro un **server HTTP vero**, e non contro
// un [httptest.ResponseRecorder], perché ciò che si sta verificando è
// esattamente quello che un recorder non ha: una risposta che comincia e non
// finisce, un flush che arriva a destinazione, un client che se ne va.
//
// Ciò che invece resta a comando è il battito di lettura: l'hub riceve un canale
// che non scatta mai e il giro lo fa la prova con [execstream.Hub.Poll]. Una
// suite che aspettasse il battito vero costerebbe un secondo per prova, e
// misurerebbe l'orologio invece del comportamento.

// ---------------------------------------------------------------- impalcatura

type flussoFixture struct {
	*api
	server *httptest.Server
	store  *jobstest.Store
	hub    *execstream.Hub
	clock  *clockDiProva
	token  string
	jobID  string
}

func newFlussoFixture(t *testing.T, tune ...func(*httpapi.Deps)) *flussoFixture {
	t.Helper()

	clock := &clockDiProva{now: time.Date(2026, 8, 17, 10, 0, 0, 0, time.UTC)}
	store := jobstest.NewStore()
	store.Now = clock.Now

	hub, err := execstream.New(execstream.Options{
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		// Un canale che non scatta mai: il ciclo dell'hub resta fermo e i giri li
		// comanda la prova.
		Tick: make(chan time.Time),
	})
	if err != nil {
		t.Fatalf("execstream.New: %v", err)
	}
	t.Cleanup(hub.Stop)

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
		deps.ExecutionStreams = hub
		deps.Now = clock.Now
		// Il battito della connessione è corto, così la prova che lo verifica non
		// aspetta venti secondi. Non tocca il battito di *lettura*, che è un'altra
		// cosa e resta a comando.
		deps.StreamTimings = httpapi.StreamTimings{Heartbeat: 50 * time.Millisecond}
		for _, fn := range tune {
			fn(deps)
		}
	})

	_, token := a.registerAndLogin()
	server := httptest.NewServer(a.handler)
	t.Cleanup(server.Close)

	f := &flussoFixture{api: a, server: server, store: store, hub: hub, clock: clock, token: token}
	f.jobID = f.creaJob()
	return f
}

func (f *flussoFixture) creaJob() string {
	f.t.Helper()
	rec := f.do(http.MethodPost, "/jobs", corpoValido(), withCookie(f.token))
	if rec.Code != http.StatusCreated {
		f.t.Fatalf("creazione del job: status %d, corpo %s", rec.Code, rec.Body)
	}
	var risposta httpapi.JobResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &risposta); err != nil {
		f.t.Fatalf("lettura della risposta di creazione: %v", err)
	}
	return risposta.ID
}

// esecuzione scrive una riga del registro, come farebbero lo scheduler e il
// worker pool. Riscrivere la stessa chiave naturale è un aggiornamento sul
// posto, che è precisamente ciò che il motore fa passando da `running` a
// `succeeded`.
func (f *flussoFixture) esecuzione(offset time.Duration, stato jobs.ExecutionStatus, excerpt string) jobs.Execution {
	f.t.Helper()
	return f.store.SeedExecution(jobs.Execution{
		JobID:           f.jobID,
		ScheduledFor:    f.clock.Now().Add(offset),
		Environment:     jobs.EnvironmentProduction,
		Attempt:         1,
		Status:          stato,
		TriggeredBy:     jobs.TriggerSchedule,
		ResponseExcerpt: excerpt,
		CreatedAt:       f.clock.Now(),
	})
}

// ------------------------------------------------------------- client SSE

type frame struct {
	// Comment è valorizzato per le righe che cominciano con `:` — il battito.
	Comment string
	Event   string
	ID      string
	Data    string
}

// sseClient legge un flusso Server-Sent Events in una goroutine, e ne consegna i
// blocchi su un canale: una prova che leggesse dal corpo direttamente si
// bloccherebbe per sempre sul primo silenzio.
type sseClient struct {
	t      *testing.T
	resp   *http.Response
	conn   net.Conn
	frames chan frame
}

// apri apre il flusso. Restituisce anche la risposta, perché metà delle prove
// riguardano ciò che succede **prima** che il flusso cominci.
func (f *flussoFixture) apri(path string, prepare ...func(*http.Request)) (*sseClient, *http.Response) {
	f.t.Helper()

	req, err := http.NewRequest(http.MethodGet, f.server.URL+path, nil)
	if err != nil {
		f.t.Fatalf("richiesta: %v", err)
	}
	req.AddCookie(&http.Cookie{Name: httpapi.SessionCookieName, Value: f.token})
	for _, fn := range prepare {
		fn(req)
	}

	// Il socket si tiene da parte: metà delle prove riguardano ciò che succede
	// quando sparisce, e per farlo sparire *male* serve la connessione vera.
	// `DisableKeepAlives` fa sì che chiuderla chiuda davvero, invece di
	// restituirla a un pool che la tiene calda.
	var conn net.Conn
	dialer := &net.Dialer{}
	client := &http.Client{Transport: &http.Transport{
		DisableKeepAlives: true,
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			c, err := dialer.DialContext(ctx, network, addr)
			conn = c
			return c, err
		},
	}}

	resp, err := client.Do(req)
	if err != nil {
		f.t.Fatalf("apertura del flusso: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, resp
	}

	if got := resp.Header.Get("Content-Type"); !strings.HasPrefix(got, "text/event-stream") {
		f.t.Fatalf("Content-Type = %q, atteso text/event-stream", got)
	}

	flusso := &sseClient{t: f.t, resp: resp, conn: conn, frames: make(chan frame, 64)}
	go flusso.leggi()
	f.t.Cleanup(flusso.chiudi)
	return flusso, resp
}

func (c *sseClient) leggi() {
	defer close(c.frames)

	reader := bufio.NewReader(c.resp.Body)
	var current frame
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimSuffix(line, "\n")

		switch {
		case strings.HasPrefix(line, ":"):
			c.frames <- frame{Comment: strings.TrimSpace(line[1:])}
		case line == "":
			if current.Event != "" || current.Data != "" {
				c.frames <- current
			}
			current = frame{}
		case strings.HasPrefix(line, "event: "):
			current.Event = line[len("event: "):]
		case strings.HasPrefix(line, "id: "):
			current.ID = line[len("id: "):]
		case strings.HasPrefix(line, "data: "):
			current.Data = line[len("data: "):]
		case strings.HasPrefix(line, "retry: "):
			// Il ritardo di riconnessione: si legge e non produce un blocco.
		}
	}
}

func (c *sseClient) chiudi() {
	if c.resp != nil {
		_ = c.resp.Body.Close()
	}
}

// sparisci chiude il socket **senza salutare**: `SetLinger(0)` fa mandare un RST
// invece del FIN, che è ciò che succede quando un portatile viene chiuso o una
// rete mobile cade. Il server non riceve nessuna fine ordinata della richiesta.
func (c *sseClient) sparisci() {
	c.t.Helper()
	tcp, ok := c.conn.(*net.TCPConn)
	if !ok {
		c.t.Skip("connessione non TCP: la prova del client sparito non è applicabile")
	}
	if err := tcp.SetLinger(0); err != nil {
		c.t.Fatalf("SetLinger: %v", err)
	}
	if err := tcp.Close(); err != nil {
		c.t.Fatalf("chiusura brutale: %v", err)
	}
}

// prossimo aspetta il blocco successivo, saltando i battiti.
func (c *sseClient) prossimo() frame {
	c.t.Helper()
	scadenza := time.After(5 * time.Second)
	for {
		select {
		case got, open := <-c.frames:
			if !open {
				c.t.Fatal("il flusso si è chiuso mentre si aspettava un evento")
			}
			if got.Comment != "" {
				continue
			}
			return got
		case <-scadenza:
			c.t.Fatal("nessun evento entro cinque secondi")
			return frame{}
		}
	}
}

// battito aspetta un battito.
func (c *sseClient) battito() frame {
	c.t.Helper()
	scadenza := time.After(5 * time.Second)
	for {
		select {
		case got, open := <-c.frames:
			if !open {
				c.t.Fatal("il flusso si è chiuso mentre si aspettava un battito")
			}
			if got.Comment == "" {
				continue
			}
			return got
		case <-scadenza:
			c.t.Fatal("nessun battito entro cinque secondi")
			return frame{}
		}
	}
}

// silenzio verifica che non arrivi niente entro una finestra breve.
func (c *sseClient) silenzio(entro time.Duration) {
	c.t.Helper()
	scadenza := time.After(entro)
	for {
		select {
		case got, open := <-c.frames:
			if !open {
				return
			}
			if got.Comment != "" {
				continue
			}
			c.t.Fatalf("evento inatteso: %+v", got)
		case <-scadenza:
			return
		}
	}
}

// attendi ripete una condizione finché non è vera, senza dormire una durata
// fissa: è la differenza fra una prova che verifica un fatto e una che verifica
// la pazienza di chi la guarda.
func attendi(t *testing.T, cosa string, condizione func() bool) {
	t.Helper()
	scadenza := time.Now().Add(5 * time.Second)
	for !condizione() {
		if time.Now().After(scadenza) {
			t.Fatalf("condizione non raggiunta entro cinque secondi: %s", cosa)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

func (f *flussoFixture) percorso() string { return "/jobs/" + f.jobID + "/executions/stream" }

// ------------------------------------------------------------------- prove

// TestUnFlussoRiceveUnEsecuzioneMentreEAperto è la prova per cui la issue esiste.
func TestUnFlussoRiceveUnEsecuzioneMentreEAperto(t *testing.T) {
	f := newFlussoFixture(t)
	client, _ := f.apri(f.percorso())

	// L'esecuzione nasce **dopo** che il flusso è aperto.
	f.esecuzione(time.Second, jobs.StatusSucceeded, "eco:ciao")
	f.hub.Poll()

	got := client.prossimo()
	if got.Event != "execution" {
		t.Fatalf("event = %q, atteso execution", got.Event)
	}
	if got.ID == "" {
		t.Error("un'esecuzione conclusa deve portare la propria posizione")
	}

	var payload httpapi.ExecutionResponse
	if err := json.Unmarshal([]byte(got.Data), &payload); err != nil {
		t.Fatalf("corpo dell'evento non decodificabile: %v", err)
	}
	if payload.JobID != f.jobID || payload.Status != "succeeded" {
		t.Errorf("corpo inatteso: %+v", payload)
	}
	if payload.ResponseExcerpt != "eco:ciao" {
		t.Errorf("response_excerpt = %q", payload.ResponseExcerpt)
	}
}

// TestIlCorpoDelFlussoEQuelloDelRegistro: lo streaming è una via d'uscita nuova
// per gli stessi dati, ed è il posto in cui una redazione si aggira per sbaglio
// (R43). La difesa è che la proiezione sia **una sola**, e questa prova la
// verifica byte per byte.
//
// La prova end-to-end con un segreto vero nel bersaglio sta in
// cmd/api/engine_test.go: qui si verifica che le due uscite non possano
// divergere, lì che ciò che esce sia davvero redatto.
func TestIlCorpoDelFlussoEQuelloDelRegistro(t *testing.T) {
	f := newFlussoFixture(t)

	// Un estratto già redatto, come lo scrive internal/dispatch: il riferimento
	// al posto del valore.
	const excerpt = `{"echo":"Bearer ${DIGEST_TOKEN}"}`
	f.esecuzione(-time.Second, jobs.StatusFailed, excerpt)

	client, _ := f.apri(f.percorso())
	f.hub.Poll()
	got := client.prossimo()

	rec := f.do(http.MethodGet, "/jobs/"+f.jobID+"/executions", nil, withCookie(f.token))
	if rec.Code != http.StatusOK {
		t.Fatalf("registro: status %d", rec.Code)
	}
	var lista httpapi.ExecutionListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &lista); err != nil {
		t.Fatalf("registro non decodificabile: %v", err)
	}
	if len(lista.Executions) != 1 {
		t.Fatalf("esecuzioni nel registro = %d, attesa 1", len(lista.Executions))
	}
	atteso, err := json.Marshal(lista.Executions[0])
	if err != nil {
		t.Fatalf("codifica: %v", err)
	}
	if got.Data != string(atteso) {
		t.Errorf("il flusso e il registro non consegnano la stessa cosa:\nflusso   = %s\nregistro = %s",
			got.Data, atteso)
	}
}

// TestLaRiconnessioneRiprendeDalLastEventID è il caso della rete che cade: il
// client torna con la propria posizione e non deve né perdere né rivedere niente.
func TestLaRiconnessioneRiprendeDalLastEventID(t *testing.T) {
	f := newFlussoFixture(t)
	client, _ := f.apri(f.percorso())

	f.esecuzione(time.Second, jobs.StatusSucceeded, "prima")
	f.hub.Poll()
	prima := client.prossimo()
	if prima.ID == "" {
		t.Fatal("il primo evento non porta una posizione")
	}
	client.chiudi()
	attendi(t, "il posto del flusso caduto si libera", func() bool { return f.hub.Open() == 0 })

	// Mentre il client è scollegato ne arriva un'altra.
	f.esecuzione(2*time.Second, jobs.StatusSucceeded, "seconda")

	ripresa, _ := f.apri(f.percorso(), func(r *http.Request) {
		r.Header.Set("Last-Event-ID", prima.ID)
	})
	f.hub.Poll()

	got := ripresa.prossimo()
	var payload httpapi.ExecutionResponse
	if err := json.Unmarshal([]byte(got.Data), &payload); err != nil {
		t.Fatalf("corpo non decodificabile: %v", err)
	}
	if payload.ResponseExcerpt != "seconda" {
		t.Fatalf("la ripresa ha consegnato %q: attesa solo la seconda esecuzione", payload.ResponseExcerpt)
	}
	// E nient'altro: la prima non torna indietro.
	ripresa.silenzio(150 * time.Millisecond)
}

// TestIlBattitoAttraversaIlSilenzio: senza, un proxy chiuderebbe la connessione
// e nessuno si accorgerebbe di un client sparito. Vedi `defaultHeartbeat`.
func TestIlBattitoAttraversaIlSilenzio(t *testing.T) {
	f := newFlussoFixture(t)
	client, _ := f.apri(f.percorso())
	if got := client.battito(); got.Comment == "" {
		t.Fatalf("battito vuoto: %+v", got)
	}
}

// TestIlClientSparitoSenzaSalutareLiberaIlPosto: un browser chiuso male non
// manda un saluto, e il posto che occupa non deve restare occupato per sempre —
// altrimenti il tetto per utente scende a zero da solo.
func TestIlClientSparitoSenzaSalutareLiberaIlPosto(t *testing.T) {
	f := newFlussoFixture(t)
	client, _ := f.apri(f.percorso())
	attendi(t, "il flusso risulta aperto", func() bool { return f.hub.Open() == 1 })

	client.sparisci()

	attendi(t, "il posto del client sparito si libera", func() bool { return f.hub.Open() == 0 })
}

// TestIlClientCheChiudeLiberaIlPosto è il caso ordinario, e sta accanto al
// precedente perché sono due percorsi diversi che devono finire allo stesso modo.
func TestIlClientCheChiudeLiberaIlPosto(t *testing.T) {
	f := newFlussoFixture(t)
	client, _ := f.apri(f.percorso())
	attendi(t, "il flusso risulta aperto", func() bool { return f.hub.Open() == 1 })

	client.chiudi()
	attendi(t, "il posto si libera", func() bool { return f.hub.Open() == 0 })
}

// TestIlTettoDelleConnessioniRispondeUnRifiutoTecnico verifica **la forma della
// risposta**, che è ciò che R10 chiede: un tetto tecnico non nomina nessun piano
// e non suggerisce nessun upgrade, perché l'upgrade non servirebbe.
func TestIlTettoDelleConnessioniRispondeUnRifiutoTecnico(t *testing.T) {
	f := newFlussoFixture(t, func(deps *httpapi.Deps) {
		hub, err := execstream.New(execstream.Options{
			Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
			Limits: execstream.Limits{MaxPerUser: 1},
			Tick:   make(chan time.Time),
		})
		if err != nil {
			t.Fatalf("execstream.New: %v", err)
		}
		t.Cleanup(hub.Stop)
		deps.ExecutionStreams = hub
	})

	primo, _ := f.apri(f.percorso())
	defer primo.chiudi()

	_, resp := f.apri(f.percorso())
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("status = %d, atteso 429", resp.StatusCode)
	}
	if resp.Header.Get("Retry-After") == "" {
		t.Error("manca Retry-After: un tetto di capienza istantanea si libera da sé")
	}

	var corpo httpapi.ErrorBody
	if err := json.NewDecoder(resp.Body).Decode(&corpo); err != nil {
		t.Fatalf("corpo non decodificabile: %v", err)
	}
	_ = resp.Body.Close()

	if corpo.Error.Code != "stream_ceiling" {
		t.Errorf("code = %q, atteso stream_ceiling", corpo.Error.Code)
	}
	if corpo.Error.Plan != "" || corpo.Error.Limit != "" {
		t.Errorf("un tetto tecnico non deve nominare un piano: plan=%q limit=%q",
			corpo.Error.Plan, corpo.Error.Limit)
	}
	if !strings.Contains(corpo.Error.Message, "nessun piano ne concede di più") {
		t.Errorf("il messaggio non dice che l'upgrade non servirebbe: %q", corpo.Error.Message)
	}
}

// TestUnaFinestraOltreLaRetentionVieneRifiutata: R10-bis vale anche in
// streaming, e vale anche quando la finestra arriva da una riconnessione.
func TestUnaFinestraOltreLaRetentionVieneRifiutata(t *testing.T) {
	f := newFlussoFixture(t)

	// Il piano Free conserva tre giorni (SPEC §8).
	oltre := f.clock.Now().Add(-10 * 24 * time.Hour)

	casi := []struct {
		nome string
		path string
	}{
		{"finestra esplicita", f.percorso() + "?since=" + oltre.Format(time.RFC3339)},
		{
			"ripresa da una posizione antica",
			f.percorso() + "?last_event_id=" + jobs.StreamPosition{
				ScheduledFor: oltre,
				Environment:  jobs.EnvironmentProduction,
				Attempt:      1,
			}.Encode(),
		},
	}

	for _, caso := range casi {
		t.Run(caso.nome, func(t *testing.T) {
			_, resp := f.apri(caso.path)
			if resp.StatusCode != http.StatusForbidden {
				t.Fatalf("status = %d, atteso 403", resp.StatusCode)
			}
			var corpo httpapi.ErrorBody
			if err := json.NewDecoder(resp.Body).Decode(&corpo); err != nil {
				t.Fatalf("corpo non decodificabile: %v", err)
			}
			_ = resp.Body.Close()
			if corpo.Error.Code != "plan_limit_retention" {
				t.Errorf("code = %q, atteso plan_limit_retention", corpo.Error.Code)
			}
			if corpo.Error.Plan == "" {
				t.Error("un limite di piano deve nominare il piano: lì l'upgrade è davvero la risposta")
			}
		})
	}
}

// TestUnaPosizioneIlleggibileVieneRifiutata: un cursore di paginazione mandato
// come `Last-Event-ID` significherebbe «all'indietro» invece che «in avanti», ed
// è il motivo per cui le due codifiche hanno un marcatore diverso.
func TestUnaPosizioneIlleggibileVieneRifiutata(t *testing.T) {
	f := newFlussoFixture(t)

	cursorePaginazione := jobs.ExecutionCursor{
		ScheduledFor: f.clock.Now(),
		Environment:  jobs.EnvironmentProduction,
		Attempt:      1,
	}.Encode()

	for _, valore := range []string{"non-un-cursore", cursorePaginazione} {
		_, resp := f.apri(f.percorso(), func(r *http.Request) {
			r.Header.Set("Last-Event-ID", valore)
		})
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("%q: status = %d, atteso 400", valore, resp.StatusCode)
		}
		var corpo httpapi.ErrorBody
		if err := json.NewDecoder(resp.Body).Decode(&corpo); err != nil {
			t.Fatalf("corpo non decodificabile: %v", err)
		}
		_ = resp.Body.Close()
		if corpo.Error.Code != "invalid_cursor" {
			t.Errorf("%q: code = %q, atteso invalid_cursor", valore, corpo.Error.Code)
		}
	}
}

// TestIlFlussoDiUnJobAltruiNonEsiste: l'autorizzazione avviene una volta sola,
// all'apertura, e questa è la prova che avviene.
func TestIlFlussoDiUnJobAltruiNonEsiste(t *testing.T) {
	f := newFlussoFixture(t)
	altro := f.store.Seed(jobs.Job{ID: "job-di-un-altro", UserID: "un-altro-utente", Name: "altrui"})

	_, resp := f.apri("/jobs/" + altro.ID + "/executions/stream")
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, atteso 404", resp.StatusCode)
	}
	_ = resp.Body.Close()
	if f.hub.Open() != 0 {
		t.Error("un flusso rifiutato non deve lasciare un posto occupato")
	}
}

// TestLoStatoIntermedioNonPortaUnaPosizione è la regola su cui si regge la
// ripresa, vista dal filo: `pending` e `running` arrivano senza `id:`, l'esito
// con.
func TestLoStatoIntermedioNonPortaUnaPosizione(t *testing.T) {
	f := newFlussoFixture(t)
	client, _ := f.apri(f.percorso())

	f.esecuzione(time.Second, jobs.StatusRunning, "")
	f.hub.Poll()
	if got := client.prossimo(); got.ID != "" {
		t.Fatalf("uno stato intermedio è arrivato con una posizione: %+v", got)
	}

	f.esecuzione(time.Second, jobs.StatusSucceeded, "fatto")
	f.hub.Poll()
	got := client.prossimo()
	if got.ID == "" {
		t.Fatalf("l'esito è arrivato senza posizione: %+v", got)
	}
	if _, err := jobs.ParseStreamPosition(got.ID); err != nil {
		t.Errorf("la posizione consegnata non è rileggibile: %v", err)
	}
}

// TestUnaChiaveSenzaExecutionsReadNonApreIlFlusso: il flusso è la stessa lettura
// del registro, e non deve essere una scorciatoia per aggirarne lo scope (R9).
func TestUnaChiaveSenzaExecutionsReadNonApreIlFlusso(t *testing.T) {
	f := newFlussoFixture(t)
	chiave := f.creaChiave("jobs:read")

	_, resp := f.apri(f.percorso(), func(r *http.Request) {
		r.Header.Del("Cookie")
		r.Header.Set("Authorization", "Bearer "+chiave)
	})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, atteso 403", resp.StatusCode)
	}
	_ = resp.Body.Close()
}

func (f *flussoFixture) creaChiave(scopes ...string) string {
	f.t.Helper()
	rec := f.do(http.MethodPost, "/keys", map[string]any{
		"name": fmt.Sprintf("prova-%d", time.Now().UnixNano()), "scopes": scopes,
	}, withCookie(f.token))
	if rec.Code != http.StatusCreated {
		f.t.Fatalf("creazione della chiave: status %d, corpo %s", rec.Code, rec.Body)
	}
	var risposta httpapi.APIKeyCreatedResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &risposta); err != nil {
		f.t.Fatalf("lettura della chiave: %v", err)
	}
	return risposta.Secret
}
