package notifypg_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/apdsoftware/postqron/services/api/internal/notify"
	"github.com/apdsoftware/postqron/services/api/internal/notifypg"
)

func newStore(t *testing.T) (*notifypg.Store, *pgxpool.Pool) {
	t.Helper()
	pool := newTestDatabase(t)
	store, err := notifypg.New(pool)
	if err != nil {
		t.Fatalf("notifypg.New: %v", err)
	}
	return store, pool
}

func newUser(t *testing.T, pool *pgxpool.Pool, email, language string) string {
	t.Helper()
	var id string
	err := pool.QueryRow(t.Context(),
		`INSERT INTO users (email, full_name, language) VALUES ($1, $2, $3) RETURNING id::text`,
		email, "Mario Rossi", language).Scan(&id)
	if err != nil {
		t.Fatalf("creazione dell'utente: %v", err)
	}
	return id
}

// newJob crea un job con i canali di avviso indicati.
func newJob(t *testing.T, pool *pgxpool.Pool, userID, name string, channels []string) string {
	t.Helper()
	var id string
	err := pool.QueryRow(t.Context(),
		`INSERT INTO jobs (user_id, name, url, schedule, alert_on_failure)
		 VALUES ($1::uuid, $2, 'https://example.com/hook', '0 9 * * *', $3::text[]::alert_channel[])
		 RETURNING id::text`,
		userID, name, channels).Scan(&id)
	if err != nil {
		t.Fatalf("creazione del job: %v", err)
	}
	return id
}

var epoch = time.Date(2026, 8, 17, 10, 0, 0, 0, time.UTC)

func failureRequest(userID, jobID, key string, at time.Time) notify.Request {
	return notify.Request{
		Event:                 notify.EventJobFailed,
		Channel:               notify.ChannelEmail,
		UserID:                userID,
		DedupeKey:             key,
		ScheduledAt:           at.Add(5 * time.Minute),
		JobID:                 jobID,
		Environment:           "production",
		ExecutionScheduledFor: at,
		ExecutionAttempt:      1,
		Payload: notify.Payload{
			JobName: "nightly", Failures: 1, LastAttemptAt: at,
			FailureKind: notify.FailureTimeout,
		},
	}
}

// ---------------------------------------------------------------- il giro

// Una notifica accodata torna indietro con il destinatario e la lingua del
// profilo, letti al momento della presa in carico.
func TestAndataERitornoDellaNotifica(t *testing.T) {
	store, pool := newStore(t)
	ctx := t.Context()
	userID := newUser(t, pool, "mario.rossi@example.com", "de")
	jobID := newJob(t, pool, userID, "nightly", []string{"email"})

	result, err := store.Enqueue(ctx, failureRequest(userID, jobID, "k-1", epoch))
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	if result != notify.Queued {
		t.Fatalf("esito dell'accodamento: %q", result)
	}

	pending, err := store.Due(ctx, epoch.Add(6*time.Minute), 10, time.Minute)
	if err != nil {
		t.Fatalf("Due: %v", err)
	}
	if len(pending) != 1 {
		t.Fatalf("notifiche pronte: %d", len(pending))
	}

	p := pending[0]
	switch {
	case p.Event != notify.EventJobFailed:
		t.Errorf("evento: %q", p.Event)
	case p.Recipient.Email != "mario.rossi@example.com":
		t.Errorf("destinatario: %q", p.Recipient.Email)
	case p.Recipient.Language != "de":
		t.Errorf("lingua: %q, attesa quella del profilo (R33)", p.Recipient.Language)
	case p.Recipient.Name != "Mario Rossi":
		t.Errorf("nome: %q", p.Recipient.Name)
	case p.JobID != jobID:
		t.Errorf("job: %q", p.JobID)
	case p.Environment != "production":
		t.Errorf("ambiente: %q", p.Environment)
	case p.Payload.JobName != "nightly" || p.Payload.Failures != 1:
		t.Errorf("payload: %+v", p.Payload)
	case p.Payload.FailureKind != notify.FailureTimeout:
		t.Errorf("classificazione: %q", p.Payload.FailureKind)
	case p.Attempts != 1:
		t.Errorf("tentativi: %d", p.Attempts)
	}

	if err := store.MarkSent(ctx, p.ID, "log-abc", epoch.Add(7*time.Minute)); err != nil {
		t.Fatalf("MarkSent: %v", err)
	}

	var status, emailLogID string
	var sentAt *time.Time
	if err := pool.QueryRow(ctx,
		`SELECT status::text, coalesce(email_log_id, ''), sent_at FROM notifications WHERE id = $1::uuid`,
		p.ID).Scan(&status, &emailLogID, &sentAt); err != nil {
		t.Fatalf("rilettura: %v", err)
	}
	if status != "sent" || emailLogID != "log-abc" || sentAt == nil {
		t.Errorf("riga chiusa male: status %q, email_log_id %q, sent_at %v", status, emailLogID, sentAt)
	}
}

// Una notifica non ancora dovuta non viene presa in carico: è la finestra di
// grazia in cui i fallimenti si raggruppano.
func TestUnaNotificaNonDovutaRestaInCoda(t *testing.T) {
	store, pool := newStore(t)
	ctx := t.Context()
	userID := newUser(t, pool, "mario.rossi@example.com", "en")
	jobID := newJob(t, pool, userID, "nightly", []string{"email"})

	if _, err := store.Enqueue(ctx, failureRequest(userID, jobID, "k-1", epoch)); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	pending, err := store.Due(ctx, epoch.Add(time.Minute), 10, time.Minute)
	if err != nil {
		t.Fatalf("Due: %v", err)
	}
	if len(pending) != 0 {
		t.Errorf("notifiche prese in carico prima della scadenza: %d", len(pending))
	}
}

// ------------------------------------------------------- la deduplicazione

// Duemila fallimenti con la stessa chiave producono **una** notifica, e il
// conteggio della raffica finisce nel payload dell'unico avviso.
//
// È la proprietà su cui poggia tutta la politica anti-spam, ed è del database:
// qui si verifica contro PostgreSQL, non contro un doppio.
func TestUnaRaffticaDiFallimentiProduceUnaNotificaSola(t *testing.T) {
	store, pool := newStore(t)
	ctx := t.Context()
	userID := newUser(t, pool, "mario.rossi@example.com", "en")
	jobID := newJob(t, pool, userID, "nightly", []string{"email"})

	const failures = 2000
	for i := range failures {
		at := epoch.Add(time.Duration(i) * time.Second)
		result, err := store.Enqueue(ctx, failureRequest(userID, jobID, "k-1", at))
		if err != nil {
			t.Fatalf("Enqueue al fallimento %d: %v", i, err)
		}
		if i == 0 && result != notify.Queued {
			t.Fatalf("il primo fallimento non ha accodato niente: %q", result)
		}
		if i > 0 && result != notify.Grouped {
			t.Fatalf("il fallimento %d non è stato raggruppato: %q", i, result)
		}
	}

	var rows int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM notifications`).Scan(&rows); err != nil {
		t.Fatalf("conteggio: %v", err)
	}
	if rows != 1 {
		t.Fatalf("%d fallimenti hanno prodotto %d notifiche", failures, rows)
	}

	pending, err := store.Due(ctx, epoch.Add(time.Hour), 10, time.Minute)
	if err != nil {
		t.Fatalf("Due: %v", err)
	}
	if got := pending[0].Payload.Failures; got != failures {
		t.Errorf("fallimenti raccontati dall'unico avviso: %d, attesi %d — "+
			"raggruppare non deve voler dire perdere il conto", got, failures)
	}
}

// Cinquanta accodamenti **concorrenti** con la stessa chiave producono una
// notifica sola.
//
// È il caso che un controllo in Go non regge: fra il «esiste già?» e l'INSERT
// due connessioni si infilano insieme. Un job che fallisce ogni secondo produce
// fallimenti concorrenti per costruzione, quindi questo non è un caso limite: è
// il caso normale del carico da cui la politica difende.
func TestAccodamentiConcorrentiNonSiSdoppiano(t *testing.T) {
	store, pool := newStore(t)
	ctx := context.Background()
	userID := newUser(t, pool, "mario.rossi@example.com", "en")
	jobID := newJob(t, pool, userID, "nightly", []string{"email"})

	const concurrency = 50
	var wg sync.WaitGroup
	errs := make(chan error, concurrency)
	for i := range concurrency {
		wg.Add(1)
		go func() {
			defer wg.Done()
			at := epoch.Add(time.Duration(i) * time.Millisecond)
			if _, err := store.Enqueue(ctx, failureRequest(userID, jobID, "k-1", at)); err != nil {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("Enqueue concorrente: %v", err)
	}

	var rows int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM notifications`).Scan(&rows); err != nil {
		t.Fatalf("conteggio: %v", err)
	}
	if rows != 1 {
		t.Errorf("%d accodamenti concorrenti hanno prodotto %d notifiche", concurrency, rows)
	}
}

// Un avviso già partito non si riapre: il conflitto su una riga chiusa non
// genera una seconda email e non tocca il conteggio di quella vecchia.
func TestUnAvvisoGiaPartitoNonSiRiapre(t *testing.T) {
	store, pool := newStore(t)
	ctx := t.Context()
	userID := newUser(t, pool, "mario.rossi@example.com", "en")
	jobID := newJob(t, pool, userID, "nightly", []string{"email"})

	if _, err := store.Enqueue(ctx, failureRequest(userID, jobID, "k-1", epoch)); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	pending, err := store.Due(ctx, epoch.Add(time.Hour), 10, time.Minute)
	if err != nil {
		t.Fatalf("Due: %v", err)
	}
	if err := store.MarkSent(ctx, pending[0].ID, "log-1", epoch.Add(time.Hour)); err != nil {
		t.Fatalf("MarkSent: %v", err)
	}

	result, err := store.Enqueue(ctx, failureRequest(userID, jobID, "k-1", epoch.Add(2*time.Hour)))
	if err != nil {
		t.Fatalf("Enqueue dopo l'invio: %v", err)
	}
	if result != notify.Grouped {
		t.Errorf("esito: %q, atteso un raggruppamento silenzioso", result)
	}

	var rows int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM notifications`).Scan(&rows); err != nil {
		t.Fatalf("conteggio: %v", err)
	}
	if rows != 1 {
		t.Errorf("notifiche in tabella: %d", rows)
	}
}

// ----------------------------------------------------------- destinatari

// Un job che non ha chiesto avvisi via email non ne riceve: `alert_on_failure`
// vuoto è una scelta legittima per un job rumoroso, e va rispettata prima di
// scrivere la riga.
func TestUnJobSenzaAvvisiEmailNonAccodaNiente(t *testing.T) {
	store, pool := newStore(t)
	ctx := t.Context()
	userID := newUser(t, pool, "mario.rossi@example.com", "en")
	muto := newJob(t, pool, userID, "muto", []string{})
	slackOnly := newJob(t, pool, userID, "solo-slack", []string{"slack"})

	for name, jobID := range map[string]string{"senza canali": muto, "solo slack": slackOnly} {
		result, err := store.Enqueue(ctx, failureRequest(userID, jobID, "k-"+name, epoch))
		if err != nil {
			t.Fatalf("%s: Enqueue: %v", name, err)
		}
		if result != notify.NoRecipient {
			t.Errorf("%s: esito %q, atteso nessun destinatario", name, result)
		}
	}

	var rows int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM notifications`).Scan(&rows); err != nil {
		t.Fatalf("conteggio: %v", err)
	}
	if rows != 0 {
		t.Errorf("notifiche accodate: %d, attese zero", rows)
	}
}

// A un account chiuso non si accoda niente, e l'account chiuso **dopo**
// l'accodamento arriva al corriere con il suo contrassegno.
func TestUnAccountChiusoNonRiceveNiente(t *testing.T) {
	store, pool := newStore(t)
	ctx := t.Context()
	userID := newUser(t, pool, "mario.rossi@example.com", "en")

	// Prima si accoda, poi si chiude l'account: la notifica esiste già.
	if _, err := store.Enqueue(ctx, notify.Request{
		Event: notify.EventWelcome, Channel: notify.ChannelEmail,
		UserID: userID, DedupeKey: "welcome:" + userID, ScheduledAt: epoch,
	}); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`UPDATE users SET deleted_at = now() WHERE id = $1::uuid`, userID); err != nil {
		t.Fatalf("chiusura dell'account: %v", err)
	}

	pending, err := store.Due(ctx, epoch.Add(time.Minute), 10, time.Minute)
	if err != nil {
		t.Fatalf("Due: %v", err)
	}
	if len(pending) != 1 || !pending[0].Recipient.Closed {
		t.Fatalf("il corriere non vede l'account chiuso: %+v", pending)
	}

	// E da chiuso non si accoda più niente.
	result, err := store.Enqueue(ctx, notify.Request{
		Event: notify.EventSecurity, Channel: notify.ChannelEmail,
		UserID: userID, DedupeKey: "security:" + userID, ScheduledAt: epoch,
	})
	if err != nil {
		t.Fatalf("Enqueue su account chiuso: %v", err)
	}
	if result != notify.NoRecipient {
		t.Errorf("esito: %q", result)
	}
}

// ------------------------------------------------------- presa in carico

// Due corrieri non si prendono la stessa notifica, e una presa in carico che
// non si chiude torna in coda alla scadenza del contratto.
func TestLaPresaInCaricoNonSiSdoppiaEScade(t *testing.T) {
	store, pool := newStore(t)
	ctx := t.Context()
	userID := newUser(t, pool, "mario.rossi@example.com", "en")

	if _, err := store.Enqueue(ctx, notify.Request{
		Event: notify.EventWelcome, Channel: notify.ChannelEmail,
		UserID: userID, DedupeKey: "welcome:" + userID, ScheduledAt: epoch,
	}); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	first, err := store.Due(ctx, epoch.Add(time.Minute), 10, 10*time.Minute)
	if err != nil {
		t.Fatalf("prima passata: %v", err)
	}
	if len(first) != 1 {
		t.Fatalf("prima passata: %d notifiche", len(first))
	}

	// Un secondo corriere, subito dopo, non vede niente.
	second, err := store.Due(ctx, epoch.Add(2*time.Minute), 10, 10*time.Minute)
	if err != nil {
		t.Fatalf("seconda passata: %v", err)
	}
	if len(second) != 0 {
		t.Errorf("seconda passata: %d notifiche, attese zero", len(second))
	}

	// Scaduto il contratto — il corriere è morto senza chiudere la riga — la
	// notifica torna disponibile, con il tentativo contato.
	third, err := store.Due(ctx, epoch.Add(20*time.Minute), 10, 10*time.Minute)
	if err != nil {
		t.Fatalf("terza passata: %v", err)
	}
	if len(third) != 1 {
		t.Fatalf("terza passata: %d notifiche, attesa una", len(third))
	}
	if third[0].Attempts != 2 {
		t.Errorf("tentativi: %d, attesi due", third[0].Attempts)
	}
}

// Il rinvio riporta la notifica in coda con il motivo scritto; la chiusura
// definitiva la toglie di mezzo.
func TestRinvioEChiusura(t *testing.T) {
	store, pool := newStore(t)
	ctx := t.Context()
	userID := newUser(t, pool, "mario.rossi@example.com", "en")

	enqueue := func(key string) string {
		t.Helper()
		if _, err := store.Enqueue(ctx, notify.Request{
			Event: notify.EventWelcome, Channel: notify.ChannelEmail,
			UserID: userID, DedupeKey: key, ScheduledAt: epoch,
		}); err != nil {
			t.Fatalf("Enqueue: %v", err)
		}
		var id string
		if err := pool.QueryRow(ctx,
			`SELECT id::text FROM notifications WHERE dedupe_key = $1`, key).Scan(&id); err != nil {
			t.Fatalf("lettura dell'id: %v", err)
		}
		return id
	}

	rinviata := enqueue("a")
	if err := store.Retry(ctx, rinviata, epoch.Add(time.Hour), "429 rate_limited"); err != nil {
		t.Fatalf("Retry: %v", err)
	}

	fallita := enqueue("b")
	if err := store.MarkFailed(ctx, fallita, "403 domain_not_verified", epoch); err != nil {
		t.Fatalf("MarkFailed: %v", err)
	}

	saltata := enqueue("c")
	if err := store.MarkSkipped(ctx, saltata, "account chiuso", epoch); err != nil {
		t.Fatalf("MarkSkipped: %v", err)
	}

	expected := map[string]string{rinviata: "pending", fallita: "failed", saltata: "skipped"}
	for id, want := range expected {
		var status, reason string
		if err := pool.QueryRow(ctx,
			`SELECT status::text, coalesce(error, '') FROM notifications WHERE id = $1::uuid`,
			id).Scan(&status, &reason); err != nil {
			t.Fatalf("rilettura: %v", err)
		}
		if status != want {
			t.Errorf("notifica %s: stato %q, atteso %q", id, status, want)
		}
		if reason == "" {
			t.Errorf("notifica %s: nessun motivo scritto", id)
		}
	}

	// La rinviata torna sola quando è ora, la fallita e la saltata mai più.
	pending, err := store.Due(ctx, epoch.Add(2*time.Hour), 10, time.Minute)
	if err != nil {
		t.Fatalf("Due: %v", err)
	}
	if len(pending) != 1 || pending[0].ID != rinviata {
		t.Errorf("notifiche pronte: %+v, attesa solo la rinviata", pending)
	}
}

// Un testo di diagnostica lunghissimo non fa fallire la chiusura della riga.
//
// Il caso vero è la pagina di blocco di Cloudflare, che arriva al posto del JSON
// previsto e può essere lunga a piacere: perdere la diagnosi perché la diagnosi
// era troppo lunga sarebbe il modo peggiore di fallire.
func TestUnErroreLunghissimoNonFaFallireLaChiusura(t *testing.T) {
	store, pool := newStore(t)
	ctx := t.Context()
	userID := newUser(t, pool, "mario.rossi@example.com", "en")

	if _, err := store.Enqueue(ctx, notify.Request{
		Event: notify.EventWelcome, Channel: notify.ChannelEmail,
		UserID: userID, DedupeKey: "welcome:" + userID, ScheduledAt: epoch,
	}); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	var id string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM notifications`).Scan(&id); err != nil {
		t.Fatalf("lettura dell'id: %v", err)
	}

	lungo := ""
	for range 5000 {
		lungo += "è"
	}
	if err := store.MarkFailed(ctx, id, lungo, epoch); err != nil {
		t.Fatalf("MarkFailed: %v", err)
	}

	var stored string
	if err := pool.QueryRow(ctx,
		`SELECT coalesce(error, '') FROM notifications WHERE id = $1::uuid`, id).Scan(&stored); err != nil {
		t.Fatalf("rilettura: %v", err)
	}
	if len([]rune(stored)) > 1100 {
		t.Errorf("testo conservato lungo %d rune: la colonna di diagnostica non è un archivio", len([]rune(stored)))
	}
	if stored == "" {
		t.Error("nessuna diagnosi conservata")
	}
}
