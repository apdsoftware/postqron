package httpapi_test

import (
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
	"github.com/apdsoftware/postqron/services/api/internal/httpapi"
	"github.com/apdsoftware/postqron/services/api/internal/paddle"
)

// Questi test fanno entrare gli eventi **dall'handler vero**, firmati con la
// chiave di prova: non esiste una scorciatoia che salti la verifica della firma,
// perché è precisamente ciò che si vuole esercitare. Nessuna rete: il collaudo
// di un pagamento in sandbox è la issue #478.

const (
	rottaPaddle   = "/webhooks/paddle"
	segretoPaddle = "pdl_ntfset_segreto_di_prova_lungo"
)

var istantePaddle = time.Date(2026, 8, 17, 10, 0, 0, 0, time.UTC)

// eventoPaddle è un payload `subscription.updated` con i soli campi che
// leggiamo.
func eventoPaddle(eventID, quando string) string {
	return `{
	  "event_id": "` + eventID + `",
	  "event_type": "subscription.updated",
	  "occurred_at": "` + quando + `",
	  "data": {
	    "id": "sub_01",
	    "status": "active",
	    "customer_id": "ctm_01",
	    "items": [{"status": "active", "price": {"id": "pri_pro_m"}}],
	    "custom_data": {"user_id": "11111111-1111-4111-8111-111111111111"}
	  }
	}`
}

// ------------------------------------------------------------------ finzioni

type archivioEventi struct {
	mu    sync.Mutex
	stati map[string]paddle.Status
	err   error
}

func nuovoArchivioEventi() *archivioEventi {
	return &archivioEventi{stati: make(map[string]paddle.Status)}
}

func (a *archivioEventi) Claim(_ context.Context, record paddle.Record) (bool, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.err != nil {
		return false, a.err
	}
	if stato, visto := a.stati[record.ID]; visto && stato != paddle.StatusFailed {
		return false, nil
	}
	a.stati[record.ID] = paddle.StatusReceived
	return true, nil
}

func (a *archivioEventi) Complete(_ context.Context, id string, stato paddle.Status, _ string, _ time.Time) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.stati[id] = stato
	return nil
}

func (a *archivioEventi) registrati() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.stati)
}

type consumatoreEntitlement struct {
	mu       sync.Mutex
	ricevute int
	fuoriUso bool
	err      error
}

func (c *consumatoreEntitlement) ApplySubscription(_ context.Context, _ paddle.Subscription) (bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.err != nil {
		return false, c.err
	}
	c.ricevute++
	return !c.fuoriUso, nil
}

func (c *consumatoreEntitlement) applicate() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.ricevute
}

// ------------------------------------------------------------------ supporto

func routerPaddle(t *testing.T, store paddle.Store, sink paddle.EntitlementSink) http.Handler {
	t.Helper()
	cfg, err := config.LoadFrom(func(string) string { return "" })
	if err != nil {
		t.Fatalf("configurazione: %v", err)
	}
	svc, err := paddle.NewService(paddle.Options{
		Secret: segretoPaddle,
		Store:  store,
		Sink:   sink,
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Now:    func() time.Time { return istantePaddle },
	})
	if err != nil {
		t.Fatalf("costruzione del servizio: %v", err)
	}
	return httpapi.NewRouter(cfg, "test",
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		httpapi.Deps{PaddleWebhook: svc})
}

func consegnaPaddle(t *testing.T, router http.Handler, corpo, firma string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, rottaPaddle, strings.NewReader(corpo))
	if firma != "" {
		req.Header.Set(paddle.HeaderSignature, firma)
	}
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

func firmaPaddle(corpo string) string {
	return paddle.Sign([]byte(segretoPaddle), []byte(corpo), istantePaddle)
}

func esitoPaddle(t *testing.T, rec *httptest.ResponseRecorder) httpapi.PaddleWebhookResponse {
	t.Helper()
	var body httpapi.PaddleWebhookResponse
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("corpo non decodificabile: %v", err)
	}
	return body
}

// -------------------------------------------------------------------- prove

func TestConsegnaPaddleFirmataVieneApplicata(t *testing.T) {
	store, sink := nuovoArchivioEventi(), &consumatoreEntitlement{}
	router := routerPaddle(t, store, sink)
	corpo := eventoPaddle("evt_01", "2026-08-17T09:00:00.000000Z")

	rec := consegnaPaddle(t, router, corpo, firmaPaddle(corpo))

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, atteso 202: %s", rec.Code, rec.Body.String())
	}
	esito := esitoPaddle(t, rec)
	if esito.Outcome != string(paddle.OutcomeApplied) {
		t.Errorf("esito = %q", esito.Outcome)
	}
	if esito.EventID != "evt_01" {
		t.Errorf("event_id = %q: il cruscotto di Paddle mostra questa risposta accanto alla consegna", esito.EventID)
	}
	if sink.applicate() != 1 {
		t.Errorf("applicazioni = %d, attesa 1", sink.applicate())
	}
}

// Una consegna non verificata risponde 401 e **non lascia traccia di sé**: né
// una riga di registro, né un entitlement. E il messaggio non dice quale dei
// motivi ha prodotto il rifiuto.
func TestConsegnaPaddleNonVerificata(t *testing.T) {
	corpo := eventoPaddle("evt_01", "2026-08-17T09:00:00.000000Z")

	casi := map[string]string{
		"firma assente":  "",
		"firma di altri": paddle.Sign([]byte("un altro segreto lungo abbastanza"), []byte(corpo), istantePaddle),
		// La firma è autentica ma copre un corpo diverso: è il tentativo di farsi
		// regalare un piano riusando una consegna vera.
		"corpo alterato dopo la firma": firmaPaddle(`{"event_id":"evt_01"}`),
		"firma troppo vecchia": paddle.Sign([]byte(segretoPaddle), []byte(corpo),
			istantePaddle.Add(-2*paddle.DefaultTolerance)),
	}

	for nome, firma := range casi {
		t.Run(nome, func(t *testing.T) {
			store, sink := nuovoArchivioEventi(), &consumatoreEntitlement{}
			router := routerPaddle(t, store, sink)

			rec := consegnaPaddle(t, router, corpo, firma)

			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, atteso 401", rec.Code)
			}
			var errore httpapi.ErrorBody
			if err := json.NewDecoder(rec.Body).Decode(&errore); err != nil {
				t.Fatalf("corpo non decodificabile: %v", err)
			}
			if errore.Error.Code != "invalid_signature" {
				t.Errorf("codice = %q", errore.Error.Code)
			}
			// Il messaggio è lo stesso per tutti i casi: dire *quale* aiuterebbe
			// soltanto chi sta provando.
			if strings.Contains(strings.ToLower(errore.Error.Message), "timestamp") ||
				strings.Contains(strings.ToLower(errore.Error.Message), "segreto") {
				t.Errorf("il messaggio racconta cosa non andava: %q", errore.Error.Message)
			}
			if store.registrati() != 0 {
				t.Errorf("eventi registrati = %d, attesi 0", store.registrati())
			}
			if sink.applicate() != 0 {
				t.Errorf("entitlement applicati = %d, attesi 0", sink.applicate())
			}
		})
	}
}

// Paddle ripete le consegne: la seconda copia risponde 200 `duplicate` e non
// produce un secondo upgrade. Un errore otterrebbe altre copie.
func TestConsegnaPaddleRipetuta(t *testing.T) {
	store, sink := nuovoArchivioEventi(), &consumatoreEntitlement{}
	router := routerPaddle(t, store, sink)
	corpo := eventoPaddle("evt_01", "2026-08-17T09:00:00.000000Z")
	firma := firmaPaddle(corpo)

	primo := consegnaPaddle(t, router, corpo, firma)
	secondo := consegnaPaddle(t, router, corpo, firma)

	if primo.Code != http.StatusAccepted {
		t.Fatalf("prima consegna: status = %d", primo.Code)
	}
	if secondo.Code != http.StatusOK {
		t.Fatalf("seconda consegna: status = %d, atteso 200", secondo.Code)
	}
	if esito := esitoPaddle(t, secondo); esito.Outcome != string(paddle.OutcomeDuplicate) {
		t.Errorf("esito = %q, atteso %q", esito.Outcome, paddle.OutcomeDuplicate)
	}
	if sink.applicate() != 1 {
		t.Fatalf("applicazioni = %d, attesa 1: il duplicato ha prodotto un secondo effetto", sink.applicate())
	}
}

// Un evento fuori ordine risponde 200 `stale`, distinto da `duplicate`: sono i
// due modi diversi in cui un webhook di fatturazione si rompe, e dal cruscotto
// di Paddle si vuole sapere quale dei due si è visto.
func TestConsegnaPaddleFuoriOrdine(t *testing.T) {
	store := nuovoArchivioEventi()
	sink := &consumatoreEntitlement{fuoriUso: true}
	router := routerPaddle(t, store, sink)
	corpo := eventoPaddle("evt_vecchio", "2026-08-17T08:00:00.000000Z")

	rec := consegnaPaddle(t, router, corpo, firmaPaddle(corpo))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, atteso 200", rec.Code)
	}
	if esito := esitoPaddle(t, rec); esito.Outcome != string(paddle.OutcomeStale) {
		t.Fatalf("esito = %q, atteso %q", esito.Outcome, paddle.OutcomeStale)
	}
}

// Un pagamento fallito viene registrato e ignorato: i Termini §4.2 promettono
// che durante i tentativi di Paddle il servizio continua.
func TestConsegnaPaddleDiTransazione(t *testing.T) {
	store, sink := nuovoArchivioEventi(), &consumatoreEntitlement{}
	router := routerPaddle(t, store, sink)
	corpo := `{
	  "event_id": "evt_txn",
	  "event_type": "transaction.payment_failed",
	  "occurred_at": "2026-08-17T09:00:00.000000Z",
	  "data": {"id": "txn_01", "subscription_id": "sub_01", "customer_id": "ctm_01"}
	}`

	rec := consegnaPaddle(t, router, corpo, firmaPaddle(corpo))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, atteso 200", rec.Code)
	}
	if esito := esitoPaddle(t, rec); esito.Outcome != string(paddle.OutcomeIgnored) {
		t.Errorf("esito = %q", esito.Outcome)
	}
	if sink.applicate() != 0 {
		t.Fatal("un pagamento fallito non deve toccare il piano")
	}
}

// Una lavorazione fallita risponde 500: è la risposta che fa ripetere Paddle, ed
// è ciò che vogliamo quando un utente ha pagato e non ha ricevuto il piano.
func TestConsegnaPaddleNonLavorata(t *testing.T) {
	store := nuovoArchivioEventi()
	sink := &consumatoreEntitlement{err: errors.New("database irraggiungibile")}
	router := routerPaddle(t, store, sink)
	corpo := eventoPaddle("evt_01", "2026-08-17T09:00:00.000000Z")

	rec := consegnaPaddle(t, router, corpo, firmaPaddle(corpo))

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, atteso 500", rec.Code)
	}
	// Il dettaglio resta nel log: al chiamante va un messaggio generico.
	var errore httpapi.ErrorBody
	if err := json.NewDecoder(rec.Body).Decode(&errore); err != nil {
		t.Fatalf("corpo non decodificabile: %v", err)
	}
	if strings.Contains(errore.Error.Message, "database") {
		t.Errorf("il messaggio espone il guasto interno: %q", errore.Error.Message)
	}
}

// Un payload firmato ma malformato è un 400: ripeterlo non lo renderà leggibile.
func TestConsegnaPaddleMalformata(t *testing.T) {
	store, sink := nuovoArchivioEventi(), &consumatoreEntitlement{}
	router := routerPaddle(t, store, sink)
	corpo := `{"event_type":"subscription.updated","occurred_at":"2026-08-17T09:00:00Z","data":{}}`

	rec := consegnaPaddle(t, router, corpo, firmaPaddle(corpo))

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, atteso 400: %s", rec.Code, rec.Body.String())
	}
}

// Il corpo si legge per intero prima di poter dire se la richiesta è legittima:
// senza un tetto, chiunque potrebbe farci allocare memoria a piacere.
func TestConsegnaPaddleTroppoGrande(t *testing.T) {
	store, sink := nuovoArchivioEventi(), &consumatoreEntitlement{}
	router := routerPaddle(t, store, sink)
	corpo := `{"event_id":"evt_01","data":"` + strings.Repeat("x", 3<<20) + `"}`

	rec := consegnaPaddle(t, router, corpo, firmaPaddle(corpo))

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, atteso 413", rec.Code)
	}
	if store.registrati() != 0 {
		t.Errorf("eventi registrati = %d, attesi 0", store.registrati())
	}
}

// Senza il segreto la rotta **non esiste**: un endpoint di fatturazione che
// accetta corpi non verificati è un modo per farsi regalare un piano, e non
// registrarlo è l'unica alternativa accettabile a registrarne uno verificato.
func TestSenzaSegretoLaRottaPaddleNonEsiste(t *testing.T) {
	cfg, err := config.LoadFrom(func(string) string { return "" })
	if err != nil {
		t.Fatalf("configurazione: %v", err)
	}
	router := httpapi.NewRouter(cfg, "test",
		slog.New(slog.NewTextHandler(io.Discard, nil)), httpapi.Deps{})

	corpo := eventoPaddle("evt_01", "2026-08-17T09:00:00.000000Z")
	rec := consegnaPaddle(t, router, corpo, firmaPaddle(corpo))

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, atteso 404", rec.Code)
	}
}
