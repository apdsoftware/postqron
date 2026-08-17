// Package reposyncpg è l'implementazione PostgreSQL di reposync.Store.
//
// Sta in un package a parte per la stessa ragione di internal/jobspg:
// internal/reposync non deve dipendere da pgx, ed è ciò che permette di provare
// i tre casi che fanno danno — job sparito, job in pausa, file non valido —
// senza un database in piedi. Qui non c'è logica di riconciliazione: c'è
// l'**atomicità**, che è la sola parte di R13 che solo il database può
// garantire.
//
// Non servono migrazioni nuove. Le colonne su cui questo package poggia
// esistono già e sono state scritte per questo: `jobs.archived_at` distinto da
// `jobs.enabled` (0005), l'indice unico parziale `jobs_repository_name_key`
// (0005), `jobs_repository_id_idx` (0005) e le quattro colonne di esito su
// `repositories` (0004).
package reposyncpg

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/apdsoftware/postqron/services/api/internal/jobs"
	"github.com/apdsoftware/postqron/services/api/internal/reposync"
)

// Store implementa [reposync.Store] su un pool pgx.
type Store struct {
	pool *pgxpool.Pool
}

// New costruisce lo store.
func New(pool *pgxpool.Pool) (*Store, error) {
	if pool == nil {
		return nil, errors.New("reposyncpg: il pool è obbligatorio")
	}
	return &Store{pool: pool}, nil
}

var _ reposync.Store = (*Store)(nil)

// repositoryColumns è ciò che [reposync.Repository] contiene, nell'ordine in
// cui scanRepository lo legge.
const repositoryColumns = `id::text, user_id::text, coalesce(installation_id, 0),
	owner, name, default_branch, config_path, enabled,
	coalesce(last_synced_commit, ''), coalesce(last_sync_status::text, '')`

// managedJobColumns è il sottoinsieme di `jobs` che la riconciliazione
// confronta e riscrive.
//
// È volutamente più corto di quello di internal/jobspg, e non è una copia
// dimagrita: `next_run_at`, `created_at` e `updated_at` non compaiono perché
// [reposync.Reconcile] non li guarda e non li scrive. Una colonna letta qui
// sarebbe una colonna che qualcuno prima o poi confronta, e confrontare
// `updated_at` renderebbe ogni push un aggiornamento di tutto.
const managedJobColumns = `id::text, user_id::text, name, coalesce(description, ''),
	coalesce(schedule, ''), coalesce(every_seconds, 0), timezone, environments::text[],
	url, method::text, headers, coalesce(body, ''),
	timeout_seconds, max_retries, retry_backoff::text, overlap_policy::text,
	alert_on_failure::text[],
	enabled, archived_at`

// RepositoriesByExternalID trova i collegamenti che seguono un repository.
func (s *Store) RepositoriesByExternalID(ctx context.Context, externalID int64) ([]reposync.Repository, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT `+repositoryColumns+`
		   FROM repositories
		  WHERE provider = $1::repository_provider AND external_id = $2
		  ORDER BY created_at`,
		reposync.Provider, externalID)
	if err != nil {
		return nil, fmt.Errorf("reposyncpg: ricerca dei repository: %w", err)
	}
	defer rows.Close()

	var out []reposync.Repository
	for rows.Next() {
		repo, err := scanRepository(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, repo)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("reposyncpg: ricerca dei repository: %w", err)
	}
	return out, nil
}

// ManagedJobs elenca i job del repository, archiviati compresi.
func (s *Store) ManagedJobs(ctx context.Context, repositoryID string) ([]jobs.Job, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT `+managedJobColumns+`
		   FROM jobs
		  WHERE repository_id = $1::uuid
		  ORDER BY created_at, id`, repositoryID)
	if err != nil {
		return nil, fmt.Errorf("reposyncpg: lettura dei job del repository: %w", err)
	}
	defer rows.Close()

	var out []jobs.Job
	for rows.Next() {
		job, err := scanManagedJob(rows)
		if err != nil {
			return nil, err
		}
		job.RepositoryID = repositoryID
		out = append(out, job)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("reposyncpg: lettura dei job del repository: %w", err)
	}
	return out, nil
}

// CountJobsOutside conta i job non archiviati dell'utente fuori da questo
// repository.
//
// «Non archiviati» è la stessa condizione di [jobs.Plan.CheckJobCount]: un job
// in pausa occupa un posto nel catalogo, uno archiviato no. E «fuori da questo
// repository» include i job creati a mano e quelli di altri repository, perché
// il tetto è sull'utente.
func (s *Store) CountJobsOutside(ctx context.Context, userID, repositoryID string) (int, error) {
	var count int
	err := s.pool.QueryRow(ctx,
		`SELECT count(*) FROM jobs
		  WHERE user_id = $1::uuid
		    AND archived_at IS NULL
		    AND (repository_id IS NULL OR repository_id <> $2::uuid)`,
		userID, repositoryID).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("reposyncpg: conteggio dei job fuori dal repository: %w", err)
	}
	return count, nil
}

// Apply applica il piano e registra l'esito, in una sola transazione.
//
// # Perché una transazione sola
//
// R13 dice che un file non valido non corrompe lo stato. La parte scoperta è
// l'altra: un file **valido** applicato a metà. Se la creazione del decimo job
// fallisce dopo che i primi nove sono passati, quello che resta è un repository
// che descrive dieci job e un database che ne ha nove, senza niente che lo
// segnali — e il push successivo, se non cambia il file, non lo corregge
// nemmeno. Una transazione toglie quel caso dall'esistenza.
//
// # Perché il lock sulla riga
//
// La `SELECT ... FOR UPDATE` sul collegamento è la **seconda porta
// dell'idempotenza**. La prima, in [reposync.Service], confronta il commit
// prima di scaricare il file e ferma le ripetizioni distanziate. Non ferma le
// simultanee: due consegne dello stesso commit che arrivano insieme leggono
// entrambe lo stato precedente e riconciliano entrambe. Qui la seconda aspetta
// la prima, poi rilegge `last_synced_commit` — con la riga bloccata, quindi
// vedendo l'esito della prima — e si ritira con [reposync.ErrAlreadySynced].
//
// # Perché anche il lock consultivo
//
// Il tetto di piano è sul numero di job dell'utente, e una creazione
// dall'API può passare fra il nostro conteggio e la nostra INSERT.
// `pg_advisory_xact_lock` sulla stessa chiave che internal/jobspg usa per la
// creazione serializza le due strade: la stringa che genera la chiave deve
// restare identica a quella di `jobspg.advisoryKey`, ed è l'unica cosa di
// questo file che dipende da un altro package senza che il compilatore lo veda.
func (s *Store) Apply(ctx context.Context, plan reposync.Plan) (reposync.Applied, error) {
	var applied reposync.Applied

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return applied, fmt.Errorf("reposyncpg: apertura della transazione: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, advisoryKey(plan.UserID)); err != nil {
		return applied, fmt.Errorf("reposyncpg: lock sulla riconciliazione: %w", err)
	}

	var lastCommit, lastStatus string
	err = tx.QueryRow(ctx,
		`SELECT coalesce(last_synced_commit, ''), coalesce(last_sync_status::text, '')
		   FROM repositories WHERE id = $1::uuid FOR UPDATE`, plan.RepositoryID).Scan(&lastCommit, &lastStatus)
	if errors.Is(err, pgx.ErrNoRows) {
		// Il collegamento è stato tolto fra la lettura e la scrittura. Non c'è
		// più niente da riconciliare, e non è un guasto da ripetere.
		return applied, reposync.ErrAlreadySynced
	}
	if err != nil {
		return applied, fmt.Errorf("reposyncpg: lettura del collegamento: %w", err)
	}
	if lastCommit == plan.Commit && lastStatus == string(reposync.SyncSucceeded) {
		return applied, reposync.ErrAlreadySynced
	}

	if err := s.applyChanges(ctx, tx, plan, &applied); err != nil {
		return reposync.Applied{}, err
	}

	// L'esito si scrive dentro la stessa transazione delle modifiche: un sync
	// applicato e non registrato verrebbe rifatto per intero al push
	// successivo, e uno registrato senza essere applicato non verrebbe rifatto
	// mai.
	_, err = tx.Exec(ctx,
		`UPDATE repositories
		    SET last_synced_at = now(),
		        last_synced_commit = $2,
		        last_sync_status = 'succeeded',
		        last_sync_error = NULL
		  WHERE id = $1::uuid`, plan.RepositoryID, plan.Commit)
	if err != nil {
		return reposync.Applied{}, fmt.Errorf("reposyncpg: registrazione dell'esito del sync: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return reposync.Applied{}, fmt.Errorf("reposyncpg: commit della riconciliazione: %w", err)
	}
	return applied, nil
}

// applyChanges scrive le modifiche nell'ordine in cui il piano le porta:
// archiviazioni, aggiornamenti, creazioni (vedi [reposync.Reconcile]).
func (s *Store) applyChanges(ctx context.Context, tx pgx.Tx, plan reposync.Plan, applied *reposync.Applied) error {
	var creations int
	for _, change := range plan.Changes {
		if change.Kind == reposync.ChangeCreate {
			creations++
		}
	}
	// Il tetto si verifica una volta sola, **dopo** le archiviazioni e prima
	// della prima creazione. Non prima di tutto: le archiviazioni di questo
	// stesso piano liberano posti, e contarle come ancora occupate farebbe
	// fallire un file che toglie dieci job e ne aggiunge dieci su un piano al
	// limite — cioè un file che ci sta.
	limiteDaVerificare := creations > 0 && plan.MaxJobs != nil

	for _, change := range plan.Changes {
		switch change.Kind {
		case reposync.ChangeArchive:
			if err := archiveJob(ctx, tx, change.Job.ID); err != nil {
				return err
			}
			applied.Archived++

		case reposync.ChangeUpdate:
			if err := updateJob(ctx, tx, change); err != nil {
				return err
			}
			applied.Updated++
			if change.Restored {
				applied.Restored++
			}

		case reposync.ChangeCreate:
			if limiteDaVerificare {
				if err := checkJobLimit(ctx, tx, plan, creations); err != nil {
					return err
				}
				limiteDaVerificare = false
			}
			if err := insertJob(ctx, tx, change.Job); err != nil {
				return err
			}
			applied.Created++
		}
	}
	return nil
}

// checkJobLimit verifica che le creazioni del piano stiano nel tetto (SPEC §8).
//
// Il conteggio è un'istruzione a sé avviata **dopo** il lock consultivo: in
// READ COMMITTED prende quindi uno snapshot che include le scritture di chi ha
// tenuto il lock prima, e le archiviazioni già fatte da questa transazione. È
// lo stesso ragionamento di jobspg.CreateJob, e vale identico qui.
//
// Il rifiuto è per intero: la transazione torna indietro, e nessuno dei job del
// file viene applicato. Applicarne una parte in silenzio è la risposta
// sbagliata a un file che sfonda il piano.
func checkJobLimit(ctx context.Context, tx pgx.Tx, plan reposync.Plan, creations int) error {
	var current int
	err := tx.QueryRow(ctx,
		`SELECT count(*) FROM jobs WHERE user_id = $1::uuid AND archived_at IS NULL`,
		plan.UserID).Scan(&current)
	if err != nil {
		return fmt.Errorf("reposyncpg: conteggio dei job: %w", err)
	}
	if current+creations > *plan.MaxJobs {
		return jobs.ErrJobLimitReached
	}
	return nil
}

// archiveJob disattiva un job sparito dal file.
//
// `enabled` non compare nella SET, ed è il punto: un job archiviato che
// l'utente aveva messo in pausa resta in pausa, e uno che era attivo resta
// attivo — semplicemente non è più eseguibile, perché `jobs_due_idx` esclude
// gli archiviati. Quando il file lo rimette, torna quello che era.
//
// `next_run_at` si azzera perché la prossima occorrenza calcolata non
// significa più niente; le esecuzioni passate restano tutte (0006), ed è ciò
// per cui questa colonna esiste invece di una DELETE.
func archiveJob(ctx context.Context, tx pgx.Tx, jobID string) error {
	_, err := tx.Exec(ctx,
		`UPDATE jobs
		    SET archived_at = now(), next_run_at = NULL
		  WHERE id = $1::uuid AND archived_at IS NULL`, jobID)
	if err != nil {
		return fmt.Errorf("reposyncpg: archiviazione del job: %w", err)
	}
	return nil
}

// updateJob riscrive i campi che il file possiede.
//
// `enabled` non c'è. È la pausa — dalla dashboard o da un downgrade di piano
// (R58) — il file non la esprime, e scriverla qui **anche solo riscrivendo il
// valore appena letto** significherebbe perdere una pausa decisa fra la lettura
// e la scrittura. La finestra non è teorica: fra `ManagedJobs` e questa UPDATE
// ci sono una chiamata a GitHub e un parsing.
func updateJob(ctx context.Context, tx pgx.Tx, change reposync.Change) error {
	job := change.Job
	headers, err := json.Marshal(nonNilHeaders(job.Headers))
	if err != nil {
		return fmt.Errorf("reposyncpg: serializzazione degli header: %w", err)
	}

	_, err = tx.Exec(ctx,
		`UPDATE jobs SET
		    description = $2, schedule = $3, every_seconds = $4, timezone = $5,
		    environments = $6::text[]::environment[], url = $7, method = $8::http_method,
		    headers = $9::jsonb, body = $10, timeout_seconds = $11,
		    max_retries = $12, retry_backoff = $13::retry_backoff,
		    overlap_policy = $14::overlap_policy,
		    alert_on_failure = $15::text[]::alert_channel[],
		    archived_at = NULL,
		    next_run_at = CASE WHEN $16::boolean THEN NULL ELSE jobs.next_run_at END
		  WHERE id = $1::uuid`,
		job.ID, nullable(job.Description),
		nullable(job.Schedule), everySeconds(job.Every), job.Timezone,
		stringsOf(job.Environments), job.URL, string(job.Method), headers, nullable(job.Body),
		int32(job.Timeout/time.Second),
		int16(job.MaxRetries), string(job.RetryBackoff), overlapPolicy(job.OverlapPolicy),
		stringsOf(job.AlertOnFailure),
		change.ResetNextRun)
	if err != nil {
		return fmt.Errorf("reposyncpg: aggiornamento del job %q: %w", job.Name, err)
	}
	return nil
}

// insertJob crea un job nato dal file.
//
// `repository_id` è valorizzato: è ciò che rende il job «gestito»
// ([jobs.Job.Managed]) e quindi non modificabile dall'API, ed è la chiave su
// cui la riconciliazione successiva lo ritroverà. `next_run_at` resta NULL: la
// prima occorrenza la calcola lo scheduler (0010).
func insertJob(ctx context.Context, tx pgx.Tx, job jobs.Job) error {
	headers, err := json.Marshal(nonNilHeaders(job.Headers))
	if err != nil {
		return fmt.Errorf("reposyncpg: serializzazione degli header: %w", err)
	}

	_, err = tx.Exec(ctx,
		`INSERT INTO jobs (
		    user_id, repository_id, name, description, schedule, every_seconds, timezone,
		    environments, url, method, headers, body, timeout_seconds,
		    max_retries, retry_backoff, overlap_policy, alert_on_failure, enabled)
		 VALUES ($1::uuid, $2::uuid, $3, $4, $5, $6, $7,
		         $8::text[]::environment[], $9, $10::http_method, $11::jsonb, $12, $13,
		         $14, $15::retry_backoff, $16::overlap_policy,
		         $17::text[]::alert_channel[], true)`,
		job.UserID, job.RepositoryID, job.Name, nullable(job.Description),
		nullable(job.Schedule), everySeconds(job.Every), job.Timezone,
		stringsOf(job.Environments), job.URL, string(job.Method), headers, nullable(job.Body),
		int32(job.Timeout/time.Second),
		int16(job.MaxRetries), string(job.RetryBackoff), overlapPolicy(job.OverlapPolicy),
		stringsOf(job.AlertOnFailure))
	if err != nil {
		return fmt.Errorf("reposyncpg: creazione del job %q: %w", job.Name, err)
	}
	return nil
}

// RecordFailure registra un sync fallito senza toccare i job.
//
// È **una sola UPDATE su `repositories`**, e la brevità è il contratto: R13
// dice che gli errori tornano all'utente senza corrompere lo stato esistente, e
// il modo di renderlo vero è che il percorso di fallimento non sappia nemmeno
// scrivere sui job.
func (s *Store) RecordFailure(ctx context.Context, repositoryID, commit, reason string) error {
	if reason == "" {
		// `repositories_sync_error_check` (0004) esige un motivo quando lo
		// stato è `failed`. Un fallimento senza motivo è comunque un
		// fallimento da registrare.
		reason = "motivo non riportato"
	}

	tag, err := s.pool.Exec(ctx,
		`UPDATE repositories
		    SET last_synced_at = now(),
		        last_synced_commit = $2,
		        last_sync_status = 'failed',
		        last_sync_error = $3
		  WHERE id = $1::uuid`,
		repositoryID, nullCommit(commit), reason)
	if err != nil {
		return fmt.Errorf("reposyncpg: registrazione del sync fallito: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("reposyncpg: collegamento %q non trovato", repositoryID)
	}
	return nil
}

// advisoryKey trasforma l'identificativo dell'utente nella chiave del lock
// consultivo.
//
// **La stringa deve restare identica a quella di `jobspg.advisoryKey`**: due
// stringhe diverse producono due lock diversi, e due lock diversi non
// serializzano niente. È una dipendenza che il compilatore non vede, ed è il
// prezzo di non esportare da internal/jobspg un dettaglio che è suo.
func advisoryKey(userID string) int64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte("postqron:jobs:create:" + userID))
	return int64(h.Sum64())
}
