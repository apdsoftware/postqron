package mailronix

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// testKey è una chiave finta con la forma di quelle vere. Il segreto è una
// parola improbabile, così i test che verificano l'assenza di segreti nei log
// possono cercarla senza falsi positivi.
const (
	testKey    = "mrx_live_segretoinventatodaltest"
	testSecret = "segretoinventatodaltest"
)

// newTestClient costruisce un client verso un server di prova, senza attese
// reali fra i tentativi.
func newTestClient(t *testing.T, baseURL string, opts ...Option) *Client {
	t.Helper()
	base := []Option{
		WithSleep(func(context.Context, time.Duration) error { return nil }),
	}
	client, err := New(Config{
		APIKey:  testKey,
		BaseURL: baseURL,
		From:    "noreply@postqron.com",
	}, append(base, opts...)...)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return client
}

func sampleEmail() Email {
	return Email{
		To:      "utente@example.com",
		Subject: "Il tuo job è fallito",
		HTML:    "<html><body>corpo</body></html>",
		Text:    "corpo",
	}
}

// TestSendRichiestaConforme verifica la forma della richiesta: è il punto in
// cui il contratto di docs/reference/mailronix-openapi.json diventa codice.
func TestSendRichiestaConforme(t *testing.T) {
	type captured struct {
		method, path, auth, contentType, userAgent, accept string
		body                                               map[string]any
	}
	var got captured

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("lettura del corpo: %v", err)
		}
		got = captured{
			method:      r.Method,
			path:        r.URL.Path,
			auth:        r.Header.Get("Authorization"),
			contentType: r.Header.Get("Content-Type"),
			userAgent:   r.Header.Get("User-Agent"),
			accept:      r.Header.Get("Accept"),
		}
		if err := json.Unmarshal(raw, &got.body); err != nil {
			t.Errorf("il corpo non è JSON: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_, _ = io.WriteString(w, `{"status":"queued","email_log_id":"9c4e2f1a-7d3b-4a5e-8f6c-1b2a3d4e5f6a"}`)
	}))
	defer server.Close()

	receipt, err := newTestClient(t, server.URL).Send(t.Context(), sampleEmail())
	if err != nil {
		t.Fatalf("Send: %v", err)
	}

	if got.method != http.MethodPost {
		t.Errorf("metodo = %q, atteso POST", got.method)
	}
	if got.path != Path {
		t.Errorf("percorso = %q, atteso %q", got.path, Path)
	}
	if want := "Bearer " + testKey; got.auth != want {
		t.Errorf("Authorization = %q, atteso %q", got.auth, want)
	}
	if got.contentType != "application/json" {
		t.Errorf("Content-Type = %q", got.contentType)
	}
	// Senza User-Agent esplicito la richiesta si ferma al blocco bot di
	// Cloudflare con 403 error code: 1010 e non raggiunge mai Mailronix.
	if got.userAgent != DefaultUserAgent {
		t.Errorf("User-Agent = %q, atteso %q", got.userAgent, DefaultUserAgent)
	}
	if got.accept != "application/json" {
		t.Errorf("Accept = %q", got.accept)
	}

	if _, ok := got.body["to"].(string); !ok {
		t.Errorf("`to` = %#v, atteso una stringa: il contratto vuole un solo destinatario", got.body["to"])
	}
	for _, field := range []string{"template_id", "variables"} {
		if _, present := got.body[field]; present {
			// R20: la logica di template sta nel repository, non su Mailronix,
			// e nel contratto questi campi escludono subject/html_body.
			t.Errorf("il payload contiene %q: la modalità dev'essere a contenuto diretto", field)
		}
	}
	for field, want := range map[string]string{
		"from":      "noreply@postqron.com",
		"to":        "utente@example.com",
		"subject":   "Il tuo job è fallito",
		"html_body": "<html><body>corpo</body></html>",
		"text_body": "corpo",
	} {
		if got.body[field] != want {
			t.Errorf("%s = %#v, atteso %q", field, got.body[field], want)
		}
	}

	if receipt.Status != StatusQueued {
		t.Errorf("Status = %q, atteso %q", receipt.Status, StatusQueued)
	}
	if receipt.EmailLogID != "9c4e2f1a-7d3b-4a5e-8f6c-1b2a3d4e5f6a" {
		t.Errorf("EmailLogID = %q", receipt.EmailLogID)
	}
}

// TestSendCorpoTestualeSolo verifica che il campo vuoto sparisca dal payload
// invece di arrivare come stringa vuota.
func TestSendCorpoTestualeSolo(t *testing.T) {
	var body map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		w.WriteHeader(http.StatusAccepted)
		_, _ = io.WriteString(w, `{"status":"queued","email_log_id":"id"}`)
	}))
	defer server.Close()

	email := sampleEmail()
	email.HTML = ""
	if _, err := newTestClient(t, server.URL).Send(t.Context(), email); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if _, present := body["html_body"]; present {
		t.Errorf("html_body presente pur essendo vuoto: %#v", body["html_body"])
	}
	if body["text_body"] != "corpo" {
		t.Errorf("text_body = %#v", body["text_body"])
	}
}

// TestSendMessaggioNonValido verifica i controlli locali: un 400 non è
// ritentabile e consuma comunque quota, quindi ciò che si può escludere prima
// di partire si esclude prima di partire.
func TestSendMessaggioNonValido(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusAccepted)
	}))
	defer server.Close()
	client := newTestClient(t, server.URL)

	cases := map[string]Email{
		"destinatario mancante":   {Subject: "x", Text: "y"},
		"destinatario malformato": {To: "non-un-indirizzo", Subject: "x", Text: "y"},
		"due destinatari":         {To: "a@example.com, b@example.com", Subject: "x", Text: "y"},
		"oggetto mancante":        {To: "a@example.com", Text: "y"},
		"oggetto di soli spazi":   {To: "a@example.com", Subject: "   ", Text: "y"},
		"nessun corpo":            {To: "a@example.com", Subject: "x"},
	}
	for name, email := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := client.Send(t.Context(), email)
			if !errors.Is(err, ErrInvalidEmail) {
				t.Fatalf("errore = %v, atteso ErrInvalidEmail", err)
			}
		})
	}
	if n := requests.Load(); n != 0 {
		t.Errorf("il server ha ricevuto %d richieste, attese 0", n)
	}
}

// TestSendBloccoCloudflare è il caso che fa perdere più tempo in produzione: la
// risposta è un 403 come quello del dominio non verificato, ma la richiesta non
// ha mai raggiunto Mailronix e il corpo non è il JSON documentato.
func TestSendBloccoCloudflare(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "text/plain; charset=UTF-8")
		w.WriteHeader(http.StatusForbidden)
		_, _ = io.WriteString(w, "error code: 1010")
	}))
	defer server.Close()

	_, err := newTestClient(t, server.URL).Send(t.Context(), sampleEmail())

	var transportErr *TransportError
	if !errors.As(err, &transportErr) {
		t.Fatalf("errore = %#v, atteso *TransportError", err)
	}
	if !transportErr.BotBlocked() {
		t.Error("BotBlocked = false: il blocco non è stato riconosciuto")
	}
	if transportErr.CloudflareCode != 1010 {
		t.Errorf("CloudflareCode = %d, atteso 1010", transportErr.CloudflareCode)
	}
	if transportErr.StatusCode != http.StatusForbidden {
		t.Errorf("StatusCode = %d, atteso 403", transportErr.StatusCode)
	}

	// Non va interpretato con i codici applicativi di Mailronix: quel 403 non
	// dice «dominio non verificato», non dice niente.
	if code := Code(err); code != "" {
		t.Errorf("Code = %q, atteso vuoto: un blocco di Cloudflare non ha un codice Mailronix", code)
	}
	if Retryable(err) {
		t.Error("Retryable = true: un blocco di Cloudflare non si risolve riprovando")
	}
	if n := requests.Load(); n != 1 {
		t.Errorf("richieste = %d, attesa 1: non si ritenta", n)
	}

	// Il messaggio deve indirizzare verso la causa vera.
	if msg := err.Error(); !strings.Contains(msg, "Cloudflare") || !strings.Contains(msg, "User-Agent") {
		t.Errorf("il messaggio non indirizza verso la causa: %q", msg)
	}
}

// TestSendBloccoCloudflareAltriCodici copre le altre pagine di blocco, che
// hanno la stessa forma.
func TestSendBloccoCloudflareAltriCodici(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		status     int
		want       int
	}{
		{"1020 access denied", "error code: 1020", http.StatusForbidden, 1020},
		{"1015 rate limited", "error code: 1015", http.StatusTooManyRequests, 1015},
		{"pagina HTML", "<html><body>Error code: 1010</body></html>", http.StatusForbidden, 1010},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, tc.body)
			}))
			defer server.Close()

			_, err := newTestClient(t, server.URL).Send(t.Context(), sampleEmail())
			var transportErr *TransportError
			if !errors.As(err, &transportErr) {
				t.Fatalf("errore = %#v, atteso *TransportError", err)
			}
			if transportErr.CloudflareCode != tc.want {
				t.Errorf("CloudflareCode = %d, atteso %d", transportErr.CloudflareCode, tc.want)
			}
			// Anche un 1015 su 429 resta di trasporto: non è il rate limit per
			// chiave di Mailronix, e ritentarlo non è previsto da nessun
			// contratto.
			if Retryable(err) {
				t.Error("Retryable = true su una pagina di blocco")
			}
		})
	}
}

// TestSendErroriApplicativi copre gli errori documentati che non si ritentano.
func TestSendErroriApplicativi(t *testing.T) {
	cases := []struct {
		name   string
		status int
		code   string
	}{
		{"payload non valido", http.StatusBadRequest, CodeInvalidRequest},
		{"chiave sconosciuta", http.StatusUnauthorized, CodeUnauthenticated},
		{"dominio non verificato", http.StatusForbidden, CodeDomainNotVerified},
		{"tenant sospeso", http.StatusForbidden, CodeTenantSuspended},
		{"limite del piano", http.StatusForbidden, CodePlanLimitExceeded},
		{"template inesistente", http.StatusNotFound, CodeTemplateNotFound},
		{"errore interno", http.StatusInternalServerError, CodeInternalError},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				requests.Add(1)
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tc.status)
				_ = json.NewEncoder(w).Encode(map[string]any{
					"error": map[string]string{"code": tc.code, "message": "descrizione"},
				})
			}))
			defer server.Close()

			_, err := newTestClient(t, server.URL).Send(t.Context(), sampleEmail())

			var apiErr *APIError
			if !errors.As(err, &apiErr) {
				t.Fatalf("errore = %#v, atteso *APIError", err)
			}
			if apiErr.Code != tc.code {
				t.Errorf("Code = %q, atteso %q", apiErr.Code, tc.code)
			}
			if apiErr.StatusCode != tc.status {
				t.Errorf("StatusCode = %d, atteso %d", apiErr.StatusCode, tc.status)
			}
			if Retryable(err) {
				t.Errorf("Retryable = true su %d: ritentarlo consuma quota senza cambiare esito", tc.status)
			}
			if n := requests.Load(); n != 1 {
				t.Errorf("richieste = %d, attesa 1", n)
			}
			if Code(err) != tc.code {
				t.Errorf("Code(err) = %q", Code(err))
			}
		})
	}
}

// TestSendRitentaTransitori verifica i due soli errori ritentabili.
func TestSendRitentaTransitori(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		code   string
	}{
		{"rate limit per chiave", http.StatusTooManyRequests, CodeRateLimited},
		{"verifica della chiave non disponibile", http.StatusServiceUnavailable, CodeAuthUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				if requests.Add(1) < 3 {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(tc.status)
					_, _ = io.WriteString(w, `{"error":{"code":"`+tc.code+`","message":"riprova"}}`)
					return
				}
				w.WriteHeader(http.StatusAccepted)
				_, _ = io.WriteString(w, `{"status":"queued","email_log_id":"riuscito"}`)
			}))
			defer server.Close()

			receipt, err := newTestClient(t, server.URL).Send(t.Context(), sampleEmail())
			if err != nil {
				t.Fatalf("Send: %v", err)
			}
			if receipt.EmailLogID != "riuscito" {
				t.Errorf("EmailLogID = %q", receipt.EmailLogID)
			}
			if n := requests.Load(); n != 3 {
				t.Errorf("richieste = %d, attese 3", n)
			}
		})
	}
}

// TestSendSiArrendeDopoIlTetto verifica che i tentativi finiscano.
func TestSendSiArrendeDopoIlTetto(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = io.WriteString(w, `{"error":{"code":"rate_limited","message":"troppe richieste"}}`)
	}))
	defer server.Close()

	client := newTestClient(t, server.URL, WithMaxAttempts(4))
	_, err := client.Send(t.Context(), sampleEmail())

	if Code(err) != CodeRateLimited {
		t.Fatalf("errore = %v, atteso rate_limited", err)
	}
	if n := requests.Load(); n != 4 {
		t.Errorf("richieste = %d, attese 4", n)
	}
}

// TestSendUnSoloTentativo verifica che WithMaxAttempts(1) disattivi i retry.
func TestSendUnSoloTentativo(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = io.WriteString(w, `{"error":{"code":"auth_unavailable","message":"non disponibile"}}`)
	}))
	defer server.Close()

	if _, err := newTestClient(t, server.URL, WithMaxAttempts(1)).Send(t.Context(), sampleEmail()); err == nil {
		t.Fatal("errore atteso")
	}
	if n := requests.Load(); n != 1 {
		t.Errorf("richieste = %d, attesa 1", n)
	}
}

// TestSendRispettaRetryAfter verifica che l'attesa suggerita dal servizio abbia
// la precedenza sul backoff quando è più lunga.
func TestSendRispettaRetryAfter(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if requests.Add(1) == 1 {
			w.Header().Set("Retry-After", "42")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = io.WriteString(w, `{"error":{"code":"rate_limited","message":"troppe"}}`)
			return
		}
		w.WriteHeader(http.StatusAccepted)
		_, _ = io.WriteString(w, `{"status":"queued","email_log_id":"id"}`)
	}))
	defer server.Close()

	var waited []time.Duration
	client := newTestClient(t, server.URL, WithSleep(func(_ context.Context, d time.Duration) error {
		waited = append(waited, d)
		return nil
	}))
	if _, err := client.Send(t.Context(), sampleEmail()); err != nil {
		t.Fatalf("Send: %v", err)
	}

	if len(waited) != 1 {
		t.Fatalf("attese = %v, attesa una sola", waited)
	}
	if waited[0] != 42*time.Second {
		t.Errorf("attesa = %v, attesi 42s dal Retry-After", waited[0])
	}
}

// TestSendBackoffCrescente verifica che le attese non siano tutte uguali: un
// retry immediato ripetuto è un modo per farsi bandire.
func TestSendBackoffCrescente(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = io.WriteString(w, `{"error":{"code":"rate_limited","message":"troppe"}}`)
	}))
	defer server.Close()

	var waited []time.Duration
	client := newTestClient(t, server.URL,
		WithMaxAttempts(4),
		WithSleep(func(_ context.Context, d time.Duration) error {
			waited = append(waited, d)
			return nil
		}))
	if _, err := client.Send(t.Context(), sampleEmail()); err == nil {
		t.Fatal("errore atteso")
	}

	want := []time.Duration{500 * time.Millisecond, time.Second, 2 * time.Second}
	if len(waited) != len(want) {
		t.Fatalf("attese = %v, attese %v", waited, want)
	}
	for i := range want {
		if waited[i] != want[i] {
			t.Errorf("attesa %d = %v, attesa %v", i+1, waited[i], want[i])
		}
	}
}

// TestSendRispostaNonInterpretabile copre l'intermediario che risponde con
// qualcosa che non è il JSON documentato — un gateway, un proxy, una pagina di
// manutenzione.
func TestSendRispostaNonInterpretabile(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
	}{
		{"pagina HTML del gateway", http.StatusBadGateway, "<html><h1>502 Bad Gateway</h1></html>"},
		{"JSON senza busta error", http.StatusBadRequest, `{"messaggio":"boh"}`},
		{"busta error senza codice", http.StatusBadRequest, `{"error":{"message":"senza codice"}}`},
		{"corpo vuoto", http.StatusForbidden, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, tc.body)
			}))
			defer server.Close()

			_, err := newTestClient(t, server.URL).Send(t.Context(), sampleEmail())
			var transportErr *TransportError
			if !errors.As(err, &transportErr) {
				t.Fatalf("errore = %#v, atteso *TransportError", err)
			}
			if transportErr.BotBlocked() {
				t.Error("BotBlocked = true senza pagina di blocco")
			}
			if Retryable(err) {
				t.Error("Retryable = true su un errore di trasporto")
			}
		})
	}
}

// TestSend202SenzaRicevuta: un 202 senza email_log_id non è una ricevuta. Può
// venire da un intermediario, e restituire un identificativo vuoto sarebbe
// peggio che ammettere di non averlo.
func TestSend202SenzaRicevuta(t *testing.T) {
	for _, body := range []string{"", "ok", `{"status":"queued"}`} {
		t.Run(body, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusAccepted)
				_, _ = io.WriteString(w, body)
			}))
			defer server.Close()

			receipt, err := newTestClient(t, server.URL).Send(t.Context(), sampleEmail())
			var transportErr *TransportError
			if !errors.As(err, &transportErr) {
				t.Fatalf("errore = %#v, atteso *TransportError", err)
			}
			if receipt.EmailLogID != "" {
				t.Errorf("EmailLogID = %q, atteso vuoto", receipt.EmailLogID)
			}
		})
	}
}

// TestSendRifiutaRedirect: seguire un redirect significherebbe mandare
// l'intestazione Authorization dove non deve andare.
func TestSendRifiutaRedirect(t *testing.T) {
	altrove := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("il redirect è stato seguito, con Authorization = %q", r.Header.Get("Authorization"))
		w.WriteHeader(http.StatusAccepted)
	}))
	defer altrove.Close()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, altrove.URL+Path, http.StatusTemporaryRedirect)
	}))
	defer server.Close()

	_, err := newTestClient(t, server.URL).Send(t.Context(), sampleEmail())
	var transportErr *TransportError
	if !errors.As(err, &transportErr) {
		t.Fatalf("errore = %#v, atteso *TransportError", err)
	}
}

// TestSendServerIrraggiungibile verifica che un errore di rete resti di
// trasporto e non venga ritentato: senza risposta non si sa se il messaggio sia
// stato accodato, e un tentativo in più lo recapiterebbe due volte.
func TestSendServerIrraggiungibile(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := server.URL
	server.Close()

	var attese int
	client := newTestClient(t, url, WithSleep(func(context.Context, time.Duration) error {
		attese++
		return nil
	}))
	_, err := client.Send(t.Context(), sampleEmail())

	var transportErr *TransportError
	if !errors.As(err, &transportErr) {
		t.Fatalf("errore = %#v, atteso *TransportError", err)
	}
	if transportErr.StatusCode != 0 {
		t.Errorf("StatusCode = %d, atteso 0: non c'è stata risposta", transportErr.StatusCode)
	}
	if attese != 0 {
		t.Errorf("attese fra i tentativi = %d, attese 0", attese)
	}
}

// TestSendContestoAnnullato verifica che l'attesa fra i tentativi non
// sopravviva alla cancellazione del contesto.
func TestSendContestoAnnullato(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = io.WriteString(w, `{"error":{"code":"rate_limited","message":"troppe"}}`)
	}))
	defer server.Close()

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	client := newTestClient(t, server.URL, WithSleep(func(ctx context.Context, _ time.Duration) error {
		cancel()
		return ctx.Err()
	}))
	// Il primo tentativo passa, la prima attesa annulla: l'errore che risale è
	// quello del contesto, non un altro 429.
	if _, err := client.Send(ctx, sampleEmail()); !errors.Is(err, context.Canceled) {
		t.Fatalf("errore = %v, atteso context.Canceled", err)
	}
}

// TestSendCorpoEnorme verifica il tetto alla lettura: una pagina di errore di
// un intermediario può essere lunga a piacere.
func TestSendCorpoEnorme(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = io.WriteString(w, strings.Repeat("x", 4*maxBodyBytes))
	}))
	defer server.Close()

	_, err := newTestClient(t, server.URL).Send(t.Context(), sampleEmail())
	var transportErr *TransportError
	if !errors.As(err, &transportErr) {
		t.Fatalf("errore = %#v, atteso *TransportError", err)
	}
	if len(transportErr.Snippet) > snippetLimit+len("…") {
		t.Errorf("frammento lungo %d caratteri", len(transportErr.Snippet))
	}
	if len(err.Error()) > 4*snippetLimit {
		t.Errorf("messaggio d'errore lungo %d caratteri: finirebbe in un log", len(err.Error()))
	}
}

func TestNewRifiutaConfigurazioneInvalida(t *testing.T) {
	cases := map[string]Config{
		"chiave mancante":     {From: "noreply@postqron.com"},
		"chiave di spazi":     {APIKey: "   ", From: "noreply@postqron.com"},
		"mittente mancante":   {APIKey: testKey},
		"mittente malformato": {APIKey: testKey, From: "non-un-indirizzo"},
		"mittente con nome":   {APIKey: testKey, From: "Postqron <noreply@postqron.com>"},
		"URL senza schema":    {APIKey: testKey, From: "noreply@postqron.com", BaseURL: "api.mailronix.com"},
		"URL non http":        {APIKey: testKey, From: "noreply@postqron.com", BaseURL: "ftp://api.mailronix.com"},
	}
	for name, cfg := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := New(cfg); err == nil {
				t.Fatal("errore atteso")
			}
		})
	}
}

func TestNewURLPredefinito(t *testing.T) {
	client, err := New(Config{APIKey: testKey, From: "noreply@postqron.com"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if client.baseURL != DefaultBaseURL {
		t.Errorf("baseURL = %q, atteso %q", client.baseURL, DefaultBaseURL)
	}
}

func TestNewTogliLaBarraFinale(t *testing.T) {
	client, err := New(Config{
		APIKey:  testKey,
		From:    "noreply@postqron.com",
		BaseURL: "https://api.mailronix.com/",
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if client.baseURL != "https://api.mailronix.com" {
		t.Errorf("baseURL = %q: la barra finale raddoppierebbe quella di Path", client.baseURL)
	}
}
