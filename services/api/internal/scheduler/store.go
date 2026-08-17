package scheduler

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// uniqueViolation è lo SQLSTATE di una violazione di vincolo unico: è così, e
// **solo** così, che si riconosce il conflitto di idempotenza di R4.
//
// Il nome del vincolo non si guarda. `job_executions` è partizionata per giorno
// e il vincolo che viene riportato è quello della partizione —
// `job_executions_20260817_pkey`, non `job_executions_pkey`. Un confronto con
// una costante passerebbe i test il giorno in cui è stato scritto e sbaglierebbe
// tutti gli altri: il caso è documentato in db/migrations/README.md e provato da
// TestConflittoDiIdempotenzaPortaIlNomeDellaPartizione.
const uniqueViolation = "23505"

// checkViolation è lo SQLSTATE con cui PostgreSQL segnala, fra le altre cose,
// una riga che non trova la propria partizione. Vale la pena distinguerlo: il
// messaggio del driver è chiaro solo a chi sa che la tabella è partizionata.
const checkViolation = "23514"

// querier è ciò che serve a queste query: lo soddisfano sia *pgxpool.Pool sia
// pgx.Tx, che è quanto basta a usare le stesse funzioni dentro e fuori
// transazione.
type querier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// jobColumns sono le colonne di `jobs` lette da ogni query, nell'ordine in cui
// scanJob le legge. In una costante perché tre query devono restare allineate:
// una colonna aggiunta a una sola delle tre è un errore che il compilatore non
// vede.
const jobColumns = `j.id::text, j.user_id::text, j.name,
	coalesce(j.schedule, ''), coalesce(j.every_seconds, 0), j.timezone,
	j.environments::text[], j.url, j.method::text, j.headers, j.body,
	j.timeout_seconds, j.max_retries, j.retry_backoff::text,
	j.overlap_policy::text, j.enabled`

// scanJob legge le colonne di jobColumns nell'ordine in cui sono dichiarate.
// `extra` riceve le colonne che la singola query aggiunge in coda.
func scanJob(row pgx.Row, extra ...any) (Job, error) {
	var (
		job          Job
		everySeconds int32
		timeout      int32
		retries      int16
		overlap      string
	)
	dest := []any{
		&job.ID, &job.UserID, &job.Name,
		&job.Expression, &everySeconds, &job.Timezone,
		&job.Environments, &job.URL, &job.Method, &job.Headers, &job.Body,
		&timeout, &retries, &job.RetryBackoff, &overlap, &job.Enabled,
	}
	dest = append(dest, extra...)

	if err := row.Scan(dest...); err != nil {
		return Job{}, err
	}
	job.Every = time.Duration(everySeconds) * time.Second
	job.Timeout = time.Duration(timeout) * time.Second
	job.MaxRetries = int(retries)
	job.Overlap = OverlapPolicy(overlap).Or()
	return job, nil
}

// ------------------------------------------------------------------ job dovuti

// dueJobsSQL è la query calda del dispatch, quella che gira in continuazione.
//
// È scritta per combaciare con `jobs_due_idx` (migrazione 0005), che è parziale
// su `(next_run_at) WHERE enabled AND archived_at IS NULL AND next_run_at IS NOT
// NULL`: le tre condizioni dell'indice compaiono tutte, `next_run_at IS NOT
// NULL` compresa, che pure sarebbe implicata dal confronto. Scriverla rende la
// combaciata verificabile a occhio, e TestPianoDellaQueryCalda la verifica sul
// piano vero invece che sull'esistenza dell'indice.
//
// `FOR UPDATE SKIP LOCKED` è ciò che permette a più repliche del motore di
// lavorare sullo stesso database senza contendersi gli stessi job: chi arriva
// secondo salta le righe già prese invece di aspettarle.
const dueJobsSQL = `
	SELECT ` + jobColumns + `, j.next_run_at
	  FROM jobs j
	 WHERE j.enabled
	   AND j.archived_at IS NULL
	   AND j.next_run_at IS NOT NULL
	   AND j.next_run_at <= $1
	 ORDER BY j.next_run_at
	   FOR UPDATE SKIP LOCKED
	 LIMIT $2`

// dueJob è un job dovuto con l'istante da cui riprendere.
type dueJob struct {
	Job    Job
	Cursor time.Time
}

func selectDueJobs(ctx context.Context, q querier, now time.Time, limit int) ([]dueJob, error) {
	rows, err := q.Query(ctx, dueJobsSQL, now, limit)
	if err != nil {
		return nil, fmt.Errorf("scheduler: lettura dei job dovuti: %w", err)
	}
	defer rows.Close()

	var out []dueJob
	for rows.Next() {
		var cursor time.Time
		job, err := scanJob(rows, &cursor)
		if err != nil {
			return nil, fmt.Errorf("scheduler: lettura di un job dovuto: %w", err)
		}
		out = append(out, dueJob{Job: job, Cursor: cursor})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("scheduler: lettura dei job dovuti: %w", err)
	}
	return out, nil
}

// unscheduledJobsSQL prende i job abilitati che non hanno ancora una prossima
// occorrenza.
//
// Sono i job appena creati: `jobs.next_run_at` è «calcolato dallo scheduler»
// (commento della colonna, 0005), quindi anche il *primo* valore è compito di
// questo motore, non di chi scrive il job. Senza questa passata un job creato da
// API o da `cron.yaml` non partirebbe mai, e non ci sarebbe niente da vedere: la
// query calda non lo troverebbe perché il suo indice esclude proprio i job senza
// prossima occorrenza.
//
// L'indice dedicato è `jobs_unscheduled_idx` (migrazione 0010). A regime
// contiene zero righe — un job resta senza prossima occorrenza per una frazione
// di secondo — ma senza di esso questa query sarebbe una scansione completa di
// `jobs` quattro volte al secondo.
const unscheduledJobsSQL = `
	SELECT ` + jobColumns + `
	  FROM jobs j
	 WHERE j.enabled
	   AND j.archived_at IS NULL
	   AND j.next_run_at IS NULL
	 ORDER BY j.created_at
	   FOR UPDATE SKIP LOCKED
	 LIMIT $1`

func selectUnscheduledJobs(ctx context.Context, q querier, limit int) ([]Job, error) {
	rows, err := q.Query(ctx, unscheduledJobsSQL, limit)
	if err != nil {
		return nil, fmt.Errorf("scheduler: lettura dei job senza prossima occorrenza: %w", err)
	}
	defer rows.Close()

	var out []Job
	for rows.Next() {
		job, err := scanJob(rows)
		if err != nil {
			return nil, fmt.Errorf("scheduler: lettura di un job senza prossima occorrenza: %w", err)
		}
		out = append(out, job)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("scheduler: lettura dei job senza prossima occorrenza: %w", err)
	}
	return out, nil
}

// updateNextRunSQL riscrive la prossima occorrenza di più job in un colpo solo.
// Un elemento NULL nell'array degli istanti significa «questo job non ha più
// occorrenze» e lo toglie dall'indice del dispatch.
//
// Nota: il trigger `jobs_set_updated_at` (0005) sposta `updated_at` a ogni
// aggiornamento, quindi anche a ogni avanzamento della prossima occorrenza. È un
// effetto della schedulazione sul campo «modificato il», non un errore di questa
// query: cambiarlo vorrebbe dire toccare un trigger su cui altre issue si
// appoggiano.
const updateNextRunSQL = `
	UPDATE jobs
	   SET next_run_at = u.next_run_at
	  FROM unnest($1::text[], $2::timestamptz[]) AS u(id, next_run_at)
	 WHERE jobs.id = u.id::uuid`

func updateNextRuns(ctx context.Context, q querier, ids []string, next []*time.Time) error {
	if len(ids) == 0 {
		return nil
	}
	if _, err := q.Exec(ctx, updateNextRunSQL, ids, next); err != nil {
		return fmt.Errorf("scheduler: aggiornamento della prossima occorrenza: %w", err)
	}
	return nil
}

// --------------------------------------------------------------- occorrenze

// insertOccurrencesSQL inserisce un lotto di occorrenze con un solo statement.
//
// `attempt` resta al default 1: i tentativi successivi sono dei retry e non
// nascono qui (R5, issue #392). `status` e `triggered_by` sono scritti per
// esteso anche se coincidono con i default, perché sono la parte semantica —
// `pending` significa «presa, non ancora partita», ed è lo stato su cui si
// appoggiano sia il recupero dopo un riavvio sia il cancello dell'esecutore.
const insertOccurrencesSQL = `
	INSERT INTO job_executions (job_id, scheduled_for, environment, status, triggered_by)
	SELECT t.job_id::uuid, t.scheduled_for, t.environment::environment, 'pending', 'schedule'
	  FROM unnest($1::text[], $2::timestamptz[], $3::text[])
	    AS t(job_id, scheduled_for, environment)`

// insertOccurrenceSQL inserisce una singola occorrenza. È il percorso lento, e
// serve a sapere *quale* occorrenza di un lotto era già presa.
const insertOccurrenceSQL = `
	INSERT INTO job_executions (job_id, scheduled_for, environment, status, triggered_by)
	VALUES ($1::uuid, $2, $3::environment, 'pending', 'schedule')`

func insertOccurrenceBatch(ctx context.Context, q querier, occs []Occurrence) error {
	ids := make([]string, len(occs))
	instants := make([]time.Time, len(occs))
	envs := make([]string, len(occs))
	for i, occ := range occs {
		ids[i] = occ.Job.ID
		instants[i] = occ.ScheduledFor
		envs[i] = occ.Environment
	}
	_, err := q.Exec(ctx, insertOccurrencesSQL, ids, instants, envs)
	return err
}

func insertOccurrence(ctx context.Context, q querier, occ Occurrence) error {
	_, err := q.Exec(ctx, insertOccurrenceSQL, occ.Job.ID, occ.ScheduledFor, occ.Environment)
	return err
}

// ------------------------------------------------------------------ recupero

// pendingOccurrencesSQL ritrova le occorrenze prese e mai partite.
//
// È il percorso di recupero dopo un riavvio, ed è il motivo per cui esiste
// `job_executions_in_flight_idx` (0006): un indice parziale sulle sole righe in
// volo, che resta minuscolo qualunque sia la dimensione della tabella.
//
// I due filtri temporali fanno cose diverse e servono entrambi:
//
//   - su `scheduled_for` (la chiave di partizionamento) delimitano quali
//     partizioni toccare — senza, la ricerca aprirebbe l'indice di ogni giorno
//     ancora vivo;
//   - su `created_at` decidono *quando* una riga si considera abbandonata. È la
//     colonna giusta: `scheduled_for` di un'occorrenza recuperata è vecchio per
//     costruzione, e usarlo qui rioffrirebbe al dispatch righe appena consegnate.
//
// Il filtro su `triggered_by` traccia il confine con le altre issue: lo scheduler
// riprende ciò che lo scheduler ha creato. Un trigger manuale (R8) e un retry
// (R5, #392) nascono altrove, hanno un padrone che sa quando riproporli, e non
// vanno raccolti da qui solo perché si trovano nello stesso stato.
const pendingOccurrencesSQL = `
	SELECT ` + jobColumns + `, e.scheduled_for, e.environment::text, e.attempt
	  FROM job_executions e
	  JOIN jobs j ON j.id = e.job_id
	 WHERE e.status = 'pending'
	   AND e.triggered_by = 'schedule'
	   AND e.scheduled_for >= $1
	   AND e.scheduled_for <= $2
	   AND e.created_at <= $3
	 ORDER BY e.scheduled_for
	 LIMIT $4`

func selectPendingOccurrences(ctx context.Context, q querier, from, to, createdBefore time.Time, limit int) ([]Occurrence, error) {
	rows, err := q.Query(ctx, pendingOccurrencesSQL, from, to, createdBefore, limit)
	if err != nil {
		return nil, fmt.Errorf("scheduler: lettura delle occorrenze in sospeso: %w", err)
	}
	defer rows.Close()

	var out []Occurrence
	for rows.Next() {
		occ := Occurrence{Recovered: true}
		job, err := scanJob(rows, &occ.ScheduledFor, &occ.Environment, &occ.Attempt)
		if err != nil {
			return nil, fmt.Errorf("scheduler: lettura di un'occorrenza in sospeso: %w", err)
		}
		occ.Job = job
		out = append(out, occ)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("scheduler: lettura delle occorrenze in sospeso: %w", err)
	}
	return out, nil
}

// expireOccurrencesSQL chiude le occorrenze prese, mai partite e ormai fuori
// dalla finestra di recupero.
//
// `skipped` è lo stato previsto per «l'occorrenza non è mai partita per
// decisione del motore» (0001), ed è la decisione presa qui. Senza questo passo
// quelle righe resterebbero `pending` per sempre: invisibili all'utente, che
// vedrebbe un'esecuzione eternamente in attesa, e dentro
// `job_executions_in_flight_idx`, che è l'indice su cui si appoggia il recupero
// e che deve restare piccolo per continuare a funzionare.
//
// Come per il recupero, si chiude solo ciò che lo scheduler ha aperto: un
// trigger manuale o un retry rimasti in sospeso non sono suoi da dichiarare
// scaduti.
//
// La sottoquery con LIMIT tiene la scrittura limitata: dopo un fermo lungo le
// righe da chiudere possono essere molte, e un solo UPDATE su tutte terrebbe un
// lock lungo proprio nel momento in cui il motore sta cercando di ripartire.
const expireOccurrencesSQL = `
	WITH stale AS (
		SELECT e.job_id, e.scheduled_for, e.environment, e.attempt
		  FROM job_executions e
		 WHERE e.status = 'pending'
		   AND e.triggered_by = 'schedule'
		   AND e.scheduled_for >= $1
		   AND e.scheduled_for < $2
		 ORDER BY e.scheduled_for
		 LIMIT $3
	)
	UPDATE job_executions e
	   SET status = 'skipped', error = $4
	  FROM stale s
	 WHERE e.job_id = s.job_id
	   AND e.scheduled_for = s.scheduled_for
	   AND e.environment = s.environment
	   AND e.attempt = s.attempt
	   AND e.status = 'pending'`

// expiredReason è il testo scritto sulle occorrenze scadute. Dice il perché, non
// il come: chi lo legge in dashboard non deve conoscere il motore.
const expiredReason = "occorrenza scaduta: non dispatchata entro la finestra di recupero dello scheduler"

func expireOccurrences(ctx context.Context, q querier, from, before time.Time, limit int) (int, error) {
	tag, err := q.Exec(ctx, expireOccurrencesSQL, from, before, limit, expiredReason)
	if err != nil {
		return 0, fmt.Errorf("scheduler: chiusura delle occorrenze scadute: %w", err)
	}
	return int(tag.RowsAffected()), nil
}

// --------------------------------------------------------- sovrapposizioni

// skipOccurrencesSQL chiude come `skipped` un elenco di occorrenze indirizzate
// per chiave naturale.
//
// È la stessa transizione di `expireOccurrencesSQL` con un altro motivo, e sta
// in due statement invece che in uno perché le due popolazioni si scelgono in
// modo diverso: là un intervallo di tempo, qui una lista che il dispatch ha già
// nominato una per una. Provare a unificarle vorrebbe dire passare la lista a
// una query che è scritta per non averne bisogno.
//
// `AND e.status = 'pending'` è la solita disciplina: se qualcuno l'ha presa nel
// frattempo — cosa che non può succedere, visto che il dispatch l'ha appena
// rifiutata, ma che non costa niente escludere — la riga è sua e non si tocca.
const skipOccurrencesSQL = `
	UPDATE job_executions e
	   SET status = 'skipped', error = $5
	  FROM unnest($1::text[], $2::timestamptz[], $3::text[], $4::smallint[])
	    AS s(job_id, scheduled_for, environment, attempt)
	 WHERE e.job_id = s.job_id::uuid
	   AND e.scheduled_for = s.scheduled_for
	   AND e.environment = s.environment::environment
	   AND e.attempt = s.attempt
	   AND e.status = 'pending'`

// overlapReason è il testo che l'utente legge sull'occorrenza saltata.
//
// Nomina il campo che l'ha decisa — `on_overlap` — perché chi la vede in
// dashboard deve poter risalire dalla riga all'impostazione senza indovinare: la
// domanda che si fa davanti a un'esecuzione mai partita è «chi l'ha decisa», e
// la risposta è una riga del suo `cron.yaml`.
const overlapReason = "occorrenza saltata: l'esecuzione precedente era ancora in corso (on_overlap: skip)"

func skipOccurrences(ctx context.Context, q querier, occs []Occurrence, reason string) (int, error) {
	ids := make([]string, len(occs))
	instants := make([]time.Time, len(occs))
	envs := make([]string, len(occs))
	attempts := make([]int16, len(occs))
	for i, occ := range occs {
		ids[i] = occ.Job.ID
		instants[i] = occ.ScheduledFor
		envs[i] = occ.Environment
		attempts[i] = occ.Attempt
	}

	tag, err := q.Exec(ctx, skipOccurrencesSQL, ids, instants, envs, attempts, reason)
	if err != nil {
		return 0, fmt.Errorf("scheduler: chiusura delle occorrenze sovrapposte: %w", err)
	}
	return int(tag.RowsAffected()), nil
}

// ---------------------------------------------------------------- partizioni

// ensurePartitionsSQL prepara la finestra di partizioni giornaliere.
//
// Lo scheduler è l'unico che scrive su `job_executions`, e senza la partizione
// del giorno l'inserimento **fallisce** — deliberatamente (0006). Chiamarla dal
// motore, e non solo da una manutenzione periodica, è ciò che evita il guasto
// più stupido possibile: un servizio che smette di funzionare a mezzanotte
// perché nessuno ha lanciato la manutenzione.
const ensurePartitionsSQL = `SELECT job_executions_ensure_partitions()`

func ensurePartitions(ctx context.Context, q querier) error {
	var created int
	if err := q.QueryRow(ctx, ensurePartitionsSQL).Scan(&created); err != nil {
		return fmt.Errorf("scheduler: preparazione delle partizioni di job_executions: %w", err)
	}
	return nil
}

// ------------------------------------------------------------------- errori

// isUniqueViolation riconosce il conflitto di idempotenza dal solo SQLSTATE.
// Vedi [uniqueViolation] per il motivo per cui il nome del vincolo non c'entra.
func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == uniqueViolation
}

// missingPartitionError traduce l'errore di una riga che non trova la propria
// partizione in un messaggio che dice cosa fare.
func missingPartitionError(err error, at time.Time) error {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != checkViolation {
		return err
	}
	return fmt.Errorf("nessuna partizione di job_executions per il %s: eseguire job_executions_ensure_partitions(): %w",
		at.UTC().Format("2006-01-02"), err)
}
