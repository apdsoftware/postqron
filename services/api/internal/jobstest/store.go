// Package jobstest contiene i doppi di prova dei job.
//
// Esiste come package a sé, e non come file `_test.go` di internal/jobs, per la
// stessa ragione di internal/authtest: serve a due suite — quella del Service e
// quella delle rotte in internal/httpapi — e duplicarlo produrrebbe due archivi
// finti liberi di divergere, il secondo dei quali smetterebbe di rispettare il
// contratto di [jobs.Store] senza che nessuno se ne accorga.
//
// L'archivio riproduce **i vincoli del database**, non una versione comoda di
// essi: unicità del nome per utente, tetto al numero di job applicato dentro
// l'inserimento, chiave naturale delle esecuzioni. Un doppio più permissivo del
// database renderebbe verdi proprio i test che devono restare rossi.
package jobstest

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/apdsoftware/postqron/services/api/internal/jobs"
)

// Store è un'implementazione in memoria di [jobs.Store].
type Store struct {
	mu   sync.Mutex
	seq  atomic.Int64
	jobs map[string]jobs.Job
	// execs è indicizzato per chiave naturale, come in `job_executions`.
	execs map[execKey]jobs.Execution
	plans map[string]jobs.Plan
	// byCode è il listino: i piani indicizzati per codice, che esistono anche
	// senza un utente che li sottoscriva.
	byCode map[string]jobs.Plan

	// Now è l'orologio con cui vengono timbrati created_at e updated_at.
	Now func() time.Time

	// FailOn, se valorizzato, fa restituire un errore all'operazione con quel
	// nome. Serve a provare che un guasto della persistenza non diventa una
	// risposta di successo.
	FailOn map[string]error

	// PartitionMissing simula la partizione giornaliera mancante (0006).
	PartitionMissing bool
}

type execKey struct {
	jobID        string
	scheduledFor int64
	environment  jobs.Environment
	attempt      int
}

// NewStore costruisce un archivio vuoto.
func NewStore() *Store {
	return &Store{
		jobs:   map[string]jobs.Job{},
		execs:  map[execKey]jobs.Execution{},
		plans:  map[string]jobs.Plan{},
		byCode: map[string]jobs.Plan{},
		FailOn: map[string]error{},
		Now:    func() time.Time { return time.Now().UTC() },
	}
}

// SetPlan assegna un piano a un utente. Senza, l'utente ha [jobs.FreePlan],
// che è ciò che il database restituisce a chi non ha una sottoscrizione viva.
func (s *Store) SetPlan(userID string, plan jobs.Plan) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.plans[userID] = plan
}

// Seed inserisce un job già formato, saltando i controlli. Serve ai test che
// devono partire da uno stato che l'API non produrrebbe — un job gestito da un
// repository, per esempio.
func (s *Store) Seed(job jobs.Job) jobs.Job {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.insert(job)
}

// SeedExecution inserisce un'esecuzione già formata.
func (s *Store) SeedExecution(exec jobs.Execution) jobs.Execution {
	s.mu.Lock()
	defer s.mu.Unlock()
	if exec.CreatedAt.IsZero() {
		exec.CreatedAt = s.Now()
	}
	s.execs[keyOf(exec)] = exec
	return exec
}

func (s *Store) insert(job jobs.Job) jobs.Job {
	now := s.Now()
	if job.ID == "" {
		job.ID = fmt.Sprintf("00000000-0000-4000-8000-%012d", s.seq.Add(1))
	}
	if job.CreatedAt.IsZero() {
		job.CreatedAt = now
	}
	job.UpdatedAt = now
	s.jobs[job.ID] = job
	return job
}

func (s *Store) fail(op string) error { return s.FailOn[op] }

// ------------------------------------------------------------------ job

// CreateJob inserisce un job applicando unicità del nome e tetto di piano.
func (s *Store) CreateJob(_ context.Context, job jobs.Job, maxJobs *int) (jobs.Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.fail("CreateJob"); err != nil {
		return jobs.Job{}, err
	}

	active := 0
	for _, existing := range s.jobs {
		if existing.UserID != job.UserID {
			continue
		}
		if existing.ArchivedAt == nil {
			active++
		}
		// L'indice unico `jobs_user_name_key` è parziale su
		// `repository_id IS NULL`: due repository dello stesso utente possono
		// avere entrambi un job `healthcheck`.
		if existing.RepositoryID == job.RepositoryID && existing.Name == job.Name {
			return jobs.Job{}, jobs.ErrNameTaken
		}
	}
	if maxJobs != nil && active >= *maxJobs {
		return jobs.Job{}, jobs.ErrJobLimitReached
	}

	return s.insert(job), nil
}

// JobByID legge un job dell'utente.
func (s *Store) JobByID(_ context.Context, userID, jobID string) (jobs.Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.fail("JobByID"); err != nil {
		return jobs.Job{}, err
	}
	job, ok := s.jobs[jobID]
	if !ok || job.UserID != userID {
		return jobs.Job{}, jobs.ErrNotFound
	}
	return job, nil
}

// CountJobs conta i job non archiviati dell'utente.
func (s *Store) CountJobs(_ context.Context, userID string) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.fail("CountJobs"); err != nil {
		return 0, err
	}
	count := 0
	for _, job := range s.jobs {
		if job.UserID == userID && job.ArchivedAt == nil {
			count++
		}
	}
	return count, nil
}

// CountActiveJobs conta i job accesi e non archiviati dell'utente.
//
// È il conteggio della capacità e non del catalogo: vedi
// [jobs.Plan.CheckActiveJobCount] per la differenza, che è ciò che permette a un
// utente sceso di piano di riaccendere qualcosa dopo un downgrade (R58).
func (s *Store) CountActiveJobs(_ context.Context, userID string) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.fail("CountActiveJobs"); err != nil {
		return 0, err
	}
	count := 0
	for _, job := range s.jobs {
		if job.UserID == userID && job.Enabled && job.ArchivedAt == nil {
			count++
		}
	}
	return count, nil
}

// ListJobs elenca i job secondo il filtro, dal più recente, con una riga in più.
func (s *Store) ListJobs(_ context.Context, filter jobs.JobFilter) ([]jobs.Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.fail("ListJobs"); err != nil {
		return nil, err
	}

	var out []jobs.Job
	for _, job := range s.jobs {
		switch {
		case job.UserID != filter.UserID:
		case !filter.IncludeArchived && job.ArchivedAt != nil:
		case filter.Enabled != nil && job.Enabled != *filter.Enabled:
		case filter.Environment != "" && !job.HasEnvironment(filter.Environment):
		case filter.Cursor != nil && !beforeJobCursor(job, *filter.Cursor):
		default:
			out = append(out, job)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].CreatedAt.After(out[j].CreatedAt)
		}
		return out[i].ID > out[j].ID
	})
	if len(out) > filter.Limit+1 {
		out = out[:filter.Limit+1]
	}
	return out, nil
}

// beforeJobCursor riproduce il confronto lessicografico `(created_at, id) < (…)`
// della clausola di keyset.
func beforeJobCursor(job jobs.Job, cursor jobs.JobCursor) bool {
	if job.CreatedAt.Equal(cursor.CreatedAt) {
		return job.ID < cursor.ID
	}
	return job.CreatedAt.Before(cursor.CreatedAt)
}

// UpdateJob riscrive le colonne modificabili.
func (s *Store) UpdateJob(_ context.Context, job jobs.Job, resetNextRun bool) (jobs.Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.fail("UpdateJob"); err != nil {
		return jobs.Job{}, err
	}
	current, ok := s.jobs[job.ID]
	if !ok || current.UserID != job.UserID {
		return jobs.Job{}, jobs.ErrNotFound
	}
	for _, other := range s.jobs {
		if other.ID != job.ID && other.UserID == job.UserID &&
			other.RepositoryID == job.RepositoryID && other.Name == job.Name {
			return jobs.Job{}, jobs.ErrNameTaken
		}
	}
	job.CreatedAt = current.CreatedAt
	job.UpdatedAt = s.Now()
	// Come il `CASE` di jobspg: o si azzera, o si lascia dov'era. Il valore
	// portato dal job non arriva mai alla colonna.
	if resetNextRun {
		job.NextRunAt = nil
	} else {
		job.NextRunAt = current.NextRunAt
	}
	// Anche la sospensione di R58 è una colonna che il chiamante non scrive: si
	// scioglie quando il job torna acceso, e resta dov'era altrimenti. Riprodurre
	// qui il `CASE` di jobspg non è pedanteria — un doppio che lasciasse il job
	// riacceso *e* marcato come sospeso renderebbe verde un test che sul database
	// vero sarebbe rosso.
	if job.Enabled {
		job.SuspendedAt = nil
		job.SuspendedReason = ""
	} else {
		job.SuspendedAt = current.SuspendedAt
		job.SuspendedReason = current.SuspendedReason
	}
	s.jobs[job.ID] = job
	return job, nil
}

// DeleteJob elimina il job e, per cascata, le sue esecuzioni.
func (s *Store) DeleteJob(_ context.Context, userID, jobID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.fail("DeleteJob"); err != nil {
		return err
	}
	job, ok := s.jobs[jobID]
	if !ok || job.UserID != userID {
		return jobs.ErrNotFound
	}
	delete(s.jobs, jobID)
	for key := range s.execs {
		if key.jobID == jobID {
			delete(s.execs, key)
		}
	}
	return nil
}

// PlanForUser restituisce il piano dell'utente.
func (s *Store) PlanForUser(_ context.Context, userID string) (jobs.Plan, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.fail("PlanForUser"); err != nil {
		return jobs.Plan{}, err
	}
	if plan, ok := s.plans[userID]; ok {
		return plan, nil
	}
	return jobs.FreePlan, nil
}

// PlanByCode legge una riga del listino per codice.
//
// L'archivio conosce i piani che i test gli hanno dato con [Store.SetPlan] o
// [Store.SetPlanByCode], più `free`, che nel database esiste sempre. Un codice
// mai visto risponde [jobs.ErrNotFound], che è ciò che fa la lettura vera: è la
// condizione in cui la portata di R25-bis non è derivabile, e un doppio che
// restituisse un piano vuoto renderebbe verde proprio il test che deve
// verificare quel caso.
func (s *Store) PlanByCode(_ context.Context, code string) (jobs.Plan, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.fail("PlanByCode"); err != nil {
		return jobs.Plan{}, err
	}
	if plan, ok := s.byCode[code]; ok {
		return plan, nil
	}
	for _, plan := range s.plans {
		if plan.Code == code {
			return plan, nil
		}
	}
	if code == jobs.FreePlan.Code {
		return jobs.FreePlan, nil
	}
	return jobs.Plan{}, jobs.ErrNotFound
}

// SetPlanByCode aggiunge una riga al listino senza assegnarla a nessun utente.
// Serve ai piani che ne moltiplicano un altro (R25-bis): il piano di
// riferimento deve esistere anche se non lo sottoscrive nessuno.
func (s *Store) SetPlanByCode(plan jobs.Plan) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.byCode[plan.Code] = plan
}

// ------------------------------------------------------------- esecuzioni

// CreateExecution registra un tentativo, con la chiave naturale come lock.
func (s *Store) CreateExecution(_ context.Context, exec jobs.Execution) (jobs.Execution, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.fail("CreateExecution"); err != nil {
		return jobs.Execution{}, err
	}
	if s.PartitionMissing {
		return jobs.Execution{}, jobs.ErrPartitionMissing
	}
	if _, ok := s.jobs[exec.JobID]; !ok {
		return jobs.Execution{}, jobs.ErrNotFound
	}
	key := keyOf(exec)
	if _, taken := s.execs[key]; taken {
		return jobs.Execution{}, jobs.ErrExecutionExists
	}
	exec.CreatedAt = s.Now()
	s.execs[key] = exec
	return exec, nil
}

// ExecutionAt legge il tentativo su una chiave naturale.
func (s *Store) ExecutionAt(_ context.Context, jobID string, scheduledFor time.Time, env jobs.Environment, attempt int) (jobs.Execution, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.fail("ExecutionAt"); err != nil {
		return jobs.Execution{}, err
	}
	exec, ok := s.execs[execKey{jobID: jobID, scheduledFor: scheduledFor.UTC().UnixNano(), environment: env, attempt: attempt}]
	if !ok {
		return jobs.Execution{}, jobs.ErrNotFound
	}
	return exec, nil
}

// ListExecutions elenca i tentativi di un job, dal più recente.
func (s *Store) ListExecutions(_ context.Context, filter jobs.ExecutionFilter) ([]jobs.Execution, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.fail("ListExecutions"); err != nil {
		return nil, err
	}

	var out []jobs.Execution
	for _, exec := range s.execs {
		switch {
		case exec.JobID != filter.JobID:
		case filter.Environment != "" && exec.Environment != filter.Environment:
		case len(filter.Status) > 0 && !containsStatus(filter.Status, exec.Status):
		case len(filter.TriggeredBy) > 0 && !containsTrigger(filter.TriggeredBy, exec.TriggeredBy):
		case !filter.Since.IsZero() && exec.ScheduledFor.Before(filter.Since):
		case !filter.Until.IsZero() && !exec.ScheduledFor.Before(filter.Until):
		case filter.Cursor != nil && !beforeExecutionCursor(exec, *filter.Cursor):
		default:
			out = append(out, exec)
		}
	}
	sort.Slice(out, func(i, j int) bool { return executionAfter(out[i], out[j]) })
	if len(out) > filter.Limit+1 {
		out = out[:filter.Limit+1]
	}
	return out, nil
}

func executionAfter(a, b jobs.Execution) bool {
	if !a.ScheduledFor.Equal(b.ScheduledFor) {
		return a.ScheduledFor.After(b.ScheduledFor)
	}
	if a.Environment != b.Environment {
		return jobs.EnvironmentRank(a.Environment) > jobs.EnvironmentRank(b.Environment)
	}
	return a.Attempt > b.Attempt
}

func beforeExecutionCursor(exec jobs.Execution, cursor jobs.ExecutionCursor) bool {
	switch {
	case !exec.ScheduledFor.Equal(cursor.ScheduledFor):
		return exec.ScheduledFor.Before(cursor.ScheduledFor)
	case exec.Environment != cursor.Environment:
		return jobs.EnvironmentRank(exec.Environment) < jobs.EnvironmentRank(cursor.Environment)
	default:
		return exec.Attempt < cursor.Attempt
	}
}

func keyOf(exec jobs.Execution) execKey {
	return execKey{
		jobID:        exec.JobID,
		scheduledFor: exec.ScheduledFor.UTC().UnixNano(),
		environment:  exec.Environment,
		attempt:      exec.Attempt,
	}
}

func containsStatus(values []jobs.ExecutionStatus, v jobs.ExecutionStatus) bool {
	for _, item := range values {
		if item == v {
			return true
		}
	}
	return false
}

func containsTrigger(values []jobs.ExecutionTrigger, v jobs.ExecutionTrigger) bool {
	for _, item := range values {
		if item == v {
			return true
		}
	}
	return false
}

// Executions restituisce tutte le esecuzioni registrate, dalla più recente.
// Serve ai test che devono contare i trigger manuali.
func (s *Store) Executions() []jobs.Execution {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]jobs.Execution, 0, len(s.execs))
	for _, exec := range s.execs {
		out = append(out, exec)
	}
	sort.Slice(out, func(i, j int) bool { return executionAfter(out[i], out[j]) })
	return out
}

// Jobs restituisce tutti i job registrati, per nome.
func (s *Store) Jobs() []jobs.Job {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]jobs.Job, 0, len(s.jobs))
	for _, job := range s.jobs {
		out = append(out, job)
	}
	sort.Slice(out, func(i, j int) bool { return strings.Compare(out[i].Name, out[j].Name) < 0 })
	return out
}

var _ jobs.Store = (*Store)(nil)
