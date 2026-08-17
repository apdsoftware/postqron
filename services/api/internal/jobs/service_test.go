package jobs_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/apdsoftware/postqron/services/api/internal/jobs"
	"github.com/apdsoftware/postqron/services/api/internal/jobstest"
)

const utente = "11111111-1111-4111-8111-111111111111"
const altroUtente = "22222222-2222-4222-8222-222222222222"

// istanteDiProva è l'ora da cui parte ogni banco. È un minuto tondo perché il
// tetto al trigger manuale lavora su una griglia, e partire a metà casella
// renderebbe i conti dei test dipendenti dall'ora scelta.
var istanteDiProva = time.Date(2026, 8, 17, 10, 0, 0, 0, time.UTC)

// orologio è un tempo che i test fanno avanzare a mano: il tetto al trigger
// manuale è una griglia temporale, e verificarla aspettando davvero renderebbe
// la suite lenta e instabile.
type orologio struct {
	mu  sync.Mutex
	now time.Time
}

func (c *orologio) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *orologio) avanza(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// dispatcherFinto registra le notifiche al motore.
type dispatcherFinto struct {
	mu       sync.Mutex
	ricevute []jobs.Execution
	err      error
}

func (d *dispatcherFinto) Dispatch(_ context.Context, exec jobs.Execution) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.ricevute = append(d.ricevute, exec)
	return d.err
}

func (d *dispatcherFinto) count() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.ricevute)
}

// guardPermissivo ammette qualunque destinazione: serve ai test che non stanno
// provando R38 e che non devono per questo toccare il DNS.
type guardPermissivo struct{}

func (guardPermissivo) CheckTarget(_ context.Context, _ *url.URL) error { return nil }

type banco struct {
	t          *testing.T
	svc        *jobs.Service
	store      *jobstest.Store
	clock      *orologio
	dispatcher *dispatcherFinto
}

func newBanco(t *testing.T) *banco {
	t.Helper()

	clock := &orologio{now: istanteDiProva}
	store := jobstest.NewStore()
	store.Now = clock.Now
	dispatcher := &dispatcherFinto{}

	svc, err := jobs.NewService(jobs.Options{
		Store:      store,
		Logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
		Dispatcher: dispatcher,
		Now:        clock.Now,
		// Esplicito, perché il predefinito non lo è più: senza, ogni banco di
		// prova costruirebbe il blocco SSRF vero e ogni `Create` di questa suite
		// interrogherebbe il DNS per `api.example.com`. Il blocco predefinito ha
		// un test suo — TestIlBloccoSSRFEPredefinito.
		Guard: guardPermissivo{},
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	return &banco{t: t, svc: svc, store: store, clock: clock, dispatcher: dispatcher}
}

func (b *banco) crea(job jobs.Job) jobs.Job {
	b.t.Helper()
	created, err := b.svc.Create(b.t.Context(), utente, job)
	if err != nil {
		b.t.Fatalf("creazione del job %q: %v", job.Name, err)
	}
	return created
}

// ------------------------------------------------------------------- creazione

// TestIlBloccoSSRFEPredefinito verifica la scelta che rende R38 vera in
// produzione: `Options.Guard` nil significa **il blocco**, non la sua assenza.
//
// È il caso che dà il nome alla issue #455, provato dal punto in cui l'utente lo
// produrrebbe: PostgreSQL sta sulla stessa macchina dell'API, sulla 5433
// (AGENTS.md §7), e un job che punta lì parlerebbe con il nostro database. Il
// controllo non ha bisogno del DNS — l'URL contiene già un indirizzo — quindi il
// test non tocca la rete.
//
// Se un giorno qualcuno rimuovesse il predefinito da [jobs.NewService] pensando
// che il guard venga collegato in cmd/api, questo test si accorge che non lo è.
func TestIlBloccoSSRFEPredefinito(t *testing.T) {
	store := jobstest.NewStore()
	svc, err := jobs.NewService(jobs.Options{
		Store:  store,
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}

	casi := []string{
		"http://127.0.0.1:5433/",                   // il nostro database
		"http://localhost:8080/internal",           // la nostra API, per nome
		"http://169.254.169.254/latest/meta-data/", // le credenziali della macchina
		"http://10.0.0.5/",
		"http://[::ffff:127.0.0.1]:5433/",
	}
	for _, raw := range casi {
		t.Run(raw, func(t *testing.T) {
			job := validJob()
			job.Name = "sonda"
			job.URL = raw

			_, err := svc.Create(t.Context(), utente, job)
			invalid, ok := jobs.AsValidation(err)
			if !ok {
				t.Fatalf("errore = %v, atteso un rifiuto di validazione", err)
			}
			var trovato bool
			for _, campo := range invalid.Fields {
				if campo.Field == "request.url" && campo.Code == "target_not_allowed" {
					trovato = true
					// Il messaggio non deve confermare *cosa* ha trovato: vedi
					// netguard.ErrNotAllowed.
					if strings.Contains(campo.Message, "127.0.0.1") || strings.Contains(campo.Message, "169.254") {
						t.Errorf("il messaggio all'utente contiene l'indirizzo: %s", campo.Message)
					}
				}
			}
			if !trovato {
				t.Fatalf("nessun rifiuto su request.url: %v", invalid.Fields)
			}
		})
	}
}

func TestCreazione(t *testing.T) {
	b := newBanco(t)

	created := b.crea(validJob())

	if created.ID == "" {
		t.Fatal("il job creato non ha identificativo")
	}
	if created.UserID != utente {
		t.Errorf("user_id = %q", created.UserID)
	}
	// `next_run_at` è dello scheduler, anche il primo valore: la migrazione 0010
	// nomina proprio questo caso — «un job appena creato da API ... nasce con la
	// colonna a NULL» — e ci costruisce sopra `jobs_unscheduled_idx`. Calcolarla
	// qui sarebbe una seconda copia della stessa verità.
	if created.NextRunAt != nil {
		t.Errorf("next_run_at = %s: la colonna è dello scheduler (0005, 0010), l'API non la calcola", created.NextRunAt)
	}
	if created.RepositoryID != "" {
		t.Errorf("l'API non deve creare job gestiti da un repository, repository_id = %q", created.RepositoryID)
	}
}

// TestCreazioneIgnoraICampiDecisiDalServizio: un client che manda `repository_id`
// non deve poter far passare un proprio job per uno sincronizzato — quelli non
// si possono più modificare da API.
func TestCreazioneIgnoraICampiDecisiDalServizio(t *testing.T) {
	b := newBanco(t)

	job := validJob()
	job.ID = "99999999-9999-4999-8999-999999999999"
	job.UserID = altroUtente
	job.RepositoryID = "33333333-3333-4333-8333-333333333333"
	archived := b.clock.Now()
	job.ArchivedAt = &archived

	created := b.crea(job)

	if created.UserID != utente {
		t.Errorf("user_id = %q, atteso quello del chiamante", created.UserID)
	}
	if created.RepositoryID != "" {
		t.Errorf("repository_id = %q, atteso vuoto", created.RepositoryID)
	}
	if created.ArchivedAt != nil {
		t.Errorf("archived_at = %s, atteso nullo", created.ArchivedAt)
	}
}

func TestCreazioneNomeDuplicato(t *testing.T) {
	b := newBanco(t)
	b.crea(validJob())

	_, err := b.svc.Create(t.Context(), utente, validJob())
	if !errors.Is(err, jobs.ErrNameTaken) {
		t.Fatalf("errore = %v, atteso ErrNameTaken", err)
	}
}

// TestTettoDiPianoAllaCreazione verifica il limite di R15 sul numero di job.
func TestTettoDiPianoAllaCreazione(t *testing.T) {
	b := newBanco(t)
	max := 2
	b.store.SetPlan(utente, jobs.Plan{
		Code: "free", Name: "Free", MaxJobs: &max, MinInterval: time.Minute,
	})

	for i := 0; i < max; i++ {
		job := validJob()
		job.Name = fmt.Sprintf("job-%d", i)
		b.crea(job)
	}

	job := validJob()
	job.Name = "job-di-troppo"
	_, err := b.svc.Create(t.Context(), utente, job)

	limit := planLimit(t, err)
	if limit.Limit != jobs.LimitJobs {
		t.Fatalf("limite = %q, atteso %q", limit.Limit, jobs.LimitJobs)
	}
	if len(b.store.Jobs()) != max {
		t.Errorf("job registrati = %d, attesi %d", len(b.store.Jobs()), max)
	}
}

// TestIlTettoValeAncheSuiJobInPausa: contare solo quelli attivi renderebbe il
// limite aggirabile creando job spenti e accendendoli a rotazione.
func TestIlTettoValeAncheSuiJobInPausa(t *testing.T) {
	b := newBanco(t)
	max := 1
	b.store.SetPlan(utente, jobs.Plan{Code: "free", MaxJobs: &max, MinInterval: time.Minute})

	primo := validJob()
	primo.Name = "primo"
	primo.Enabled = false
	b.crea(primo)

	secondo := validJob()
	secondo.Name = "secondo"
	if _, err := b.svc.Create(t.Context(), utente, secondo); !errors.Is(err, jobs.ErrJobLimitReached) {
		if _, ok := jobs.AsPlanLimit(err); !ok {
			t.Fatalf("errore = %v, atteso un limite di piano", err)
		}
	}
}

// TestIlTettoResisteAllaCorsa: il conteggio del Service produce il messaggio,
// ma la garanzia è nell'istruzione di inserimento.
func TestIlTettoResisteAllaCorsa(t *testing.T) {
	b := newBanco(t)
	max := 1
	b.store.SetPlan(utente, jobs.Plan{Code: "free", MaxJobs: &max, MinInterval: time.Minute})

	var wg sync.WaitGroup
	esiti := make([]error, 4)
	for i := range esiti {
		wg.Add(1)
		go func() {
			defer wg.Done()
			job := validJob()
			job.Name = fmt.Sprintf("concorrente-%d", i)
			_, esiti[i] = b.svc.Create(context.Background(), utente, job)
		}()
	}
	wg.Wait()

	riusciti := 0
	for _, err := range esiti {
		if err == nil {
			riusciti++
		}
	}
	if riusciti != 1 {
		t.Fatalf("creazioni riuscite = %d, attesa 1 (tetto = %d)", riusciti, max)
	}
}

// ------------------------------------------------------------------- modifica

func TestModificaParziale(t *testing.T) {
	b := newBanco(t)
	created := b.crea(validJob())

	nome := "digest-serale"
	updated, err := b.svc.Update(t.Context(), utente, created.ID, jobs.Patch{Name: &nome})
	if err != nil {
		t.Fatalf("modifica: %v", err)
	}
	if updated.Name != nome {
		t.Errorf("nome = %q, atteso %q", updated.Name, nome)
	}
	// Ciò che non è stato mandato non cambia: è tutta la differenza fra un PATCH
	// e una sostituzione.
	if updated.URL != created.URL || updated.Schedule != created.Schedule {
		t.Errorf("la modifica ha toccato campi non richiesti: %+v", updated)
	}
}

// TestCambioDiModalita: valorizzare `every` azzera `schedule`, altrimenti
// l'unico modo di cambiare modalità sarebbe mandare la coppia che il vincolo XOR
// rifiuta.
func TestCambioDiModalita(t *testing.T) {
	b := newBanco(t)
	b.store.SetPlan(utente, planPro())
	created := b.crea(validJob())

	every := 30 * time.Second
	updated, err := b.svc.Update(t.Context(), utente, created.ID, jobs.Patch{Every: &every})
	if err != nil {
		t.Fatalf("passaggio a intervallo: %v", err)
	}
	if updated.Schedule != "" {
		t.Errorf("schedule = %q, atteso vuoto dopo il passaggio a `every`", updated.Schedule)
	}
	if updated.Every != every {
		t.Errorf("every = %s, atteso %s", updated.Every, every)
	}

	expression := "0 9 * * *"
	updated, err = b.svc.Update(t.Context(), utente, created.ID, jobs.Patch{Schedule: &expression})
	if err != nil {
		t.Fatalf("ritorno al cron: %v", err)
	}
	if updated.Every != 0 {
		t.Errorf("every = %s, atteso zero dopo il ritorno a `schedule`", updated.Every)
	}
}

// TestModificaCheViolaIlPiano: la risoluzione si riverifica a ogni scrittura,
// non solo alla creazione.
func TestModificaCheViolaIlPiano(t *testing.T) {
	b := newBanco(t)
	created := b.crea(validJob())

	every := time.Second
	_, err := b.svc.Update(t.Context(), utente, created.ID, jobs.Patch{Every: &every})
	limit := planLimit(t, err)
	if limit.Limit != jobs.LimitResolution {
		t.Fatalf("limite = %q, atteso %q", limit.Limit, jobs.LimitResolution)
	}
}

// TestLaModificaAzzeraLaProssimaOccorrenzaSoloQuandoServe.
//
// L'API non calcola mai `next_run_at`, ma decide quando invalidarla. Sbagliare
// in un verso lascia un job che parte all'orario della schedulazione vecchia;
// sbagliare nell'altro è un aggiornamento perso ai danni dello scheduler, che
// quella colonna la fa avanzare in continuazione.
func TestLaModificaAzzeraLaProssimaOccorrenzaSoloQuandoServe(t *testing.T) {
	// Il job parte con la prossima occorrenza già scritta, come l'avrebbe
	// lasciata lo scheduler dopo la sua prima passata.
	prossima := istanteDiProva.Add(time.Hour)

	cases := []struct {
		nome     string
		patch    func() jobs.Patch
		azzerata bool
	}{
		{"pausa", func() jobs.Patch { spento := false; return jobs.Patch{Enabled: &spento} }, true},
		{"cambio di espressione", func() jobs.Patch { e := "0 18 * * *"; return jobs.Patch{Schedule: &e} }, true},
		{"cambio di fuso", func() jobs.Patch { tz := "UTC"; return jobs.Patch{Timezone: &tz} }, true},
		{"cambio di modalità", func() jobs.Patch { every := 5 * time.Minute; return jobs.Patch{Every: &every} }, true},
		{"solo la descrizione", func() jobs.Patch { d := "nuova"; return jobs.Patch{Description: &d} }, false},
		{"solo il timeout", func() jobs.Patch { t := 10 * time.Second; return jobs.Patch{Timeout: &t} }, false},
		{"solo l'URL", func() jobs.Patch { u := "https://example.com/altro"; return jobs.Patch{URL: &u} }, false},
	}

	for _, tc := range cases {
		t.Run(tc.nome, func(t *testing.T) {
			b := newBanco(t)
			b.store.SetPlan(utente, planTeam())

			job := validJob()
			job.UserID = utente
			job.NextRunAt = &prossima
			seminato := b.store.Seed(job)

			updated, err := b.svc.Update(t.Context(), utente, seminato.ID, tc.patch())
			if err != nil {
				t.Fatalf("modifica: %v", err)
			}

			if tc.azzerata && updated.NextRunAt != nil {
				t.Errorf("next_run_at = %s, attesa azzerata: lo scheduler deve ricalcolarla", updated.NextRunAt)
			}
			if !tc.azzerata && (updated.NextRunAt == nil || !updated.NextRunAt.Equal(prossima)) {
				t.Errorf("next_run_at = %v, attesa intatta (%s): riscriverla è un aggiornamento perso",
					updated.NextRunAt, prossima)
			}
		})
	}
}

// --------------------------------------------------- job gestiti da un repository

// seedGestito registra un job che nasce da un `cron.yaml` (R13).
func (b *banco) seedGestito(tune ...func(*jobs.Job)) jobs.Job {
	b.t.Helper()
	job := validJob()
	job.UserID = utente
	job.RepositoryID = "33333333-3333-4333-8333-333333333333"
	job.Name = "da-repository"
	for _, fn := range tune {
		fn(&job)
	}
	return b.store.Seed(job)
}

func TestJobGestitoNonSiModifica(t *testing.T) {
	b := newBanco(t)
	gestito := b.seedGestito()

	nome := "rinominato"
	_, err := b.svc.Update(t.Context(), utente, gestito.ID, jobs.Patch{Name: &nome})
	if !errors.Is(err, jobs.ErrManaged) {
		t.Fatalf("errore = %v, atteso ErrManaged: la riconciliazione riporterebbe indietro la modifica", err)
	}
}

// TestJobGestitoSiPuoMetterInPausa: la 0005 tiene `enabled` distinto da
// `archived_at` proprio perché la pausa dell'utente sopravviva al sync.
func TestJobGestitoSiPuoMetterInPausa(t *testing.T) {
	b := newBanco(t)
	gestito := b.seedGestito()

	spento := false
	updated, err := b.svc.Update(t.Context(), utente, gestito.ID, jobs.Patch{Enabled: &spento})
	if err != nil {
		t.Fatalf("pausa di un job gestito: %v", err)
	}
	if updated.Enabled {
		t.Error("il job non è stato messo in pausa")
	}
}

func TestJobGestitoNonSiElimina(t *testing.T) {
	b := newBanco(t)
	gestito := b.seedGestito()

	if err := b.svc.Delete(t.Context(), utente, gestito.ID); !errors.Is(err, jobs.ErrManaged) {
		t.Fatalf("errore = %v, atteso ErrManaged", err)
	}
}

// TestJobGestitoArchiviatoSiElimina: dal file è già sparito, eliminarlo non
// contraddice nessuna riconciliazione futura.
func TestJobGestitoArchiviatoSiElimina(t *testing.T) {
	b := newBanco(t)
	archived := b.clock.Now()
	gestito := b.seedGestito(func(j *jobs.Job) { j.ArchivedAt = &archived })

	if err := b.svc.Delete(t.Context(), utente, gestito.ID); err != nil {
		t.Fatalf("eliminazione di un job archiviato: %v", err)
	}
}

func TestJobArchiviatoNonSiModifica(t *testing.T) {
	b := newBanco(t)
	archived := b.clock.Now()
	gestito := b.seedGestito(func(j *jobs.Job) { j.ArchivedAt = &archived })

	spento := false
	if _, err := b.svc.Update(t.Context(), utente, gestito.ID, jobs.Patch{Enabled: &spento}); !errors.Is(err, jobs.ErrArchived) {
		t.Fatalf("errore = %v, atteso ErrArchived", err)
	}
}

// ------------------------------------------------------------------- isolamento

func TestUnJobAltruiNonEsiste(t *testing.T) {
	b := newBanco(t)
	created := b.crea(validJob())

	nome := "furto"
	operazioni := map[string]func() error{
		"lettura": func() error { _, err := b.svc.Get(t.Context(), altroUtente, created.ID); return err },
		"modifica": func() error {
			_, err := b.svc.Update(t.Context(), altroUtente, created.ID, jobs.Patch{Name: &nome})
			return err
		},
		"eliminazione": func() error { return b.svc.Delete(t.Context(), altroUtente, created.ID) },
		"esecuzioni": func() error {
			_, err := b.svc.Executions(t.Context(), altroUtente, created.ID, jobs.ExecutionOptions{})
			return err
		},
		"trigger": func() error { _, err := b.svc.Trigger(t.Context(), altroUtente, created.ID, ""); return err },
	}

	for nome, op := range operazioni {
		if err := op(); !errors.Is(err, jobs.ErrNotFound) {
			t.Errorf("%s su job altrui: errore = %v, atteso ErrNotFound", nome, err)
		}
	}
}

// -------------------------------------------------------------- trigger manuale

func TestTriggerManuale(t *testing.T) {
	b := newBanco(t)
	created := b.crea(validJob())

	exec, err := b.svc.Trigger(t.Context(), utente, created.ID, "")
	if err != nil {
		t.Fatalf("trigger: %v", err)
	}

	if exec.TriggeredBy != jobs.TriggerManual {
		t.Errorf("triggered_by = %q, atteso %q: senza la traccia, un abuso sotto soglia sarebbe invisibile",
			exec.TriggeredBy, jobs.TriggerManual)
	}
	if exec.Status != jobs.StatusPending {
		t.Errorf("status = %q, atteso %q", exec.Status, jobs.StatusPending)
	}
	if exec.Environment != jobs.EnvironmentProduction {
		t.Errorf("environment = %q", exec.Environment)
	}
	if exec.Attempt != 1 {
		t.Errorf("attempt = %d, atteso 1", exec.Attempt)
	}
	if b.dispatcher.count() != 1 {
		t.Errorf("notifiche al motore = %d, attesa 1", b.dispatcher.count())
	}
}

// TestIlTriggerManualeNonAggiraIlPiano è il punto che decide se il piano Free
// vale qualcosa: senza tetto, un ciclo lo trasforma in esecuzioni illimitate.
func TestIlTriggerManualeNonAggiraIlPiano(t *testing.T) {
	b := newBanco(t)
	created := b.crea(validJob())

	if _, err := b.svc.Trigger(t.Context(), utente, created.ID, ""); err != nil {
		t.Fatalf("primo trigger: %v", err)
	}

	// Secondo trigger nello stesso minuto: su Free la risoluzione è di un
	// minuto, quindi la casella è occupata.
	b.clock.avanza(10 * time.Second)
	_, err := b.svc.Trigger(t.Context(), utente, created.ID, "")
	limit := planLimit(t, err)
	if limit.Limit != jobs.LimitManualTrigger {
		t.Fatalf("limite = %q, atteso %q", limit.Limit, jobs.LimitManualTrigger)
	}
	if limit.RetryAfter <= 0 || limit.RetryAfter > time.Minute {
		t.Errorf("retry_after = %s, atteso dentro il minuto", limit.RetryAfter)
	}
	if got := len(b.store.Executions()); got != 1 {
		t.Fatalf("esecuzioni registrate = %d, attesa 1", got)
	}

	// Passato il minuto, la casella successiva è libera.
	b.clock.avanza(time.Minute)
	if _, err := b.svc.Trigger(t.Context(), utente, created.ID, ""); err != nil {
		t.Fatalf("trigger nella casella successiva: %v", err)
	}
	if got := len(b.store.Executions()); got != 2 {
		t.Errorf("esecuzioni registrate = %d, attese 2", got)
	}
}

// TestIlTriggerManualeSegueLaRisoluzioneDelPiano: il costo di un «esegui adesso»
// è quello di un'occorrenza schedulata, che è ciò per cui l'utente paga.
func TestIlTriggerManualeSegueLaRisoluzioneDelPiano(t *testing.T) {
	b := newBanco(t)
	b.store.SetPlan(utente, planPro()) // risoluzione 10 secondi
	created := b.crea(validJob())

	if _, err := b.svc.Trigger(t.Context(), utente, created.ID, ""); err != nil {
		t.Fatalf("primo trigger: %v", err)
	}

	b.clock.avanza(3 * time.Second)
	if _, err := b.svc.Trigger(t.Context(), utente, created.ID, ""); err == nil {
		t.Fatal("un secondo trigger dentro i dieci secondi doveva essere rifiutato")
	}

	b.clock.avanza(10 * time.Second)
	if _, err := b.svc.Trigger(t.Context(), utente, created.ID, ""); err != nil {
		t.Fatalf("trigger dopo dieci secondi: %v", err)
	}
}

// TestTriggerInConflittoConUnaOccorrenzaSchedulata: la casella occupata da una
// schedulazione non è un limite superato, è un conflitto. Un client che riceve
// `plan_limit_manual_trigger` mostrerebbe un invito all'upgrade a chi non ha
// superato niente.
func TestTriggerInConflittoConUnaOccorrenzaSchedulata(t *testing.T) {
	b := newBanco(t)
	created := b.crea(validJob())

	b.store.SeedExecution(jobs.Execution{
		JobID:        created.ID,
		ScheduledFor: b.clock.Now().Truncate(time.Minute),
		Environment:  jobs.EnvironmentProduction,
		Attempt:      1,
		Status:       jobs.StatusSucceeded,
		TriggeredBy:  jobs.TriggerSchedule,
	})

	_, err := b.svc.Trigger(t.Context(), utente, created.ID, "")
	if !errors.Is(err, jobs.ErrExecutionExists) {
		t.Fatalf("errore = %v, atteso ErrExecutionExists", err)
	}
	if _, ok := jobs.AsPlanLimit(err); ok {
		t.Error("un conflitto con la schedulazione non è un limite di piano")
	}
}

// TestBudgetAggregatoDeiTriggerManuali verifica il tetto che tiene dove la
// casella per job non basta: sui piani senza tetto rigido al numero di job, ogni
// job ha la propria casella e nulla impedirebbe a un utente con cinquemila job
// di superare di cinque volte la soglia di fair use del proprio piano.
func TestBudgetAggregatoDeiTriggerManuali(t *testing.T) {
	b := newBanco(t)
	fairUse := 2
	b.store.SetPlan(utente, jobs.Plan{
		Code: "team", Name: "Team",
		FairUseJobs: &fairUse, // nessun tetto rigido: i job si creano comunque
		MinInterval: time.Second,
	})

	var creati []jobs.Job
	for i := 0; i < 4; i++ {
		job := validJob()
		job.Name = fmt.Sprintf("job-%d", i)
		creati = append(creati, b.crea(job))
	}

	// I primi due consumano il budget: due esecuzioni manuali al secondo, che è
	// la soglia dichiarata dal piano.
	for i := 0; i < fairUse; i++ {
		if _, err := b.svc.Trigger(t.Context(), utente, creati[i].ID, ""); err != nil {
			t.Fatalf("trigger %d: %v", i, err)
		}
	}

	// Il terzo ha una casella tutta sua e passerebbe il controllo per job: a
	// fermarlo è solo l'aggregato.
	_, err := b.svc.Trigger(t.Context(), utente, creati[fairUse].ID, "")
	limit := planLimit(t, err)
	if limit.Limit != jobs.LimitManualTrigger {
		t.Fatalf("limite = %q, atteso %q", limit.Limit, jobs.LimitManualTrigger)
	}
	if limit.RetryAfter <= 0 {
		t.Errorf("retry_after = %s, atteso positivo", limit.RetryAfter)
	}

	// Passata la finestra, il secchio si è riempito.
	b.clock.avanza(time.Second)
	if _, err := b.svc.Trigger(t.Context(), utente, creati[fairUse].ID, ""); err != nil {
		t.Fatalf("trigger dopo la finestra: %v", err)
	}
}

// TestSuiPianiConTettoRigidoLAggregatoNonSiIntromette: con `max_jobs` job e un
// burst pari a `max_jobs`, ogni job dev'essere eseguibile a mano una volta per
// casella senza che l'aggregato lo impedisca.
func TestSuiPianiConTettoRigidoLAggregatoNonSiIntromette(t *testing.T) {
	b := newBanco(t)
	max := 3
	b.store.SetPlan(utente, jobs.Plan{
		Code: "free", Name: "Free", MaxJobs: &max, MinInterval: time.Minute,
	})

	for i := 0; i < max; i++ {
		job := validJob()
		job.Name = fmt.Sprintf("job-%d", i)
		created := b.crea(job)
		if _, err := b.svc.Trigger(t.Context(), utente, created.ID, ""); err != nil {
			t.Fatalf("trigger del job %d: %v", i, err)
		}
	}
}

func TestTriggerRifiutato(t *testing.T) {
	cases := []struct {
		nome  string
		tune  func(*jobs.Job)
		env   jobs.Environment
		check func(*testing.T, error)
	}{
		{
			nome: "job in pausa",
			tune: func(j *jobs.Job) { j.Enabled = false },
			check: func(t *testing.T, err error) {
				if !errors.Is(err, jobs.ErrDisabled) {
					t.Fatalf("errore = %v, atteso ErrDisabled", err)
				}
			},
		},
		{
			nome: "ambiente non dichiarato",
			env:  jobs.EnvironmentStaging,
			check: func(t *testing.T, err error) {
				if got := codes(t, err)["environment"]; got != "unknown_value" {
					t.Fatalf("codice = %q, atteso \"unknown_value\"", got)
				}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.nome, func(t *testing.T) {
			b := newBanco(t)
			job := validJob()
			if tc.tune != nil {
				tc.tune(&job)
			}
			created := b.crea(job)

			_, err := b.svc.Trigger(t.Context(), utente, created.ID, tc.env)
			tc.check(t, err)
		})
	}
}

// TestTriggerSuJobMultiAmbienteEsigeLAmbiente: indovinarlo significherebbe
// eseguire in produzione una prova destinata a staging.
func TestTriggerSuJobMultiAmbienteEsigeLAmbiente(t *testing.T) {
	b := newBanco(t)
	b.store.SetPlan(utente, planTeam())

	job := validJob()
	job.Environments = []jobs.Environment{jobs.EnvironmentStaging, jobs.EnvironmentProduction}
	created := b.crea(job)

	_, err := b.svc.Trigger(t.Context(), utente, created.ID, "")
	if got := codes(t, err)["environment"]; got != "required" {
		t.Fatalf("codice = %q, atteso \"required\" (errore: %v)", got, err)
	}

	exec, err := b.svc.Trigger(t.Context(), utente, created.ID, jobs.EnvironmentStaging)
	if err != nil {
		t.Fatalf("trigger su staging: %v", err)
	}
	if exec.Environment != jobs.EnvironmentStaging {
		t.Errorf("environment = %q", exec.Environment)
	}

	// L'altro ambiente ha la propria casella: due esecuzioni per occorrenza, come
	// prevede R23.
	if _, err := b.svc.Trigger(t.Context(), utente, created.ID, jobs.EnvironmentProduction); err != nil {
		t.Fatalf("trigger su production: %v", err)
	}
}

func TestPartizioneMancante(t *testing.T) {
	b := newBanco(t)
	created := b.crea(validJob())
	b.store.PartitionMissing = true

	if _, err := b.svc.Trigger(t.Context(), utente, created.ID, ""); !errors.Is(err, jobs.ErrPartitionMissing) {
		t.Fatalf("errore = %v, atteso ErrPartitionMissing", err)
	}
}

// ------------------------------------------------------------- paginazione

func TestPaginazioneDeiJob(t *testing.T) {
	b := newBanco(t)
	b.store.SetPlan(utente, planTeam())

	const totale = 7
	for i := 0; i < totale; i++ {
		job := validJob()
		job.Name = fmt.Sprintf("job-%02d", i)
		b.crea(job)
		// Creazioni allo stesso istante renderebbero l'ordine indeterminato: il
		// cursore è (created_at, id) proprio perché la parità va rotta.
		b.clock.avanza(time.Second)
	}

	visti := map[string]bool{}
	cursore := ""
	pagine := 0
	for {
		page, err := b.svc.List(t.Context(), utente, jobs.ListOptions{Limit: 3, Cursor: cursore})
		if err != nil {
			t.Fatalf("pagina %d: %v", pagine, err)
		}
		pagine++
		for _, job := range page.Items {
			if visti[job.ID] {
				t.Fatalf("il job %q compare in due pagine", job.Name)
			}
			visti[job.ID] = true
		}
		if page.NextCursor == "" {
			break
		}
		cursore = page.NextCursor
		if pagine > 10 {
			t.Fatal("la paginazione non termina")
		}
	}

	if len(visti) != totale {
		t.Errorf("job visti = %d, attesi %d", len(visti), totale)
	}
	if pagine != 3 {
		t.Errorf("pagine = %d, attese 3 (7 job a 3 per pagina)", pagine)
	}
}

func TestPaginazioneDelleEsecuzioni(t *testing.T) {
	b := newBanco(t)
	created := b.crea(validJob())

	const totale = 5
	for i := 0; i < totale; i++ {
		b.store.SeedExecution(jobs.Execution{
			JobID:        created.ID,
			ScheduledFor: b.clock.Now().Add(time.Duration(i) * time.Minute),
			Environment:  jobs.EnvironmentProduction,
			Attempt:      1,
			Status:       jobs.StatusSucceeded,
			TriggeredBy:  jobs.TriggerSchedule,
		})
	}

	page, err := b.svc.Executions(t.Context(), utente, created.ID, jobs.ExecutionOptions{Limit: 2})
	if err != nil {
		t.Fatalf("prima pagina: %v", err)
	}
	if len(page.Items) != 2 {
		t.Fatalf("righe = %d, attese 2", len(page.Items))
	}
	// Dalla più recente: è ciò che serve a una vista dei log.
	if !page.Items[0].ScheduledFor.After(page.Items[1].ScheduledFor) {
		t.Error("le esecuzioni non sono ordinate dalla più recente")
	}
	if page.NextCursor == "" {
		t.Fatal("manca il cursore della pagina successiva")
	}

	seconda, err := b.svc.Executions(t.Context(), utente, created.ID,
		jobs.ExecutionOptions{Limit: 2, Cursor: page.NextCursor})
	if err != nil {
		t.Fatalf("seconda pagina: %v", err)
	}
	if seconda.Items[0].ScheduledFor.After(page.Items[1].ScheduledFor) {
		t.Error("la seconda pagina ricomincia da capo invece di proseguire")
	}
}

// TestFiltroSuiTriggerManuali: limitarli senza poterli contare renderebbe
// impossibile accorgersi di un abuso che sta sotto la soglia.
func TestFiltroSuiTriggerManuali(t *testing.T) {
	b := newBanco(t)
	created := b.crea(validJob())

	if _, err := b.svc.Trigger(t.Context(), utente, created.ID, ""); err != nil {
		t.Fatalf("trigger: %v", err)
	}
	b.store.SeedExecution(jobs.Execution{
		JobID:        created.ID,
		ScheduledFor: b.clock.Now().Add(time.Hour),
		Environment:  jobs.EnvironmentProduction,
		Attempt:      1,
		Status:       jobs.StatusSucceeded,
		TriggeredBy:  jobs.TriggerSchedule,
	})

	page, err := b.svc.Executions(t.Context(), utente, created.ID, jobs.ExecutionOptions{
		TriggeredBy: []jobs.ExecutionTrigger{jobs.TriggerManual},
	})
	if err != nil {
		t.Fatalf("filtro: %v", err)
	}
	if len(page.Items) != 1 || page.Items[0].TriggeredBy != jobs.TriggerManual {
		t.Fatalf("righe = %+v, attesa la sola esecuzione manuale", page.Items)
	}
}

func TestCursoreNonValido(t *testing.T) {
	b := newBanco(t)
	created := b.crea(validJob())

	if _, err := b.svc.List(t.Context(), utente, jobs.ListOptions{Cursor: "non-un-cursore"}); !errors.Is(err, jobs.ErrInvalidCursor) {
		t.Errorf("elenco job: errore = %v, atteso ErrInvalidCursor", err)
	}
	// Un cursore dell'altro tipo non deve essere accettato: il prefisso esiste
	// perché scambiarli produrrebbe una pagina silenziosamente sbagliata.
	altro := jobs.JobCursor{CreatedAt: b.clock.Now(), ID: created.ID}.Encode()
	if _, err := b.svc.Executions(t.Context(), utente, created.ID, jobs.ExecutionOptions{Cursor: altro}); !errors.Is(err, jobs.ErrInvalidCursor) {
		t.Errorf("registro: errore = %v, atteso ErrInvalidCursor", err)
	}
}

func TestLimiteDiPaginaLimitato(t *testing.T) {
	if got := jobs.ClampLimit(0); got != jobs.DefaultPageSize {
		t.Errorf("limite predefinito = %d, atteso %d", got, jobs.DefaultPageSize)
	}
	if got := jobs.ClampLimit(100000); got != jobs.MaxPageSize {
		t.Errorf("limite massimo = %d, atteso %d", got, jobs.MaxPageSize)
	}
	if got := jobs.ClampLimit(10); got != 10 {
		t.Errorf("limite = %d, atteso 10", got)
	}
}
