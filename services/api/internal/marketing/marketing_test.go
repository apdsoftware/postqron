// Le promesse di §2.8, una per test.
//
// Il documento è stato approvato e dice cinque cose precise. Ognuna qui ha un
// controllo che la tiene vera, e il nome del test dice quale frase difende:
// quando uno di questi diventa rosso, ciò che si è rotto non è un dettaglio di
// realizzazione, è una riga di un documento legale.
package marketing_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/apdsoftware/postqron/services/api/internal/emailrender"
	"github.com/apdsoftware/postqron/services/api/internal/marketing"
	"github.com/apdsoftware/postqron/services/api/internal/notify"
)

// Un segreto di prova lungo abbastanza per [marketing.NewSigner].
const testSecret = "un-segreto-di-prova-lungo-almeno-trentadue-byte"

var testSite = emailrender.Site{
	ProductName:   "Postqron",
	PublicBaseURL: "https://postqron.test",
	AppBaseURL:    "https://app.postqron.test",
	SupportEmail:  "support@postqron.test",
}

// testUpdate è una comunicazione valida, in inglese e in tedesco.
func testUpdate() marketing.Update {
	return marketing.Update{Content: map[string]marketing.Content{
		"en": {Headline: "Overlapping runs", Paragraphs: []string{"Skip, queue or overlap."}},
		"de": {Headline: "Überlappende Läufe", Paragraphs: []string{"Überspringen, einreihen oder überlappen."}},
	}}
}

// harness è il giro completo senza database e senza rete.
type harness struct {
	store   *marketing.MemoryStore
	sender  *marketing.RecordingSender
	service *marketing.Service
	courier *marketing.Courier
}

func newHarness(t *testing.T, users ...marketing.Recipient) *harness {
	t.Helper()

	store := marketing.NewMemoryStore()
	for _, u := range users {
		store.WithUser(u)
	}

	signer, err := marketing.NewSigner(testSecret)
	if err != nil {
		t.Fatalf("NewSigner: %v", err)
	}
	service, err := marketing.NewService(marketing.Options{
		Store:      store,
		Signer:     signer,
		APIBaseURL: "https://api.postqron.test",
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}

	sender := &marketing.RecordingSender{}
	courier, err := marketing.NewCourier(marketing.CourierOptions{
		Service:  service,
		Renderer: newRenderer(t),
		Sender:   sender,
	})
	if err != nil {
		t.Fatalf("NewCourier: %v", err)
	}
	return &harness{store: store, sender: sender, service: service, courier: courier}
}

// newRenderer costruisce il compilatore vero sui template del repository.
//
// Come in internal/notify: si sostituisce solo la rete. Ciò che va verificato —
// che il link di disiscrizione sia nel messaggio — vive nel corpo compilato, e
// un renderer finto renderebbe verde un test che non guarda niente.
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

func user(id, language string) marketing.Recipient {
	return marketing.Recipient{
		UserID: id, Email: id + "@example.test", Name: "Sam", Language: language,
	}
}

// ---------------------------------------------------------------- il consenso

// «The legal basis is your consent (Art. 6(1)(a))»: senza, non parte niente.
//
// Le tre fasi sono il ciclo di vita completo, e la terza è quella che conta: non
// basta che il consenso abiliti l'invio, deve essere la sua **revoca** a
// disabilitarlo di nuovo.
func TestSenzaConsensoNonParteNiente(t *testing.T) {
	h := newHarness(t, user("u-1", "en"))
	ctx := context.Background()

	// Chi non ha mai deciso non ha acconsentito: l'assenza di una decisione è un
	// rifiuto, non un valore predefinito da cui ricadere.
	result, err := h.courier.Send(ctx, "u-1", testUpdate())
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if result != marketing.NoConsent {
		t.Errorf("senza aver mai deciso l'esito è %q, atteso %q", result, marketing.NoConsent)
	}
	if len(h.sender.Sent()) != 0 {
		t.Fatalf("è partita un'email senza consenso: %d messaggi", len(h.sender.Sent()))
	}

	if _, err := h.service.Grant(ctx, "u-1", "203.0.113.7"); err != nil {
		t.Fatalf("Grant: %v", err)
	}
	result, err = h.courier.Send(ctx, "u-1", testUpdate())
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if result != marketing.Sent {
		t.Errorf("con il consenso l'esito è %q, atteso %q", result, marketing.Sent)
	}
	if len(h.sender.Sent()) != 1 {
		t.Fatalf("con il consenso sono partiti %d messaggi, atteso 1", len(h.sender.Sent()))
	}

	if _, err := h.service.Withdraw(ctx, "u-1", "203.0.113.7"); err != nil {
		t.Fatalf("Withdraw: %v", err)
	}
	result, err = h.courier.Send(ctx, "u-1", testUpdate())
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if result != marketing.NoConsent {
		t.Errorf("dopo la revoca l'esito è %q, atteso %q", result, marketing.NoConsent)
	}
	if len(h.sender.Sent()) != 1 {
		t.Errorf("dopo la revoca sono partiti %d messaggi in tutto, atteso 1", len(h.sender.Sent()))
	}
}

// «Asked for on its own»: il consenso non si presta dal link di disiscrizione.
//
// Il link funziona senza accedere: se da lì si potesse anche *concedere* il
// consenso, chiunque ne venisse in possesso potrebbe iscrivere l'intestatario
// dell'indirizzo. Il rifiuto sta in due posti — qui e nel vincolo
// `marketing_consents_grant_needs_session_check` della 0019 — perché il secondo
// non si aggira con un `if` dimenticato.
func TestIlConsensoNonSiPrestaSenzaSessione(t *testing.T) {
	err := marketing.Record{
		UserID:   "u-1",
		Decision: marketing.DecisionGranted,
		Source:   marketing.SourceUnsubscribeLink,
	}.Validate()
	if err == nil {
		t.Fatal("un consenso dichiarato proveniente dal link di disiscrizione è stato accettato")
	}
	if !strings.Contains(err.Error(), "senza accedere") {
		t.Errorf("il rifiuto non dice perché: %v", err)
	}
}

// «We keep a record of when you consented and when you withdrew».
//
// La traccia è la prova del diritto di scrivere, quindi conserva **tutte** le
// decisioni e non solo l'ultima: un utente che acconsente, ritira e riacconsente
// lascia tre righe, e il periodo in mezzo resta dimostrabile. Due colonne su
// `users` avrebbero perso il primo consenso, cioè proprio quello su cui un
// reclamo verterebbe.
func TestLaTracciaRegistraDatoERitirato(t *testing.T) {
	h := newHarness(t, user("u-1", "en"))
	ctx := context.Background()

	for _, step := range []func() (marketing.Applied, error){
		func() (marketing.Applied, error) { return h.service.Grant(ctx, "u-1", "") },
		func() (marketing.Applied, error) { return h.service.Withdraw(ctx, "u-1", "") },
		func() (marketing.Applied, error) { return h.service.Grant(ctx, "u-1", "") },
	} {
		if _, err := step(); err != nil {
			t.Fatalf("decisione: %v", err)
		}
	}

	history, err := h.service.History(ctx, "u-1")
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	attese := []marketing.Decision{
		marketing.DecisionGranted, // la più recente per prima
		marketing.DecisionWithdrawn,
		marketing.DecisionGranted,
	}
	if len(history) != len(attese) {
		t.Fatalf("la traccia ha %d righe, attese %d: %+v", len(history), len(attese), history)
	}
	for i, attesa := range attese {
		if history[i].Decision != attesa {
			t.Errorf("riga %d: decisione %q, attesa %q", i, history[i].Decision, attesa)
		}
		if history[i].OccurredAt.IsZero() {
			t.Errorf("riga %d: senza data. È la data che il documento promette di conservare", i)
		}
		if history[i].Source == "" {
			t.Errorf("riga %d: senza provenienza. È la parte della prova che dimostra "+
				"che il consenso non è stato chiesto in blocco", i)
		}
	}
}

// Una decisione ripetuta non allunga la traccia.
//
// Un secondo clic sullo stesso link, un form rinviato dal browser e una `POST`
// ripetuta sono la stessa decisione. La traccia deve raccontare le volte in cui
// l'utente ha cambiato idea, non le volte in cui una richiesta è stata ripetuta:
// una traccia gonfia di duplicati è più difficile da leggere proprio quando
// serve leggerla.
func TestUnaDecisioneRipetutaNonAllungaLaTraccia(t *testing.T) {
	h := newHarness(t, user("u-1", "en"))
	ctx := context.Background()

	if _, err := h.service.Grant(ctx, "u-1", ""); err != nil {
		t.Fatalf("Grant: %v", err)
	}
	applied, err := h.service.Grant(ctx, "u-1", "")
	if err != nil {
		t.Fatalf("Grant ripetuta: %v", err)
	}
	if applied != marketing.Unchanged {
		t.Errorf("la ripetizione ha prodotto %q, atteso %q", applied, marketing.Unchanged)
	}

	history, err := h.service.History(ctx, "u-1")
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	if len(history) != 1 {
		t.Errorf("la traccia ha %d righe dopo due consensi identici, attesa 1", len(history))
	}
}

// ------------------------------------------------------------ il link

// «Every marketing message carries an unsubscribe link».
//
// Il controllo non si accontenta della presenza di un link: verifica che sia
// **quello di quell'utente**, cioè che il token si verifichi e restituisca lui.
// Un link presente ma firmato per qualcun altro sarebbe peggio dell'assenza.
func TestOgniMessaggioPortaIlLinkDiDisiscrizione(t *testing.T) {
	h := newHarness(t, user("u-1", "en"))
	ctx := context.Background()

	if _, err := h.service.Grant(ctx, "u-1", ""); err != nil {
		t.Fatalf("Grant: %v", err)
	}
	if _, err := h.courier.Send(ctx, "u-1", testUpdate()); err != nil {
		t.Fatalf("Send: %v", err)
	}

	sent := h.sender.Sent()
	if len(sent) != 1 {
		t.Fatalf("messaggi partiti: %d, atteso 1", len(sent))
	}

	link := h.service.UnsubscribeURL("u-1")
	for corpo, testo := range map[string]string{"HTML": sent[0].HTML, "testo": sent[0].Text} {
		if !strings.Contains(testo, link) {
			t.Errorf("il corpo %s non porta il link di disiscrizione", corpo)
		}
	}

	// E il link deve funzionare: la firma torna, e riporta a questo utente.
	signer, err := marketing.NewSigner(testSecret)
	if err != nil {
		t.Fatalf("NewSigner: %v", err)
	}
	token := strings.TrimPrefix(link, "https://api.postqron.test/marketing/unsubscribe?token=")
	userID, err := signer.Verify(token)
	if err != nil {
		t.Fatalf("il link nell'email non si verifica: %v", err)
	}
	if userID != "u-1" {
		t.Errorf("il link porta all'utente %q invece che a u-1", userID)
	}
}

// «Works with one click and without signing in».
//
// La disiscrizione non chiede una sessione: l'unica credenziale è la firma del
// token, e il chiamante non passa nessun utente — lo ricava dal token.
func TestLaDisiscrizioneFunzionaSenzaSessione(t *testing.T) {
	h := newHarness(t, user("u-1", "en"))
	ctx := context.Background()

	if _, err := h.service.Grant(ctx, "u-1", ""); err != nil {
		t.Fatalf("Grant: %v", err)
	}

	token := tokenDi(t, h.service.UnsubscribeURL("u-1"))
	outcome, err := h.service.Unsubscribe(ctx, token, "198.51.100.4")
	if err != nil {
		t.Fatalf("Unsubscribe: %v", err)
	}
	if outcome.Applied != marketing.Recorded {
		t.Errorf("la disiscrizione ha prodotto %q, atteso %q", outcome.Applied, marketing.Recorded)
	}
	if outcome.UserID != "u-1" {
		t.Errorf("la disiscrizione ha colpito %q invece di u-1", outcome.UserID)
	}

	result, err := h.courier.Send(ctx, "u-1", testUpdate())
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if result != marketing.NoConsent {
		t.Errorf("dopo la disiscrizione dal link l'esito è %q, atteso %q", result, marketing.NoConsent)
	}

	// La provenienza resta scritta: è ciò che distingue una revoca chiesta dalle
	// impostazioni da una arrivata dal link.
	history, err := h.service.History(ctx, "u-1")
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	if history[0].Source != marketing.SourceUnsubscribeLink {
		t.Errorf("la revoca è registrata come %q, attesa %q", history[0].Source, marketing.SourceUnsubscribeLink)
	}
}

// La lettura del link **non** disiscrive nessuno.
//
// È il controllo che difende dal danno silenzioso: quell'indirizzo lo visitano
// gli scanner antivirus dei server di posta aziendali, i prefetch del browser e
// i crawler. Se bastasse leggerlo per disiscrivere, quegli utenti smetterebbero
// di ricevere le comunicazioni senza aver mai cliccato — e per R20.1 non
// avremmo nemmeno il modo di accorgercene.
func TestLeggereIlLinkNonDisiscriveNessuno(t *testing.T) {
	h := newHarness(t, user("u-1", "en"))
	ctx := context.Background()

	if _, err := h.service.Grant(ctx, "u-1", ""); err != nil {
		t.Fatalf("Grant: %v", err)
	}
	token := tokenDi(t, h.service.UnsubscribeURL("u-1"))

	// Dieci letture, come dieci scanner che aprono lo stesso messaggio.
	for range 10 {
		preview, err := h.service.Preview(ctx, token)
		if err != nil {
			t.Fatalf("Preview: %v", err)
		}
		if !preview.Consented {
			t.Fatal("la lettura del link ha revocato il consenso")
		}
		if preview.Language != "en" {
			t.Errorf("la pagina parlerebbe %q invece della lingua del profilo", preview.Language)
		}
	}

	state, err := h.service.Status(ctx, "u-1")
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if !state.Consented {
		t.Error("dopo dieci letture il consenso non c'è più")
	}

	history, err := h.service.History(ctx, "u-1")
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	if len(history) != 1 {
		t.Errorf("la lettura ha scritto nella traccia: %d righe, attesa 1", len(history))
	}

	// E l'anteprima non rivela l'indirizzo: la pagina la apre chiunque abbia il
	// link, e dirgli a chi appartiene sarebbe un'altra cosa da quella promessa.
	preview, err := h.service.Preview(ctx, token)
	if err != nil {
		t.Fatalf("Preview: %v", err)
	}
	if strings.Contains(preview.UserID, "@") {
		t.Error("l'anteprima porta un indirizzo email")
	}
}

// Un token che non si verifica non disiscrive nessuno, e non dice perché.
func TestUnTokenAlteratoNonVale(t *testing.T) {
	h := newHarness(t, user("u-1", "en"), user("u-2", "en"))
	ctx := context.Background()

	if _, err := h.service.Grant(ctx, "u-1", ""); err != nil {
		t.Fatalf("Grant: %v", err)
	}
	valido := tokenDi(t, h.service.UnsubscribeURL("u-1"))
	firma, _ := strings.CutPrefix(valido, "u-1.")

	casi := map[string]string{
		"vuoto":               "",
		"senza firma":         "u-1",
		"firma alterata":      "u-1." + strings.Repeat("0", len(firma)),
		"utente scambiato":    "u-2." + firma,
		"firma allungata":     tokenDi(t, h.service.UnsubscribeURL("u-2")) + "x",
		"separatore mancante": "u-1" + firma,
	}

	for nome, token := range casi {
		t.Run(nome, func(t *testing.T) {
			if _, err := h.service.Unsubscribe(ctx, token, ""); err == nil {
				t.Fatal("un token non valido ha disiscritto qualcuno")
			}
			if _, err := h.service.Preview(ctx, token); err == nil {
				t.Error("un token non valido ha prodotto un'anteprima")
			}
		})
	}

	state, err := h.service.Status(ctx, "u-1")
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if !state.Consented {
		t.Error("un token non valido ha revocato il consenso")
	}
}

// ------------------------------------------------- il confine col transazionale

// «Unsubscribing stops marketing email only».
//
// È la frase su cui è più facile sbagliare, perché il modo di sbagliarla è
// **non fare niente di sbagliato**: basta che un giorno qualcuno consulti il
// consenso al marketing in un punto che non è il marketing. Qui si disiscrive un
// utente e poi gli si manda un avviso di job fallito, che deve arrivare.
func TestDopoLaDisiscrizioneLeTransazionaliContinuano(t *testing.T) {
	h := newHarness(t, user("u-1", "en"))
	ctx := context.Background()

	if _, err := h.service.Grant(ctx, "u-1", ""); err != nil {
		t.Fatalf("Grant: %v", err)
	}
	if _, err := h.service.Unsubscribe(ctx, tokenDi(t, h.service.UnsubscribeURL("u-1")), ""); err != nil {
		t.Fatalf("Unsubscribe: %v", err)
	}

	// Il marketing è fermo.
	result, err := h.courier.Send(ctx, "u-1", testUpdate())
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if result != marketing.NoConsent {
		t.Fatalf("il marketing non si è fermato: %q", result)
	}

	// La coda transazionale, che di questo consenso non sa niente, continua.
	queue := notify.NewMemoryQueue()
	queue.WithRecipient(notify.Recipient{
		UserID: "u-1", Email: "u-1@example.test", Name: "Sam", Language: "en",
	})
	transazionale := &notify.RecordingSender{}
	service, err := notify.NewService(notify.Options{Queue: queue})
	if err != nil {
		t.Fatalf("notify.NewService: %v", err)
	}
	courier, err := notify.NewCourier(notify.CourierOptions{
		Queue:    queue,
		Renderer: newRenderer(t),
		Sender:   transazionale,
		Now:      func() time.Time { return time.Now().Add(time.Hour) },
	})
	if err != nil {
		t.Fatalf("notify.NewCourier: %v", err)
	}

	if err := service.Security(ctx, notify.SecurityEvent{
		UserID: "u-1", Kind: notify.SecurityAPIKeyRevoked, ResourceName: "CI deploy",
	}); err != nil {
		t.Fatalf("Security: %v", err)
	}
	if err := service.JobFailed(ctx, notify.JobFailure{
		UserID: "u-1", JobID: "job-1", JobName: "nightly-invoices",
		Environment: "production", Kind: notify.FailureTimeout,
	}); err != nil {
		t.Fatalf("JobFailed: %v", err)
	}

	stats, err := courier.Deliver(ctx)
	if err != nil {
		t.Fatalf("Deliver: %v", err)
	}
	if stats.Sent != 2 {
		t.Fatalf("dopo la disiscrizione sono partite %d email transazionali, attese 2: "+
			"§2.8 dice che la disiscrizione ferma solo il marketing", stats.Sent)
	}
	for _, email := range transazionale.Sent() {
		if strings.Contains(strings.ToLower(email.HTML), "unsubscribe") {
			t.Error("un'email transazionale offre la disiscrizione")
		}
	}
}

// ------------------------------------------------------------------- lingua

// R33: la lingua del profilo decide, anche per il marketing.
func TestLaComunicazioneParlaLaLinguaDelProfilo(t *testing.T) {
	h := newHarness(t, user("u-de", "de"), user("u-es", "es"))
	ctx := context.Background()

	for _, id := range []string{"u-de", "u-es"} {
		if _, err := h.service.Grant(ctx, id, ""); err != nil {
			t.Fatalf("Grant %s: %v", id, err)
		}
		if _, err := h.courier.Send(ctx, id, testUpdate()); err != nil {
			t.Fatalf("Send %s: %v", id, err)
		}
	}

	sent := h.sender.Sent()
	if len(sent) != 2 {
		t.Fatalf("messaggi partiti: %d, attesi 2", len(sent))
	}
	// Il tedesco è tradotto: arriva il testo tedesco.
	if !strings.Contains(sent[0].Text, "Überlappende Läufe") {
		t.Error("l'utente tedesco non ha ricevuto il testo tedesco")
	}
	// Lo spagnolo non lo è: ricade sull'inglese, che è la sorgente e il ripiego.
	if !strings.Contains(sent[1].Text, "Overlapping runs") {
		t.Error("l'utente spagnolo non è ricaduto sull'inglese")
	}
}

// Una comunicazione senza inglese non si manda: sarebbe un invio che lascia
// fuori chi ha scelto una lingua non tradotta.
func TestUnaComunicazioneSenzaIngleseNonSiManda(t *testing.T) {
	h := newHarness(t, user("u-1", "en"))

	_, err := h.courier.Send(context.Background(), "u-1", marketing.Update{
		Content: map[string]marketing.Content{
			"it": {Headline: "Novità", Paragraphs: []string{"Testo."}},
		},
	})
	if err == nil {
		t.Fatal("una comunicazione senza la lingua sorgente è stata accettata")
	}
}

// Un account che non c'è non riceve, e non è un errore.
func TestUnAccountAssenteNonRiceve(t *testing.T) {
	h := newHarness(t)

	result, err := h.courier.Send(context.Background(), "u-ignoto", testUpdate())
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if result != marketing.NoRecipient {
		t.Errorf("esito %q, atteso %q", result, marketing.NoRecipient)
	}
}

// ------------------------------------------------------------------ supporto

// tokenDi estrae il token da un link di disiscrizione.
func tokenDi(t *testing.T, link string) string {
	t.Helper()
	_, token, found := strings.Cut(link, "token=")
	if !found {
		t.Fatalf("il link non porta un token: %q", link)
	}
	return token
}
