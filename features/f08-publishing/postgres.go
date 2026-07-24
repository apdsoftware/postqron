package publishing

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type PostgresStore struct {
	pool *pgxpool.Pool
}

func NewPostgresStore(pool *pgxpool.Pool) (*PostgresStore, error) {
	if pool == nil {
		return nil, fmt.Errorf("%w: postgres pool is required", ErrInvalidArgument)
	}
	return &PostgresStore{pool: pool}, nil
}

func (store *PostgresStore) Enqueue(
	ctx context.Context,
	job Job,
) (EnqueueResult, error) {
	if err := validateJob(job); err != nil {
		return EnqueueResult{}, err
	}
	transaction, err := store.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return EnqueueResult{}, fmt.Errorf("begin publishing enqueue: %w", err)
	}
	defer func() {
		_ = transaction.Rollback(ctx)
	}()

	var insertedID string
	err = transaction.QueryRow(ctx, `
		INSERT INTO f08_publication_jobs (
			id,
			command_id,
			workspace_id,
			post_id,
			draft_id,
			generation,
			invalidation_key,
			status,
			execute_at_utc,
			created_at,
			updated_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
		ON CONFLICT (command_id) DO NOTHING
		RETURNING id
	`,
		job.ID,
		job.CommandID,
		job.WorkspaceID,
		job.PostID,
		job.DraftID,
		job.Generation,
		job.InvalidationKey,
		string(job.Status),
		job.ExecuteAtUTC,
		job.CreatedAt,
		job.UpdatedAt,
	).Scan(&insertedID)
	if errors.Is(err, pgx.ErrNoRows) {
		var existing EnqueueResult
		err = transaction.QueryRow(ctx, `
			SELECT id, false, status
			FROM f08_publication_jobs
			WHERE command_id = $1
		`, job.CommandID).Scan(&existing.JobID, &existing.Created, &existing.Status)
		if err != nil {
			return EnqueueResult{}, fmt.Errorf("read idempotent publishing job: %w", err)
		}
		if err := transaction.Commit(ctx); err != nil {
			return EnqueueResult{}, fmt.Errorf("commit idempotent publishing enqueue: %w", err)
		}
		return existing, nil
	}
	if err != nil {
		return EnqueueResult{}, mapPostgresError("insert publishing job", err)
	}

	for _, destination := range job.Destinations {
		_, err = transaction.Exec(ctx, `
			INSERT INTO f08_publication_destinations (
				id,
				job_id,
				command_id,
				workspace_id,
				post_id,
				generation,
				channel_id,
				provider,
				connection_id,
				payload,
				idempotency_key,
				status,
				attempt_count,
				cycle_attempt_count,
				max_attempts,
				next_attempt_at,
				manual_retry_count
			)
			VALUES (
				$1, $2, $3, $4, $5, $6, $7, $8,
				$9, $10, $11, $12, $13, $14, $15, $16, $17
			)
		`,
			destination.ID,
			destination.JobID,
			destination.CommandID,
			destination.WorkspaceID,
			destination.PostID,
			destination.Generation,
			destination.ChannelID,
			destination.Provider,
			destination.ConnectionID,
			destination.Payload,
			destination.IdempotencyKey,
			string(destination.Status),
			destination.AttemptCount,
			destination.CycleAttemptCount,
			destination.MaxAttempts,
			destination.NextAttemptAt,
			destination.ManualRetryCount,
		)
		if err != nil {
			return EnqueueResult{}, mapPostgresError("insert publication destination", err)
		}
	}
	if err := transaction.Commit(ctx); err != nil {
		return EnqueueResult{}, fmt.Errorf("commit publishing enqueue: %w", err)
	}
	return EnqueueResult{JobID: job.ID, Created: true, Status: job.Status}, nil
}

func (store *PostgresStore) ClaimDue(
	ctx context.Context,
	now, lockedUntil time.Time,
	leaseToken string,
) (Destination, bool, error) {
	if leaseToken == "" || !lockedUntil.After(now) {
		return Destination{}, false, ErrInvalidArgument
	}
	transaction, err := store.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return Destination{}, false, fmt.Errorf("begin publication claim: %w", err)
	}
	defer func() {
		_ = transaction.Rollback(ctx)
	}()

	var destinationID string
	err = transaction.QueryRow(ctx, `
		SELECT id
		FROM f08_publication_destinations
		WHERE next_attempt_at <= $1
		  AND (
			status IN ('pending', 'retry_wait')
			OR (
				status = 'publishing'
				AND locked_until <= $1
			)
		  )
		ORDER BY next_attempt_at, id
		FOR UPDATE SKIP LOCKED
		LIMIT 1
	`, now).Scan(&destinationID)
	if errors.Is(err, pgx.ErrNoRows) {
		if err := transaction.Commit(ctx); err != nil {
			return Destination{}, false, fmt.Errorf("commit empty publication claim: %w", err)
		}
		return Destination{}, false, nil
	}
	if err != nil {
		return Destination{}, false, fmt.Errorf("select publication claim: %w", err)
	}

	_, err = transaction.Exec(ctx, `
		UPDATE f08_publication_attempts
		SET completed_at = $2,
			outcome = 'retry',
			error_code = 'worker_lease_expired',
			error_detail = 'Worker lease expired before recording an outcome.'
		WHERE destination_id = $1
		  AND outcome = 'in_progress'
	`, destinationID, now)
	if err != nil {
		return Destination{}, false, fmt.Errorf("close expired publication attempt: %w", err)
	}

	destination, err := scanDestination(transaction.QueryRow(ctx, destinationSelect+`
		WHERE id = $1
	`, destinationID))
	if err != nil {
		return Destination{}, false, err
	}
	destination.AttemptCount++
	destination.CycleAttemptCount++
	_, err = transaction.Exec(ctx, `
		UPDATE f08_publication_destinations
		SET status = 'publishing',
			attempt_count = $2,
			cycle_attempt_count = $3,
			lease_token = $4,
			locked_until = $5
		WHERE id = $1
	`,
		destinationID,
		destination.AttemptCount,
		destination.CycleAttemptCount,
		leaseToken,
		lockedUntil,
	)
	if err != nil {
		return Destination{}, false, fmt.Errorf("persist publication claim: %w", err)
	}
	_, err = transaction.Exec(ctx, `
		INSERT INTO f08_publication_attempts (
			destination_id,
			attempt_number,
			lease_token,
			started_at,
			outcome
		)
		VALUES ($1, $2, $3, $4, 'in_progress')
	`, destinationID, destination.AttemptCount, leaseToken, now)
	if err != nil {
		return Destination{}, false, mapPostgresError("insert publication attempt", err)
	}
	if err := refreshJob(ctx, transaction, destination.JobID, now); err != nil {
		return Destination{}, false, err
	}
	if err := transaction.Commit(ctx); err != nil {
		return Destination{}, false, fmt.Errorf("commit publication claim: %w", err)
	}
	destination.Status = DestinationPublishing
	destination.LeaseToken = leaseToken
	destination.LockedUntil = &lockedUntil
	return destination, true, nil
}

func (store *PostgresStore) MarkCancelled(
	ctx context.Context,
	id, leaseToken string,
	diagnostic Diagnostic,
	now time.Time,
) error {
	return store.transition(ctx, transition{
		destinationID: id,
		leaseToken:    leaseToken,
		status:        DestinationCancelled,
		diagnostic:    diagnostic,
		now:           now,
		outcome:       "cancelled",
	})
}

func (store *PostgresStore) MarkPublished(
	ctx context.Context,
	id, leaseToken, remoteID string,
	now time.Time,
) error {
	if remoteID == "" {
		return ErrInvalidArgument
	}
	return store.transition(ctx, transition{
		destinationID: id,
		leaseToken:    leaseToken,
		status:        DestinationPublished,
		remoteID:      remoteID,
		now:           now,
		outcome:       "published",
	})
}

func (store *PostgresStore) MarkRetry(
	ctx context.Context,
	id, leaseToken string,
	diagnostic Diagnostic,
	next time.Time,
) error {
	if !next.After(diagnostic.At) {
		return ErrInvalidArgument
	}
	return store.transition(ctx, transition{
		destinationID: id,
		leaseToken:    leaseToken,
		status:        DestinationRetryWait,
		diagnostic:    diagnostic,
		next:          next,
		now:           diagnostic.At,
		outcome:       "retry",
	})
}

func (store *PostgresStore) MarkDeadLetter(
	ctx context.Context,
	id, leaseToken string,
	diagnostic Diagnostic,
	now time.Time,
) error {
	return store.transition(ctx, transition{
		destinationID: id,
		leaseToken:    leaseToken,
		status:        DestinationDeadLetter,
		diagnostic:    diagnostic,
		now:           now,
		outcome:       "dead_letter",
	})
}

func (store *PostgresStore) RetryDeadLetter(
	ctx context.Context,
	workspaceID, destinationID, actorID string,
	now time.Time,
) (Destination, error) {
	if workspaceID == "" || destinationID == "" || actorID == "" || now.IsZero() {
		return Destination{}, ErrInvalidArgument
	}
	transaction, err := store.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return Destination{}, fmt.Errorf("begin manual publication retry: %w", err)
	}
	defer func() {
		_ = transaction.Rollback(ctx)
	}()

	destination, err := scanDestination(transaction.QueryRow(ctx, destinationSelect+`
		WHERE id = $1 AND workspace_id = $2
		FOR UPDATE
	`, destinationID, workspaceID))
	if errors.Is(err, ErrNotFound) {
		return Destination{}, ErrNotFound
	}
	if err != nil {
		return Destination{}, err
	}
	if destination.Status != DestinationDeadLetter {
		return Destination{}, ErrConflict
	}
	tag, err := transaction.Exec(ctx, `
		UPDATE f08_publication_dead_letters
		SET resolved_at = $2,
			retried_by_account_id = $3
		WHERE destination_id = $1
		  AND resolved_at IS NULL
	`, destinationID, now, actorID)
	if err != nil {
		return Destination{}, fmt.Errorf("resolve publication dead letter: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return Destination{}, ErrConflict
	}
	_, err = transaction.Exec(ctx, `
		UPDATE f08_publication_destinations
		SET status = 'retry_wait',
			cycle_attempt_count = 0,
			next_attempt_at = $2,
			dead_lettered_at = NULL,
			manual_retry_count = manual_retry_count + 1
		WHERE id = $1
	`, destinationID, now)
	if err != nil {
		return Destination{}, fmt.Errorf("requeue publication dead letter: %w", err)
	}
	if err := refreshJob(ctx, transaction, destination.JobID, now); err != nil {
		return Destination{}, err
	}
	if err := transaction.Commit(ctx); err != nil {
		return Destination{}, fmt.Errorf("commit manual publication retry: %w", err)
	}
	destination.Status = DestinationRetryWait
	destination.CycleAttemptCount = 0
	destination.NextAttemptAt = now
	destination.DeadLetteredAt = nil
	destination.ManualRetryCount++
	return destination, nil
}

func (store *PostgresStore) GetJob(
	ctx context.Context,
	workspaceID, jobID string,
) (Job, error) {
	job, err := scanJob(store.pool.QueryRow(ctx, jobSelect+`
		WHERE workspace_id = $1 AND id = $2
	`, workspaceID, jobID))
	if err != nil {
		return Job{}, err
	}
	rows, err := store.pool.Query(ctx, destinationSelect+`
		WHERE job_id = $1
		ORDER BY id
	`, jobID)
	if err != nil {
		return Job{}, fmt.Errorf("list publication destinations: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		destination, scanErr := scanDestination(rows)
		if scanErr != nil {
			return Job{}, scanErr
		}
		job.Destinations = append(job.Destinations, destination)
	}
	if err := rows.Err(); err != nil {
		return Job{}, fmt.Errorf("iterate publication destinations: %w", err)
	}
	return job, nil
}

type transition struct {
	destinationID string
	leaseToken    string
	status        DestinationStatus
	diagnostic    Diagnostic
	remoteID      string
	next          time.Time
	now           time.Time
	outcome       string
}

func (store *PostgresStore) transition(
	ctx context.Context,
	change transition,
) error {
	if change.destinationID == "" ||
		change.leaseToken == "" ||
		change.now.IsZero() {
		return ErrInvalidArgument
	}
	transaction, err := store.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin publication transition: %w", err)
	}
	defer func() {
		_ = transaction.Rollback(ctx)
	}()

	var jobID string
	var attemptNumber int
	var tag pgconn.CommandTag
	switch change.status {
	case DestinationPublished:
		tag, err = transaction.Exec(ctx, `
			UPDATE f08_publication_destinations
			SET status = 'published',
				lease_token = NULL,
				locked_until = NULL,
				remote_id = $3,
				error_code = NULL,
				error_detail = NULL,
				error_retryable = false,
				error_at = NULL,
				published_at = $4
			WHERE id = $1
			  AND status = 'publishing'
			  AND lease_token = $2
		`, change.destinationID, change.leaseToken, change.remoteID, change.now)
	case DestinationRetryWait:
		tag, err = transaction.Exec(ctx, `
			UPDATE f08_publication_destinations
			SET status = 'retry_wait',
				lease_token = NULL,
				locked_until = NULL,
				next_attempt_at = $3,
				error_code = $4,
				error_detail = $5,
				error_retryable = $6,
				error_at = $7
			WHERE id = $1
			  AND status = 'publishing'
			  AND lease_token = $2
		`,
			change.destinationID,
			change.leaseToken,
			change.next,
			change.diagnostic.Code,
			change.diagnostic.Detail,
			change.diagnostic.Retryable,
			change.diagnostic.At,
		)
	case DestinationDeadLetter:
		tag, err = transaction.Exec(ctx, `
			UPDATE f08_publication_destinations
			SET status = 'dead_letter',
				lease_token = NULL,
				locked_until = NULL,
				error_code = $3,
				error_detail = $4,
				error_retryable = false,
				error_at = $5,
				dead_lettered_at = $6
			WHERE id = $1
			  AND status = 'publishing'
			  AND lease_token = $2
		`,
			change.destinationID,
			change.leaseToken,
			change.diagnostic.Code,
			change.diagnostic.Detail,
			change.diagnostic.At,
			change.now,
		)
	case DestinationCancelled:
		tag, err = transaction.Exec(ctx, `
			UPDATE f08_publication_destinations
			SET status = 'cancelled',
				lease_token = NULL,
				locked_until = NULL,
				error_code = $3,
				error_detail = $4,
				error_retryable = false,
				error_at = $5,
				cancelled_at = $6
			WHERE id = $1
			  AND status = 'publishing'
			  AND lease_token = $2
		`,
			change.destinationID,
			change.leaseToken,
			change.diagnostic.Code,
			change.diagnostic.Detail,
			change.diagnostic.At,
			change.now,
		)
	default:
		return ErrInvalidArgument
	}
	if err != nil {
		return mapPostgresError("update publication destination", err)
	}
	if tag.RowsAffected() != 1 {
		return ErrConflict
	}
	err = transaction.QueryRow(ctx, `
		SELECT job_id, attempt_count
		FROM f08_publication_destinations
		WHERE id = $1
	`, change.destinationID).Scan(&jobID, &attemptNumber)
	if err != nil {
		return fmt.Errorf("read transitioned publication destination: %w", err)
	}
	tag, err = transaction.Exec(ctx, `
		UPDATE f08_publication_attempts
		SET completed_at = $2,
			outcome = $3,
			error_code = NULLIF($4, ''),
			error_detail = NULLIF($5, ''),
			remote_id = NULLIF($6, '')
		WHERE destination_id = $1
		  AND lease_token = $7
		  AND attempt_number = $8
		  AND outcome = 'in_progress'
	`,
		change.destinationID,
		change.now,
		change.outcome,
		change.diagnostic.Code,
		change.diagnostic.Detail,
		change.remoteID,
		change.leaseToken,
		attemptNumber,
	)
	if err != nil {
		return fmt.Errorf("complete publication attempt: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return ErrConflict
	}
	if change.status == DestinationDeadLetter {
		_, err = transaction.Exec(ctx, `
			INSERT INTO f08_publication_dead_letters (
				destination_id,
				job_id,
				diagnostic_code,
				diagnostic_detail,
				failed_at
			)
			VALUES ($1, $2, $3, $4, $5)
		`,
			change.destinationID,
			jobID,
			change.diagnostic.Code,
			change.diagnostic.Detail,
			change.now,
		)
		if err != nil {
			return mapPostgresError("insert publication dead letter", err)
		}
	}
	if err := refreshJob(ctx, transaction, jobID, change.now); err != nil {
		return err
	}
	if err := transaction.Commit(ctx); err != nil {
		return fmt.Errorf("commit publication transition: %w", err)
	}
	return nil
}

func refreshJob(
	ctx context.Context,
	transaction pgx.Tx,
	jobID string,
	now time.Time,
) error {
	tag, err := transaction.Exec(ctx, `
		UPDATE f08_publication_jobs
		SET status = aggregate.status,
			updated_at = $2
		FROM (
			SELECT
				job_id,
				CASE
					WHEN bool_or(status IN ('pending', 'retry_wait')) THEN 'queued'
					WHEN bool_or(status = 'publishing') THEN 'publishing'
					WHEN bool_and(status = 'published') THEN 'published'
					WHEN bool_or(status = 'published')
						AND bool_or(status IN ('dead_letter', 'cancelled'))
						THEN 'partially_failed'
					WHEN bool_or(status = 'dead_letter') THEN 'failed'
					ELSE 'cancelled'
				END AS status
			FROM f08_publication_destinations
			WHERE job_id = $1
			GROUP BY job_id
		) AS aggregate
		WHERE f08_publication_jobs.id = aggregate.job_id
	`, jobID, now)
	if err != nil {
		return fmt.Errorf("refresh publication job status: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return ErrNotFound
	}
	return nil
}

const jobSelect = `
	SELECT
		id,
		command_id,
		workspace_id,
		post_id,
		draft_id,
		generation,
		invalidation_key,
		status,
		execute_at_utc,
		created_at,
		updated_at
	FROM f08_publication_jobs
`

const destinationSelect = `
	SELECT
		id,
		job_id,
		command_id,
		workspace_id,
		post_id,
		generation,
		channel_id,
		provider,
		connection_id,
		payload,
		idempotency_key,
		status,
		attempt_count,
		cycle_attempt_count,
		max_attempts,
		next_attempt_at,
		COALESCE(lease_token, ''),
		locked_until,
		COALESCE(remote_id, ''),
		COALESCE(error_code, ''),
		COALESCE(error_detail, ''),
		error_retryable,
		error_at,
		published_at,
		dead_lettered_at,
		cancelled_at,
		manual_retry_count
	FROM f08_publication_destinations
`

type rowScanner interface {
	Scan(...any) error
}

func scanJob(row rowScanner) (Job, error) {
	var job Job
	err := row.Scan(
		&job.ID,
		&job.CommandID,
		&job.WorkspaceID,
		&job.PostID,
		&job.DraftID,
		&job.Generation,
		&job.InvalidationKey,
		&job.Status,
		&job.ExecuteAtUTC,
		&job.CreatedAt,
		&job.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return Job{}, ErrNotFound
	}
	if err != nil {
		return Job{}, fmt.Errorf("scan publication job: %w", err)
	}
	return job, nil
}

func scanDestination(row rowScanner) (Destination, error) {
	var destination Destination
	var diagnosticAt *time.Time
	err := row.Scan(
		&destination.ID,
		&destination.JobID,
		&destination.CommandID,
		&destination.WorkspaceID,
		&destination.PostID,
		&destination.Generation,
		&destination.ChannelID,
		&destination.Provider,
		&destination.ConnectionID,
		&destination.Payload,
		&destination.IdempotencyKey,
		&destination.Status,
		&destination.AttemptCount,
		&destination.CycleAttemptCount,
		&destination.MaxAttempts,
		&destination.NextAttemptAt,
		&destination.LeaseToken,
		&destination.LockedUntil,
		&destination.RemoteID,
		&destination.LastDiagnostic.Code,
		&destination.LastDiagnostic.Detail,
		&destination.LastDiagnostic.Retryable,
		&diagnosticAt,
		&destination.PublishedAt,
		&destination.DeadLetteredAt,
		&destination.CancelledAt,
		&destination.ManualRetryCount,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return Destination{}, ErrNotFound
	}
	if err != nil {
		return Destination{}, fmt.Errorf("scan publication destination: %w", err)
	}
	if diagnosticAt != nil {
		destination.LastDiagnostic.At = *diagnosticAt
	}
	return destination, nil
}

func mapPostgresError(action string, err error) error {
	var postgresError *pgconn.PgError
	if errors.As(err, &postgresError) && postgresError.Code == "23505" {
		return fmt.Errorf("%s: %w", action, ErrConflict)
	}
	return fmt.Errorf("%s: %w", action, err)
}

var _ Store = (*PostgresStore)(nil)
