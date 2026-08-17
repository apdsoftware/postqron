package httpexec_test

import (
	"encoding/json"
	"io"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/apdsoftware/postqron/services/api/internal/httpexec"
	"github.com/apdsoftware/postqron/services/api/internal/scheduler"
)

// guardDiProva è la sorgente del client nei test.
//
// In esercizio è `*netguard.Guard` e il suo client è l'unico protetto (R38).
// Qui serve la deroga per la stessa ragione per cui `netguard.AllowForTest`
// esiste dentro netguard: un `httptest.Server` vive su 127.0.0.1, che è
// esattamente ciò che il guard ha il compito di rifiutare, e senza un bersaglio
// HTTP vero l'esecutore non sarebbe verificabile su nulla di ciò che fa.
type guardDiProva struct {
	client   *http.Client
	chiamate atomic.Int64
}

func (g *guardDiProva) Client() *http.Client {
	g.chiamate.Add(1)
	return g.client
}

// nuovoEsecutore costruisce l'esecutore su un client dato.
func nuovoEsecutore(t *testing.T, client *http.Client, tune ...func(*httpexec.Options)) *httpexec.Executor {
	t.Helper()
	opts := httpexec.Options{Guard: &guardDiProva{client: client}}
	for _, f := range tune {
		f(&opts)
	}
	exec, err := httpexec.New(opts)
	if err != nil {
		t.Fatalf("httpexec.New: %v", err)
	}
	return exec
}

// jobSpec descrive il bersaglio di prova. I campi a zero prendono un default.
type jobSpec struct {
	URL     string
	Method  string
	Headers map[string]string
	Body    string
}

// occorrenza costruisce l'occorrenza che il worker pool passerebbe
// all'esecutore.
func occorrenza(t *testing.T, spec jobSpec) scheduler.Occurrence {
	t.Helper()

	if spec.Method == "" {
		spec.Method = http.MethodGet
	}
	var headers json.RawMessage
	if spec.Headers != nil {
		raw, err := json.Marshal(spec.Headers)
		if err != nil {
			t.Fatalf("serializzazione degli header: %v", err)
		}
		headers = raw
	}
	var body *string
	if spec.Body != "" {
		body = &spec.Body
	}

	return scheduler.Occurrence{
		Job: scheduler.Job{
			ID:           "11111111-1111-1111-1111-111111111111",
			UserID:       "22222222-2222-2222-2222-222222222222",
			Name:         "job-di-prova",
			Every:        time.Minute,
			Timezone:     "UTC",
			Environments: []string{"production"},
			URL:          spec.URL,
			Method:       spec.Method,
			Headers:      headers,
			Body:         body,
			Timeout:      30 * time.Second,
			Enabled:      true,
		},
		ScheduledFor: time.Date(2026, 8, 17, 10, 0, 0, 0, time.UTC),
		Environment:  "production",
		Attempt:      1,
		EnqueuedAt:   time.Date(2026, 8, 17, 10, 0, 0, 0, time.UTC),
	}
}

// richiestaRicevuta è ciò che il bersaglio ha visto arrivare.
type richiestaRicevuta struct {
	Method  string
	Path    string
	Query   string
	Host    string
	Headers http.Header
	Body    string
}

// registro raccoglie le richieste arrivate al bersaglio.
type registro struct {
	mu    sync.Mutex
	viste []richiestaRicevuta
}

func (r *registro) aggiungi(req richiestaRicevuta) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.viste = append(r.viste, req)
}

func (r *registro) tutte() []richiestaRicevuta {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]richiestaRicevuta(nil), r.viste...)
}

// leggi trascrive una richiesta arrivata al bersaglio.
func leggi(t *testing.T, r *http.Request) richiestaRicevuta {
	t.Helper()
	var corpo []byte
	if r.Body != nil {
		letto, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("lettura del corpo ricevuto: %v", err)
		}
		corpo = letto
	}
	return richiestaRicevuta{
		Method:  r.Method,
		Path:    r.URL.Path,
		Query:   r.URL.RawQuery,
		Host:    r.Host,
		Headers: r.Header.Clone(),
		Body:    string(corpo),
	}
}

// portaChiusa restituisce un indirizzo su cui nessuno è in ascolto: è il modo
// deterministico di ottenere una connessione rifiutata.
func portaChiusa(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("apertura del listener: %v", err)
	}
	addr := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatalf("chiusura del listener: %v", err)
	}
	return addr
}
