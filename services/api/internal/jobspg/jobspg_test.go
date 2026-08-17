package jobspg_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/apdsoftware/postqron/services/api/internal/jobs"
	"github.com/apdsoftware/postqron/services/api/internal/jobspg"
)

// newStore prepara un database usa e getta con lo schema applicato e ci
// restituisce lo store insieme all'utente proprietario dei job.
func newStore(t *testing.T) (*jobspg.Store, string, *pgxpool.Pool) {
	t.Helper()

	pool := newTestDatabase(t)
	store, err := jobspg.New(pool)
	if err != nil {
		t.Fatalf("jobspg.New: %v", err)
	}
	return store, newUser(t, pool, "mario.rossi@example.com"), pool
}

func newUser(t *testing.T, pool *pgxpool.Pool, email string) string {
	t.Helper()
	var id string
	err := pool.QueryRow(t.Context(),
		`INSERT INTO users (email) VALUES ($1) RETURNING id::text`, email).Scan(&id)
	if err != nil {
		t.Fatalf("creazione dell'utente: %v", err)
	}
	return id
}

// subscribe assegna un piano all'utente, come farebbe il webhook Paddle (R16).
func subscribe(t *testing.T, pool *pgxpool.Pool, userID, planCode string) {
	t.Helper()
	_, err := pool.Exec(t.Context(),
		`INSERT INTO subscriptions (user_id, plan_code, status) VALUES ($1::uuid, $2, 'active')`,
		userID, planCode)
	if err != nil {
		t.Fatalf("sottoscrizione al piano %q: %v", planCode, err)
	}
}

func sampleJob(userID string) jobs.Job {
	job := jobs.NewJob()
	job.UserID = userID
	job.Name = "daily-digest"
	job.Schedule = "0 9 * * *"
	job.Timezone = "Europe/Rome"
	job.URL = "https://api.example.com/tasks/digest"
	job.Headers = map[string]string{"Authorization": "Bearer ${DIGEST_TOKEN}"}
	job.Body = `{"kind":"daily"}`
	next := time.Now().UTC().Add(time.Hour).Truncate(time.Second)
	job.NextRunAt = &next
	return job
}

// ------------------------------------------------------------------ scrittura

// TestAndataERitornoDelJob verifica che ogni campo sopravviva alla scrittura e
// alla rilettura: un campo perso per strada è un job che esegue qualcosa di
// diverso da quello che l'utente ha chiesto.
func TestAndataERitornoDelJob(t *testing.T) {
	store, userID, _ := newStore(t)

	created, err := store.CreateJob(t.Context(), sampleJob(userID), nil)
	if err != nil {
		t.Fatalf("CreateJob: %v", err)
	}

	letto, err := store.JobByID(t.Context(), userID, created.ID)
	if err != nil {
		t.Fatalf("JobByID: %v", err)
	}

	want := sampleJob(userID)
	switch {
	case letto.Name != want.Name:
		t.Errorf("name = %q", letto.Name)
	case letto.Schedule != want.Schedule:
		t.Errorf("schedule = %q", letto.Schedule)
	case letto.Every != 0:
		t.Errorf("every = %s, atteso zero su un job cron", letto.Every)
	case letto.Timezone != want.Timezone:
		t.Errorf("timezone = %q", letto.Timezone)
	case letto.URL != want.URL:
		t.Errorf("url = %q", letto.URL)
	case letto.Method != want.Method:
		t.Errorf("method = %q", letto.Method)
	case letto.Body != want.Body:
		t.Errorf("body = %q", letto.Body)
	case letto.Timeout != want.Timeout:
		t.Errorf("timeout = %s", letto.Timeout)
	case letto.MaxRetries != want.MaxRetries:
		t.Errorf("max_retries = %d", letto.MaxRetries)
	case letto.RetryBackoff != want.RetryBackoff:
		t.Errorf("retry_backoff = %q", letto.RetryBackoff)
	case letto.Enabled != want.Enabled:
		t.Errorf("enabled = %v", letto.Enabled)
	case letto.RepositoryID != "":
		t.Errorf("repository_id = %q, atteso vuoto", letto.RepositoryID)
	}

	// Gli header sono jsonb: i riferimenti `${VAR}` ai segreti del workspace
	// restano non risolti a riposo (SPEC §9, R43).
	if letto.Headers["Authorization"] != "Bearer ${DIGEST_TOKEN}" {
		t.Errorf("headers = %v", letto.Headers)
	}
	if len(letto.Environments) != 1 || letto.Environments[0] != jobs.EnvironmentProduction {
		t.Errorf("environments = %v", letto.Environments)
	}
	if len(letto.AlertOnFailure) != 1 || letto.AlertOnFailure[0] != jobs.AlertEmail {
		t.Errorf("alert_on_failure = %v", letto.AlertOnFailure)
	}
	if letto.NextRunAt == nil || !letto.NextRunAt.Equal(*want.NextRunAt) {
		t.Errorf("next_run_at = %v, atteso %v", letto.NextRunAt, want.NextRunAt)
	}
}

func TestModalitaAIntervallo(t *testing.T) {
	store, userID, _ := newStore(t)

	job := sampleJob(userID)
	job.Schedule = ""
	job.Every = 10 * time.Second

	created, err := store.CreateJob(t.Context(), job, nil)
	if err != nil {
		t.Fatalf("CreateJob: %v", err)
	}
	if created.Every != 10*time.Second {
		t.Errorf("every = %s, atteso 10s", created.Every)
	}
	// L'altra metà del vincolo XOR: la colonna `schedule` dev'essere NULL.
	if created.Schedule != "" {
		t.Errorf("schedule = %q, atteso vuoto", created.Schedule)
	}
}

// TestIlVincoloXOREsiste è la controprova del rifiuto anticipato dell'API: se
// questo test fallisse, vorrebbe dire che il database accetta ciò che l'API
// rifiuta, e la validazione a monte sarebbe una scelta di comodo invece che una
// necessità.
func TestIlVincoloXOREsiste(t *testing.T) {
	store, userID, _ := newStore(t)

	t.Run("entrambe le modalità", func(t *testing.T) {
		job := sampleJob(userID)
		job.Every = 30 * time.Second // e `schedule` è già valorizzata
		if _, err := store.CreateJob(t.Context(), job, nil); err == nil {
			t.Fatal("il database ha accettato un job con entrambe le modalità")
		}
	})

	t.Run("nessuna modalità", func(t *testing.T) {
		job := sampleJob(userID)
		job.Name = "senza-modalita"
		job.Schedule = ""
		if _, err := store.CreateJob(t.Context(), job, nil); err == nil {
			t.Fatal("il database ha accettato un job senza schedulazione")
		}
	})
}

// TestLaValidazioneDellAPICopreIVincoliDelDatabase è il test che tiene allineate
// le tre copie della verità: ciò che l'API accetta, il database non lo rifiuta.
//
// Un 500 perché PostgreSQL ha respinto ciò che l'API aveva accettato è un
// difetto, e questo è il punto in cui si scopre invece che in produzione.
func TestLaValidazioneDellAPICopreIVincoliDelDatabase(t *testing.T) {
	store, userID, _ := newStore(t)

	// Casi limite accettati dalla validazione dell'API: devono passare anche di
	// qua, altrimenti una delle due copie è più permissiva dell'altra.
	casi := []struct {
		nome string
		tune func(*jobs.Job)
	}{
		{"nome di un carattere", func(j *jobs.Job) { j.Name = "a" }},
		{"nome al limite", func(j *jobs.Job) { j.Name = "a" + strings.Repeat("b", jobs.MaxNameLength-1) }},
		{"nome con punti e trattini", func(j *jobs.Job) { j.Name = "a.b-c_d1" }},
		{"timeout minimo", func(j *jobs.Job) { j.Timeout = jobs.MinTimeout }},
		{"timeout massimo", func(j *jobs.Job) { j.Timeout = jobs.MaxTimeout }},
		{"nessun retry", func(j *jobs.Job) { j.MaxRetries = 0 }},
		{"retry al massimo", func(j *jobs.Job) { j.MaxRetries = jobs.MaxRetriesAllowed }},
		{"nessun avviso", func(j *jobs.Job) { j.AlertOnFailure = nil }},
		{"tutti i canali", func(j *jobs.Job) { j.AlertOnFailure = jobs.AlertChannels }},
		{"entrambi gli ambienti", func(j *jobs.Job) { j.Environments = jobs.Environments }},
		{"corpo al limite", func(j *jobs.Job) { j.Body = strings.Repeat("x", jobs.MaxBodyLength) }},
		{"url in chiaro", func(j *jobs.Job) { j.URL = "http://example.com/hook" }},
		{"espressione con due spazi", func(j *jobs.Job) { j.Schedule = "0  9  *  *  *" }},
		{"intervallo di un secondo", func(j *jobs.Job) { j.Schedule = ""; j.Every = time.Second }},
		{"senza header", func(j *jobs.Job) { j.Headers = nil }},
		{"senza corpo", func(j *jobs.Job) { j.Body = "" }},
	}

	for i, caso := range casi {
		t.Run(caso.nome, func(t *testing.T) {
			job := sampleJob(userID)
			job.Name = fmt.Sprintf("caso-%02d", i)
			caso.tune(&job)

			// Passa prima dalla validazione dell'API, che è ciò che il caso
			// dichiara di rappresentare: se l'API lo rifiuta, il caso è scritto
			// male e non prova niente sul database.
			if err := job.Validate(t.Context(), planPermissivo(), nil); err != nil {
				t.Fatalf("il caso non è accettato dall'API: %v", err)
			}
			if _, err := store.CreateJob(t.Context(), job, nil); err != nil {
				t.Fatalf("l'API lo accetta ma il database lo rifiuta: %v", err)
			}
		})
	}
}

// planPermissivo consente tutto ciò che il listino consente al massimo, così che
// i casi limite di forma non vengano fermati da un limite commerciale.
func planPermissivo() jobs.Plan {
	return jobs.Plan{
		Code: "agency", Name: "Agency",
		MinInterval: time.Second, EnvironmentsEnabled: true,
	}
}

func TestNomeUnicoPerUtente(t *testing.T) {
	store, userID, pool := newStore(t)

	if _, err := store.CreateJob(t.Context(), sampleJob(userID), nil); err != nil {
		t.Fatalf("primo job: %v", err)
	}
	if _, err := store.CreateJob(t.Context(), sampleJob(userID), nil); !errors.Is(err, jobs.ErrNameTaken) {
		t.Fatalf("errore = %v, atteso ErrNameTaken", err)
	}

	// Lo stesso nome per un altro utente è legittimo: l'ambito dell'unicità è
	// l'utente, non il servizio.
	altro := newUser(t, pool, "giulia.bianchi@example.com")
	if _, err := store.CreateJob(t.Context(), sampleJob(altro), nil); err != nil {
		t.Fatalf("stesso nome per un altro utente: %v", err)
	}
}

// TestIlTettoDiPianoEApplicatoDentroLInserimento verifica la proprietà che una
// SELECT seguita da un INSERT non può dare: due creazioni simultanee al
// ventesimo job non ne producono ventuno.
func TestIlTettoDiPianoEApplicatoDentroLInserimento(t *testing.T) {
	store, userID, pool := newStore(t)
	max := 3

	var wg sync.WaitGroup
	esiti := make([]error, 10)
	for i := range esiti {
		wg.Add(1)
		go func() {
			defer wg.Done()
			job := sampleJob(userID)
			job.Name = fmt.Sprintf("concorrente-%02d", i)
			_, esiti[i] = store.CreateJob(context.Background(), job, &max)
		}()
	}
	wg.Wait()

	riusciti := 0
	for i, err := range esiti {
		switch {
		case err == nil:
			riusciti++
		case errors.Is(err, jobs.ErrJobLimitReached):
		default:
			t.Errorf("creazione %d: errore inatteso %v", i, err)
		}
	}
	if riusciti != max {
		t.Fatalf("creazioni riuscite = %d, attese %d", riusciti, max)
	}

	var conteggio int
	if err := pool.QueryRow(t.Context(),
		`SELECT count(*) FROM jobs WHERE user_id = $1::uuid`, userID).Scan(&conteggio); err != nil {
		t.Fatalf("conteggio: %v", err)
	}
	if conteggio != max {
		t.Errorf("job nel database = %d, attesi %d: il tetto è stato sfondato", conteggio, max)
	}
}

// TestIJobArchiviatiNonOccupanoIlTetto: un job archiviato è sparito dal
// `cron.yaml` e non è più nel catalogo dell'utente.
func TestIJobArchiviatiNonOccupanoIlTetto(t *testing.T) {
	store, userID, pool := newStore(t)
	max := 1

	created, err := store.CreateJob(t.Context(), sampleJob(userID), &max)
	if err != nil {
		t.Fatalf("primo job: %v", err)
	}
	if _, err := pool.Exec(t.Context(),
		`UPDATE jobs SET archived_at = now() WHERE id = $1::uuid`, created.ID); err != nil {
		t.Fatalf("archiviazione: %v", err)
	}

	secondo := sampleJob(userID)
	secondo.Name = "secondo"
	if _, err := store.CreateJob(t.Context(), secondo, &max); err != nil {
		t.Fatalf("il job archiviato occupa ancora un posto: %v", err)
	}
}

func TestAggiornamentoEdEliminazione(t *testing.T) {
	store, userID, pool := newStore(t)
	created, err := store.CreateJob(t.Context(), sampleJob(userID), nil)
	if err != nil {
		t.Fatalf("CreateJob: %v", err)
	}

	// Con `resetNextRun = false` la colonna resta dov'era, **anche se il job in
	// mano al chiamante ne porta un'altra**: è la protezione contro
	// l'aggiornamento perso ai danni dello scheduler, che la fa avanzare fra la
	// lettura e la scrittura.
	sorpasso := created.CreatedAt.Add(90 * time.Minute).Truncate(time.Second)
	if _, err := pool.Exec(t.Context(),
		`UPDATE jobs SET next_run_at = $2 WHERE id = $1::uuid`, created.ID, sorpasso); err != nil {
		t.Fatalf("avanzamento simulato dello scheduler: %v", err)
	}

	created.Name = "digest-serale"
	created.Enabled = false
	created.NextRunAt = nil // ignorato: decide il flag, non il campo
	created.Headers = map[string]string{}

	updated, err := store.UpdateJob(t.Context(), created, false)
	if err != nil {
		t.Fatalf("UpdateJob: %v", err)
	}
	if updated.Name != "digest-serale" {
		t.Errorf("aggiornamento non applicato: %+v", updated)
	}
	if updated.Enabled {
		t.Error("il job non è stato messo in pausa")
	}
	if updated.NextRunAt == nil || !updated.NextRunAt.Equal(sorpasso) {
		t.Fatalf("next_run_at = %v, atteso %s intatto: l'avanzamento dello scheduler è stato sovrascritto",
			updated.NextRunAt, sorpasso)
	}

	// Con `true` la colonna si azzera, ed è così che l'API dice «ricalcolala».
	updated.Schedule = ""
	updated.Every = 5 * time.Minute
	updated, err = store.UpdateJob(t.Context(), updated, true)
	if err != nil {
		t.Fatalf("UpdateJob con reset: %v", err)
	}
	if updated.Every != 5*time.Minute || updated.Schedule != "" {
		t.Errorf("cambio di modalità non applicato: %+v", updated)
	}
	if updated.NextRunAt != nil {
		t.Errorf("next_run_at = %v, attesa azzerata", updated.NextRunAt)
	}
	// Il trigger `jobs_set_updated_at` della 0005 tiene la colonna allineata
	// senza dipendere dal chiamante.
	//
	// Il paragone è con l'`updated_at` precedente, non con `created_at`: quello
	// lo scrive il processo Go, questo il trigger dentro PostgreSQL, e in
	// sviluppo il database sta in un container il cui orologio insegue l'host
	// con una deriva di millisecondi. Confrontarli fa fallire il test quando il
	// codice è corretto — è successo in `internal/secretspg`, con 14,5 ms di
	// scarto. Sul valore precedente l'orologio è lo stesso, e la verifica è
	// anche più stretta: dimostra che il trigger è *scattato*.
	if !updated.UpdatedAt.After(created.UpdatedAt) {
		t.Errorf("il trigger non ha aggiornato updated_at: %s non è successivo a %s",
			updated.UpdatedAt, created.UpdatedAt)
	}

	// L'ambito sull'utente è parte del contratto, non un filtro dimenticabile.
	altro := newUser(t, pool, "giulia.bianchi@example.com")
	if err := store.DeleteJob(t.Context(), altro, created.ID); !errors.Is(err, jobs.ErrNotFound) {
		t.Errorf("eliminazione di un job altrui: errore = %v, atteso ErrNotFound", err)
	}

	if err := store.DeleteJob(t.Context(), userID, created.ID); err != nil {
		t.Fatalf("DeleteJob: %v", err)
	}
	if _, err := store.JobByID(t.Context(), userID, created.ID); !errors.Is(err, jobs.ErrNotFound) {
		t.Errorf("dopo l'eliminazione: errore = %v, atteso ErrNotFound", err)
	}
}

// TestIdentificativoMalformato: un uuid che non è un uuid non indirizza nessuna
// riga, e rispondere 500 darebbe la colpa al servizio.
func TestIdentificativoMalformato(t *testing.T) {
	store, userID, _ := newStore(t)

	if _, err := store.JobByID(t.Context(), userID, "non-un-uuid"); !errors.Is(err, jobs.ErrNotFound) {
		t.Errorf("JobByID: errore = %v, atteso ErrNotFound", err)
	}
	if err := store.DeleteJob(t.Context(), userID, "non-un-uuid"); !errors.Is(err, jobs.ErrNotFound) {
		t.Errorf("DeleteJob: errore = %v, atteso ErrNotFound", err)
	}
}

// --------------------------------------------------------------------- piani

// TestPianoPerUtente legge la matrice di SPEC §8 dal database, che è la fonte di
// verità: se i due divergessero, i limiti applicati non sarebbero quelli
// venduti.
func TestPianoPerUtente(t *testing.T) {
	store, userID, pool := newStore(t)

	// Senza sottoscrizione si ricade su `free`: la registrazione (#396) non crea
	// una riga in `subscriptions`.
	plan, err := store.PlanForUser(t.Context(), userID)
	if err != nil {
		t.Fatalf("PlanForUser: %v", err)
	}
	if plan.Code != "free" {
		t.Fatalf("piano = %q, atteso \"free\"", plan.Code)
	}
	if plan.MinInterval != time.Minute {
		t.Errorf("risoluzione minima su Free = %s, attesa 1m", plan.MinInterval)
	}
	if plan.MaxJobs == nil || *plan.MaxJobs != 20 {
		t.Errorf("max_jobs su Free = %v, atteso 20", plan.MaxJobs)
	}
	if plan.EnvironmentsEnabled {
		t.Error("il piano Free non ha più ambienti")
	}
	if plan.LogRetention != 3*24*time.Hour {
		t.Errorf("retention su Free = %s, attesa 72h", plan.LogRetention)
	}

	casi := map[string]struct {
		minInterval time.Duration
		maxJobs     *int
		fairUse     *int
	}{
		"pro":    {10 * time.Second, intPtr(200), nil},
		"team":   {time.Second, nil, intPtr(1000)},
		"agency": {time.Second, nil, nil},
	}
	for code, atteso := range casi {
		t.Run(code, func(t *testing.T) {
			user := newUser(t, pool, code+"@example.com")
			subscribe(t, pool, user, code)

			plan, err := store.PlanForUser(t.Context(), user)
			if err != nil {
				t.Fatalf("PlanForUser: %v", err)
			}
			if plan.Code != code {
				t.Fatalf("piano = %q, atteso %q", plan.Code, code)
			}
			if plan.MinInterval != atteso.minInterval {
				t.Errorf("risoluzione minima = %s, attesa %s", plan.MinInterval, atteso.minInterval)
			}
			if !equalIntPtr(plan.MaxJobs, atteso.maxJobs) {
				t.Errorf("max_jobs = %v, atteso %v", plan.MaxJobs, atteso.maxJobs)
			}
			if !equalIntPtr(plan.FairUseJobs, atteso.fairUse) {
				t.Errorf("fair_use_jobs = %v, atteso %v", plan.FairUseJobs, atteso.fairUse)
			}
		})
	}
}

// TestUnaSottoscrizioneAnnullataNonDaEntitlement: la riga resta come storico e
// non deve continuare a concedere il piano.
func TestUnaSottoscrizioneAnnullataNonDaEntitlement(t *testing.T) {
	store, userID, pool := newStore(t)
	subscribe(t, pool, userID, "team")

	if _, err := pool.Exec(t.Context(),
		`UPDATE subscriptions SET status = 'canceled', canceled_at = now() WHERE user_id = $1::uuid`,
		userID); err != nil {
		t.Fatalf("annullamento: %v", err)
	}

	plan, err := store.PlanForUser(t.Context(), userID)
	if err != nil {
		t.Fatalf("PlanForUser: %v", err)
	}
	if plan.Code != "free" {
		t.Errorf("piano = %q, atteso \"free\" dopo l'annullamento", plan.Code)
	}
}

// --------------------------------------------------------------- esecuzioni

// TestLaChiaveNaturaleEIlLockDelTriggerManuale prova la proprietà su cui si
// appoggia il tetto per job: il rifiuto è del database, è atomico e sopravvive a
// un riavvio dell'API.
func TestLaChiaveNaturaleEIlLockDelTriggerManuale(t *testing.T) {
	store, userID, _ := newStore(t)
	job, err := store.CreateJob(t.Context(), sampleJob(userID), nil)
	if err != nil {
		t.Fatalf("CreateJob: %v", err)
	}

	slot := time.Now().UTC().Truncate(time.Minute)
	exec := jobs.Execution{
		JobID: job.ID, ScheduledFor: slot, Environment: jobs.EnvironmentProduction,
		Attempt: 1, Status: jobs.StatusPending, TriggeredBy: jobs.TriggerManual,
	}

	created, err := store.CreateExecution(t.Context(), exec)
	if err != nil {
		t.Fatalf("CreateExecution: %v", err)
	}
	if created.TriggeredBy != jobs.TriggerManual || created.Status != jobs.StatusPending {
		t.Errorf("esecuzione = %+v", created)
	}

	if _, err := store.CreateExecution(t.Context(), exec); !errors.Is(err, jobs.ErrExecutionExists) {
		t.Fatalf("secondo inserimento: errore = %v, atteso ErrExecutionExists", err)
	}

	// L'altro ambiente ha la propria casella: due esecuzioni per occorrenza (R23).
	altro := exec
	altro.Environment = jobs.EnvironmentStaging
	if _, err := store.CreateExecution(t.Context(), altro); err != nil {
		t.Fatalf("stessa occorrenza in un altro ambiente: %v", err)
	}

	letto, err := store.ExecutionAt(t.Context(), job.ID, slot, jobs.EnvironmentProduction, 1)
	if err != nil {
		t.Fatalf("ExecutionAt: %v", err)
	}
	if letto.TriggeredBy != jobs.TriggerManual {
		t.Errorf("triggered_by = %q", letto.TriggeredBy)
	}
}

func TestEsecuzioneSenzaPartizione(t *testing.T) {
	store, userID, _ := newStore(t)
	job, err := store.CreateJob(t.Context(), sampleJob(userID), nil)
	if err != nil {
		t.Fatalf("CreateJob: %v", err)
	}

	// La 0006 prepara due settimane di partizioni: un istante di dieci anni fa
	// non ne ha nessuna. È deliberato che l'inserimento fallisca invece di
	// finire in una partizione di default, e altrettanto deliberato che l'API lo
	// riconosca come indisponibilità temporanea e non come guasto generico.
	_, err = store.CreateExecution(t.Context(), jobs.Execution{
		JobID:        job.ID,
		ScheduledFor: time.Now().UTC().AddDate(-10, 0, 0),
		Environment:  jobs.EnvironmentProduction,
		Attempt:      1,
		Status:       jobs.StatusPending,
		TriggeredBy:  jobs.TriggerManual,
	})
	if !errors.Is(err, jobs.ErrPartitionMissing) {
		t.Fatalf("errore = %v, atteso ErrPartitionMissing", err)
	}
}

// TestElencoDelleEsecuzioni cammina all'indietro sulla chiave primaria di
// `job_executions`: è l'ordine dell'indice, senza ordinamenti aggiuntivi, ed è
// ciò che rende la query costante mentre la tabella cresce.
func TestElencoDelleEsecuzioni(t *testing.T) {
	store, userID, pool := newStore(t)
	job, err := store.CreateJob(t.Context(), sampleJob(userID), nil)
	if err != nil {
		t.Fatalf("CreateJob: %v", err)
	}

	base := time.Now().UTC().Truncate(time.Minute)
	const totale = 7
	for i := 0; i < totale; i++ {
		trigger := jobs.TriggerSchedule
		status := jobs.StatusSucceeded
		if i%3 == 0 {
			trigger, status = jobs.TriggerManual, jobs.StatusFailed
		}
		scheduledFor := base.Add(time.Duration(i) * time.Minute)
		if _, err := store.CreateExecution(t.Context(), jobs.Execution{
			JobID:        job.ID,
			ScheduledFor: scheduledFor,
			Environment:  jobs.EnvironmentProduction,
			Attempt:      1,
			// L'API inserisce solo tentativi `pending`; questi arrivano dal
			// motore, e `job_executions_terminal_check` esige che uno stato
			// terminale porti con sé i propri istanti — un log che non dice
			// quando è successo non è un log.
			Status:      jobs.StatusPending,
			TriggeredBy: trigger,
		}); err != nil {
			t.Fatalf("esecuzione %d: %v", i, err)
		}
		if _, err := pool.Exec(t.Context(),
			`UPDATE job_executions SET status = $3::text::execution_status,
			        started_at = $2, finished_at = $2 + interval '2 seconds'
			  WHERE job_id = $1::uuid AND scheduled_for = $2`,
			job.ID, scheduledFor, string(status)); err != nil {
			t.Fatalf("esito dell'esecuzione %d: %v", i, err)
		}
	}

	// Paginazione a chiave: nessuna riga saltata, nessuna ripetuta.
	visti := map[time.Time]bool{}
	var cursore *jobs.ExecutionCursor
	pagine := 0
	for {
		rows, err := store.ListExecutions(t.Context(), jobs.ExecutionFilter{
			JobID: job.ID, Limit: 3, Cursor: cursore,
		})
		if err != nil {
			t.Fatalf("pagina %d: %v", pagine, err)
		}
		pagine++

		fine := len(rows) <= 3
		if !fine {
			rows = rows[:3]
		}
		for i, row := range rows {
			if visti[row.ScheduledFor] {
				t.Fatalf("riga ripetuta: %s", row.ScheduledFor)
			}
			visti[row.ScheduledFor] = true
			if i > 0 && !rows[i-1].ScheduledFor.After(row.ScheduledFor) {
				t.Fatalf("ordine non decrescente: %s dopo %s", row.ScheduledFor, rows[i-1].ScheduledFor)
			}
		}
		if fine {
			break
		}
		last := rows[len(rows)-1]
		cursore = &jobs.ExecutionCursor{
			ScheduledFor: last.ScheduledFor, Environment: last.Environment, Attempt: last.Attempt,
		}
		if pagine > 10 {
			t.Fatal("la paginazione non termina")
		}
	}
	if len(visti) != totale {
		t.Errorf("righe viste = %d, attese %d", len(visti), totale)
	}

	// Filtro per origine: è ciò che rende contabili i trigger manuali.
	manuali, err := store.ListExecutions(t.Context(), jobs.ExecutionFilter{
		JobID: job.ID, Limit: 100, TriggeredBy: []jobs.ExecutionTrigger{jobs.TriggerManual},
	})
	if err != nil {
		t.Fatalf("filtro per origine: %v", err)
	}
	if len(manuali) != 3 {
		t.Errorf("esecuzioni manuali = %d, attese 3", len(manuali))
	}

	// Filtro per stato e per finestra temporale.
	falliti, err := store.ListExecutions(t.Context(), jobs.ExecutionFilter{
		JobID: job.ID, Limit: 100,
		Status: []jobs.ExecutionStatus{jobs.StatusFailed},
		Since:  base.Add(time.Minute),
	})
	if err != nil {
		t.Fatalf("filtro per stato: %v", err)
	}
	if len(falliti) != 2 {
		t.Errorf("esecuzioni fallite dopo il primo minuto = %d, attese 2", len(falliti))
	}
}

// TestLeEsecuzioniSeguonoIlJobEliminato: `ON DELETE CASCADE` della 0006, che è
// anche il motivo per cui nessuna tabella riferisce `job_executions`.
func TestLeEsecuzioniSeguonoIlJobEliminato(t *testing.T) {
	store, userID, pool := newStore(t)
	job, err := store.CreateJob(t.Context(), sampleJob(userID), nil)
	if err != nil {
		t.Fatalf("CreateJob: %v", err)
	}
	if _, err := store.CreateExecution(t.Context(), jobs.Execution{
		JobID: job.ID, ScheduledFor: time.Now().UTC().Truncate(time.Second),
		Environment: jobs.EnvironmentProduction, Attempt: 1,
		Status: jobs.StatusPending, TriggeredBy: jobs.TriggerManual,
	}); err != nil {
		t.Fatalf("CreateExecution: %v", err)
	}

	if err := store.DeleteJob(t.Context(), userID, job.ID); err != nil {
		t.Fatalf("DeleteJob: %v", err)
	}

	var rimaste int
	if err := pool.QueryRow(t.Context(),
		`SELECT count(*) FROM job_executions WHERE job_id = $1::uuid`, job.ID).Scan(&rimaste); err != nil {
		t.Fatalf("conteggio: %v", err)
	}
	if rimaste != 0 {
		t.Errorf("esecuzioni rimaste = %d, attese 0", rimaste)
	}
}

// ------------------------------------------------------------ elenco dei job

func TestElencoEFiltriDeiJob(t *testing.T) {
	store, userID, pool := newStore(t)

	for i := 0; i < 5; i++ {
		job := sampleJob(userID)
		job.Name = fmt.Sprintf("job-%02d", i)
		job.Enabled = i%2 == 0
		if i == 4 {
			job.Environments = []jobs.Environment{jobs.EnvironmentStaging}
		}
		if _, err := store.CreateJob(t.Context(), job, nil); err != nil {
			t.Fatalf("job %d: %v", i, err)
		}
	}
	if _, err := pool.Exec(t.Context(),
		`UPDATE jobs SET archived_at = now() WHERE name = 'job-00'`); err != nil {
		t.Fatalf("archiviazione: %v", err)
	}

	// Gli archiviati sono esclusi per default: non sono più nel `cron.yaml`
	// dell'utente.
	rows, err := store.ListJobs(t.Context(), jobs.JobFilter{UserID: userID, Limit: 100})
	if err != nil {
		t.Fatalf("ListJobs: %v", err)
	}
	if len(rows) != 4 {
		t.Errorf("job = %d, attesi 4", len(rows))
	}

	rows, err = store.ListJobs(t.Context(), jobs.JobFilter{UserID: userID, Limit: 100, IncludeArchived: true})
	if err != nil {
		t.Fatalf("ListJobs con archiviati: %v", err)
	}
	if len(rows) != 5 {
		t.Errorf("job con archiviati = %d, attesi 5", len(rows))
	}

	attivi := true
	rows, err = store.ListJobs(t.Context(), jobs.JobFilter{UserID: userID, Limit: 100, Enabled: &attivi})
	if err != nil {
		t.Fatalf("ListJobs attivi: %v", err)
	}
	if len(rows) != 2 { // job-02 e job-04; job-00 è attivo ma archiviato
		t.Errorf("job attivi = %d, attesi 2", len(rows))
	}

	rows, err = store.ListJobs(t.Context(), jobs.JobFilter{
		UserID: userID, Limit: 100, Environment: jobs.EnvironmentStaging,
	})
	if err != nil {
		t.Fatalf("ListJobs per ambiente: %v", err)
	}
	if len(rows) != 1 || rows[0].Name != "job-04" {
		t.Errorf("filtro per ambiente: %d righe", len(rows))
	}
}

// ------------------------------------------------------------------ supporto

func intPtr(v int) *int { return &v }

func equalIntPtr(a, b *int) bool {
	switch {
	case a == nil && b == nil:
		return true
	case a == nil || b == nil:
		return false
	default:
		return *a == *b
	}
}
