package billingpg_test

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/apdsoftware/postqron/services/api/internal/billing"
	"github.com/apdsoftware/postqron/services/api/internal/billingpg"
	"github.com/apdsoftware/postqron/services/api/internal/jobs"
	"github.com/apdsoftware/postqron/services/api/internal/jobspg"
	"github.com/apdsoftware/postqron/services/api/internal/paddle"
)

// Questo file prova ciò che internal/billing non può provare da solo: **R58 è
// una istruzione SQL**, e una finzione in memoria proverebbe la finzione. Qui
// gira contro le migrazioni vere, sui piani veri di SPEC §8 inseriti dalla 0003.

var (
	t1 = time.Date(2026, 8, 17, 9, 0, 0, 0, time.UTC)
	t2 = time.Date(2026, 8, 17, 9, 30, 0, 0, time.UTC)
	t3 = time.Date(2026, 8, 17, 10, 0, 0, 0, time.UTC)
)

// ------------------------------------------------------------------ supporto

func nuovoStore(t *testing.T) (*billingpg.Store, *pgxpool.Pool) {
	t.Helper()
	pool := newTestDatabase(t)
	store, err := billingpg.New(pool)
	if err != nil {
		t.Fatalf("costruzione dello store: %v", err)
	}
	return store, pool
}

func nuovoUtente(t *testing.T, pool *pgxpool.Pool, email string) string {
	t.Helper()
	var id string
	if err := pool.QueryRow(t.Context(),
		`INSERT INTO users (email) VALUES ($1) RETURNING id::text`, email).Scan(&id); err != nil {
		t.Fatalf("creazione dell'utente: %v", err)
	}
	return id
}

// sottoscrive crea una riga di sottoscrizione già in forza, come l'avrebbe
// lasciata un acquisto precedente.
func sottoscrive(t *testing.T, pool *pgxpool.Pool, userID, planCode, subID string) {
	t.Helper()
	_, err := pool.Exec(t.Context(),
		`INSERT INTO subscriptions
		     (user_id, plan_code, status, billing_period, paddle_subscription_id, paddle_event_occurred_at)
		 VALUES ($1::uuid, $2, 'active', 'monthly', $3, $4)`,
		userID, planCode, subID, t1)
	if err != nil {
		t.Fatalf("sottoscrizione al piano %q: %v", planCode, err)
	}
}

// creaJob inserisce un job a intervallo. `every` a zero produce un job cron, che
// per costruzione non può violare la risoluzione di nessun piano (SPEC §9).
func creaJob(t *testing.T, pool *pgxpool.Pool, userID, nome string, every int, enabled bool) string {
	t.Helper()
	var (
		id       string
		schedule any
		seconds  any
	)
	if every > 0 {
		seconds = every
	} else {
		schedule = "0 9 * * *"
	}
	err := pool.QueryRow(t.Context(),
		`INSERT INTO jobs (user_id, name, schedule, every_seconds, url, enabled)
		 VALUES ($1::uuid, $2, $3, $4, 'https://esempio.test/hook', $5)
		 RETURNING id::text`,
		userID, nome, schedule, seconds, enabled).Scan(&id)
	if err != nil {
		t.Fatalf("creazione del job %q: %v", nome, err)
	}
	return id
}

type statoJob struct {
	Enabled   bool
	Sospeso   bool
	Motivo    string
	NextRunAt *time.Time
}

func leggiJob(t *testing.T, pool *pgxpool.Pool, jobID string) statoJob {
	t.Helper()
	var (
		stato  statoJob
		motivo *string
		quando *time.Time
	)
	err := pool.QueryRow(t.Context(),
		`SELECT enabled, suspended_at, suspended_reason::text, next_run_at
		   FROM jobs WHERE id = $1::uuid`, jobID).
		Scan(&stato.Enabled, &quando, &motivo, &stato.NextRunAt)
	if err != nil {
		t.Fatalf("lettura del job: %v", err)
	}
	stato.Sospeso = quando != nil
	if motivo != nil {
		stato.Motivo = *motivo
	}
	return stato
}

func contaJob(t *testing.T, pool *pgxpool.Pool, userID string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(t.Context(),
		`SELECT count(*) FROM jobs WHERE user_id = $1::uuid`, userID).Scan(&n); err != nil {
		t.Fatalf("conteggio dei job: %v", err)
	}
	return n
}

func cambio(userID, subID, planCode string, stato paddle.SubscriptionStatus, quando time.Time) billing.SubscriptionChange {
	return billing.SubscriptionChange{
		UserID:               userID,
		PaddleSubscriptionID: subID,
		PaddleCustomerID:     "ctm_01",
		PlanCode:             planCode,
		Period:               paddle.PeriodMonthly,
		Status:               string(stato),
		OccurredAt:           quando,
	}
}

// -------------------------------------------------------------------- R58

// Il caso che R58 descrive per esteso: i job attivi superano il tetto del piano
// di destinazione, quindi **si sospendono tutti** e l'utente ne riaccende quanti
// il piano ne consente. Non scegliamo noi quali salvare.
func TestDowngradeOltreIlTettoSospendeTutto(t *testing.T) {
	store, pool := nuovoStore(t)
	utente := nuovoUtente(t, pool, "oltre@esempio.test")
	sottoscrive(t, pool, utente, "pro", "sub_01")

	// Venticinque job orari: il piano Pro ne regge 200, il Free 20.
	var jobIDs []string
	for i := range 25 {
		jobIDs = append(jobIDs, creaJob(t, pool, utente, fmt.Sprintf("job-%02d", i), 3600, true))
	}

	// Il pagamento non è andato a buon fine e la sottoscrizione è finita: si torna
	// al piano Free (Termini §4.2).
	risultato, err := store.SaveSubscription(t.Context(),
		cambio(utente, "sub_01", paddle.PlanFree, paddle.SubscriptionCanceled, t2))
	if err != nil {
		t.Fatalf("scrittura della sottoscrizione: %v", err)
	}
	if !risultato.Applied {
		t.Fatal("la cancellazione doveva essere applicata")
	}

	sospensione, err := store.EnforcePlanLimits(t.Context(), utente, paddle.PlanFree, t2)
	if err != nil {
		t.Fatalf("applicazione di R58: %v", err)
	}

	if sospensione.ByJobLimit != 25 || sospensione.ByResolution != 0 {
		t.Fatalf("sospensione = %+v, attesi 25 per tetto", sospensione)
	}
	for _, id := range jobIDs {
		stato := leggiJob(t, pool, id)
		switch {
		case stato.Enabled:
			t.Fatalf("job %s ancora acceso", id)
		case !stato.Sospeso:
			t.Fatalf("job %s spento senza traccia della sospensione: sarebbe indistinguibile da una pausa dell'utente", id)
		case stato.Motivo != string(billing.ReasonJobLimit):
			t.Fatalf("job %s: motivo = %q", id, stato.Motivo)
		case stato.NextRunAt != nil:
			t.Fatalf("job %s: prossima occorrenza non azzerata", id)
		}
	}

	// **Nulla viene cancellato**: è la promessa di R58 e dei Termini §4.1.
	if n := contaJob(t, pool, utente); n != 25 {
		t.Fatalf("job rimasti = %d, attesi 25: R58 non cancella niente", n)
	}

	// E il piano che l'API applicherà è davvero il Free: le due letture — quella
	// che decide cosa l'utente può fare e quella che glielo mostra — devono
	// concordare.
	piani, err := jobspg.New(pool)
	if err != nil {
		t.Fatalf("costruzione di jobspg: %v", err)
	}
	piano, err := piani.PlanForUser(t.Context(), utente)
	if err != nil {
		t.Fatalf("lettura del piano: %v", err)
	}
	if piano.Code != paddle.PlanFree {
		t.Fatalf("piano applicato = %q, atteso free", piano.Code)
	}
}

// «Se i job attivi rientrano già nel nuovo tetto, non si tocca niente.» Fermare
// tutto quando non serve sarebbe un danno gratuito.
func TestDowngradeDentroIlTettoNonToccaNiente(t *testing.T) {
	store, pool := nuovoStore(t)
	utente := nuovoUtente(t, pool, "dentro@esempio.test")
	sottoscrive(t, pool, utente, "pro", "sub_01")

	var jobIDs []string
	for i := range 5 {
		jobIDs = append(jobIDs, creaJob(t, pool, utente, fmt.Sprintf("job-%d", i), 3600, true))
	}

	if _, err := store.SaveSubscription(t.Context(),
		cambio(utente, "sub_01", paddle.PlanFree, paddle.SubscriptionCanceled, t2)); err != nil {
		t.Fatalf("scrittura: %v", err)
	}
	sospensione, err := store.EnforcePlanLimits(t.Context(), utente, paddle.PlanFree, t2)
	if err != nil {
		t.Fatalf("applicazione di R58: %v", err)
	}

	if sospensione.Total() != 0 {
		t.Fatalf("sospensione = %+v, atteso niente", sospensione)
	}
	for _, id := range jobIDs {
		if stato := leggiJob(t, pool, id); !stato.Enabled || stato.Sospeso {
			t.Fatalf("job %s toccato senza motivo: %+v", id, stato)
		}
	}
}

// «La risoluzione è un secondo vincolo, indipendente dal numero.» Cinque job
// stanno comodamente nel tetto del Free, ma tre girano ogni secondo e il Free si
// ferma al minuto: quei tre si spengono, e **solo quelli** — riaccenderne un
// altro non libererebbe posto, perché non è una questione di posto.
func TestRisoluzioneSospendeAncheQuandoIlNumeroCiSta(t *testing.T) {
	store, pool := nuovoStore(t)
	utente := nuovoUtente(t, pool, "risoluzione@esempio.test")
	sottoscrive(t, pool, utente, "team", "sub_01")

	fitti := []string{
		creaJob(t, pool, utente, "fitto-1", 1, true),
		creaJob(t, pool, utente, "fitto-2", 1, true),
		creaJob(t, pool, utente, "fitto-3", 30, true),
	}
	larghi := []string{
		creaJob(t, pool, utente, "largo-1", 3600, true),
		creaJob(t, pool, utente, "largo-2", 0, true), // cron: mai più fitto del minuto
	}

	if _, err := store.SaveSubscription(t.Context(),
		cambio(utente, "sub_01", paddle.PlanFree, paddle.SubscriptionCanceled, t2)); err != nil {
		t.Fatalf("scrittura: %v", err)
	}
	sospensione, err := store.EnforcePlanLimits(t.Context(), utente, paddle.PlanFree, t2)
	if err != nil {
		t.Fatalf("applicazione di R58: %v", err)
	}

	if sospensione.ByResolution != 3 || sospensione.ByJobLimit != 0 {
		t.Fatalf("sospensione = %+v, attesi 3 per risoluzione", sospensione)
	}
	for _, id := range fitti {
		stato := leggiJob(t, pool, id)
		if stato.Enabled || stato.Motivo != string(billing.ReasonResolution) {
			t.Fatalf("job troppo fitto %s: %+v", id, stato)
		}
	}
	for _, id := range larghi {
		if stato := leggiJob(t, pool, id); !stato.Enabled || stato.Sospeso {
			t.Fatalf("job compatibile %s toccato: %+v", id, stato)
		}
	}
}

// Quando scattano entrambe le regole, il motivo registrato è quello che l'utente
// deve risolvere per primo: riaccendere un job troppo fitto fallirebbe comunque,
// anche dopo aver liberato posto.
func TestQuandoScattanoEntrambeVinceLaRisoluzione(t *testing.T) {
	store, pool := nuovoStore(t)
	utente := nuovoUtente(t, pool, "entrambe@esempio.test")
	sottoscrive(t, pool, utente, "team", "sub_01")

	var fitti, larghi []string
	for i := range 25 {
		if i < 5 {
			fitti = append(fitti, creaJob(t, pool, utente, fmt.Sprintf("fitto-%02d", i), 1, true))
			continue
		}
		larghi = append(larghi, creaJob(t, pool, utente, fmt.Sprintf("largo-%02d", i), 3600, true))
	}

	if _, err := store.SaveSubscription(t.Context(),
		cambio(utente, "sub_01", paddle.PlanFree, paddle.SubscriptionCanceled, t2)); err != nil {
		t.Fatalf("scrittura: %v", err)
	}
	sospensione, err := store.EnforcePlanLimits(t.Context(), utente, paddle.PlanFree, t2)
	if err != nil {
		t.Fatalf("applicazione di R58: %v", err)
	}

	if sospensione.ByResolution != 5 || sospensione.ByJobLimit != 20 {
		t.Fatalf("sospensione = %+v, attesi 5 per risoluzione e 20 per tetto", sospensione)
	}
	for _, id := range fitti {
		if motivo := leggiJob(t, pool, id).Motivo; motivo != string(billing.ReasonResolution) {
			t.Errorf("job fitto %s: motivo = %q", id, motivo)
		}
	}
	for _, id := range larghi {
		if motivo := leggiJob(t, pool, id).Motivo; motivo != string(billing.ReasonJobLimit) {
			t.Errorf("job largo %s: motivo = %q", id, motivo)
		}
	}
}

// Una consegna fallita e ripetuta riesegue R58: la seconda passata non deve
// trovare più niente da spegnere, né sovrascrivere l'istante della prima.
func TestApplicazioneDiR58Idempotente(t *testing.T) {
	store, pool := nuovoStore(t)
	utente := nuovoUtente(t, pool, "idempotente@esempio.test")
	sottoscrive(t, pool, utente, "pro", "sub_01")
	for i := range 25 {
		creaJob(t, pool, utente, fmt.Sprintf("job-%02d", i), 3600, true)
	}

	prima, err := store.EnforcePlanLimits(t.Context(), utente, paddle.PlanFree, t2)
	if err != nil {
		t.Fatalf("prima applicazione: %v", err)
	}
	seconda, err := store.EnforcePlanLimits(t.Context(), utente, paddle.PlanFree, t3)
	if err != nil {
		t.Fatalf("seconda applicazione: %v", err)
	}

	if prima.Total() != 25 {
		t.Fatalf("prima applicazione = %+v", prima)
	}
	if seconda.Total() != 0 {
		t.Fatalf("seconda applicazione = %+v, attesa nessuna sospensione", seconda)
	}
}

// Un job già in pausa non è attivo: non occupa un posto fra gli attivi e non c'è
// ragione di marcarlo come sospeso da noi. Un job archiviato è già fuori dal
// `cron.yaml` dell'utente e non si tocca affatto.
func TestPausaDellUtenteEArchiviatiRestanoDistinti(t *testing.T) {
	store, pool := nuovoStore(t)
	utente := nuovoUtente(t, pool, "distinti@esempio.test")

	inPausa := creaJob(t, pool, utente, "in-pausa", 1, false)
	archiviato := creaJob(t, pool, utente, "archiviato", 1, true)
	if _, err := pool.Exec(t.Context(),
		`UPDATE jobs SET archived_at = now() WHERE id = $1::uuid`, archiviato); err != nil {
		t.Fatalf("archiviazione: %v", err)
	}
	acceso := creaJob(t, pool, utente, "acceso", 1, true)

	sospensione, err := store.EnforcePlanLimits(t.Context(), utente, paddle.PlanFree, t2)
	if err != nil {
		t.Fatalf("applicazione di R58: %v", err)
	}
	if sospensione.ByResolution != 1 {
		t.Fatalf("sospensione = %+v, atteso il solo job acceso", sospensione)
	}
	if stato := leggiJob(t, pool, inPausa); stato.Sospeso {
		t.Error("un job già in pausa non va marcato come sospeso da noi")
	}
	if stato := leggiJob(t, pool, archiviato); stato.Sospeso || !stato.Enabled {
		t.Errorf("job archiviato toccato: %+v", stato)
	}
	if stato := leggiJob(t, pool, acceso); !stato.Sospeso {
		t.Error("il job acceso doveva essere sospeso")
	}
}

// Un piano senza tetto rigido — Team, Agency — non sospende per numero. La sola
// soglia di fair use non è un rifiuto secco (0003).
func TestPianoSenzaTettoNonSospendePerNumero(t *testing.T) {
	store, pool := nuovoStore(t)
	utente := nuovoUtente(t, pool, "illimitato@esempio.test")
	for i := range 30 {
		creaJob(t, pool, utente, fmt.Sprintf("job-%02d", i), 3600, true)
	}

	sospensione, err := store.EnforcePlanLimits(t.Context(), utente, paddle.PlanTeam, t2)
	if err != nil {
		t.Fatalf("applicazione di R58: %v", err)
	}
	if sospensione.Total() != 0 {
		t.Fatalf("sospensione = %+v, attesa nessuna", sospensione)
	}
}

// ------------------------------------------------------- ordine degli eventi

// Un evento diverso e più vecchio che arriva dopo uno più recente è nuovo,
// legittimo e firmato: senza filigrana riporterebbe in vita un piano che
// l'utente non ha più.
func TestEventoFuoriOrdineNonRetrocedeLaSottoscrizione(t *testing.T) {
	store, pool := nuovoStore(t)
	utente := nuovoUtente(t, pool, "ordine@esempio.test")

	if _, err := store.SaveSubscription(t.Context(),
		cambio(utente, "sub_01", paddle.PlanPro, paddle.SubscriptionActive, t2)); err != nil {
		t.Fatalf("evento recente: %v", err)
	}
	if _, err := store.SaveSubscription(t.Context(),
		cambio(utente, "sub_01", paddle.PlanFree, paddle.SubscriptionCanceled, t3)); err != nil {
		t.Fatalf("cancellazione: %v", err)
	}

	// Arriva ora l'aggiornamento che precedeva la cancellazione.
	risultato, err := store.SaveSubscription(t.Context(),
		cambio(utente, "sub_01", paddle.PlanTeam, paddle.SubscriptionActive, t1))
	if err != nil {
		t.Fatalf("evento vecchio: %v", err)
	}
	if risultato.Applied {
		t.Fatal("un evento più vecchio non va applicato")
	}

	var piano, stato string
	if err := pool.QueryRow(t.Context(),
		`SELECT plan_code, status::text FROM subscriptions WHERE paddle_subscription_id = 'sub_01'`).
		Scan(&piano, &stato); err != nil {
		t.Fatalf("lettura della sottoscrizione: %v", err)
	}
	if piano != paddle.PlanFree || stato != "canceled" {
		t.Fatalf("sottoscrizione = %s/%s: l'evento vecchio ha retrocesso l'account", piano, stato)
	}
}

// Lo stesso evento rilavorato dopo un fallimento porta lo stesso `occurred_at`:
// deve poter essere riapplicato, o una consegna fallita resterebbe persa.
func TestStessoIstanteVieneRiapplicato(t *testing.T) {
	store, pool := nuovoStore(t)
	utente := nuovoUtente(t, pool, "ripetuto@esempio.test")

	for range 2 {
		risultato, err := store.SaveSubscription(t.Context(),
			cambio(utente, "sub_01", paddle.PlanPro, paddle.SubscriptionActive, t2))
		if err != nil {
			t.Fatalf("scrittura: %v", err)
		}
		if !risultato.Applied {
			t.Fatal("un evento con lo stesso istante va riapplicato: è la rilavorazione di un fallimento")
		}
	}

	var righe int
	if err := pool.QueryRow(t.Context(),
		`SELECT count(*) FROM subscriptions WHERE user_id = $1::uuid`, utente).Scan(&righe); err != nil {
		t.Fatalf("conteggio: %v", err)
	}
	if righe != 1 {
		t.Fatalf("righe di sottoscrizione = %d, attesa 1", righe)
	}
}

// L'indice parziale `subscriptions_one_live_per_user_idx` ammette una sola
// sottoscrizione viva per utente: comprare un piano quando se ne ha già un altro
// deve chiudere il precedente, non fallire.
func TestUnaSolaSottoscrizioneVivaPerUtente(t *testing.T) {
	store, pool := nuovoStore(t)
	utente := nuovoUtente(t, pool, "unasola@esempio.test")
	sottoscrive(t, pool, utente, "pro", "sub_vecchia")

	if _, err := store.SaveSubscription(t.Context(),
		cambio(utente, "sub_nuova", paddle.PlanTeam, paddle.SubscriptionActive, t2)); err != nil {
		t.Fatalf("nuova sottoscrizione: %v", err)
	}

	var vive int
	if err := pool.QueryRow(t.Context(),
		`SELECT count(*) FROM subscriptions WHERE user_id = $1::uuid AND status <> 'canceled'`,
		utente).Scan(&vive); err != nil {
		t.Fatalf("conteggio: %v", err)
	}
	if vive != 1 {
		t.Fatalf("sottoscrizioni vive = %d, attesa 1", vive)
	}

	piani, err := jobspg.New(pool)
	if err != nil {
		t.Fatalf("costruzione di jobspg: %v", err)
	}
	piano, err := piani.PlanForUser(t.Context(), utente)
	if err != nil {
		t.Fatalf("lettura del piano: %v", err)
	}
	if piano.Code != paddle.PlanTeam {
		t.Fatalf("piano = %q, atteso team", piano.Code)
	}
}

// Senza `custom_data.user_id` l'account si ricava dalla riga già legata a quella
// sottoscrizione Paddle. Senza nemmeno quella, non si indovina.
func TestAttribuzioneDellaSottoscrizione(t *testing.T) {
	store, pool := nuovoStore(t)
	utente := nuovoUtente(t, pool, "attribuzione@esempio.test")
	sottoscrive(t, pool, utente, "pro", "sub_nota")

	risultato, err := store.SaveSubscription(t.Context(),
		cambio("", "sub_nota", paddle.PlanTeam, paddle.SubscriptionActive, t2))
	if err != nil {
		t.Fatalf("attribuzione dalla sottoscrizione: %v", err)
	}
	if risultato.UserID != utente {
		t.Fatalf("utente = %q, atteso %q", risultato.UserID, utente)
	}

	_, err = store.SaveSubscription(t.Context(),
		cambio("", "sub_ignota", paddle.PlanPro, paddle.SubscriptionActive, t2))
	if !errors.Is(err, billing.ErrUnknownSubscriber) {
		t.Fatalf("atteso ErrUnknownSubscriber, ottenuto %v", err)
	}

	// Un `custom_data` manomesso non deve produrre un errore interno: deve solo
	// non corrispondere a nessun account.
	_, err = store.SaveSubscription(t.Context(),
		cambio("non-è-un-uuid", "sub_ignota2", paddle.PlanPro, paddle.SubscriptionActive, t2))
	if !errors.Is(err, billing.ErrUnknownSubscriber) {
		t.Fatalf("atteso ErrUnknownSubscriber, ottenuto %v", err)
	}
}

// Su uno stato non pagante il piano diventa `free` ma resta la traccia di cosa
// l'utente stava pagando: serve all'assistenza e alle statistiche di ricavo.
func TestIlPrezzoPagatoRestaTracciatoDopoLaCancellazione(t *testing.T) {
	store, pool := nuovoStore(t)
	utente := nuovoUtente(t, pool, "traccia@esempio.test")

	acquisto := cambio(utente, "sub_01", paddle.PlanPro, paddle.SubscriptionActive, t1)
	acquisto.PaddlePriceID = "pri_pro_m"
	if _, err := store.SaveSubscription(t.Context(), acquisto); err != nil {
		t.Fatalf("acquisto: %v", err)
	}

	if _, err := store.SaveSubscription(t.Context(),
		cambio(utente, "sub_01", paddle.PlanFree, paddle.SubscriptionCanceled, t2)); err != nil {
		t.Fatalf("cancellazione: %v", err)
	}

	var prezzo *string
	var canceledAt *time.Time
	if err := pool.QueryRow(t.Context(),
		`SELECT paddle_price_id, canceled_at FROM subscriptions WHERE paddle_subscription_id = 'sub_01'`).
		Scan(&prezzo, &canceledAt); err != nil {
		t.Fatalf("lettura: %v", err)
	}
	if prezzo == nil || *prezzo != "pri_pro_m" {
		t.Fatalf("prezzo tracciato = %v", prezzo)
	}
	// `subscriptions_canceled_check` esige l'istante: senza il ripiego
	// sull'istante dell'evento l'INSERT sarebbe stato rifiutato.
	if canceledAt == nil {
		t.Fatal("istante di annullamento non valorizzato")
	}
}

// ------------------------------------------------------- checkout ed entitlement

// R63: la dichiarazione va registrata, con la partita IVA se c'è. E la colonna
// rifiuta una riga senza conferma, perché un intento senza conferma non è un
// intento da conservare.
func TestIntentoDiCheckout(t *testing.T) {
	store, pool := nuovoStore(t)
	utente := nuovoUtente(t, pool, "intento@esempio.test")

	if err := store.RecordCheckoutIntent(t.Context(), billing.CheckoutIntent{
		UserID: utente, PlanCode: "pro", Period: paddle.PeriodYearly,
		PriceID: "pri_pro_y", BusinessUse: true, VATNumber: "IT03835250162", CreatedAt: t1,
	}); err != nil {
		t.Fatalf("registrazione: %v", err)
	}
	// Senza partita IVA: è il caso normale dei regimi minimi che non ne hanno.
	if err := store.RecordCheckoutIntent(t.Context(), billing.CheckoutIntent{
		UserID: utente, PlanCode: "team", Period: paddle.PeriodMonthly,
		PriceID: "pri_team_m", BusinessUse: true, CreatedAt: t2,
	}); err != nil {
		t.Fatalf("registrazione senza partita IVA: %v", err)
	}

	var righe int
	if err := pool.QueryRow(t.Context(),
		`SELECT count(*) FROM paddle_checkout_intents WHERE user_id = $1::uuid`, utente).Scan(&righe); err != nil {
		t.Fatalf("conteggio: %v", err)
	}
	if righe != 2 {
		t.Fatalf("intenti registrati = %d, attesi 2", righe)
	}

	if err := store.RecordCheckoutIntent(t.Context(), billing.CheckoutIntent{
		UserID: utente, PlanCode: "pro", Period: paddle.PeriodMonthly,
		PriceID: "pri_pro_m", BusinessUse: false, CreatedAt: t3,
	}); err == nil {
		t.Fatal("il database deve rifiutare un intento senza conferma di uso professionale")
	}
}

// L'entitlement è ciò che si mostra all'utente, e deve nominare i job fermi: R58
// dice che l'interfaccia deve *dire* cosa è successo.
func TestEntitlementRaccontaISospesi(t *testing.T) {
	store, pool := nuovoStore(t)
	utente := nuovoUtente(t, pool, "entitlement@esempio.test")

	// Senza sottoscrizione si ricade sul Free, come in jobspg.PlanForUser.
	ent, err := store.Entitlement(t.Context(), utente)
	if err != nil {
		t.Fatalf("lettura: %v", err)
	}
	if ent.PlanCode != "free" || ent.PlanName != "Free" {
		t.Fatalf("entitlement iniziale = %+v", ent)
	}

	sottoscrive(t, pool, utente, "team", "sub_01")
	for i := range 25 {
		every := 3600
		if i < 5 {
			every = 1
		}
		creaJob(t, pool, utente, fmt.Sprintf("job-%02d", i), every, true)
	}
	if _, err := store.EnforcePlanLimits(t.Context(), utente, paddle.PlanFree, t2); err != nil {
		t.Fatalf("applicazione di R58: %v", err)
	}
	if _, err := store.SaveSubscription(t.Context(),
		cambio(utente, "sub_01", paddle.PlanFree, paddle.SubscriptionCanceled, t3)); err != nil {
		t.Fatalf("cancellazione: %v", err)
	}

	ent, err = store.Entitlement(t.Context(), utente)
	if err != nil {
		t.Fatalf("lettura: %v", err)
	}
	switch {
	case ent.PlanCode != "free":
		t.Errorf("piano = %q", ent.PlanCode)
	case ent.ActiveJobs != 0:
		t.Errorf("job attivi = %d, attesi 0", ent.ActiveJobs)
	case ent.Suspended.ByResolution != 5:
		t.Errorf("sospesi per risoluzione = %d, attesi 5", ent.Suspended.ByResolution)
	case ent.Suspended.ByJobLimit != 20:
		t.Errorf("sospesi per tetto = %d, attesi 20", ent.Suspended.ByJobLimit)
	case ent.MaxJobs == nil || *ent.MaxJobs != 20:
		t.Errorf("tetto del piano = %v", ent.MaxJobs)
	}
}

func TestPianoInesistente(t *testing.T) {
	store, _ := nuovoStore(t)
	if _, err := store.Plan(t.Context(), "piano_inventato"); err == nil {
		t.Fatal("atteso un errore")
	}
	piano, err := store.Plan(t.Context(), "agency")
	if err != nil {
		t.Fatalf("lettura del piano: %v", err)
	}
	if piano.Name != "Agency" || !piano.IsPublic {
		t.Fatalf("piano = %+v", piano)
	}
}

// I limiti letti da qui e quelli applicati dall'API sono la stessa matrice: se
// divergessero, l'utente vedrebbe un tetto e ne subirebbe un altro.
func TestILimitiSonoQuelliDelListino(t *testing.T) {
	store, pool := nuovoStore(t)
	utente := nuovoUtente(t, pool, "limiti@esempio.test")
	piani, err := jobspg.New(pool)
	if err != nil {
		t.Fatalf("costruzione di jobspg: %v", err)
	}

	for _, codice := range []string{"free", "pro", "team", "agency"} {
		var piano jobs.Plan
		if codice == "free" {
			piano, err = piani.PlanForUser(t.Context(), utente)
		} else {
			piano, err = piani.PlanByCode(t.Context(), codice)
		}
		if err != nil {
			t.Fatalf("lettura del piano %q: %v", codice, err)
		}

		if _, err := store.EnforcePlanLimits(t.Context(), utente, codice, t2); err != nil {
			t.Fatalf("applicazione di R58 sul piano %q: %v", codice, err)
		}
		t.Logf("piano %s: tetto=%v risoluzione=%s", codice, piano.MaxJobs, piano.MinInterval)
	}
}
