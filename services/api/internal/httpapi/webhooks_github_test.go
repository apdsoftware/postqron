package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/apdsoftware/postqron/services/api/internal/config"
	"github.com/apdsoftware/postqron/services/api/internal/githubhook"
	"github.com/apdsoftware/postqron/services/api/internal/httpapi"
)

const (
	rottaWebhook   = "/webhooks/github"
	segretoWebhook = "segreto-del-webhook-di-prova"
)

// payloadPushHTTP è un payload `push` con i soli campi che leggiamo.
const payloadPushHTTP = `{
  "ref": "refs/heads/main",
  "after": "2222222222222222222222222222222222222222",
  "repository": {"id": 42, "name": "infra", "full_name": "acme/infra",
                 "owner": {"login": "acme"}, "default_branch": "main"},
  "installation": {"id": 7}
}`

// ------------------------------------------------------------------ finzioni

type archivioConsegne struct {
	mu    sync.Mutex
	stati map[string]githubhook.Status
	err   error
}

func nuovoArchivioConsegne() *archivioConsegne {
	return &archivioConsegne{stati: make(map[string]githubhook.Status)}
}

func (a *archivioConsegne) Claim(_ context.Context, consegna githubhook.Delivery) (bool, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.err != nil {
		return false, a.err
	}
	if stato, vista := a.stati[consegna.ID]; vista && stato != githubhook.StatusFailed {
		return false, nil
	}
	a.stati[consegna.ID] = githubhook.StatusReceived
	return true, nil
}

func (a *archivioConsegne) Complete(_ context.Context, id string, stato githubhook.Status, _ string, _ time.Time) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.stati[id] = stato
	return nil
}

func (a *archivioConsegne) registrate() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.stati)
}

type consumatorePush struct {
	mu       sync.Mutex
	ricevute int
	err      error
}

func (c *consumatorePush) HandlePush(_ context.Context, _ githubhook.PushEvent) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.err != nil {
		return c.err
	}
	c.ricevute++
	return nil
}

func (c *consumatorePush) conteggio() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.ricevute
}

// ------------------------------------------------------------------ supporto

func routerConWebhook(t *testing.T, store githubhook.Store, sink githubhook.PushSink) http.Handler {
	t.Helper()
	cfg, err := config.LoadFrom(func(string) string { return "" })
	if err != nil {
		t.Fatalf("configurazione di default non valida: %v", err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	svc, err := githubhook.NewService(githubhook.Options{
		Secret: segretoWebhook,
		Store:  store,
		Sink:   sink,
		Logger: logger,
	})
	if err != nil {
		t.Fatalf("costruzione del servizio githubhook: %v", err)
	}
	return httpapi.NewRouter(cfg, "test", logger, httpapi.Deps{GitHubWebhook: svc})
}

// consegna compone una richiesta HTTP come la manda GitHub. `firma` vuota
// significa «firmata correttamente».
func consegna(corpo []byte, evento, id, firma string) *http.Request {
	if firma == "" {
		firma = githubhook.Sign([]byte(segretoWebhook), corpo)
	}
	req := httptest.NewRequest(http.MethodPost, rottaWebhook, bytes.NewReader(corpo))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(githubhook.HeaderSignature, firma)
	req.Header.Set(githubhook.HeaderEvent, evento)
	req.Header.Set(githubhook.HeaderDelivery, id)
	return req
}

func decodeWebhookResponse(t *testing.T, rec *httptest.ResponseRecorder) httpapi.GitHubWebhookResponse {
	t.Helper()
	var body httpapi.GitHubWebhookResponse
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("corpo non decodificabile: %v", err)
	}
	return body
}

func decodeWebhookError(t *testing.T, rec *httptest.ResponseRecorder) httpapi.ErrorBody {
	t.Helper()
	var body httpapi.ErrorBody
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("corpo non decodificabile: %v", err)
	}
	return body
}

// ------------------------------------------------------------------ i test

func TestWebhookGitHubAccettaUnaPushFirmata(t *testing.T) {
	store, sink := nuovoArchivioConsegne(), &consumatorePush{}
	router := routerConWebhook(t, store, sink)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, consegna([]byte(payloadPushHTTP), "push", "consegna-http-1", ""))

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, atteso 202: %s", rec.Code, rec.Body.String())
	}
	body := decodeWebhookResponse(t, rec)
	if body.Outcome != string(githubhook.OutcomeAccepted) {
		t.Errorf("outcome = %q", body.Outcome)
	}
	if body.Delivery != "consegna-http-1" || body.Event != "push" {
		t.Errorf("risposta = %+v", body)
	}
	if sink.conteggio() != 1 {
		t.Errorf("il consumatore ha ricevuto %d push, attesa 1", sink.conteggio())
	}
}

// TestWebhookGitHubRifiutaLeRichiesteNonFirmate è il rifiuto di R11 visto dal
// lato HTTP: tre modi di sbagliare la firma, un solo codice di errore.
func TestWebhookGitHubRifiutaLeRichiesteNonFirmate(t *testing.T) {
	corpo := []byte(payloadPushHTTP)
	valida := githubhook.Sign([]byte(segretoWebhook), corpo)
	alterato := append([]byte(nil), corpo...)
	alterato[len(alterato)-1] = ' '

	casi := []struct {
		nome  string
		corpo []byte
		firma string
	}{
		{"firma assente", corpo, "assente"},
		{"firma errata", corpo, githubhook.Sign([]byte("un-altro-segreto-lungo"), corpo)},
		{"firma di forma sbagliata", corpo, "sha256=non-esadecimale"},
		{"algoritmo diverso", corpo, "sha1=" + strings.TrimPrefix(valida, "sha256=")},
		{"corpo alterato dopo la firma", alterato, valida},
	}

	for _, caso := range casi {
		t.Run(caso.nome, func(t *testing.T) {
			store, sink := nuovoArchivioConsegne(), &consumatorePush{}
			router := routerConWebhook(t, store, sink)

			req := consegna(caso.corpo, "push", "consegna-non-firmata", caso.firma)
			if caso.firma == "assente" {
				req.Header.Del(githubhook.HeaderSignature)
			}

			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)

			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, atteso 401: %s", rec.Code, rec.Body.String())
			}
			if code := decodeWebhookError(t, rec).Error.Code; code != "invalid_signature" {
				t.Errorf("code = %q, atteso invalid_signature", code)
			}
			// Nessun effetto: né una riga registrata né una push consegnata.
			if store.registrate() != 0 {
				t.Errorf("registrate %d consegne, attese 0", store.registrate())
			}
			if sink.conteggio() != 0 {
				t.Errorf("il consumatore è stato chiamato %d volte", sink.conteggio())
			}
		})
	}
}

// TestWebhookGitHubNonRivelaCosaContenevaLaRichiestaRifiutata: la risposta a una
// richiesta non verificata non deve restituire niente di ciò che conteneva. È
// un endpoint pubblico, e ciò che risponde lo legge chi ha inviato.
func TestWebhookGitHubNonRivelaCosaContenevaLaRichiestaRifiutata(t *testing.T) {
	store, sink := nuovoArchivioConsegne(), &consumatorePush{}
	router := routerConWebhook(t, store, sink)

	corpo := []byte(`{"ref":"refs/heads/segretissimo","repository":{"full_name":"acme/riservato"}}`)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, consegna(corpo, "push", "consegna-riservata", "sha256=00"))

	risposta := rec.Body.String()
	for _, vietato := range []string{"segretissimo", "riservato", "consegna-riservata", "acme"} {
		if strings.Contains(risposta, vietato) {
			t.Errorf("la risposta contiene %q: %s", vietato, risposta)
		}
	}
}

// TestWebhookGitHubIgnoraLaConsegnaRipetuta è il punto 3 di R11 dal lato HTTP:
// è quello che succede premendo «Redeliver» nel registro dell'App.
func TestWebhookGitHubIgnoraLaConsegnaRipetuta(t *testing.T) {
	store, sink := nuovoArchivioConsegne(), &consumatorePush{}
	router := routerConWebhook(t, store, sink)

	primo := httptest.NewRecorder()
	router.ServeHTTP(primo, consegna([]byte(payloadPushHTTP), "push", "consegna-ripetuta", ""))
	if primo.Code != http.StatusAccepted {
		t.Fatalf("prima consegna: status = %d", primo.Code)
	}

	secondo := httptest.NewRecorder()
	router.ServeHTTP(secondo, consegna([]byte(payloadPushHTTP), "push", "consegna-ripetuta", ""))

	// 200 e non 202: la consegna è stata riconosciuta, ma non è successo niente
	// di nuovo. Un errore farebbe ripetere GitHub all'infinito.
	if secondo.Code != http.StatusOK {
		t.Fatalf("seconda consegna: status = %d, atteso 200: %s", secondo.Code, secondo.Body.String())
	}
	if outcome := decodeWebhookResponse(t, secondo).Outcome; outcome != string(githubhook.OutcomeDuplicate) {
		t.Errorf("outcome = %q, atteso duplicate", outcome)
	}
	if sink.conteggio() != 1 {
		t.Errorf("il consumatore ha ricevuto %d push, attesa 1", sink.conteggio())
	}
}

func TestWebhookGitHubRispondeAlPing(t *testing.T) {
	store, sink := nuovoArchivioConsegne(), &consumatorePush{}
	router := routerConWebhook(t, store, sink)

	corpo := []byte(`{"zen":"Speak like a human.","hook_id":99,"app_id":4622302}`)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, consegna(corpo, "ping", "consegna-ping", ""))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, atteso 200: %s", rec.Code, rec.Body.String())
	}
	if outcome := decodeWebhookResponse(t, rec).Outcome; outcome != string(githubhook.OutcomePong) {
		t.Errorf("outcome = %q, atteso pong", outcome)
	}
	if sink.conteggio() != 0 {
		t.Error("il ping è arrivato al consumatore delle push")
	}
}

func TestWebhookGitHubRifiutaUnaRichiestaFirmataMaMalformata(t *testing.T) {
	casi := []struct {
		nome    string
		corpo   string
		evento  string
		id      string
		toglila string // testata da rimuovere
	}{
		{nome: "payload illeggibile", corpo: `{"ref":`, evento: "push", id: "consegna-x"},
		{nome: "payload senza repository", corpo: `{"ref":"refs/heads/main"}`, evento: "push", id: "consegna-x"},
		{nome: "senza identificativo di consegna", corpo: payloadPushHTTP, evento: "push", id: "x",
			toglila: githubhook.HeaderDelivery},
		{nome: "senza evento", corpo: payloadPushHTTP, evento: "push", id: "consegna-x",
			toglila: githubhook.HeaderEvent},
	}

	for _, caso := range casi {
		t.Run(caso.nome, func(t *testing.T) {
			store, sink := nuovoArchivioConsegne(), &consumatorePush{}
			router := routerConWebhook(t, store, sink)

			req := consegna([]byte(caso.corpo), caso.evento, caso.id, "")
			if caso.toglila != "" {
				req.Header.Del(caso.toglila)
			}

			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, atteso 400: %s", rec.Code, rec.Body.String())
			}
			if code := decodeWebhookError(t, rec).Error.Code; code != "invalid_request" {
				t.Errorf("code = %q, atteso invalid_request", code)
			}
			if sink.conteggio() != 0 {
				t.Error("una richiesta malformata è arrivata al consumatore")
			}
		})
	}
}

// TestWebhookGitHubRifiutaUnCorpoEnorme: il corpo va letto per intero prima di
// poter dire se la richiesta è legittima, quindi il tetto serve *prima* della
// verifica. Senza, chiunque potrebbe farci allocare memoria a piacere.
func TestWebhookGitHubRifiutaUnCorpoEnorme(t *testing.T) {
	store, sink := nuovoArchivioConsegne(), &consumatorePush{}
	router := routerConWebhook(t, store, sink)

	enorme := bytes.Repeat([]byte("a"), (2<<20)+1)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, consegna(enorme, "push", "consegna-enorme", ""))

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, atteso 413: %s", rec.Code, rec.Body.String())
	}
	if store.registrate() != 0 || sink.conteggio() != 0 {
		t.Error("un corpo oltre il limite ha prodotto effetti")
	}
}

// TestWebhookGitHubRispondeCinquecentoQuandoLaLavorazioneFallisce: è la
// risposta che fa ripetere GitHub, ed è voluta.
func TestWebhookGitHubRispondeCinquecentoQuandoLaLavorazioneFallisce(t *testing.T) {
	store, sink := nuovoArchivioConsegne(), &consumatorePush{}
	sink.err = errors.New("database non raggiungibile")
	router := routerConWebhook(t, store, sink)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, consegna([]byte(payloadPushHTTP), "push", "consegna-fallita", ""))

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, atteso 500: %s", rec.Code, rec.Body.String())
	}
	// Il dettaglio del guasto resta nei log, non nella risposta.
	if strings.Contains(rec.Body.String(), "database non raggiungibile") {
		t.Errorf("la risposta espone il guasto interno: %s", rec.Body.String())
	}
}

// TestWebhookGitHubNonRegistratoSenzaServizio: senza segreto configurato la
// rotta non esiste. Non è un caso limite — è la configurazione di chiunque non
// abbia la GitHub App, e un endpoint pubblico che non verifica niente sarebbe
// peggio dell'assenza della funzionalità.
func TestWebhookGitHubNonRegistratoSenzaServizio(t *testing.T) {
	rec := httptest.NewRecorder()
	newRouter(t).ServeHTTP(rec, consegna([]byte(payloadPushHTTP), "push", "consegna-x", ""))

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, atteso 404", rec.Code)
	}
}

func TestWebhookGitHubAccettaSoloPOST(t *testing.T) {
	router := routerConWebhook(t, nuovoArchivioConsegne(), &consumatorePush{})

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, rottaWebhook, nil))

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, atteso 405", rec.Code)
	}
}
