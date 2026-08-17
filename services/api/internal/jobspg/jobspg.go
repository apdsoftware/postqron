// Package jobspg è l'implementazione PostgreSQL di jobs.Store.
//
// Sta in un package a parte perché internal/jobs non deve dipendere da pgx: è
// ciò che permette di provare i limiti di piano, il rifiuto anticipato e il
// tetto al trigger manuale senza un database in piedi. Qui, viceversa, non c'è
// logica: solo le query, e i vincoli che le migrazioni 0005 e 0006 già
// garantiscono.
package jobspg

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/apdsoftware/postqron/services/api/internal/jobs"
)

// SQLSTATE che questo package distingue.
const (
	uniqueViolation = "23505"
	// checkViolation copre due cose diverse. Con un nome di vincolo è un CHECK
	// della tabella — cioè un difetto nostro, perché la validazione avrebbe
	// dovuto fermare la richiesta prima. **Senza** nome di vincolo è il
	// routing di partizione che non ha trovato dove mettere la riga: la
	// distinzione è quella, e non il testo del messaggio, che PostgreSQL
	// traduce secondo `lc_messages`.
	checkViolation = "23514"
	// foreignKeyViolation su `job_executions.job_id` significa che il job è
	// stato eliminato fra la lettura e l'inserimento.
	foreignKeyViolation = "23503"
	// invalidTextRepresentation è ciò che PostgreSQL risponde a un uuid
	// malformato. Un identificativo che non è un uuid non indirizza nessuna
	// riga: vale «non trovato», non un errore interno.
	invalidTextRepresentation = "22P02"
)

// Store implementa [jobs.Store] su un pool pgx.
type Store struct {
	pool *pgxpool.Pool
}

// New costruisce lo store.
func New(pool *pgxpool.Pool) (*Store, error) {
	if pool == nil {
		return nil, errors.New("jobspg: il pool è obbligatorio")
	}
	return &Store{pool: pool}, nil
}

var _ jobs.Store = (*Store)(nil)

// jobColumns è l'elenco delle colonne lette in ogni SELECT su `jobs`, nello
// stesso ordine in cui scanJob le legge. Sta in una costante perché quattro
// query devono restare allineate: una colonna aggiunta in una sola di esse è un
// errore che il compilatore non vede.
//
// Gli enum e gli array di enum sono proiettati a testo: senza il cast, pgx
// dovrebbe conoscere i tipi definiti dalla 0001, e conoscerli richiede o una
// registrazione all'apertura di ogni connessione o un round-trip di
// introspezione. Il cast costa niente e toglie la dipendenza.
const jobColumns = `id::text, user_id::text, coalesce(repository_id::text, ''),
	name, coalesce(description, ''),
	schedule, every_seconds, timezone, environments::text[],
	url, method::text, headers, coalesce(body, ''),
	timeout_seconds, max_retries, retry_backoff::text, alert_on_failure::text[],
	enabled, archived_at, next_run_at, created_at, updated_at`

const executionColumns = `job_id::text, scheduled_for, environment::text, attempt,
	status::text, triggered_by::text,
	started_at, finished_at, duration_ms,
	response_status, coalesce(response_excerpt, ''), coalesce(error, ''), created_at`

// ---------------------------------------------------------------------- job

// querier è ciò che il pool e una transazione hanno in comune.
type querier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// CreateJob inserisce un job applicando il tetto di piano.
//
// # Perché serve una transazione e un lock
//
// La forma ovvia — contare i job e poi inserire — è una corsa: due creazioni
// simultanee al ventesimo job passano entrambe il controllo, e il piano Free ne
// ospita ventuno. La forma quasi-ovvia — mettere il `count(*)` dentro la
// clausola WHERE dell'INSERT — **non la risolve**, e vale la pena dire perché,
// perché sembra che lo faccia: in READ COMMITTED lo snapshot è preso all'inizio
// di ogni istruzione, quindi due istruzioni avviate insieme contano entrambe lo
// stato precedente e inseriscono entrambe. È esattamente ciò che
// TestIlTettoDiPianoEApplicatoDentroLInserimento ha mostrato.
//
// Ciò che funziona è serializzare le creazioni **dello stesso utente**: un lock
// consultivo di transazione sulla chiave dell'utente, e poi il conteggio. Il
// conteggio è un'istruzione a sé, avviata dopo l'acquisizione del lock, e in
// READ COMMITTED prende quindi uno snapshot che include l'inserimento di chi ha
// tenuto il lock prima. Utenti diversi non si aspettano fra loro, e i piani
// senza tetto rigido non passano nemmeno dal lock.
func (s *Store) CreateJob(ctx context.Context, job jobs.Job, maxJobs *int) (jobs.Job, error) {
	headers, err := json.Marshal(nonNilHeaders(job.Headers))
	if err != nil {
		return jobs.Job{}, fmt.Errorf("jobspg: serializzazione degli header: %w", err)
	}

	if maxJobs == nil {
		// Nessun tetto da difendere: un giro in meno, e nessuna serializzazione
		// per i piani venduti come illimitati, che sono quelli con più job.
		return s.insertJob(ctx, s.pool, job, headers)
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return jobs.Job{}, annotate("apertura della transazione", err)
	}
	// Il rollback dopo un commit riuscito è un'operazione senza effetto: serve
	// solo a chiudere i percorsi d'uscita anticipata.
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, advisoryKey(job.UserID)); err != nil {
		return jobs.Job{}, annotate("lock sulla creazione del job", err)
	}

	var current int
	err = tx.QueryRow(ctx,
		`SELECT count(*) FROM jobs WHERE user_id = $1::uuid AND archived_at IS NULL`,
		job.UserID).Scan(&current)
	if err != nil {
		return jobs.Job{}, annotate("conteggio dei job", err)
	}
	if current >= *maxJobs {
		return jobs.Job{}, jobs.ErrJobLimitReached
	}

	created, err := s.insertJob(ctx, tx, job, headers)
	if err != nil {
		return jobs.Job{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return jobs.Job{}, annotate("commit della creazione del job", err)
	}
	return created, nil
}

func (s *Store) insertJob(ctx context.Context, db querier, job jobs.Job, headers []byte) (jobs.Job, error) {
	row := db.QueryRow(ctx,
		`INSERT INTO jobs (
			user_id, name, description, schedule, every_seconds, timezone,
			environments, url, method, headers, body, timeout_seconds,
			max_retries, retry_backoff, alert_on_failure, enabled, next_run_at)
		 VALUES ($1::uuid, $2, $3, $4, $5, $6,
		         $7::text[]::environment[], $8, $9::http_method, $10::jsonb, $11, $12,
		         $13, $14::retry_backoff, $15::text[]::alert_channel[], $16, $17)
		 RETURNING `+jobColumns,
		job.UserID, job.Name, nullable(job.Description),
		nullable(job.Schedule), everySeconds(job.Every), job.Timezone,
		stringsOf(job.Environments), job.URL, string(job.Method), headers, nullable(job.Body),
		int32(job.Timeout/time.Second),
		int16(job.MaxRetries), string(job.RetryBackoff), stringsOf(job.AlertOnFailure),
		job.Enabled, job.NextRunAt)

	created, err := scanJob(row)
	if err == nil {
		return created, nil
	}

	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == uniqueViolation {
		return jobs.Job{}, jobs.ErrNameTaken
	}
	return jobs.Job{}, notFoundOnMalformedID(annotate("inserimento del job", err))
}

// advisoryKey trasforma l'identificativo dell'utente nella chiave del lock
// consultivo.
//
// Due utenti diversi possono in teoria collidere sulla stessa chiave: l'effetto
// è che le loro creazioni si aspettano a vicenda per il tempo di un inserimento,
// il che è irrilevante. Ciò che conta è il contrario — che lo stesso utente
// produca sempre la stessa chiave — ed è garantito.
func advisoryKey(userID string) int64 {
	h := fnv.New64a()
	// L'hash non fallisce mai: Write su fnv restituisce sempre nil.
	_, _ = h.Write([]byte("postqron:jobs:create:" + userID))
	return int64(h.Sum64())
}

// JobByID legge un job dell'utente.
func (s *Store) JobByID(ctx context.Context, userID, jobID string) (jobs.Job, error) {
	row := s.pool.QueryRow(ctx,
		`SELECT `+jobColumns+`
		   FROM jobs
		  WHERE id = $2::uuid AND user_id = $1::uuid`, userID, jobID)
	job, err := scanJob(row)
	if err != nil {
		return jobs.Job{}, notFoundOnMalformedID(err)
	}
	return job, nil
}

// CountJobs conta i job non archiviati dell'utente.
func (s *Store) CountJobs(ctx context.Context, userID string) (int, error) {
	var count int
	err := s.pool.QueryRow(ctx,
		`SELECT count(*) FROM jobs WHERE user_id = $1::uuid AND archived_at IS NULL`,
		userID).Scan(&count)
	if err != nil {
		return 0, annotate("conteggio dei job", err)
	}
	return count, nil
}

// ListJobs elenca i job secondo il filtro.
//
// La paginazione è a chiave e non a offset: il confronto di tupla
// `(created_at, id) < ($cursor)` è ciò che permette di riprendere esattamente da
// dove si era arrivati anche mentre l'elenco cambia sotto. L'ordinamento
// combacia con `jobs_user_id_idx`, che è (user_id, created_at DESC).
func (s *Store) ListJobs(ctx context.Context, filter jobs.JobFilter) ([]jobs.Job, error) {
	var cursorCreatedAt *time.Time
	var cursorID *string
	if filter.Cursor != nil {
		createdAt := filter.Cursor.CreatedAt
		id := filter.Cursor.ID
		cursorCreatedAt, cursorID = &createdAt, &id
	}

	rows, err := s.pool.Query(ctx,
		`SELECT `+jobColumns+`
		   FROM jobs
		  WHERE user_id = $1::uuid
		    AND ($2::boolean OR archived_at IS NULL)
		    AND ($3::boolean IS NULL OR enabled = $3::boolean)
		    AND ($4::text IS NULL OR environments @> ARRAY[$4::text::environment])
		    AND ($5::timestamptz IS NULL OR (created_at, id) < ($5::timestamptz, $6::uuid))
		  ORDER BY created_at DESC, id DESC
		  LIMIT $7`,
		filter.UserID, filter.IncludeArchived, filter.Enabled,
		nullableEnvironment(filter.Environment), cursorCreatedAt, cursorID,
		// Una riga in più della pagina: è così che il chiamante sa che ce n'è
		// un'altra senza pagare un `count(*)`.
		filter.Limit+1)
	if err != nil {
		return nil, notFoundOnMalformedID(annotate("elenco dei job", err))
	}
	defer rows.Close()

	var out []jobs.Job
	for rows.Next() {
		job, err := scanJob(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, job)
	}
	return out, annotate("elenco dei job", rows.Err())
}

// UpdateJob riscrive le colonne modificabili di un job.
//
// `next_run_at` è l'eccezione: o la si azzera, o la si lascia dov'è. Il `CASE`
// nella SET evita di riscriverla con il valore letto poco prima, che sarebbe un
// aggiornamento perso ai danni dello scheduler — la colonna è sua (0005, 0010) e
// lui la fa avanzare a ogni passata.
func (s *Store) UpdateJob(ctx context.Context, job jobs.Job, resetNextRun bool) (jobs.Job, error) {
	headers, err := json.Marshal(nonNilHeaders(job.Headers))
	if err != nil {
		return jobs.Job{}, fmt.Errorf("jobspg: serializzazione degli header: %w", err)
	}

	row := s.pool.QueryRow(ctx,
		`UPDATE jobs SET
		    name = $3, description = $4, schedule = $5, every_seconds = $6, timezone = $7,
		    environments = $8::text[]::environment[], url = $9, method = $10::http_method,
		    headers = $11::jsonb, body = $12, timeout_seconds = $13,
		    max_retries = $14, retry_backoff = $15::retry_backoff,
		    alert_on_failure = $16::text[]::alert_channel[], enabled = $17,
		    next_run_at = CASE WHEN $18::boolean THEN NULL ELSE jobs.next_run_at END
		  WHERE id = $2::uuid AND user_id = $1::uuid
		 RETURNING `+jobColumns,
		job.UserID, job.ID, job.Name, nullable(job.Description),
		nullable(job.Schedule), everySeconds(job.Every), job.Timezone,
		stringsOf(job.Environments), job.URL, string(job.Method), headers, nullable(job.Body),
		int32(job.Timeout/time.Second),
		int16(job.MaxRetries), string(job.RetryBackoff), stringsOf(job.AlertOnFailure),
		job.Enabled, resetNextRun)

	updated, err := scanJob(row)
	if err == nil {
		return updated, nil
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == uniqueViolation {
		return jobs.Job{}, jobs.ErrNameTaken
	}
	return jobs.Job{}, notFoundOnMalformedID(annotate("aggiornamento del job", err))
}

// DeleteJob elimina un job dell'utente.
func (s *Store) DeleteJob(ctx context.Context, userID, jobID string) error {
	tag, err := s.pool.Exec(ctx,
		`DELETE FROM jobs WHERE id = $2::uuid AND user_id = $1::uuid`, userID, jobID)
	if err != nil {
		return notFoundOnMalformedID(annotate("eliminazione del job", err))
	}
	if tag.RowsAffected() == 0 {
		return jobs.ErrNotFound
	}
	return nil
}

// PlanForUser restituisce il piano dell'utente (R15).
//
// Il piano è quello della sottoscrizione non annullata, che l'indice parziale
// `subscriptions_one_live_per_user_idx` garantisce essere al massimo una. Chi
// non ne ha ricade su `free`: è il caso di ogni account appena registrato,
// perché la registrazione (#396) non crea una riga in `subscriptions`.
//
// Uno stato `past_due` o `paused` continua a dare gli entitlement del proprio
// piano. Cosa debba succedere al mancato pagamento è R58 e appartiene al
// billing: qui va deciso comunque qualcosa, e togliere i job a un cliente per un
// pagamento in ritardo di un'ora sarebbe la scelta peggiore delle due.
func (s *Store) PlanForUser(ctx context.Context, userID string) (jobs.Plan, error) {
	row := s.pool.QueryRow(ctx,
		`SELECT `+planColumns+`
		   FROM plans
		  WHERE code = coalesce(
		            (SELECT plan_code FROM subscriptions
		              WHERE user_id = $1::uuid AND status <> 'canceled'
		              LIMIT 1),
		            'free')`, userID)

	plan, err := scanPlan(row)
	if errors.Is(err, pgx.ErrNoRows) {
		// La 0003 inserisce i quattro piani: se `free` non c'è, il database non è
		// migrato. Dirlo è meglio che far girare l'API con limiti inventati.
		return jobs.Plan{}, errors.New("jobspg: nessun piano trovato: la migrazione 0003 non è stata applicata")
	}
	if err != nil {
		return jobs.Plan{}, annotate("lettura del piano", err)
	}
	return plan, nil
}

// PlanByCode legge una riga del listino per codice.
//
// A differenza di [Store.PlanForUser] non passa dalle sottoscrizioni: serve alla
// portata dei piani che ne moltiplicano un altro (R25-bis), dove il piano da
// leggere non è quello di nessun utente in particolare.
func (s *Store) PlanByCode(ctx context.Context, code string) (jobs.Plan, error) {
	row := s.pool.QueryRow(ctx, `SELECT `+planColumns+` FROM plans WHERE code = $1`, code)

	plan, err := scanPlan(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return jobs.Plan{}, jobs.ErrNotFound
	}
	if err != nil {
		return jobs.Plan{}, annotate("lettura del piano", err)
	}
	return plan, nil
}

// planColumns è il sottoinsieme della matrice di SPEC §8 che l'API applica.
// L'elenco è uno solo perché le due letture devono restituire la stessa forma:
// due elenchi divergerebbero alla prima colonna aggiunta, e la differenza si
// noterebbe come un limite che si applica a un utente e non a un altro.
const planColumns = `code, name, max_jobs, fair_use_jobs, min_interval_seconds,
	        log_retention_days, environments_enabled, multi_workspace_enabled`

func scanPlan(row pgx.Row) (jobs.Plan, error) {
	var plan jobs.Plan
	var minInterval, retentionDays int32
	if err := row.Scan(&plan.Code, &plan.Name, &plan.MaxJobs, &plan.FairUseJobs,
		&minInterval, &retentionDays, &plan.EnvironmentsEnabled, &plan.MultiWorkspace); err != nil {
		return jobs.Plan{}, err
	}
	plan.MinInterval = time.Duration(minInterval) * time.Second
	plan.LogRetention = time.Duration(retentionDays) * 24 * time.Hour
	return plan, nil
}

// -------------------------------------------------------------- esecuzioni

// CreateExecution registra un tentativo.
//
// L'inserimento è tentato e non preceduto da una SELECT di controllo: la chiave
// primaria di `job_executions` è il lock di idempotenza (R4, migrazione 0006) e
// una verifica prima dell'inserimento riaprirebbe proprio la finestra che quella
// chiave chiude.
func (s *Store) CreateExecution(ctx context.Context, exec jobs.Execution) (jobs.Execution, error) {
	row := s.pool.QueryRow(ctx,
		`INSERT INTO job_executions (job_id, scheduled_for, environment, attempt, status, triggered_by)
		 VALUES ($1::uuid, $2, $3::text::environment, $4, $5::text::execution_status, $6::text::execution_trigger)
		 RETURNING `+executionColumns,
		exec.JobID, exec.ScheduledFor.UTC(), string(exec.Environment), int16(exec.Attempt),
		string(exec.Status), string(exec.TriggeredBy))

	created, err := scanExecution(row)
	if err == nil {
		return created, nil
	}

	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch {
		case pgErr.Code == uniqueViolation:
			return jobs.Execution{}, jobs.ErrExecutionExists
		case pgErr.Code == checkViolation && pgErr.ConstraintName == "":
			// Nessun vincolo nominato: è il routing di partizione che non ha
			// trovato dove mettere la riga. Vedi `job_executions_ensure_partitions`
			// nella 0006 — è deliberato che fallisca invece di finire in una
			// partizione di default.
			return jobs.Execution{}, fmt.Errorf("%w: %s", jobs.ErrPartitionMissing, pgErr.Message)
		case pgErr.Code == foreignKeyViolation:
			return jobs.Execution{}, jobs.ErrNotFound
		}
	}
	return jobs.Execution{}, notFoundOnMalformedID(annotate("inserimento dell'esecuzione", err))
}

// ExecutionAt legge il tentativo su una chiave naturale.
func (s *Store) ExecutionAt(ctx context.Context, jobID string, scheduledFor time.Time, env jobs.Environment, attempt int) (jobs.Execution, error) {
	row := s.pool.QueryRow(ctx,
		`SELECT `+executionColumns+`
		   FROM job_executions
		  WHERE job_id = $1::uuid AND scheduled_for = $2
		    AND environment = $3::text::environment AND attempt = $4`,
		jobID, scheduledFor.UTC(), string(env), int16(attempt))
	exec, err := scanExecution(row)
	if err != nil {
		return jobs.Execution{}, notFoundOnMalformedID(err)
	}
	return exec, nil
}

// ListExecutions elenca i tentativi di un job.
//
// L'ordinamento e il cursore seguono la chiave primaria (scheduled_for,
// environment, attempt): è una lettura all'indietro dell'indice che esiste già,
// senza ordinamenti aggiuntivi. Con 86.400 righe al giorno per job, è la
// differenza fra una query costante e una che peggiora ogni giorno.
func (s *Store) ListExecutions(ctx context.Context, filter jobs.ExecutionFilter) ([]jobs.Execution, error) {
	var cursorScheduledFor *time.Time
	var cursorEnvironment *string
	var cursorAttempt *int16
	if filter.Cursor != nil {
		scheduledFor := filter.Cursor.ScheduledFor
		environment := string(filter.Cursor.Environment)
		attempt := int16(filter.Cursor.Attempt)
		cursorScheduledFor, cursorEnvironment, cursorAttempt = &scheduledFor, &environment, &attempt
	}

	rows, err := s.pool.Query(ctx,
		`SELECT `+executionColumns+`
		   FROM job_executions
		  WHERE job_id = $1::uuid
		    AND ($2::text[] IS NULL OR status = ANY($2::text[]::execution_status[]))
		    AND ($3::text IS NULL OR environment = $3::text::environment)
		    AND ($4::text[] IS NULL OR triggered_by = ANY($4::text[]::execution_trigger[]))
		    AND ($5::timestamptz IS NULL OR scheduled_for >= $5::timestamptz)
		    AND ($6::timestamptz IS NULL OR scheduled_for < $6::timestamptz)
		    AND ($7::timestamptz IS NULL
		         OR (scheduled_for, environment, attempt)
		            < ($7::timestamptz, $8::text::environment, $9::smallint))
		  ORDER BY scheduled_for DESC, environment DESC, attempt DESC
		  LIMIT $10`,
		filter.JobID,
		nullableList(stringsOf(filter.Status)),
		nullableEnvironment(filter.Environment),
		nullableList(stringsOf(filter.TriggeredBy)),
		nullableTime(filter.Since), nullableTime(filter.Until),
		cursorScheduledFor, cursorEnvironment, cursorAttempt,
		filter.Limit+1)
	if err != nil {
		return nil, notFoundOnMalformedID(annotate("elenco delle esecuzioni", err))
	}
	defer rows.Close()

	var out []jobs.Execution
	for rows.Next() {
		exec, err := scanExecution(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, exec)
	}
	return out, annotate("elenco delle esecuzioni", rows.Err())
}

// ------------------------------------------------------------------ scanner

// scanner è ciò che pgx.Row e pgx.Rows hanno in comune.
type scanner interface {
	Scan(dest ...any) error
}

func scanJob(row scanner) (jobs.Job, error) {
	var (
		job          jobs.Job
		schedule     *string
		every        *int32
		environments []string
		headers      []byte
		timeout      int32
		maxRetries   int16
		backoff      string
		method       string
		channels     []string
	)
	err := row.Scan(
		&job.ID, &job.UserID, &job.RepositoryID,
		&job.Name, &job.Description,
		&schedule, &every, &job.Timezone, &environments,
		&job.URL, &method, &headers, &job.Body,
		&timeout, &maxRetries, &backoff, &channels,
		&job.Enabled, &job.ArchivedAt, &job.NextRunAt, &job.CreatedAt, &job.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return jobs.Job{}, jobs.ErrNotFound
	}
	if err != nil {
		return jobs.Job{}, err
	}

	if schedule != nil {
		job.Schedule = *schedule
	}
	if every != nil {
		job.Every = time.Duration(*every) * time.Second
	}
	job.Method = jobs.Method(method)
	job.RetryBackoff = jobs.Backoff(backoff)
	job.Timeout = time.Duration(timeout) * time.Second
	job.MaxRetries = int(maxRetries)

	job.Environments = make([]jobs.Environment, 0, len(environments))
	for _, env := range environments {
		job.Environments = append(job.Environments, jobs.Environment(env))
	}
	job.AlertOnFailure = make([]jobs.AlertChannel, 0, len(channels))
	for _, channel := range channels {
		job.AlertOnFailure = append(job.AlertOnFailure, jobs.AlertChannel(channel))
	}

	job.Headers = map[string]string{}
	if len(headers) > 0 {
		if err := json.Unmarshal(headers, &job.Headers); err != nil {
			return jobs.Job{}, fmt.Errorf("jobspg: header del job %s illeggibili: %w", job.ID, err)
		}
	}
	return job, nil
}

func scanExecution(row scanner) (jobs.Execution, error) {
	var (
		exec           jobs.Execution
		environment    string
		attempt        int16
		status         string
		trigger        string
		durationMS     *int32
		responseStatus *int16
	)
	err := row.Scan(
		&exec.JobID, &exec.ScheduledFor, &environment, &attempt,
		&status, &trigger,
		&exec.StartedAt, &exec.FinishedAt, &durationMS,
		&responseStatus, &exec.ResponseExcerpt, &exec.Error, &exec.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return jobs.Execution{}, jobs.ErrNotFound
	}
	if err != nil {
		return jobs.Execution{}, err
	}

	exec.Environment = jobs.Environment(environment)
	exec.Attempt = int(attempt)
	exec.Status = jobs.ExecutionStatus(status)
	exec.TriggeredBy = jobs.ExecutionTrigger(trigger)
	if durationMS != nil {
		value := int(*durationMS)
		exec.DurationMS = &value
	}
	if responseStatus != nil {
		value := int(*responseStatus)
		exec.ResponseStatus = &value
	}
	return exec, nil
}

// ------------------------------------------------------------------ supporto

// nullable manda NULL invece della stringa vuota, che è la forma con cui le
// colonne facoltative della 0005 rappresentano «non c'è».
func nullable(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

func nullableEnvironment(env jobs.Environment) *string {
	if env == "" {
		return nil
	}
	value := string(env)
	return &value
}

func nullableList(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	return values
}

func nullableTime(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	value := t.UTC()
	return &value
}

// everySeconds traduce l'intervallo nella colonna `every_seconds`, che è NULL
// quando il job usa la modalità cron — l'altra metà del vincolo XOR.
func everySeconds(every time.Duration) *int32 {
	if every <= 0 {
		return nil
	}
	value := int32(every / time.Second)
	return &value
}

func nonNilHeaders(headers map[string]string) map[string]string {
	if headers == nil {
		return map[string]string{}
	}
	return headers
}

func stringsOf[T ~string](values []T) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		out = append(out, string(value))
	}
	return out
}

// notFoundOnMalformedID traduce un uuid illeggibile in «non trovato».
//
// Un identificativo che non è un uuid arriva da un client che se l'è inventato:
// rispondere 500 darebbe la colpa al servizio, e rispondere 400 direbbe che la
// forma dell'identificativo è verificabile dall'esterno — che è un dettaglio in
// più di quanti ne servano.
func notFoundOnMalformedID(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == invalidTextRepresentation {
		return jobs.ErrNotFound
	}
	return err
}

// annotate aggiunge il contesto dell'operazione senza perdere l'errore
// originale, che è ciò su cui i chiamanti fanno errors.As.
func annotate(operation string, err error) error {
	if err == nil {
		return nil
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == checkViolation && pgErr.ConstraintName != "" {
		// Un CHECK violato è un difetto della validazione: l'API ha accettato
		// qualcosa che il database rifiuta. Nominare il vincolo nel log è ciò
		// che rende il difetto diagnosticabile invece che misterioso.
		return fmt.Errorf("jobspg: %s rifiutato dal vincolo %s (la validazione dell'API avrebbe dovuto fermarlo): %w",
			operation, pgErr.ConstraintName, err)
	}
	return fmt.Errorf("jobspg: %s: %w", operation, err)
}
