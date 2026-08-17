package githubhook_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/apdsoftware/postqron/services/api/internal/githubhook"
)

// ------------------------------------------------------------------ finzioni

// archivio è uno [githubhook.Store] in memoria che riproduce la sola regola che
// conta: una consegna già vista non si rilavora, tranne quando la lavorazione
// precedente è fallita.
type archivio struct {
	mu    sync.Mutex
	righe map[string]*riga

	claims    int
	completes int

	failClaim    error
	failComplete error
}

type riga struct {
	consegna  githubhook.Delivery
	stato     githubhook.Status
	motivo    string
	tentativi int
}

func nuovoArchivio() *archivio {
	return &archivio{righe: make(map[string]*riga)}
}

func (a *archivio) Claim(_ context.Context, consegna githubhook.Delivery) (bool, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.claims++
	if a.failClaim != nil {
		return false, a.failClaim
	}

	esistente, vista := a.righe[consegna.ID]
	if vista {
		if esistente.stato != githubhook.StatusFailed {
			return false, nil
		}
		esistente.stato = githubhook.StatusReceived
		esistente.motivo = ""
		esistente.tentativi++
		return true, nil
	}

	a.righe[consegna.ID] = &riga{consegna: consegna, stato: githubhook.StatusReceived, tentativi: 1}
	return true, nil
}

func (a *archivio) Complete(_ context.Context, id string, stato githubhook.Status, motivo string, _ time.Time) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.completes++
	if a.failComplete != nil {
		return a.failComplete
	}
	esistente, vista := a.righe[id]
	if !vista {
		return errors.New("consegna non registrata")
	}
	esistente.stato = stato
	esistente.motivo = motivo
	return nil
}

func (a *archivio) stato(id string) githubhook.Status {
	a.mu.Lock()
	defer a.mu.Unlock()
	if esistente, vista := a.righe[id]; vista {
		return esistente.stato
	}
	return ""
}

func (a *archivio) numeroRighe() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.righe)
}

func (a *archivio) chiamate() (int, int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.claims, a.completes
}

// consumatore è il confine verso #422, in finzione.
type consumatore struct {
	mu       sync.Mutex
	ricevuti []githubhook.PushEvent
	err      error
}

func (c *consumatore) HandlePush(_ context.Context, evento githubhook.PushEvent) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.err != nil {
		return c.err
	}
	c.ricevuti = append(c.ricevuti, evento)
	return nil
}

func (c *consumatore) conteggio() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.ricevuti)
}

func (c *consumatore) ultimo(t *testing.T) githubhook.PushEvent {
	t.Helper()
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.ricevuti) == 0 {
		t.Fatal("il consumatore non ha ricevuto nessuna push")
	}
	return c.ricevuti[len(c.ricevuti)-1]
}

// ------------------------------------------------------------------ supporto

func nuovoServizio(t *testing.T, store githubhook.Store, sink githubhook.PushSink) *githubhook.Service {
	t.Helper()
	svc, err := githubhook.NewService(githubhook.Options{
		Secret: string(segreto),
		Store:  store,
		Sink:   sink,
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Now:    func() time.Time { return time.Date(2026, 8, 17, 10, 0, 0, 0, time.UTC) },
	})
	if err != nil {
		t.Fatalf("costruzione del servizio: %v", err)
	}
	return svc
}

// payloadPush è un payload `push` ridotto ai campi che leggiamo, nella forma in
// cui GitHub li manda.
const payloadPush = `{
  "ref": "refs/heads/main",
  "before": "1111111111111111111111111111111111111111",
  "after": "2222222222222222222222222222222222222222",
  "created": false,
  "deleted": false,
  "forced": false,
  "repository": {
    "id": 987654321,
    "name": "infra",
    "full_name": "acme/infra",
    "private": true,
    "owner": {"login": "acme"},
    "default_branch": "main"
  },
  "installation": {"id": 55512345},
  "pusher": {"name": "chi-ha-spinto"}
}`

func consegnaPush(t *testing.T, id string) githubhook.Request {
	t.Helper()
	corpo := []byte(payloadPush)
	return githubhook.Request{
		Signature: githubhook.Sign(segreto, corpo),
		Event:     githubhook.EventPush,
		Delivery:  id,
		Body:      corpo,
	}
}

// ------------------------------------------------------------------ i test

func TestPushVerificataArrivaAlConsumatore(t *testing.T) {
	store, sink := nuovoArchivio(), &consumatore{}
	svc := nuovoServizio(t, store, sink)

	res, err := svc.Receive(context.Background(), consegnaPush(t, "consegna-1"))
	if err != nil {
		t.Fatalf("consegna legittima rifiutata: %v", err)
	}
	if res.Outcome != githubhook.OutcomeAccepted {
		t.Fatalf("outcome = %q, atteso %q", res.Outcome, githubhook.OutcomeAccepted)
	}
	if sink.conteggio() != 1 {
		t.Fatalf("il consumatore ha ricevuto %d push, attesa 1", sink.conteggio())
	}
	if got := store.stato("consegna-1"); got != githubhook.StatusProcessed {
		t.Errorf("stato = %q, atteso %q", got, githubhook.StatusProcessed)
	}

	evento := sink.ultimo(t)
	if evento.Repository.ExternalID != 987654321 {
		t.Errorf("ExternalID = %d", evento.Repository.ExternalID)
	}
	if evento.Repository.FullName != "acme/infra" || evento.Repository.Owner != "acme" || evento.Repository.Name != "infra" {
		t.Errorf("repository = %+v", evento.Repository)
	}
	if evento.InstallationID != 55512345 {
		t.Errorf("InstallationID = %d", evento.InstallationID)
	}
	if evento.After != "2222222222222222222222222222222222222222" {
		t.Errorf("After = %q", evento.After)
	}
	if evento.Delivery != "consegna-1" {
		t.Errorf("Delivery = %q", evento.Delivery)
	}
	if !evento.IsDefaultBranch() {
		t.Error("IsDefaultBranch = false su una push su main con default_branch main")
	}
	if !evento.HasContent() {
		t.Error("HasContent = false su una push con un commit")
	}
}

// TestConsegnaNonVerificataNonProduceEffetti è il punto 2 di R11: una richiesta
// che non supera la verifica non deve lasciare traccia di sé da nessuna parte —
// non una riga registrata, non una chiamata al consumatore.
func TestConsegnaNonVerificataNonProduceEffetti(t *testing.T) {
	corpo := []byte(payloadPush)
	valida := githubhook.Sign(segreto, corpo)
	alterato := append([]byte(nil), corpo...)
	alterato[len(alterato)-1] = ' '

	casi := []struct {
		nome      string
		richiesta githubhook.Request
	}{
		{"firma assente", githubhook.Request{Event: "push", Delivery: "x", Body: corpo}},
		{"firma sbagliata", githubhook.Request{
			Signature: githubhook.Sign([]byte("segreto-sbagliato-lungo"), corpo),
			Event:     "push", Delivery: "x", Body: corpo,
		}},
		{"corpo alterato dopo la firma", githubhook.Request{
			Signature: valida, Event: "push", Delivery: "x", Body: alterato,
		}},
	}

	for _, caso := range casi {
		t.Run(caso.nome, func(t *testing.T) {
			store, sink := nuovoArchivio(), &consumatore{}
			svc := nuovoServizio(t, store, sink)

			res, err := svc.Receive(context.Background(), caso.richiesta)
			if !errors.Is(err, githubhook.ErrInvalidSignature) {
				t.Fatalf("err = %v, atteso ErrInvalidSignature", err)
			}
			if res.Outcome != "" || res.Delivery != "" {
				t.Errorf("il rifiuto ha restituito un esito: %+v", res)
			}
			if claims, completes := store.chiamate(); claims != 0 || completes != 0 {
				t.Errorf("lo store è stato toccato: %d Claim, %d Complete", claims, completes)
			}
			if store.numeroRighe() != 0 {
				t.Errorf("registrate %d consegne, attese 0", store.numeroRighe())
			}
			if sink.conteggio() != 0 {
				t.Errorf("il consumatore è stato chiamato %d volte", sink.conteggio())
			}
		})
	}
}

// TestConsegnaRipetutaNonProduceDoppioEffetto è il punto 3: GitHub ripete, e
// una ripetizione porta lo stesso identificativo di consegna.
func TestConsegnaRipetutaNonProduceDoppioEffetto(t *testing.T) {
	store, sink := nuovoArchivio(), &consumatore{}
	svc := nuovoServizio(t, store, sink)

	prima, err := svc.Receive(context.Background(), consegnaPush(t, "consegna-ripetuta"))
	if err != nil {
		t.Fatalf("prima consegna: %v", err)
	}
	if prima.Outcome != githubhook.OutcomeAccepted {
		t.Fatalf("prima consegna: outcome = %q", prima.Outcome)
	}

	// Identica alla prima, byte per byte: è ciò che manda il pulsante «Redeliver»
	// del registro dell'App.
	seconda, err := svc.Receive(context.Background(), consegnaPush(t, "consegna-ripetuta"))
	if err != nil {
		t.Fatalf("seconda consegna: %v", err)
	}
	if seconda.Outcome != githubhook.OutcomeDuplicate {
		t.Fatalf("seconda consegna: outcome = %q, atteso %q", seconda.Outcome, githubhook.OutcomeDuplicate)
	}
	if sink.conteggio() != 1 {
		t.Fatalf("il consumatore ha ricevuto %d push, attesa 1", sink.conteggio())
	}
	if store.numeroRighe() != 1 {
		t.Errorf("registrate %d consegne, attesa 1", store.numeroRighe())
	}
}

// TestConsegneConcorrentiProduconoUnEffettoSolo: le due copie di una consegna
// possono arrivare insieme. Il vincolo che le separa è lo store, e questo test
// serve a dire che il servizio non aggiunge una finestra propria fra la
// decisione e l'effetto.
func TestConsegneConcorrentiProduconoUnEffettoSolo(t *testing.T) {
	store, sink := nuovoArchivio(), &consumatore{}
	svc := nuovoServizio(t, store, sink)

	const copie = 8
	var wg sync.WaitGroup
	esiti := make([]githubhook.Outcome, copie)
	for i := range copie {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res, err := svc.Receive(context.Background(), consegnaPush(t, "consegna-concorrente"))
			if err != nil {
				t.Errorf("copia %d: %v", i, err)
				return
			}
			esiti[i] = res.Outcome
		}()
	}
	wg.Wait()

	accettate := 0
	for _, esito := range esiti {
		if esito == githubhook.OutcomeAccepted {
			accettate++
		}
	}
	if accettate != 1 {
		t.Errorf("consegne accettate = %d, attesa 1", accettate)
	}
	if sink.conteggio() != 1 {
		t.Errorf("il consumatore ha ricevuto %d push, attesa 1", sink.conteggio())
	}
}

// TestConsegnaFallitaVieneRilavorataAllaRipetizione: è il caso per cui GitHub
// ripete davvero, ed è il modo in cui collauderemo l'endpoint una volta che
// api.postqron.com risponderà.
func TestConsegnaFallitaVieneRilavorataAllaRipetizione(t *testing.T) {
	store, sink := nuovoArchivio(), &consumatore{}
	sink.err = errors.New("database non raggiungibile")
	svc := nuovoServizio(t, store, sink)

	if _, err := svc.Receive(context.Background(), consegnaPush(t, "consegna-fallita")); err == nil {
		t.Fatal("una lavorazione fallita non ha prodotto errore")
	}
	if got := store.stato("consegna-fallita"); got != githubhook.StatusFailed {
		t.Fatalf("stato = %q, atteso %q", got, githubhook.StatusFailed)
	}

	// Il consumatore si riprende, e la ripetizione della stessa consegna va
	// lavorata: scartarla come duplicato perderebbe l'evento per sempre.
	sink.err = nil
	res, err := svc.Receive(context.Background(), consegnaPush(t, "consegna-fallita"))
	if err != nil {
		t.Fatalf("ripetizione: %v", err)
	}
	if res.Outcome != githubhook.OutcomeAccepted {
		t.Fatalf("ripetizione: outcome = %q, atteso %q", res.Outcome, githubhook.OutcomeAccepted)
	}
	if sink.conteggio() != 1 {
		t.Errorf("il consumatore ha ricevuto %d push, attesa 1", sink.conteggio())
	}
	if got := store.stato("consegna-fallita"); got != githubhook.StatusProcessed {
		t.Errorf("stato = %q, atteso %q", got, githubhook.StatusProcessed)
	}
}

func TestPingRispondePong(t *testing.T) {
	store, sink := nuovoArchivio(), &consumatore{}
	svc := nuovoServizio(t, store, sink)

	corpo := []byte(`{"zen":"Design for failure.","hook_id":1234}`)
	res, err := svc.Receive(context.Background(), githubhook.Request{
		Signature: githubhook.Sign(segreto, corpo),
		Event:     githubhook.EventPing,
		Delivery:  "consegna-ping",
		Body:      corpo,
	})
	if err != nil {
		t.Fatalf("ping rifiutato: %v", err)
	}
	if res.Outcome != githubhook.OutcomePong {
		t.Fatalf("outcome = %q, atteso %q", res.Outcome, githubhook.OutcomePong)
	}
	if sink.conteggio() != 0 {
		t.Error("il ping è arrivato al consumatore delle push")
	}
	if got := store.stato("consegna-ping"); got != githubhook.StatusIgnored {
		t.Errorf("stato = %q, atteso %q", got, githubhook.StatusIgnored)
	}
}

func TestEventoNonTrattatoVieneRegistratoEIgnorato(t *testing.T) {
	store, sink := nuovoArchivio(), &consumatore{}
	svc := nuovoServizio(t, store, sink)

	corpo := []byte(`{"action":"created"}`)
	res, err := svc.Receive(context.Background(), githubhook.Request{
		Signature: githubhook.Sign(segreto, corpo),
		Event:     "installation",
		Delivery:  "consegna-installation",
		Body:      corpo,
	})
	if err != nil {
		t.Fatalf("evento non trattato rifiutato: %v", err)
	}
	if res.Outcome != githubhook.OutcomeIgnored {
		t.Fatalf("outcome = %q, atteso %q", res.Outcome, githubhook.OutcomeIgnored)
	}
	if sink.conteggio() != 0 {
		t.Error("un evento diverso da push è arrivato al consumatore")
	}
	// Registrato comunque: la deduplicazione dev'essere uniforme, e il registro
	// serve a capire cosa è arrivato davvero.
	if got := store.stato("consegna-installation"); got != githubhook.StatusIgnored {
		t.Errorf("stato = %q, atteso %q", got, githubhook.StatusIgnored)
	}
}

func TestSenzaConsumatoreLaPushVieneSoloRegistrata(t *testing.T) {
	store := nuovoArchivio()
	svc := nuovoServizio(t, store, nil)

	res, err := svc.Receive(context.Background(), consegnaPush(t, "consegna-senza-sink"))
	if err != nil {
		t.Fatalf("consegna rifiutata: %v", err)
	}
	if res.Outcome != githubhook.OutcomeIgnored {
		t.Fatalf("outcome = %q, atteso %q", res.Outcome, githubhook.OutcomeIgnored)
	}
	if got := store.stato("consegna-senza-sink"); got != githubhook.StatusIgnored {
		t.Errorf("stato = %q, atteso %q", got, githubhook.StatusIgnored)
	}
	if svc.HasSink() {
		t.Error("HasSink = true senza consumatore")
	}
}

func TestTestateObbligatorie(t *testing.T) {
	corpo := []byte(payloadPush)
	firma := githubhook.Sign(segreto, corpo)

	casi := []struct {
		nome      string
		richiesta githubhook.Request
	}{
		{"consegna assente", githubhook.Request{Signature: firma, Event: "push", Body: corpo}},
		{"consegna troppo lunga", githubhook.Request{
			Signature: firma, Event: "push", Delivery: lunga(101), Body: corpo,
		}},
		{"evento assente", githubhook.Request{Signature: firma, Delivery: "x", Body: corpo}},
		{"evento troppo lungo", githubhook.Request{
			Signature: firma, Event: lunga(51), Delivery: "x", Body: corpo,
		}},
	}

	for _, caso := range casi {
		t.Run(caso.nome, func(t *testing.T) {
			store, sink := nuovoArchivio(), &consumatore{}
			svc := nuovoServizio(t, store, sink)

			_, err := svc.Receive(context.Background(), caso.richiesta)
			if !errors.Is(err, githubhook.ErrInvalidRequest) {
				t.Fatalf("err = %v, atteso ErrInvalidRequest", err)
			}
			if store.numeroRighe() != 0 || sink.conteggio() != 0 {
				t.Error("una richiesta malformata ha prodotto effetti")
			}
		})
	}
}

func TestPayloadPushNonValido(t *testing.T) {
	casi := map[string]string{
		"JSON illeggibile":      `{"ref":`,
		"senza repository":      `{"ref":"refs/heads/main"}`,
		"repository senza id":   `{"ref":"refs/heads/main","repository":{"full_name":"acme/infra"}}`,
		"repository senza nome": `{"ref":"refs/heads/main","repository":{"id":42}}`,
		"senza riferimento":     `{"repository":{"id":42,"full_name":"acme/infra"}}`,
	}

	for nome, payload := range casi {
		t.Run(nome, func(t *testing.T) {
			store, sink := nuovoArchivio(), &consumatore{}
			svc := nuovoServizio(t, store, sink)

			corpo := []byte(payload)
			_, err := svc.Receive(context.Background(), githubhook.Request{
				Signature: githubhook.Sign(segreto, corpo),
				Event:     githubhook.EventPush,
				Delivery:  "consegna-malformata",
				Body:      corpo,
			})
			if !errors.Is(err, githubhook.ErrInvalidRequest) {
				t.Fatalf("err = %v, atteso ErrInvalidRequest", err)
			}
			// Non registrata: non c'è niente da deduplicare in una consegna che
			// non sappiamo leggere, e ripeterla non la renderà leggibile.
			if store.numeroRighe() != 0 || sink.conteggio() != 0 {
				t.Error("un payload non valido ha prodotto effetti")
			}
		})
	}
}

// TestPayloadConCampiSconosciutiVieneAccettato: GitHub aggiunge campi ai propri
// payload senza preavviso, e rifiutarne uno sconosciuto trasformerebbe
// un'aggiunta innocua in un webhook che smette di funzionare.
func TestPayloadConCampiSconosciutiVieneAccettato(t *testing.T) {
	store, sink := nuovoArchivio(), &consumatore{}
	svc := nuovoServizio(t, store, sink)

	corpo := []byte(`{
	  "ref": "refs/heads/main",
	  "after": "3333333333333333333333333333333333333333",
	  "repository": {"id": 7, "full_name": "acme/infra", "default_branch": "main",
	                 "campo_che_non_conosciamo": {"annidato": true}},
	  "installation": {"id": 9},
	  "campo_futuro": [1, 2, 3]
	}`)
	res, err := svc.Receive(context.Background(), githubhook.Request{
		Signature: githubhook.Sign(segreto, corpo),
		Event:     githubhook.EventPush,
		Delivery:  "consegna-campi-nuovi",
		Body:      corpo,
	})
	if err != nil {
		t.Fatalf("payload con campi sconosciuti rifiutato: %v", err)
	}
	if res.Outcome != githubhook.OutcomeAccepted {
		t.Fatalf("outcome = %q", res.Outcome)
	}
	// L'owner non c'era: si ricava dal nome completo invece di restare vuoto.
	if evento := sink.ultimo(t); evento.Repository.Owner != "acme" || evento.Repository.Name != "infra" {
		t.Errorf("repository = %+v", evento.Repository)
	}
}

func TestCancellazioneDiUnRamo(t *testing.T) {
	store, sink := nuovoArchivio(), &consumatore{}
	svc := nuovoServizio(t, store, sink)

	corpo := []byte(`{
	  "ref": "refs/heads/vecchio",
	  "before": "4444444444444444444444444444444444444444",
	  "after": "0000000000000000000000000000000000000000",
	  "deleted": true,
	  "repository": {"id": 7, "full_name": "acme/infra", "owner": {"login": "acme"},
	                 "name": "infra", "default_branch": "main"},
	  "installation": {"id": 9}
	}`)
	if _, err := svc.Receive(context.Background(), githubhook.Request{
		Signature: githubhook.Sign(segreto, corpo),
		Event:     githubhook.EventPush,
		Delivery:  "consegna-ramo-cancellato",
		Body:      corpo,
	}); err != nil {
		t.Fatalf("consegna rifiutata: %v", err)
	}

	evento := sink.ultimo(t)
	if evento.HasContent() {
		t.Error("HasContent = true su un ramo cancellato")
	}
	if evento.IsDefaultBranch() {
		t.Error("IsDefaultBranch = true su un ramo diverso dal predefinito")
	}
	if evento.Branch() != "vecchio" {
		t.Errorf("Branch = %q", evento.Branch())
	}
}

func TestBranchSuTag(t *testing.T) {
	evento := githubhook.PushEvent{Ref: "refs/tags/v1.0.0"}
	if evento.Branch() != "" {
		t.Errorf("Branch = %q su un tag, attesa stringa vuota", evento.Branch())
	}
	if evento.IsDefaultBranch() {
		t.Error("IsDefaultBranch = true su un tag")
	}
}

// TestErroreDelloStoreRisale: se la consegna non si riesce a registrare, non si
// può sapere se è la prima o la seconda copia. L'unica risposta corretta è
// l'errore, che diventa un 500 e fa ripetere GitHub.
func TestErroreDelloStoreRisale(t *testing.T) {
	store, sink := nuovoArchivio(), &consumatore{}
	store.failClaim = errors.New("connessione persa")
	svc := nuovoServizio(t, store, sink)

	_, err := svc.Receive(context.Background(), consegnaPush(t, "consegna-store-rotto"))
	if err == nil {
		t.Fatal("un errore dello store non ha prodotto errore")
	}
	if errors.Is(err, githubhook.ErrInvalidSignature) || errors.Is(err, githubhook.ErrInvalidRequest) {
		t.Fatalf("err = %v: un guasto interno non va scambiato per un rifiuto", err)
	}
	if sink.conteggio() != 0 {
		t.Error("il consumatore è stato chiamato senza aver registrato la consegna")
	}
}

// TestErroreDiCompleteNonRibaltaLEffetto: la registrazione dell'esito fallisce
// *dopo* che il consumatore ha lavorato. Rispondere male a GitHub farebbe
// ripetere una consegna il cui effetto c'è già stato.
func TestErroreDiCompleteNonRibaltaLEffetto(t *testing.T) {
	store, sink := nuovoArchivio(), &consumatore{}
	store.failComplete = errors.New("connessione persa")
	svc := nuovoServizio(t, store, sink)

	res, err := svc.Receive(context.Background(), consegnaPush(t, "consegna-complete-rotto"))
	if err != nil {
		t.Fatalf("err = %v, atteso nessun errore", err)
	}
	if res.Outcome != githubhook.OutcomeAccepted {
		t.Errorf("outcome = %q, atteso %q", res.Outcome, githubhook.OutcomeAccepted)
	}
	if sink.conteggio() != 1 {
		t.Errorf("il consumatore ha ricevuto %d push, attesa 1", sink.conteggio())
	}
}

func TestServizioSenzaSegretoNonSiCostruisce(t *testing.T) {
	casi := map[string]githubhook.Options{
		"segreto assente":       {Secret: "", Store: nuovoArchivio()},
		"segreto di soli spazi": {Secret: "      ", Store: nuovoArchivio()},
		"segreto troppo corto":  {Secret: "corto", Store: nuovoArchivio()},
		"store assente":         {Secret: string(segreto)},
	}

	for nome, opts := range casi {
		t.Run(nome, func(t *testing.T) {
			if _, err := githubhook.NewService(opts); err == nil {
				t.Fatal("il servizio si è costruito: un webhook così accetterebbe qualunque cosa")
			}
		})
	}
}

func TestSecretFromEnv(t *testing.T) {
	ambiente := map[string]string{githubhook.SecretEnvVar: "  segreto-con-spazi-attorno  "}
	if got := githubhook.SecretFromEnv(func(k string) string { return ambiente[k] }); got != "segreto-con-spazi-attorno" {
		t.Errorf("SecretFromEnv = %q", got)
	}
	if got := githubhook.SecretFromEnv(func(string) string { return "" }); got != "" {
		t.Errorf("SecretFromEnv = %q su ambiente vuoto", got)
	}
}

func lunga(n int) string {
	byteArray := make([]byte, n)
	for i := range byteArray {
		byteArray[i] = 'a'
	}
	return string(byteArray)
}
