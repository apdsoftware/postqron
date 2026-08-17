package paddle_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/apdsoftware/postqron/services/api/internal/paddle"
)

// ------------------------------------------------------------------ finzioni

// archivio è un [paddle.Store] in memoria che riproduce la sola regola che
// conta: un evento già visto non si rilavora, tranne quando la lavorazione
// precedente è fallita.
type archivio struct {
	mu    sync.Mutex
	righe map[string]*riga

	failClaim error
}

type riga struct {
	record    paddle.Record
	stato     paddle.Status
	motivo    string
	tentativi int
}

func nuovoArchivio() *archivio { return &archivio{righe: make(map[string]*riga)} }

func (a *archivio) Claim(_ context.Context, record paddle.Record) (bool, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.failClaim != nil {
		return false, a.failClaim
	}

	esistente, visto := a.righe[record.ID]
	if visto {
		if esistente.stato != paddle.StatusFailed {
			return false, nil
		}
		esistente.stato = paddle.StatusReceived
		esistente.motivo = ""
		esistente.tentativi++
		return true, nil
	}

	a.righe[record.ID] = &riga{record: record, stato: paddle.StatusReceived, tentativi: 1}
	return true, nil
}

func (a *archivio) Complete(_ context.Context, id string, stato paddle.Status, motivo string, _ time.Time) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	esistente, visto := a.righe[id]
	if !visto {
		return errors.New("evento non registrato")
	}
	esistente.stato = stato
	esistente.motivo = motivo
	return nil
}

func (a *archivio) stato(id string) paddle.Status {
	a.mu.Lock()
	defer a.mu.Unlock()
	if esistente, visto := a.righe[id]; visto {
		return esistente.stato
	}
	return ""
}

func (a *archivio) numeroRighe() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.righe)
}

// destinatario è un [paddle.EntitlementSink] che conta le applicazioni e
// riproduce la filigrana: un evento più vecchio dell'ultimo applicato per quella
// sottoscrizione non viene applicato.
type destinatario struct {
	mu       sync.Mutex
	ricevute []paddle.Subscription
	filigran map[string]time.Time

	errore error
}

func nuovoDestinatario() *destinatario {
	return &destinatario{filigran: make(map[string]time.Time)}
}

func (d *destinatario) ApplySubscription(_ context.Context, sub paddle.Subscription) (bool, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.errore != nil {
		return false, d.errore
	}
	if ultima, visto := d.filigran[sub.ID]; visto && sub.Event.OccurredAt.Before(ultima) {
		return false, nil
	}
	d.filigran[sub.ID] = sub.Event.OccurredAt
	d.ricevute = append(d.ricevute, sub)
	return true, nil
}

func (d *destinatario) applicate() []paddle.Subscription {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]paddle.Subscription(nil), d.ricevute...)
}

// ------------------------------------------------------------------ supporto

func silenzioso() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// servizio costruisce il servizio con l'orologio fermo su `adesso`. La
// tolleranza resta quella vera: i corpi dei test vengono firmati con lo stesso
// istante, quindi non c'è niente da allentare.
func servizio(t *testing.T, store paddle.Store, sink paddle.EntitlementSink) *paddle.Service {
	t.Helper()
	svc, err := paddle.NewService(paddle.Options{
		Secret: string(segreto),
		Store:  store,
		Sink:   sink,
		Logger: silenzioso(),
		Now:    func() time.Time { return adesso },
	})
	if err != nil {
		t.Fatalf("costruzione del servizio: %v", err)
	}
	return svc
}

// consegna firma un corpo con il segreto di prova e lo fa entrare dall'handler
// vero. È il modo in cui questi test collaudano il webhook **senza rete**: non
// esiste una scorciatoia che salti la verifica della firma, perché è proprio
// quella che vogliamo esercitare.
func consegna(t *testing.T, svc *paddle.Service, corpo string) (paddle.Result, error) {
	t.Helper()
	return svc.Receive(context.Background(), paddle.Request{
		Signature: paddle.Sign(segreto, []byte(corpo), adesso),
		Body:      []byte(corpo),
	})
}

// eventoSottoscrizione compone il payload di un evento `subscription.*`.
func eventoSottoscrizione(eventID, tipo, quando, subID, stato, priceID, userID string) string {
	return fmt.Sprintf(`{
	  "event_id": %q,
	  "event_type": %q,
	  "occurred_at": %q,
	  "notification_id": "ntf_01",
	  "data": {
	    "id": %q,
	    "status": %q,
	    "customer_id": "ctm_01",
	    "current_billing_period": {"starts_at": "2026-08-01T00:00:00.000000Z", "ends_at": "2026-09-01T00:00:00.000000Z"},
	    "items": [{"status": "active", "price": {"id": %q}}],
	    "custom_data": {"user_id": %q}
	  }
	}`, eventID, tipo, quando, subID, stato, priceID, userID)
}

const (
	primo    = "2026-08-17T09:00:00.000000Z"
	secondo  = "2026-08-17T09:30:00.000000Z"
	utente   = "11111111-1111-4111-8111-111111111111"
	prezzoPr = "pri_pro_mensile"
)

// -------------------------------------------------------------------- prove

func TestSottoscrizioneVerificataArrivaAlConsumatore(t *testing.T) {
	store, sink := nuovoArchivio(), nuovoDestinatario()
	svc := servizio(t, store, sink)

	esito, err := consegna(t, svc, eventoSottoscrizione(
		"evt_01", paddle.EventSubscriptionCreated, primo, "sub_01", "active", prezzoPr, utente))
	if err != nil {
		t.Fatalf("consegna legittima rifiutata: %v", err)
	}
	if esito.Outcome != paddle.OutcomeApplied {
		t.Fatalf("esito = %q, atteso %q", esito.Outcome, paddle.OutcomeApplied)
	}
	if store.stato("evt_01") != paddle.StatusProcessed {
		t.Fatalf("stato registrato = %q, atteso %q", store.stato("evt_01"), paddle.StatusProcessed)
	}

	applicate := sink.applicate()
	if len(applicate) != 1 {
		t.Fatalf("applicazioni = %d, attesa 1", len(applicate))
	}
	sub := applicate[0]
	switch {
	case sub.ID != "sub_01":
		t.Errorf("sottoscrizione = %q", sub.ID)
	case sub.UserID != utente:
		t.Errorf("utente da custom_data = %q", sub.UserID)
	case len(sub.PriceIDs) != 1 || sub.PriceIDs[0] != prezzoPr:
		t.Errorf("prezzi = %v", sub.PriceIDs)
	case sub.Status != paddle.SubscriptionActive:
		t.Errorf("stato = %q", sub.Status)
	case sub.Event.ID != "evt_01":
		t.Errorf("evento = %q", sub.Event.ID)
	}
}

// Una consegna non verificata non deve lasciare traccia di sé da nessuna parte:
// né una riga di registro, né un entitlement.
func TestConsegnaNonVerificataNonProduceEffetti(t *testing.T) {
	store, sink := nuovoArchivio(), nuovoDestinatario()
	svc := servizio(t, store, sink)
	corpo := eventoSottoscrizione("evt_01", paddle.EventSubscriptionCreated, primo, "sub_01", "active", prezzoPr, utente)

	casi := map[string]string{
		"firma assente":  "",
		"firma di altri": paddle.Sign([]byte("segreto di qualcun altro, lungo"), []byte(corpo), adesso),
		// La firma è autentica ma copre un corpo diverso da quello mandato: è il
		// tentativo di farsi regalare un piano riusando una consegna vera.
		"corpo alterato dopo la firma": paddle.Sign(segreto, []byte("{}"), adesso),
	}

	for nome, firma := range casi {
		t.Run(nome, func(t *testing.T) {
			_, err := svc.Receive(context.Background(), paddle.Request{Signature: firma, Body: []byte(corpo)})
			if !errors.Is(err, paddle.ErrInvalidSignature) {
				t.Fatalf("atteso ErrInvalidSignature, ottenuto %v", err)
			}
		})
	}

	if store.numeroRighe() != 0 {
		t.Errorf("righe registrate = %d, attese 0", store.numeroRighe())
	}
	if len(sink.applicate()) != 0 {
		t.Errorf("entitlement applicati = %d, attesi 0", len(sink.applicate()))
	}
}

// Paddle ripete le consegne. La seconda copia dello stesso evento non deve
// produrre un secondo upgrade — ed è un successo, non un errore: rispondere male
// otterrebbe altre copie.
func TestEventoRipetutoNonProduceDoppioEffetto(t *testing.T) {
	store, sink := nuovoArchivio(), nuovoDestinatario()
	svc := servizio(t, store, sink)
	corpo := eventoSottoscrizione("evt_01", paddle.EventSubscriptionUpdated, primo, "sub_01", "active", prezzoPr, utente)

	primoEsito, err := consegna(t, svc, corpo)
	if err != nil {
		t.Fatalf("prima consegna: %v", err)
	}
	secondoEsito, err := consegna(t, svc, corpo)
	if err != nil {
		t.Fatalf("seconda consegna: %v", err)
	}

	if primoEsito.Outcome != paddle.OutcomeApplied {
		t.Errorf("prima consegna: esito = %q", primoEsito.Outcome)
	}
	if secondoEsito.Outcome != paddle.OutcomeDuplicate {
		t.Errorf("seconda consegna: esito = %q, atteso %q", secondoEsito.Outcome, paddle.OutcomeDuplicate)
	}
	if applicate := sink.applicate(); len(applicate) != 1 {
		t.Fatalf("applicazioni = %d, attesa 1: il duplicato ha prodotto un secondo effetto", len(applicate))
	}
}

// Due copie della stessa consegna che arrivano insieme sono il caso per cui
// [paddle.Store.Claim] dev'essere una sola istruzione atomica.
func TestConsegneConcorrentiProduconoUnEffettoSolo(t *testing.T) {
	store, sink := nuovoArchivio(), nuovoDestinatario()
	svc := servizio(t, store, sink)
	corpo := eventoSottoscrizione("evt_01", paddle.EventSubscriptionUpdated, primo, "sub_01", "active", prezzoPr, utente)

	const copie = 8
	var wg sync.WaitGroup
	esiti := make([]paddle.Outcome, copie)
	for i := range copie {
		wg.Add(1)
		go func() {
			defer wg.Done()
			esito, err := svc.Receive(context.Background(), paddle.Request{
				Signature: paddle.Sign(segreto, []byte(corpo), adesso),
				Body:      []byte(corpo),
			})
			if err != nil {
				t.Errorf("consegna %d: %v", i, err)
				return
			}
			esiti[i] = esito.Outcome
		}()
	}
	wg.Wait()

	var applicati int
	for _, esito := range esiti {
		if esito == paddle.OutcomeApplied {
			applicati++
		}
	}
	if applicati != 1 {
		t.Fatalf("consegne applicate = %d, attesa 1", applicati)
	}
	if applicate := sink.applicate(); len(applicate) != 1 {
		t.Fatalf("applicazioni = %d, attesa 1", len(applicate))
	}
}

// Il caso che la deduplicazione **non** copre: un evento diverso e più vecchio
// che arriva dopo uno più recente. È nuovo, legittimo e firmato — e senza la
// filigrana riporterebbe in vita un piano che l'utente non ha più.
func TestEventoFuoriOrdineNonRetrocedeIlPiano(t *testing.T) {
	store, sink := nuovoArchivio(), nuovoDestinatario()
	svc := servizio(t, store, sink)

	// Arriva prima la cancellazione (più recente), poi l'aggiornamento che la
	// precedeva: è l'ordine che produce un ritentativo di Paddle con backoff.
	if _, err := consegna(t, svc, eventoSottoscrizione(
		"evt_recente", paddle.EventSubscriptionCanceled, secondo, "sub_01", "canceled", prezzoPr, utente)); err != nil {
		t.Fatalf("consegna recente: %v", err)
	}

	esito, err := consegna(t, svc, eventoSottoscrizione(
		"evt_vecchio", paddle.EventSubscriptionUpdated, primo, "sub_01", "active", prezzoPr, utente))
	if err != nil {
		t.Fatalf("consegna vecchia: %v", err)
	}

	if esito.Outcome != paddle.OutcomeStale {
		t.Fatalf("esito = %q, atteso %q", esito.Outcome, paddle.OutcomeStale)
	}
	// Registrato come ignorato e non come fallito: non c'è niente da rilavorare,
	// e marcarlo fallito lo farebbe riprovare all'infinito.
	if store.stato("evt_vecchio") != paddle.StatusIgnored {
		t.Errorf("stato registrato = %q, atteso %q", store.stato("evt_vecchio"), paddle.StatusIgnored)
	}

	applicate := sink.applicate()
	if len(applicate) != 1 {
		t.Fatalf("applicazioni = %d, attesa 1", len(applicate))
	}
	if applicate[0].Status != paddle.SubscriptionCanceled {
		t.Fatalf("stato in forza = %q: l'evento vecchio ha retrocesso l'account",
			applicate[0].Status)
	}
}

// Un pagamento fallito **non** cambia il piano: i Termini §4.2 dicono che
// durante i tentativi di Paddle il servizio continua. L'evento va registrato —
// serve a capire cosa è arrivato — e non applicato.
func TestEventoDiTransazioneVieneRegistratoEIgnorato(t *testing.T) {
	store, sink := nuovoArchivio(), nuovoDestinatario()
	svc := servizio(t, store, sink)

	corpo := `{
	  "event_id": "evt_txn",
	  "event_type": "transaction.payment_failed",
	  "occurred_at": "2026-08-17T09:00:00.000000Z",
	  "data": {"id": "txn_01", "subscription_id": "sub_01", "customer_id": "ctm_01", "status": "past_due"}
	}`

	esito, err := consegna(t, svc, corpo)
	if err != nil {
		t.Fatalf("consegna: %v", err)
	}
	if esito.Outcome != paddle.OutcomeIgnored {
		t.Fatalf("esito = %q, atteso %q", esito.Outcome, paddle.OutcomeIgnored)
	}
	if store.stato("evt_txn") != paddle.StatusIgnored {
		t.Errorf("stato registrato = %q", store.stato("evt_txn"))
	}
	if len(sink.applicate()) != 0 {
		t.Fatalf("un pagamento fallito non deve toccare il piano")
	}
}

// `past_due` mantiene il diritto al piano; `paused` e `canceled` no.
func TestStatiCheDannoDirittoAlPiano(t *testing.T) {
	casi := map[paddle.SubscriptionStatus]bool{
		paddle.SubscriptionActive:   true,
		paddle.SubscriptionTrialing: true,
		paddle.SubscriptionPastDue:  true,
		paddle.SubscriptionPaused:   false,
		paddle.SubscriptionCanceled: false,
		"stato_sconosciuto":         false,
	}

	for stato, atteso := range casi {
		if got := stato.Entitles(); got != atteso {
			t.Errorf("%q.Entitles() = %v, atteso %v", stato, got, atteso)
		}
	}
}

// Una lavorazione fallita risponde 500 — che è ciò che fa ripetere Paddle — e la
// ripetizione dev'essere rilavorata, non scartata come duplicato.
func TestEventoFallitoVieneRilavoratoAllaRipetizione(t *testing.T) {
	store, sink := nuovoArchivio(), nuovoDestinatario()
	sink.errore = errors.New("database irraggiungibile")
	svc := servizio(t, store, sink)
	corpo := eventoSottoscrizione("evt_01", paddle.EventSubscriptionUpdated, primo, "sub_01", "active", prezzoPr, utente)

	if _, err := consegna(t, svc, corpo); err == nil {
		t.Fatal("atteso un errore dalla prima consegna")
	}
	if store.stato("evt_01") != paddle.StatusFailed {
		t.Fatalf("stato registrato = %q, atteso %q", store.stato("evt_01"), paddle.StatusFailed)
	}

	sink.errore = nil
	esito, err := consegna(t, svc, corpo)
	if err != nil {
		t.Fatalf("ripetizione: %v", err)
	}
	if esito.Outcome != paddle.OutcomeApplied {
		t.Fatalf("esito = %q, atteso %q: la ripetizione è stata scartata come duplicato", esito.Outcome, paddle.OutcomeApplied)
	}
}

func TestSenzaConsumatoreLEventoVieneSoloRegistrato(t *testing.T) {
	store := nuovoArchivio()
	svc := servizio(t, store, nil)

	esito, err := consegna(t, svc, eventoSottoscrizione(
		"evt_01", paddle.EventSubscriptionCreated, primo, "sub_01", "active", prezzoPr, utente))
	if err != nil {
		t.Fatalf("consegna: %v", err)
	}
	if esito.Outcome != paddle.OutcomeIgnored {
		t.Fatalf("esito = %q, atteso %q", esito.Outcome, paddle.OutcomeIgnored)
	}
	if store.stato("evt_01") != paddle.StatusIgnored {
		t.Errorf("stato registrato = %q", store.stato("evt_01"))
	}
	if svc.HasSink() {
		t.Error("HasSink() vero senza consumatore")
	}
}

func TestPayloadNonValido(t *testing.T) {
	casi := map[string]string{
		"non è JSON":                 `non json`,
		"senza event_id":             `{"event_type":"subscription.updated","occurred_at":"2026-08-17T09:00:00Z","data":{}}`,
		"senza event_type":           `{"event_id":"evt_01","occurred_at":"2026-08-17T09:00:00Z","data":{}}`,
		"senza occurred_at":          `{"event_id":"evt_01","event_type":"subscription.updated","data":{}}`,
		"occurred_at illeggibile":    `{"event_id":"evt_01","event_type":"subscription.updated","occurred_at":"ieri","data":{}}`,
		"sottoscrizione senza id":    `{"event_id":"evt_01","event_type":"subscription.updated","occurred_at":"2026-08-17T09:00:00Z","data":{"status":"active"}}`,
		"sottoscrizione senza stato": `{"event_id":"evt_01","event_type":"subscription.updated","occurred_at":"2026-08-17T09:00:00Z","data":{"id":"sub_01"}}`,
	}

	for nome, corpo := range casi {
		t.Run(nome, func(t *testing.T) {
			store, sink := nuovoArchivio(), nuovoDestinatario()
			svc := servizio(t, store, sink)

			_, err := consegna(t, svc, corpo)
			if !errors.Is(err, paddle.ErrInvalidRequest) {
				t.Fatalf("atteso ErrInvalidRequest, ottenuto %v", err)
			}
			// Un payload illeggibile non lascia una riga che dichiara di aver preso
			// in carico un evento mai lavorabile.
			if store.numeroRighe() != 0 {
				t.Errorf("righe registrate = %d, attese 0", store.numeroRighe())
			}
		})
	}
}

// Paddle aggiunge campi ai propri payload senza preavviso: rifiutarne uno
// sconosciuto trasformerebbe un'aggiunta innocua in utenti che pagano e non
// ricevono il piano.
func TestCampiSconosciutiNelPayloadVengonoAccettati(t *testing.T) {
	store, sink := nuovoArchivio(), nuovoDestinatario()
	svc := servizio(t, store, sink)

	corpo := `{
	  "event_id": "evt_01",
	  "event_type": "subscription.updated",
	  "occurred_at": "2026-08-17T09:00:00.000000Z",
	  "campo_del_futuro": {"qualsiasi": true},
	  "data": {
	    "id": "sub_01",
	    "status": "active",
	    "campo_nuovo": 42,
	    "items": [{"status": "active", "price": {"id": "pri_pro_mensile", "unit_price": {"amount": "900"}}}],
	    "custom_data": {"user_id": "11111111-1111-4111-8111-111111111111", "altro": "x"}
	  }
	}`

	esito, err := consegna(t, svc, corpo)
	if err != nil {
		t.Fatalf("payload con campi sconosciuti rifiutato: %v", err)
	}
	if esito.Outcome != paddle.OutcomeApplied {
		t.Fatalf("esito = %q", esito.Outcome)
	}
}

// Una voce disattivata è storico dentro la sottoscrizione: farci risalire il
// piano darebbe a chi ha cambiato prezzo il piano vecchio insieme al nuovo.
func TestVociDisattivateNonContano(t *testing.T) {
	store, sink := nuovoArchivio(), nuovoDestinatario()
	svc := servizio(t, store, sink)

	corpo := `{
	  "event_id": "evt_01",
	  "event_type": "subscription.updated",
	  "occurred_at": "2026-08-17T09:00:00.000000Z",
	  "data": {
	    "id": "sub_01",
	    "status": "active",
	    "items": [
	      {"status": "inactive", "price": {"id": "pri_pro_mensile"}},
	      {"status": "active", "price": {"id": "pri_team_mensile"}}
	    ]
	  }
	}`

	if _, err := consegna(t, svc, corpo); err != nil {
		t.Fatalf("consegna: %v", err)
	}
	applicate := sink.applicate()
	if len(applicate) != 1 {
		t.Fatalf("applicazioni = %d", len(applicate))
	}
	if len(applicate[0].PriceIDs) != 1 || applicate[0].PriceIDs[0] != "pri_team_mensile" {
		t.Fatalf("prezzi attivi = %v, atteso il solo pri_team_mensile", applicate[0].PriceIDs)
	}
}

// Una disdetta programmata non cambia il piano adesso: i Termini §4.1 dicono che
// ha effetto alla fine del periodo pagato. Serve a poterlo dire, non a farlo.
func TestDisdettaProgrammataNonCambiaLoStato(t *testing.T) {
	store, sink := nuovoArchivio(), nuovoDestinatario()
	svc := servizio(t, store, sink)

	corpo := `{
	  "event_id": "evt_01",
	  "event_type": "subscription.updated",
	  "occurred_at": "2026-08-17T09:00:00.000000Z",
	  "data": {
	    "id": "sub_01",
	    "status": "active",
	    "scheduled_change": {"action": "cancel", "effective_at": "2026-09-01T00:00:00.000000Z"},
	    "items": [{"status": "active", "price": {"id": "pri_pro_mensile"}}]
	  }
	}`

	if _, err := consegna(t, svc, corpo); err != nil {
		t.Fatalf("consegna: %v", err)
	}
	sub := sink.applicate()[0]
	if sub.Status != paddle.SubscriptionActive {
		t.Errorf("stato = %q: una disdetta programmata non toglie il piano subito", sub.Status)
	}
	if sub.ScheduledCancelAt == nil || !sub.ScheduledCancelAt.Equal(time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("disdetta programmata = %v", sub.ScheduledCancelAt)
	}
}

func TestErroreDelloStoreRisale(t *testing.T) {
	store := nuovoArchivio()
	store.failClaim = errors.New("database irraggiungibile")
	svc := servizio(t, store, nuovoDestinatario())

	_, err := consegna(t, svc, eventoSottoscrizione(
		"evt_01", paddle.EventSubscriptionCreated, primo, "sub_01", "active", prezzoPr, utente))
	if err == nil {
		t.Fatal("un errore dello store deve risalire: è il 500 che fa ripetere Paddle")
	}
}

func TestServizioSenzaSegretoNonSiCostruisce(t *testing.T) {
	casi := map[string]paddle.Options{
		"segreto assente": {Store: nuovoArchivio()},
		"segreto corto":   {Secret: "corto", Store: nuovoArchivio()},
		"store assente":   {Secret: string(segreto)},
	}

	for nome, opts := range casi {
		t.Run(nome, func(t *testing.T) {
			if _, err := paddle.NewService(opts); err == nil {
				t.Fatal("atteso un errore di costruzione")
			}
		})
	}
}

func TestSecretFromEnv(t *testing.T) {
	ambiente := map[string]string{paddle.SecretEnvVar: "  pdl_ntfset_valore  "}
	if got := paddle.SecretFromEnv(func(k string) string { return ambiente[k] }); got != "pdl_ntfset_valore" {
		t.Fatalf("SecretFromEnv = %q", got)
	}
	if got := paddle.SecretFromEnv(func(string) string { return "   " }); got != "" {
		t.Fatalf("un valore di soli spazi non è un segreto: %q", got)
	}
}
