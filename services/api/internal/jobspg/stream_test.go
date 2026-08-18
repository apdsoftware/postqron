package jobspg_test

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/apdsoftware/postqron/services/api/internal/jobs"
	"github.com/apdsoftware/postqron/services/api/internal/jobspg"
)

// Le prove della lettura in avanti (SPEC §4.2) contro PostgreSQL vero.
//
// Qui si verificano le due cose che internal/jobs non può provare da solo: che
// il confronto per tuple significhi davvero «dopo» sull'indice della chiave
// primaria, e che una finestra a cavallo della mezzanotte attraversi due
// partizioni giornaliere invece di fermarsi alla prima.

// scrivi inserisce una riga del registro e la porta allo stato voluto.
//
// L'esecuzione nasce `pending` e ci viene scritto sopra l'esito, che è
// esattamente la sequenza del motore (`claimSQL`, `finishSQL`): scrivere uno
// stato terminale in un colpo solo violerebbe `job_executions_terminal_check`,
// che pretende gli istanti di inizio e fine — ed è quel vincolo a rendere vero
// che un log non dice mai «è andata così» senza dire quando.
func scrivi(t *testing.T, store *jobspg.Store, pool *pgxpool.Pool, jobID string, quando time.Time, env jobs.Environment, attempt int, stato jobs.ExecutionStatus) {
	t.Helper()
	if _, err := store.CreateExecution(t.Context(), jobs.Execution{
		JobID:        jobID,
		ScheduledFor: quando,
		Environment:  env,
		Attempt:      attempt,
		Status:       jobs.StatusPending,
		TriggeredBy:  jobs.TriggerSchedule,
	}); err != nil {
		t.Fatalf("inserimento dell'esecuzione a %s: %v", quando.Format(time.RFC3339Nano), err)
	}
	if stato == jobs.StatusPending {
		return
	}

	// `started_at` e `finished_at` li scrive PostgreSQL, come in esercizio: sono
	// due istanti dello stesso orologio, e prenderli da Go li metterebbe su un
	// orologio diverso da quello del container.
	if _, err := pool.Exec(t.Context(), `
		UPDATE job_executions
		   SET status = $5::execution_status,
		       started_at = CASE WHEN $5 = 'skipped' THEN NULL ELSE now() END,
		       finished_at = CASE WHEN $5 = 'skipped' THEN NULL ELSE now() END
		 WHERE job_id = $1::uuid AND scheduled_for = $2
		   AND environment = $3::text::environment AND attempt = $4`,
		jobID, quando.UTC(), string(env), int16(attempt), string(stato)); err != nil {
		t.Fatalf("aggiornamento dell'esecuzione a %s: %v", quando.Format(time.RFC3339Nano), err)
	}
}

// TestLaLetturaInAvantiCamminaSullaChiaveNaturale verifica il verso, il confine
// escluso del cursore e l'ordine — compreso quello dei retry, che condividono
// `scheduled_for` con l'occorrenza che ritentano e si distinguono per `attempt`
// (0006, `enqueueRetrySQL`).
func TestLaLetturaInAvantiCamminaSullaChiaveNaturale(t *testing.T) {
	store, userID, pool := newStore(t)
	job, err := store.CreateJob(t.Context(), sampleJob(userID), nil)
	if err != nil {
		t.Fatalf("CreateJob: %v", err)
	}

	base := time.Now().UTC().Truncate(time.Minute)
	// Due occorrenze, e sulla prima un retry: l'ordine atteso è
	// (base, staging), (base, production), (base, production, 2), (base+1m, …).
	scrivi(t, store, pool, job.ID, base, jobs.EnvironmentStaging, 1, jobs.StatusFailed)
	scrivi(t, store, pool, job.ID, base, jobs.EnvironmentProduction, 1, jobs.StatusFailed)
	scrivi(t, store, pool, job.ID, base, jobs.EnvironmentProduction, 2, jobs.StatusSucceeded)
	scrivi(t, store, pool, job.ID, base.Add(time.Minute), jobs.EnvironmentProduction, 1, jobs.StatusSucceeded)

	rows, err := store.ListExecutionsForward(t.Context(), jobs.ExecutionFilter{
		JobID: job.ID, Limit: 10,
	})
	if err != nil {
		t.Fatalf("ListExecutionsForward: %v", err)
	}
	if len(rows) != 4 {
		t.Fatalf("righe = %d, attese 4", len(rows))
	}

	// L'ordine è quello della chiave primaria letta in avanti. `staging` viene
	// prima di `production` perché l'enum ordina per dichiarazione (0001), non
	// alfabeticamente: è la stessa regola su cui si regge il cursore, e se le due
	// divergessero la ripresa salterebbe righe.
	atteso := []string{
		"staging/1", "production/1", "production/2", "production/1",
	}
	for i, row := range rows {
		got := string(row.Environment) + "/" + strconv.Itoa(row.Attempt)
		if got != atteso[i] {
			t.Fatalf("riga %d = %s, attesa %s (ordine: %s)", i, got, atteso[i], leggibile(rows))
		}
	}

	// Il cursore è un confine **escluso**: ripartendo dalla seconda riga si
	// ottengono la terza e la quarta, e nient'altro.
	cursore := &jobs.ExecutionCursor{
		ScheduledFor: rows[1].ScheduledFor,
		Environment:  rows[1].Environment,
		Attempt:      rows[1].Attempt,
	}
	dopo, err := store.ListExecutionsForward(t.Context(), jobs.ExecutionFilter{
		JobID: job.ID, Cursor: cursore, Limit: 10,
	})
	if err != nil {
		t.Fatalf("ListExecutionsForward con cursore: %v", err)
	}
	if len(dopo) != 2 {
		t.Fatalf("righe dopo il cursore = %d, attese 2: %s", len(dopo), leggibile(dopo))
	}
	if dopo[0].Attempt != 2 {
		t.Errorf("la prima riga dopo il cursore è %+v: il cursore non è escluso", dopo[0])
	}

	// Il tetto è esatto: nessuna riga in più da cui dedurre una pagina
	// successiva, perché un flusso non ne ha una.
	limitate, err := store.ListExecutionsForward(t.Context(), jobs.ExecutionFilter{
		JobID: job.ID, Limit: 2,
	})
	if err != nil {
		t.Fatalf("ListExecutionsForward con limite: %v", err)
	}
	if len(limitate) != 2 {
		t.Fatalf("righe con limite 2 = %d, attese esattamente 2", len(limitate))
	}
}

// TestUnFlussoAttraversaLaMezzanotte è la prova che la partizione giornaliera
// non è un confine per la lettura.
//
// `job_executions` è partizionata per giorno su `scheduled_for` (0006): una
// finestra che scavalca la mezzanotte UTC tocca due tabelle diverse, e il
// pianificatore deve leggerle **entrambe** e **in ordine**. Verificarlo invece
// di supporlo è il punto: un `Append` non ordinato consegnerebbe le righe del
// giorno nuovo prima di quelle del vecchio, e il cursore del flusso — che è
// crescente — salterebbe tutto il giorno precedente.
func TestUnFlussoAttraversaLaMezzanotte(t *testing.T) {
	store, userID, pool := newStore(t)
	job, err := store.CreateJob(t.Context(), sampleJob(userID), nil)
	if err != nil {
		t.Fatalf("CreateJob: %v", err)
	}

	// La mezzanotte UTC **di oggi**: la 0006 prepara le partizioni da ieri a
	// quattordici giorni avanti, quindi entrambi i lati esistono senza dover
	// creare niente a mano.
	mezzanotte := time.Now().UTC().Truncate(24 * time.Hour)
	prima := mezzanotte.Add(-time.Second)
	dopo := mezzanotte.Add(time.Second)

	scrivi(t, store, pool, job.ID, prima, jobs.EnvironmentProduction, 1, jobs.StatusSucceeded)
	scrivi(t, store, pool, job.ID, mezzanotte, jobs.EnvironmentProduction, 1, jobs.StatusSucceeded)
	scrivi(t, store, pool, job.ID, dopo, jobs.EnvironmentProduction, 1, jobs.StatusSucceeded)

	// Le tre righe stanno davvero in due partizioni diverse: senza questa
	// verifica la prova passerebbe anche su una tabella non partizionata, cioè
	// non proverebbe niente.
	var partizioni int
	if err := pool.QueryRow(t.Context(), `
		SELECT count(DISTINCT tableoid) FROM job_executions WHERE job_id = $1::uuid`,
		job.ID).Scan(&partizioni); err != nil {
		t.Fatalf("conteggio delle partizioni: %v", err)
	}
	if partizioni != 2 {
		t.Fatalf("partizioni toccate = %d, attese 2: la prova non sta attraversando nessun confine", partizioni)
	}

	rows, err := store.ListExecutionsForward(t.Context(), jobs.ExecutionFilter{
		JobID: job.ID,
		Since: prima.Add(-time.Minute),
		Limit: 10,
	})
	if err != nil {
		t.Fatalf("ListExecutionsForward: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("righe = %d, attese 3: la finestra si è fermata al confine di partizione (%s)",
			len(rows), leggibile(rows))
	}
	for i := 1; i < len(rows); i++ {
		if !rows[i-1].ScheduledFor.Before(rows[i].ScheduledFor) {
			t.Fatalf("righe fuori ordine attraverso la mezzanotte: %s", leggibile(rows))
		}
	}

	// E la ripresa da una posizione **del giorno precedente** consegna il resto,
	// che è il caso vero di un client riconnesso a cavallo della mezzanotte.
	cursore := &jobs.ExecutionCursor{
		ScheduledFor: prima,
		Environment:  jobs.EnvironmentProduction,
		Attempt:      1,
	}
	ripresa, err := store.ListExecutionsForward(t.Context(), jobs.ExecutionFilter{
		JobID: job.ID, Cursor: cursore, Limit: 10,
	})
	if err != nil {
		t.Fatalf("ripresa attraverso la mezzanotte: %v", err)
	}
	if len(ripresa) != 2 {
		t.Fatalf("righe riprese = %d, attese 2: %s", len(ripresa), leggibile(ripresa))
	}
	if !ripresa[0].ScheduledFor.Equal(mezzanotte) {
		t.Errorf("la ripresa parte da %s, attesa la mezzanotte", ripresa[0].ScheduledFor)
	}
}

// TestLaFinestraDelFlussoEsclude verifica che `Since` sia inclusivo e tagli
// davvero: è il confine con cui la retention si applica in lettura (R10-bis).
func TestLaFinestraDelFlussoEsclude(t *testing.T) {
	store, userID, pool := newStore(t)
	job, err := store.CreateJob(t.Context(), sampleJob(userID), nil)
	if err != nil {
		t.Fatalf("CreateJob: %v", err)
	}

	base := time.Now().UTC().Truncate(time.Minute)
	scrivi(t, store, pool, job.ID, base.Add(-time.Hour), jobs.EnvironmentProduction, 1, jobs.StatusSucceeded)
	scrivi(t, store, pool, job.ID, base, jobs.EnvironmentProduction, 1, jobs.StatusSucceeded)

	rows, err := store.ListExecutionsForward(t.Context(), jobs.ExecutionFilter{
		JobID: job.ID, Since: base, Limit: 10,
	})
	if err != nil {
		t.Fatalf("ListExecutionsForward: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("righe = %d, attesa 1: %s", len(rows), leggibile(rows))
	}
	if !rows[0].ScheduledFor.Equal(base) {
		t.Errorf("`Since` non è inclusivo: riga = %s, atteso %s", rows[0].ScheduledFor, base)
	}
}

// ------------------------------------------------------------------ supporto

func leggibile(rows []jobs.Execution) string {
	parts := make([]string, 0, len(rows))
	for _, row := range rows {
		parts = append(parts, row.ScheduledFor.Format(time.RFC3339)+"/"+
			string(row.Environment)+"/"+strconv.Itoa(row.Attempt))
	}
	return strings.Join(parts, " ")
}
