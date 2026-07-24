package analytics

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type PostgresRepository struct {
	pool *pgxpool.Pool
}

func NewPostgresRepository(pool *pgxpool.Pool) (*PostgresRepository, error) {
	if pool == nil {
		return nil, fmt.Errorf("%w: postgres pool is required", ErrInvalidArgument)
	}
	return &PostgresRepository{pool: pool}, nil
}

func (repository *PostgresRepository) Register(
	ctx context.Context,
	target SyncTarget,
) (RegisterResult, error) {
	if err := validateTarget(target); err != nil {
		return RegisterResult{}, err
	}
	var insertedID string
	err := repository.pool.QueryRow(ctx, `
		INSERT INTO f18_analytics_targets (
			id,
			workspace_id,
			content_id,
			channel_id,
			channel_type,
			provider,
			connection_id,
			remote_id,
			published_at,
			cursor,
			state,
			attempt_count,
			consecutive_failures,
			next_sync_at,
			created_at,
			updated_at
		)
		VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8,
			$9, $10, $11, $12, $13, $14, $15, $16
		)
		ON CONFLICT (provider, connection_id, remote_id) DO NOTHING
		RETURNING id
	`,
		target.ID,
		target.WorkspaceID,
		target.ContentID,
		target.ChannelID,
		string(target.ChannelType),
		target.Provider,
		target.ConnectionID,
		target.RemoteID,
		target.PublishedAt,
		target.Cursor,
		string(target.State),
		target.AttemptCount,
		target.ConsecutiveFailures,
		target.NextSyncAt,
		target.CreatedAt,
		target.UpdatedAt,
	).Scan(&insertedID)
	if err == nil {
		return RegisterResult{TargetID: insertedID, Created: true}, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return RegisterResult{}, mapPostgresError("insert analytics target", err)
	}

	var existing SyncTarget
	err = repository.pool.QueryRow(ctx, `
		SELECT
			id,
			workspace_id,
			content_id,
			channel_id,
			channel_type,
			provider,
			connection_id,
			remote_id
		FROM f18_analytics_targets
		WHERE provider = $1
		  AND connection_id = $2
		  AND remote_id = $3
	`,
		target.Provider,
		target.ConnectionID,
		target.RemoteID,
	).Scan(
		&existing.ID,
		&existing.WorkspaceID,
		&existing.ContentID,
		&existing.ChannelID,
		&existing.ChannelType,
		&existing.Provider,
		&existing.ConnectionID,
		&existing.RemoteID,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return RegisterResult{}, ErrNotFound
	}
	if err != nil {
		return RegisterResult{}, fmt.Errorf("read analytics target conflict: %w", err)
	}
	if !sameRemoteTarget(existing, target) {
		return RegisterResult{}, ErrConflict
	}
	return RegisterResult{TargetID: existing.ID, Created: false}, nil
}

func (repository *PostgresRepository) ClaimDue(
	ctx context.Context,
	now, lockedUntil time.Time,
	leaseToken string,
) (SyncTarget, bool, error) {
	if leaseToken == "" || !lockedUntil.After(now) {
		return SyncTarget{}, false, ErrInvalidArgument
	}
	transaction, err := repository.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return SyncTarget{}, false, fmt.Errorf("begin analytics claim: %w", err)
	}
	defer func() {
		_ = transaction.Rollback(ctx)
	}()
	var targetID string
	err = transaction.QueryRow(ctx, `
		SELECT id
		FROM f18_analytics_targets
		WHERE next_sync_at <= $1
		  AND (
			state IN (
				'pending',
				'retry_wait',
				'current',
				'unavailable',
				'permission_missing'
			)
			OR (
				state = 'syncing'
				AND locked_until <= $1
			)
		  )
		ORDER BY next_sync_at, id
		FOR UPDATE SKIP LOCKED
		LIMIT 1
	`, now).Scan(&targetID)
	if errors.Is(err, pgx.ErrNoRows) {
		if err := transaction.Commit(ctx); err != nil {
			return SyncTarget{}, false, fmt.Errorf("commit empty analytics claim: %w", err)
		}
		return SyncTarget{}, false, nil
	}
	if err != nil {
		return SyncTarget{}, false, fmt.Errorf("select analytics claim: %w", err)
	}
	_, err = transaction.Exec(ctx, `
		UPDATE f18_analytics_targets
		SET state = 'syncing',
			attempt_count = attempt_count + 1,
			lease_token = $2,
			locked_until = $3,
			updated_at = $4
		WHERE id = $1
	`, targetID, leaseToken, lockedUntil, now)
	if err != nil {
		return SyncTarget{}, false, fmt.Errorf("persist analytics claim: %w", err)
	}
	target, err := scanTarget(transaction.QueryRow(
		ctx,
		targetSelect+" WHERE id = $1",
		targetID,
	))
	if err != nil {
		return SyncTarget{}, false, err
	}
	if err := transaction.Commit(ctx); err != nil {
		return SyncTarget{}, false, fmt.Errorf("commit analytics claim: %w", err)
	}
	return target, true, nil
}

func (repository *PostgresRepository) SaveSuccess(
	ctx context.Context,
	targetID, leaseToken string,
	observations []Observation,
	cursor string,
	state TargetState,
	nextSyncAt, now time.Time,
) error {
	if (state != TargetCurrent && state != TargetUnavailable &&
		state != TargetPermissionMissing) ||
		!nextSyncAt.After(now) {
		return ErrInvalidArgument
	}
	if err := validateObservations(targetID, observations); err != nil {
		return err
	}
	transaction, err := repository.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin analytics success: %w", err)
	}
	defer func() {
		_ = transaction.Rollback(ctx)
	}()
	if err := lockClaimedTarget(ctx, transaction, targetID, leaseToken); err != nil {
		return err
	}
	if err := insertObservations(ctx, transaction, observations); err != nil {
		return err
	}
	command, err := transaction.Exec(ctx, `
		UPDATE f18_analytics_targets
		SET cursor = $3,
			state = $4,
			consecutive_failures = 0,
			next_sync_at = $5,
			lease_token = NULL,
			locked_until = NULL,
			last_error_code = NULL,
			last_error_at = NULL,
			updated_at = $6
		WHERE id = $1
		  AND lease_token = $2
		  AND state = 'syncing'
	`,
		targetID,
		leaseToken,
		cursor,
		string(state),
		nextSyncAt,
		now,
	)
	if err != nil {
		return fmt.Errorf("complete analytics sync: %w", err)
	}
	if command.RowsAffected() != 1 {
		return ErrConflict
	}
	if err := transaction.Commit(ctx); err != nil {
		return fmt.Errorf("commit analytics success: %w", err)
	}
	return nil
}

func (repository *PostgresRepository) SaveRetry(
	ctx context.Context,
	targetID, leaseToken, code string,
	nextSyncAt, now time.Time,
) error {
	if code == "" || !nextSyncAt.After(now) {
		return ErrInvalidArgument
	}
	command, err := repository.pool.Exec(ctx, `
		UPDATE f18_analytics_targets
		SET state = 'retry_wait',
			consecutive_failures = consecutive_failures + 1,
			next_sync_at = $4,
			lease_token = NULL,
			locked_until = NULL,
			last_error_code = $3,
			last_error_at = $5,
			updated_at = $5
		WHERE id = $1
		  AND lease_token = $2
		  AND state = 'syncing'
	`, targetID, leaseToken, code, nextSyncAt, now)
	if err != nil {
		return fmt.Errorf("save analytics retry: %w", err)
	}
	if command.RowsAffected() != 1 {
		return ErrConflict
	}
	return nil
}

func (repository *PostgresRepository) Defer(
	ctx context.Context,
	targetID, leaseToken string,
	nextSyncAt, now time.Time,
) error {
	if !nextSyncAt.After(now) {
		return ErrInvalidArgument
	}
	command, err := repository.pool.Exec(ctx, `
		UPDATE f18_analytics_targets
		SET state = 'retry_wait',
			next_sync_at = $3,
			lease_token = NULL,
			locked_until = NULL,
			updated_at = $4
		WHERE id = $1
		  AND lease_token = $2
		  AND state = 'syncing'
	`, targetID, leaseToken, nextSyncAt, now)
	if err != nil {
		return fmt.Errorf("defer analytics target: %w", err)
	}
	if command.RowsAffected() != 1 {
		return ErrConflict
	}
	return nil
}

func (repository *PostgresRepository) SaveFailure(
	ctx context.Context,
	targetID, leaseToken string,
	observations []Observation,
	code string,
	now time.Time,
) error {
	if code == "" {
		return ErrInvalidArgument
	}
	if err := validateObservations(targetID, observations); err != nil {
		return err
	}
	transaction, err := repository.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin analytics failure: %w", err)
	}
	defer func() {
		_ = transaction.Rollback(ctx)
	}()
	if err := lockClaimedTarget(ctx, transaction, targetID, leaseToken); err != nil {
		return err
	}
	if err := insertObservations(ctx, transaction, observations); err != nil {
		return err
	}
	command, err := transaction.Exec(ctx, `
		UPDATE f18_analytics_targets
		SET state = 'failed',
			consecutive_failures = consecutive_failures + 1,
			lease_token = NULL,
			locked_until = NULL,
			last_error_code = $3,
			last_error_at = $4,
			updated_at = $4
		WHERE id = $1
		  AND lease_token = $2
		  AND state = 'syncing'
	`, targetID, leaseToken, code, now)
	if err != nil {
		return fmt.Errorf("save terminal analytics failure: %w", err)
	}
	if command.RowsAffected() != 1 {
		return ErrConflict
	}
	if err := transaction.Commit(ctx); err != nil {
		return fmt.Errorf("commit analytics failure: %w", err)
	}
	return nil
}

func (repository *PostgresRepository) Overview(
	ctx context.Context,
	query OverviewQuery,
) (Overview, error) {
	rows, err := repository.pool.Query(ctx, targetSelect+`
		WHERE workspace_id = $1
		  AND published_at >= $2
		  AND published_at < $3
		  AND (
			COALESCE(cardinality($4::text[]), 0) = 0
			OR channel_id = ANY($4::text[])
		  )
		ORDER BY channel_id, published_at, id
	`, query.WorkspaceID, query.From, query.To, query.ChannelIDs)
	if err != nil {
		return Overview{}, fmt.Errorf("query analytics overview targets: %w", err)
	}
	defer rows.Close()
	targets := make([]SyncTarget, 0)
	for rows.Next() {
		target, scanErr := scanTarget(rows)
		if scanErr != nil {
			return Overview{}, scanErr
		}
		targets = append(targets, target)
	}
	if err := rows.Err(); err != nil {
		return Overview{}, fmt.Errorf("iterate analytics overview targets: %w", err)
	}
	observations := make(map[string][]Observation, len(targets))
	for _, target := range targets {
		metricRows, queryErr := repository.pool.Query(ctx, `
			SELECT
				target_id,
				metric,
				original_name,
				period,
				observed_at,
				value,
				state,
				api_version,
				reason_code
			FROM f18_analytics_observations
			WHERE target_id = $1
			ORDER BY observed_at
		`, target.ID)
		if queryErr != nil {
			return Overview{}, fmt.Errorf("query analytics observations: %w", queryErr)
		}
		for metricRows.Next() {
			var observation Observation
			if err := metricRows.Scan(
				&observation.TargetID,
				&observation.Metric,
				&observation.OriginalName,
				&observation.Period,
				&observation.ObservedAt,
				&observation.Value,
				&observation.State,
				&observation.APIVersion,
				&observation.ReasonCode,
			); err != nil {
				metricRows.Close()
				return Overview{}, fmt.Errorf("scan analytics observation: %w", err)
			}
			observations[target.ID] = append(
				observations[target.ID],
				observation,
			)
		}
		if err := metricRows.Err(); err != nil {
			metricRows.Close()
			return Overview{}, fmt.Errorf("iterate analytics observations: %w", err)
		}
		metricRows.Close()
	}
	return summarize(query, targets, observations), nil
}

const targetSelect = `
	SELECT
		id,
		workspace_id,
		content_id,
		channel_id,
		channel_type,
		provider,
		connection_id,
		remote_id,
		published_at,
		cursor,
		state,
		attempt_count,
		consecutive_failures,
		next_sync_at,
		COALESCE(lease_token, ''),
		locked_until,
		COALESCE(last_error_code, ''),
		last_error_at,
		created_at,
		updated_at
	FROM f18_analytics_targets
`

type scanner interface {
	Scan(...any) error
}

func scanTarget(row scanner) (SyncTarget, error) {
	var target SyncTarget
	err := row.Scan(
		&target.ID,
		&target.WorkspaceID,
		&target.ContentID,
		&target.ChannelID,
		&target.ChannelType,
		&target.Provider,
		&target.ConnectionID,
		&target.RemoteID,
		&target.PublishedAt,
		&target.Cursor,
		&target.State,
		&target.AttemptCount,
		&target.ConsecutiveFailures,
		&target.NextSyncAt,
		&target.LeaseToken,
		&target.LockedUntil,
		&target.LastErrorCode,
		&target.LastErrorAt,
		&target.CreatedAt,
		&target.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return SyncTarget{}, ErrNotFound
	}
	if err != nil {
		return SyncTarget{}, fmt.Errorf("scan analytics target: %w", err)
	}
	return target, nil
}

func lockClaimedTarget(
	ctx context.Context,
	transaction pgx.Tx,
	targetID, leaseToken string,
) error {
	if targetID == "" || leaseToken == "" {
		return ErrInvalidArgument
	}
	var id string
	err := transaction.QueryRow(ctx, `
		SELECT id
		FROM f18_analytics_targets
		WHERE id = $1
		  AND lease_token = $2
		  AND state = 'syncing'
		FOR UPDATE
	`, targetID, leaseToken).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrConflict
	}
	if err != nil {
		return fmt.Errorf("lock claimed analytics target: %w", err)
	}
	return nil
}

func insertObservations(
	ctx context.Context,
	transaction pgx.Tx,
	observations []Observation,
) error {
	for _, observation := range observations {
		_, err := transaction.Exec(ctx, `
			INSERT INTO f18_analytics_observations (
				target_id,
				metric,
				original_name,
				period,
				observed_at,
				value,
				state,
				api_version,
				reason_code
			)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
			ON CONFLICT (
				target_id,
				metric,
				original_name,
				period,
				observed_at
			)
			DO UPDATE SET
				value = EXCLUDED.value,
				state = EXCLUDED.state,
				api_version = EXCLUDED.api_version,
				reason_code = EXCLUDED.reason_code
		`,
			observation.TargetID,
			string(observation.Metric),
			observation.OriginalName,
			observation.Period,
			observation.ObservedAt,
			observation.Value,
			string(observation.State),
			observation.APIVersion,
			observation.ReasonCode,
		)
		if err != nil {
			return mapPostgresError("insert analytics observation", err)
		}
	}
	return nil
}

func mapPostgresError(operation string, err error) error {
	var postgresError *pgconn.PgError
	if errors.As(err, &postgresError) {
		switch postgresError.Code {
		case "23505":
			return fmt.Errorf("%s: %w", operation, ErrConflict)
		case "23503":
			return fmt.Errorf("%s: %w", operation, ErrNotFound)
		case "23514", "23502", "22P02":
			return fmt.Errorf("%s: %w", operation, ErrInvalidArgument)
		}
	}
	return fmt.Errorf("%s: %w", operation, err)
}
