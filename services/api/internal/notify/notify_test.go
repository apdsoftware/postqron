package notify_test

import (
	"context"
	"errors"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/apdsoftware/postqron/services/api/internal/emailrender"
	"github.com/apdsoftware/postqron/services/api/internal/mailronix"
	"github.com/apdsoftware/postqron/services/api/internal/notify"
)

var testSite = emailrender.Site{
	ProductName:   "Postqron",
	PublicBaseURL: "https://postqron.test",
	AppBaseURL:    "https://app.postqron.test",
	SupportEmail:  "support@postqron.test",
}

// newRenderer costruisce il compilatore vero sui template del repository.
//
// I test di questo package usano il renderer reale e sostituiscono soltanto la
// rete: è il confine giusto, perché ciò che va verificato — la lingua, il
// contenuto, l'assenza di segreti — vive nel messaggio compilato, e un renderer
// finto renderebbe verdi dei test che non guardano niente.
func newRenderer(t *testing.T) *emailrender.Renderer {
	t.Helper()
	dir, err := emailrender.FindDir(".")
	if err != nil {
		t.Fatalf("FindDir: %v", err)
	}
	r, err := emailrender.NewFromDir(dir, testSite)
	if err != nil {
		t.Fatalf("NewFromDir: %v", err)
	}
	return r
}

// harness è il giro completo senza database e senza rete: coda in memoria,
// renderer vero, mittente che registra.
type harness struct {
	queue   *notify.MemoryQueue
	sender  *notify.RecordingSender
	service *notify.Service
	courier *notify.Courier
	clock   *stubClock
}

type stubClock struct{ at time.Time }

func (c *stubClock) now() time.Time      { return c.at }
func (c *stubClock) add(d time.Duration) { c.at = c.at.Add(d) }

func newHarness(t *testing.T, recipients ...notify.Recipient) *harness {
	t.Helper()

	clock := &stubClock{at: time.Date(2026, 8, 17, 9, 41, 0, 0, time.UTC)}
	queue := notify.NewMemoryQueue()
	for _, r := range recipients {
		queue.WithRecipient(r)
	}
	sender := &notify.RecordingSender{}

	service, err := notify.NewService(notify.Options{Queue: queue, Now: clock.now})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	courier, err := notify.NewCourier(notify.CourierOptions{
		Queue:    queue,
		Renderer: newRenderer(t),
		Sender:   sender,
		Now:      clock.now,
	})
	if err != nil {
		t.Fatalf("NewCourier: %v", err)
	}
	return &harness{queue: queue, sender: sender, service: service, courier: courier, clock: clock}
}

// deliver fa passare il corriere, dopo aver spostato l'orologio oltre la
// finestra di grazia.
func (h *harness) deliver(t *testing.T) notify.Stats {
	t.Helper()
	h.clock.add(h.service.Policy().FailureGrace + time.Second)
	stats, err := h.courier.Deliver(context.Background())
	if err != nil {
		t.Fatalf("Deliver: %v", err)
	}
	return stats
}

func recipient(userID, language string) notify.Recipient {
	return notify.Recipient{
		UserID:   userID,
		Email:    userID + "@example.test",
		Name:     "Sam",
		Language: language,
	}
}

// ------------------------------------------------------------------ i quattro

// I quattro eventi di R21 arrivano a destinazione, e ciascuno **nella lingua del
// profilo dell'utente**.
//
// La lingua è il punto: non è quella del browser di chi ha causato l'evento, e
// non potrebbe esserlo — un alert di job fallito lo scatena il motore, che un
// browser non ce l'ha. Qui i quattro eventi partono da quattro utenti con
// quattro lingue diverse e ognuno riceve la sua (R33).
func TestOgniEventoArrivaNellaLinguaDelProfilo(t *testing.T) {
	h := newHarness(t,
		recipient("u-welcome", "it"),
		recipient("u-job", "de"),
		recipient("u-plan", "fr"),
		recipient("u-security", "es"),
	)
	ctx := context.Background()

	if err := h.service.Welcome(ctx, "u-welcome"); err != nil {
		t.Fatalf("Welcome: %v", err)
	}
	if err := h.service.JobFailed(ctx, notify.JobFailure{
		UserID: "u-job", JobID: "job-1", JobName: "nightly-invoices",
		Environment: "production", Kind: notify.FailureHTTPStatus, HTTPStatus: 502,
		OccurredAt: h.clock.at,
	}); err != nil {
		t.Fatalf("JobFailed: %v", err)
	}
	if err := h.service.PlanChanged(ctx, notify.PlanChange{
		UserID: "u-plan", PreviousPlan: "Pro", NewPlan: "Free", EffectiveAt: h.clock.at,
	}); err != nil {
		t.Fatalf("PlanChanged: %v", err)
	}
	if err := h.service.Security(ctx, notify.SecurityEvent{
		UserID: "u-security", Kind: notify.SecurityAPIKeyRevoked, ResourceName: "CI deploy",
	}); err != nil {
		t.Fatalf("Security: %v", err)
	}

	stats := h.deliver(t)
	if stats.Sent != 4 {
		t.Fatalf("consegnate a Mailronix %d email su 4: %+v", stats.Sent, stats)
	}

	sent := h.sender.Sent()
	if len(sent) != 4 {
		t.Fatalf("il mittente ha visto %d messaggi", len(sent))
	}

	// Ogni destinatario ha il proprio, e i link portano il prefisso della sua
	// lingua: è il segno visibile che la compilazione è avvenuta in quella
	// lingua e non in un'altra (SPEC §8-bis).
	expected := map[string]string{
		"u-welcome@example.test":  "/it/",
		"u-job@example.test":      "/de/",
		"u-plan@example.test":     "/fr/",
		"u-security@example.test": "/es/",
	}
	for _, email := range sent {
		prefix, ok := expected[email.To]
		if !ok {
			t.Fatalf("messaggio a un destinatario inatteso: %q", email.To)
		}
		delete(expected, email.To)
		if !strings.Contains(email.HTML, "https://app.postqron.test"+prefix) &&
			!strings.Contains(email.HTML, "https://postqron.test"+prefix) {
			t.Errorf("il messaggio per %s non ha link nella sua lingua (%s):\n%s",
				email.To, prefix, email.HTML)
		}
		if strings.TrimSpace(email.Subject) == "" || strings.TrimSpace(email.Text) == "" {
			t.Errorf("messaggio incompleto per %s: oggetto %q", email.To, email.Subject)
		}
	}
	if len(expected) != 0 {
		t.Errorf("destinatari senza messaggio: %v", expected)
	}
}

// La lingua si legge al momento dell'invio, non a quello dell'accodamento: un
// utente che cambia lingua fra i due momenti riceve l'email in quella nuova.
func TestLaLinguaSiLeggeAlMomentoDellInvio(t *testing.T) {
	h := newHarness(t, recipient("u-1", "en"))
	ctx := context.Background()

	if err := h.service.Welcome(ctx, "u-1"); err != nil {
		t.Fatalf("Welcome: %v", err)
	}
	// L'utente cambia lingua dopo che la notifica è già in coda.
	h.queue.WithRecipient(recipient("u-1", "de"))

	h.deliver(t)

	sent := h.sender.Sent()
	if len(sent) != 1 {
		t.Fatalf("messaggi spediti: %d", len(sent))
	}
	if !strings.Contains(sent[0].HTML, `lang="de"`) && !strings.Contains(sent[0].HTML, "/de/") {
		t.Errorf("l'email non è nella lingua scelta dopo l'accodamento:\n%s", sent[0].HTML)
	}
}

// ------------------------------------------------------------- anti-spam

// Un job che fallisce in continuazione non produce un'email per fallimento.
//
// È la difesa che R20.1 rende indispensabile: finire in suppression list
// significa smettere di ricevere **anche** le email che contano, e non
// accorgersene, perché la risposta all'invio resta `202` in entrambi i casi.
func TestUnJobCheFallisceInContinuazioneNonProduceUnEmailPerFallimento(t *testing.T) {
	h := newHarness(t, recipient("u-1", "en"))
	ctx := context.Background()

	// Si parte dall'inizio di una finestra: il bordo fra due finestre è un
	// comportamento dichiarato e ha un test suo, e mescolarlo qui renderebbe
	// questo test una misura di due cose insieme.
	h.clock.at = time.Date(2026, 8, 17, 10, 0, 0, 0, time.UTC)

	// Un'ora di fallimenti al secondo: 3.600 chiamate a JobFailed.
	for i := range 3600 {
		if err := h.service.JobFailed(ctx, notify.JobFailure{
			UserID: "u-1", JobID: "job-1", JobName: "webhook", Environment: "production",
			Kind: notify.FailureTimeout, OccurredAt: h.clock.at, Attempt: 1,
		}); err != nil {
			t.Fatalf("JobFailed al fallimento %d: %v", i, err)
		}
		h.clock.add(time.Second)
	}

	if got := h.queue.Len(); got != 1 {
		t.Fatalf("3.600 fallimenti hanno prodotto %d notifiche: la finestra non sta limitando niente", got)
	}

	stats := h.deliver(t)
	if stats.Sent != 1 {
		t.Fatalf("email consegnate a Mailronix: %d", stats.Sent)
	}

	// Il raggruppamento non perde l'informazione: l'unica email dice quante
	// volte è successo, che è più utile di 3.600 messaggi identici.
	body := h.sender.Sent()[0].Text
	if !strings.Contains(body, "has failed") {
		t.Errorf("l'avviso non racconta i fallimenti:\n%s", body)
	}
	if strings.Contains(body, "failed 1 time in a row") {
		t.Errorf("l'avviso racconta un fallimento solo dopo averne raggruppati 3.600:\n%s", body)
	}
}

// Il tetto è per (job, ambiente): una prova in staging non zittisce un guasto in
// produzione. Sono due fatti distinti con esiti separati (R23).
func TestIlTettoNonConfondeGliAmbienti(t *testing.T) {
	h := newHarness(t, recipient("u-1", "en"))
	ctx := context.Background()

	for _, environment := range []string{"production", "staging", "production", "staging"} {
		if err := h.service.JobFailed(ctx, notify.JobFailure{
			UserID: "u-1", JobID: "job-1", JobName: "webhook", Environment: environment,
			Kind: notify.FailureUnknown, OccurredAt: h.clock.at,
		}); err != nil {
			t.Fatalf("JobFailed: %v", err)
		}
	}

	if got := len(h.queue.OfEvent(notify.EventJobFailed)); got != 2 {
		t.Fatalf("notifiche accodate: %d, attese due (una per ambiente)", got)
	}
}

// Passata la finestra, l'avviso si ripete. Non è una svista del tetto: è la
// conseguenza di R20.1 — se il primo messaggio fosse finito in una suppression
// list non lo sapremmo, e un solo avviso lascerebbe l'utente con un job fermo e
// nessun secondo tentativo in arrivo.
func TestPassataLaFinestraLAvvisoSiRipete(t *testing.T) {
	h := newHarness(t, recipient("u-1", "en"))
	ctx := context.Background()

	failure := notify.JobFailure{
		UserID: "u-1", JobID: "job-1", JobName: "webhook", Environment: "production",
		Kind: notify.FailureUnknown,
	}
	if err := h.service.JobFailed(ctx, failure); err != nil {
		t.Fatalf("JobFailed: %v", err)
	}
	h.clock.add(h.service.Policy().FailureWindow + time.Minute)
	if err := h.service.JobFailed(ctx, failure); err != nil {
		t.Fatalf("JobFailed: %v", err)
	}

	if got := len(h.queue.OfEvent(notify.EventJobFailed)); got != 2 {
		t.Errorf("notifiche accodate: %d, attese due — un avviso che non si ripete mai "+
			"è un avviso di cui non si può verificare l'arrivo (R20.1)", got)
	}
}

// Il benvenuto si manda una volta sola nella vita di un account.
func TestIlBenvenutoNonSiRipete(t *testing.T) {
	h := newHarness(t, recipient("u-1", "en"))
	ctx := context.Background()

	for range 3 {
		if err := h.service.Welcome(ctx, "u-1"); err != nil {
			t.Fatalf("Welcome: %v", err)
		}
		h.clock.add(48 * time.Hour)
	}
	if got := h.queue.Len(); got != 1 {
		t.Errorf("benvenuti accodati: %d", got)
	}
}

// Un rinnovo che non cambia il piano non è una variazione da comunicare: Paddle
// manda un evento a ogni periodo, e un'email che annuncia un cambiamento mai
// avvenuto è rumore mensile.
func TestUnPianoCheNonCambiaNonProduceEmail(t *testing.T) {
	h := newHarness(t, recipient("u-1", "en"))

	if err := h.service.PlanChanged(context.Background(), notify.PlanChange{
		UserID: "u-1", PreviousPlan: "Pro", NewPlan: "Pro", EffectiveAt: h.clock.at,
	}); err != nil {
		t.Fatalf("PlanChanged: %v", err)
	}
	if got := h.queue.Len(); got != 0 {
		t.Errorf("notifiche accodate: %d, attese zero", got)
	}
}

// La stessa variazione consegnata due volte da Paddle produce una email sola.
func TestUnWebhookRipetutoNonProduceDueEmail(t *testing.T) {
	h := newHarness(t, recipient("u-1", "en"))
	change := notify.PlanChange{
		UserID: "u-1", PreviousPlan: "Pro", NewPlan: "Free",
		EffectiveAt:         time.Date(2026, 8, 17, 9, 0, 0, 0, time.UTC),
		SuspendedByJobLimit: 4,
	}

	for range 2 {
		if err := h.service.PlanChanged(context.Background(), change); err != nil {
			t.Fatalf("PlanChanged: %v", err)
		}
		h.clock.add(time.Minute)
	}
	if got := h.queue.Len(); got != 1 {
		t.Errorf("notifiche accodate: %d", got)
	}
}

// Due eventi di sicurezza di **tipo diverso** non si sopprimono a vicenda: la
// finestra raggruppa una raffica dello stesso fatto, non fatti distinti.
func TestLaFinestraDiSicurezzaNonSopprimeUnEventoDiversoTipo(t *testing.T) {
	h := newHarness(t, recipient("u-1", "en"))
	ctx := context.Background()

	kinds := []notify.SecurityKind{
		notify.SecurityAPIKeyCreated,
		notify.SecurityAPIKeyCreated,
		notify.SecurityAPIKeyRevoked,
		notify.SecurityPasswordChanged,
	}
	for _, kind := range kinds {
		if err := h.service.Security(ctx, notify.SecurityEvent{UserID: "u-1", Kind: kind}); err != nil {
			t.Fatalf("Security: %v", err)
		}
	}
	if got := h.queue.Len(); got != 3 {
		t.Errorf("notifiche accodate: %d, attese tre (una per tipo)", got)
	}
}

// ------------------------------------------------------- il recapito fallisce

// Un recapito che fallisce in modo transitorio torna in coda, e non brucia il
// messaggio.
func TestUnRecapitoTransitorioTornaInCoda(t *testing.T) {
	h := newHarness(t, recipient("u-1", "en"))
	h.sender.Err = &mailronix.APIError{
		StatusCode: http.StatusTooManyRequests,
		Code:       "rate_limited",
		Message:    "too many requests",
	}

	if err := h.service.Welcome(context.Background(), "u-1"); err != nil {
		t.Fatalf("Welcome: %v", err)
	}
	stats := h.deliver(t)

	if stats.Retried != 1 || stats.Failed != 0 {
		t.Fatalf("esito della passata: %+v, atteso un solo rinvio", stats)
	}
	rows := h.queue.All()
	if rows[0].Status != "pending" {
		t.Errorf("stato della notifica: %q, attesa ancora in coda", rows[0].Status)
	}

	// Al giro dopo Mailronix risponde, e il messaggio parte.
	h.sender.Err = nil
	h.clock.add(time.Hour)
	if _, err := h.courier.Deliver(context.Background()); err != nil {
		t.Fatalf("Deliver: %v", err)
	}
	if h.sender.Len() != 1 {
		t.Errorf("messaggi spediti: %d", h.sender.Len())
	}
	if got := h.queue.All()[0].EmailLogID; got == "" {
		t.Error("email_log_id non registrato: è l'unica cosa che una 202 permette di sapere (R20.1)")
	}
}

// Un errore definitivo chiude la riga invece di ritentare all'infinito, e non
// trascina con sé le altre notifiche della stessa passata.
func TestUnRecapitoDefinitivoNonFermaLaCoda(t *testing.T) {
	h := newHarness(t, recipient("u-1", "en"), recipient("u-2", "en"))
	ctx := context.Background()

	if err := h.service.Welcome(ctx, "u-1"); err != nil {
		t.Fatalf("Welcome: %v", err)
	}
	if err := h.service.Welcome(ctx, "u-2"); err != nil {
		t.Fatalf("Welcome: %v", err)
	}

	// Un 403 non è ritentabile: consumerebbe quota senza cambiare esito.
	h.sender.Err = &mailronix.APIError{
		StatusCode: http.StatusForbidden,
		Code:       "domain_not_verified",
		Message:    "domain not verified",
	}

	stats := h.deliver(t)
	if stats.Failed != 2 || stats.Retried != 0 {
		t.Fatalf("esito della passata: %+v, attesi due fallimenti definitivi", stats)
	}
	for _, row := range h.queue.All() {
		if row.Status != "failed" {
			t.Errorf("notifica %s in stato %q", row.Event, row.Status)
		}
		if row.Reason == "" {
			t.Error("una notifica chiusa senza motivo scritto non si diagnostica")
		}
	}
}

// A un account chiuso dopo l'accodamento non si scrive.
func TestAUnAccountChiusoNonSiScrive(t *testing.T) {
	h := newHarness(t, recipient("u-1", "en"))

	if err := h.service.Welcome(context.Background(), "u-1"); err != nil {
		t.Fatalf("Welcome: %v", err)
	}
	closed := recipient("u-1", "en")
	closed.Closed = true
	h.queue.WithRecipient(closed)

	stats := h.deliver(t)
	if stats.Skipped != 1 || h.sender.Len() != 0 {
		t.Errorf("esito: %+v, messaggi spediti %d", stats, h.sender.Len())
	}
}

// ---------------------------------------------------------------- segreti

// Nessun campo del payload può ospitare un segreto.
//
// È il vincolo espresso come proprietà del tipo, gemello di quello di
// internal/emailrender: quello impedisce a un template di interpolare una
// credenziale, questo le impedisce di finire in una colonna `jsonb` che poi il
// template legge. Coprono i due estremi dello stesso percorso.
func TestPayloadCarriesNoSecrets(t *testing.T) {
	suspicious := []string{
		"secret", "token", "password", "credential",
		"key", "bearer", "signature", "private", "hash", "salt",
		// Il testo grezzo di una risposta o di un errore non è un segreto per
		// definizione, ma dalla #458 in poi può contenerne uno: gli URL dei job
		// portano i valori del workspace risolti.
		"responsebody", "errortext", "responseexcerpt",
	}

	payload := reflect.TypeOf(notify.Payload{})
	for i := range payload.NumField() {
		field := payload.Field(i)
		name := strings.ToLower(field.Name)
		for _, needle := range suspicious {
			// `SecurityKind` contiene «key»? no: il controllo è per sottostringa
			// e va fatto sul nome intero, con l'unica eccezione dichiarata qui
			// sotto.
			if name == "securitykind" {
				continue
			}
			if strings.Contains(name, needle) {
				t.Errorf("Payload.%s: il nome suggerisce un segreto. "+
					"Un'email transazionale non trasporta credenziali, e ciò che non entra "+
					"in questa struttura non può uscire da un template né finire in `notifications.payload`",
					field.Name)
			}
		}
	}
}

// Il valore di un segreto non arriva nel corpo dell'email nemmeno passando per
// i campi che accettano testo libero.
//
// I due campi che un chiamante potrebbe riempire con qualunque cosa sono il nome
// del job e il nome della risorsa, e sono nomi scelti dall'utente: qui si
// verifica che il percorso non ne aggiunga altri — in particolare che l'esito di
// un fallimento resti una **classificazione** e non diventi il testo di un
// errore, che è dove un URL con un token finirebbe.
func TestIlCorpoDellEmailNonPortaValoriSensibili(t *testing.T) {
	h := newHarness(t, recipient("u-1", "en"))
	ctx := context.Background()

	if err := h.service.JobFailed(ctx, notify.JobFailure{
		UserID: "u-1", JobID: "job-1", JobName: "nightly", Environment: "production",
		Kind: notify.FailureHTTPStatus, HTTPStatus: 401, OccurredAt: h.clock.at,
	}); err != nil {
		t.Fatalf("JobFailed: %v", err)
	}
	if err := h.service.Security(ctx, notify.SecurityEvent{
		UserID: "u-1", Kind: notify.SecurityAPIKeyCreated,
		ResourceName: "CI deploy", SourceIP: "203.0.113.7",
	}); err != nil {
		t.Fatalf("Security: %v", err)
	}

	h.deliver(t)

	// Le forme in cui un segreto di questo prodotto si riconosce a occhio. Non
	// si cerca la parola «password»: il testo di sicurezza la nomina di
	// proposito, per dire che non la chiediamo mai per email. Si cercano i
	// **valori**.
	forbidden := []string{
		"pq_live_",      // chiave API di Postqron (R9)
		"mrx_live_",     // chiave API di Mailronix
		"Bearer ",       // un'intestazione di autorizzazione finita in un testo
		"client_secret", // un segreto OAuth di un bersaglio
		"api_key=",      // un segreto nella query di un URL di job (R43)
		"?token=",
		"&token=",
	}
	for _, email := range h.sender.Sent() {
		for _, body := range []string{email.Subject, email.Text, email.HTML} {
			for _, needle := range forbidden {
				if strings.Contains(body, needle) {
					t.Errorf("il corpo contiene %q:\n%s", needle, body)
				}
			}
		}
	}
}

// Un'email transazionale non porta un link di disiscrizione.
//
// Non è una dimenticanza da colmare: le transazionali si mandano perché l'utente
// ha un account e non si possono disattivare — il piè di pagina lo dice — mentre
// le comunicazioni di marketing hanno consenso separato e disiscrizione
// obbligatoria, e passano da un'altra parte. Un link di disiscrizione su un
// avviso di sicurezza verrebbe usato, e l'avviso dopo non arriverebbe.
func TestLeEmailTransazionaliNonHannoUnLinkDiDisiscrizione(t *testing.T) {
	h := newHarness(t, recipient("u-1", "en"))
	ctx := context.Background()

	if err := h.service.Welcome(ctx, "u-1"); err != nil {
		t.Fatalf("Welcome: %v", err)
	}
	if err := h.service.PlanChanged(ctx, notify.PlanChange{
		UserID: "u-1", PreviousPlan: "Free", NewPlan: "Pro", EffectiveAt: h.clock.at,
	}); err != nil {
		t.Fatalf("PlanChanged: %v", err)
	}
	h.deliver(t)

	marketing := []string{
		"unsubscribe", "opt out", "opt-out", "manage preferences",
		"email preferences", "stop receiving",
	}
	for _, email := range h.sender.Sent() {
		body := strings.ToLower(email.Text + email.HTML)
		for _, needle := range marketing {
			if strings.Contains(body, needle) {
				t.Errorf("un'email transazionale offre una disiscrizione (%q): "+
					"le transazionali non si disattivano, e mescolarle col marketing "+
					"significa perdere gli avvisi che contano", needle)
			}
		}
	}
}

// ------------------------------------------------------------------ vincoli

// Un evento senza template non produce un'email a caso: chiude la riga con
// scritto perché.
func TestUnEventoSenzaTemplateNonProduceUnEmailACaso(t *testing.T) {
	h := newHarness(t, recipient("u-1", "en"))

	// `job_recovered` è nell'enumerato della 0001 ma non fra gli eventi di R21.
	if _, err := h.queue.Enqueue(context.Background(), notify.Request{
		Event: notify.Event("job_recovered"), Channel: notify.ChannelEmail,
		UserID: "u-1", ScheduledAt: h.clock.at,
	}); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	stats := h.deliver(t)
	if stats.Failed != 1 || h.sender.Len() != 0 {
		t.Errorf("esito: %+v, messaggi spediti %d", stats, h.sender.Len())
	}
}

// Ogni evento di questo package ha un template che lo compila.
func TestOgniEventoHaUnTemplate(t *testing.T) {
	h := newHarness(t, recipient("u-1", "en"))
	ctx := context.Background()

	valid := map[notify.Event]notify.Request{
		notify.EventWelcome: {UserID: "u-1"},
		notify.EventJobFailed: {
			UserID: "u-1", JobID: "job-1", Environment: "production",
			Payload: notify.Payload{
				JobName: "n", Failures: 1, LastAttemptAt: h.clock.at, FailureKind: notify.FailureTimeout,
			},
		},
		notify.EventPlanChanged: {
			UserID: "u-1",
			Payload: notify.Payload{
				PreviousPlan: "Free", NewPlan: "Pro", EffectiveAt: h.clock.at,
			},
		},
		notify.EventSecurity: {
			UserID: "u-1",
			Payload: notify.Payload{
				SecurityKind: notify.SecurityPasswordChanged, OccurredAt: h.clock.at,
			},
		},
	}

	for _, event := range notify.Events() {
		req, ok := valid[event]
		if !ok {
			t.Fatalf("l'evento %s non ha un caso di prova: aggiungerlo qui è parte dell'aggiungerlo a Events()", event)
		}
		req.Event = event
		req.Channel = notify.ChannelEmail
		req.ScheduledAt = h.clock.at
		req.DedupeKey = string(event)
		if _, err := h.queue.Enqueue(ctx, req); err != nil {
			t.Fatalf("Enqueue di %s: %v", event, err)
		}
	}

	stats := h.deliver(t)
	if stats.Sent != len(notify.Events()) {
		t.Errorf("consegnate %d email su %d: %+v", stats.Sent, len(notify.Events()), stats)
	}
}

// La classificazione di un fallimento è la stessa da questa parte e dall'altra:
// un valore che emailrender non conosce farebbe fallire la compilazione al primo
// invio in produzione, non qui.
func TestOgniFailureKindEComprensibileDalRenderer(t *testing.T) {
	kinds := []notify.FailureKind{
		notify.FailureTimeout, notify.FailureConnection, notify.FailureDNS,
		notify.FailureTLS, notify.FailureHTTPStatus, notify.FailureUnknown,
	}
	renderer := newRenderer(t)

	for _, kind := range kinds {
		t.Run(string(kind), func(t *testing.T) {
			data := emailrender.JobFailedData{
				JobID: "job-1", JobName: "n", Environment: emailrender.EnvironmentProduction,
				ConsecutiveFailures: 1, LastAttemptAt: time.Now(),
				FailureKind: emailrender.FailureKind(kind), HTTPStatus: 500,
			}
			if _, err := renderer.Render(emailrender.EventJobFailed, "en", data); err != nil {
				t.Errorf("il renderer non conosce %q: %v", kind, err)
			}
		})
	}
}

// Lo stesso per i tipi di evento di sicurezza.
func TestOgniSecurityKindEComprensibileDalRenderer(t *testing.T) {
	kinds := []notify.SecurityKind{
		notify.SecurityPasswordChanged, notify.SecurityAPIKeyCreated,
		notify.SecurityAPIKeyRevoked, notify.SecurityAccountImpersonated,
	}
	renderer := newRenderer(t)

	for _, kind := range kinds {
		t.Run(string(kind), func(t *testing.T) {
			data := emailrender.SecurityAlertData{
				Kind:       emailrender.SecurityEventKind(kind),
				OccurredAt: time.Now(),
			}
			if _, err := renderer.Render(emailrender.EventSecurityAlert, "en", data); err != nil {
				t.Errorf("il renderer non conosce %q: %v", kind, err)
			}
		})
	}
}

// Un accodamento senza utente è un errore del chiamante, non una riga a caso in
// coda.
func TestAccodamentiIncompletiVengonoRifiutati(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	cases := map[string]error{
		"benvenuto senza utente": h.service.Welcome(ctx, "  "),
		"job senza ambiente": h.service.JobFailed(ctx, notify.JobFailure{
			UserID: "u-1", JobID: "job-1",
		}),
		"piano senza nomi": h.service.PlanChanged(ctx, notify.PlanChange{
			UserID: "u-1", PreviousPlan: "Free",
		}),
		"sicurezza senza tipo": h.service.Security(ctx, notify.SecurityEvent{UserID: "u-1"}),
	}
	for name, err := range cases {
		if err == nil {
			t.Errorf("%s: accettato senza errore", name)
		}
	}
	if h.queue.Len() != 0 {
		t.Errorf("notifiche accodate: %d, attese zero", h.queue.Len())
	}
}

// Un errore della coda risale a chi accoda, che è l'unico che può decidere cosa
// farne. La decisione — registrarlo e proseguire — sta nei package di dominio.
func TestUnErroreDellaCodaRisale(t *testing.T) {
	h := newHarness(t)
	boom := errors.New("database irraggiungibile")
	h.queue.EnqueueErr = boom

	if err := h.service.Welcome(context.Background(), "u-1"); !errors.Is(err, boom) {
		t.Errorf("errore restituito: %v", err)
	}
}
