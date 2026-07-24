package smartqueue

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
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

func (repository *PostgresRepository) CreateQueue(
	ctx context.Context, queue Queue, maxQueues int,
) (Queue, error) {
	windows, err := json.Marshal(queue.Windows)
	if err != nil {
		return Queue{}, fmt.Errorf("encode smart queue windows: %w", err)
	}
	transaction, err := repository.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return Queue{}, fmt.Errorf("begin create smart queue: %w", err)
	}
	defer func() { _ = transaction.Rollback(ctx) }()
	if err := lockWorkspace(ctx, transaction, queue.WorkspaceID); err != nil {
		return Queue{}, err
	}
	var count int
	if err := transaction.QueryRow(
		ctx, `SELECT count(*) FROM f20_smart_queues WHERE workspace_id = $1`,
		queue.WorkspaceID,
	).Scan(&count); err != nil {
		return Queue{}, fmt.Errorf("count smart queues: %w", err)
	}
	if count >= maxQueues {
		return Queue{}, ErrCapacityExceeded
	}
	_, err = transaction.Exec(ctx, `
		INSERT INTO f20_smart_queues (
			id, workspace_id, name, time_zone, interval_minutes, horizon_days,
			windows, revision, created_by_account_id, created_at, updated_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
	`, queue.ID, queue.WorkspaceID, queue.Name, queue.TimeZone,
		queue.IntervalMinutes, queue.HorizonDays, windows, queue.Revision,
		queue.CreatedBy, queue.CreatedAt, queue.UpdatedAt,
	)
	if err != nil {
		return Queue{}, fmt.Errorf("insert smart queue: %w", err)
	}
	if err := transaction.Commit(ctx); err != nil {
		return Queue{}, fmt.Errorf("commit smart queue: %w", err)
	}
	return cloneQueue(queue), nil
}

func (repository *PostgresRepository) GetQueue(
	ctx context.Context, workspaceID, queueID string,
) (Queue, error) {
	return scanQueue(repository.pool.QueryRow(ctx, queueSelect+`
		WHERE workspace_id = $1 AND id = $2
	`, workspaceID, queueID))
}

func (repository *PostgresRepository) UpdateQueue(
	ctx context.Context, queue Queue, expectedRevision int64,
) (Queue, error) {
	windows, err := json.Marshal(queue.Windows)
	if err != nil {
		return Queue{}, fmt.Errorf("encode smart queue windows: %w", err)
	}
	tag, err := repository.pool.Exec(ctx, `
		UPDATE f20_smart_queues
		SET name = $4, time_zone = $5, interval_minutes = $6,
			horizon_days = $7, windows = $8, revision = $9, updated_at = $10
		WHERE workspace_id = $1 AND id = $2 AND revision = $3
	`, queue.WorkspaceID, queue.ID, expectedRevision, queue.Name, queue.TimeZone,
		queue.IntervalMinutes, queue.HorizonDays, windows, queue.Revision, queue.UpdatedAt,
	)
	if err != nil {
		return Queue{}, fmt.Errorf("update smart queue: %w", err)
	}
	if tag.RowsAffected() != 1 {
		if _, getErr := repository.GetQueue(ctx, queue.WorkspaceID, queue.ID); errors.Is(getErr, ErrNotFound) {
			return Queue{}, ErrNotFound
		}
		return Queue{}, ErrConflict
	}
	return cloneQueue(queue), nil
}

func (repository *PostgresRepository) ListReservedInstants(
	ctx context.Context, workspaceID, queueID string, from, until time.Time,
) ([]time.Time, error) {
	rows, err := repository.pool.Query(ctx, `
		SELECT starts_at_utc
		FROM f20_slot_reservations
		WHERE workspace_id = $1 AND queue_id = $2
		  AND starts_at_utc >= $3 AND starts_at_utc <= $4
		ORDER BY starts_at_utc, id
	`, workspaceID, queueID, from, until)
	if err != nil {
		return nil, fmt.Errorf("list smart queue reservations: %w", err)
	}
	defer rows.Close()
	result := make([]time.Time, 0)
	for rows.Next() {
		var instant time.Time
		if err := rows.Scan(&instant); err != nil {
			return nil, fmt.Errorf("scan smart queue reservation instant: %w", err)
		}
		result = append(result, instant.UTC())
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate smart queue reservations: %w", err)
	}
	return result, nil
}

func (repository *PostgresRepository) CreatePreview(
	ctx context.Context, preview Preview,
) error {
	_, err := repository.pool.Exec(ctx, `
		INSERT INTO f20_slot_previews (
			token, workspace_id, queue_id, queue_revision, starts_at_utc,
			local_date_time, time_zone, utc_offset_minutes, not_before_utc,
			search_until_utc, created_at, expires_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)
	`, preview.Token, preview.WorkspaceID, preview.QueueID, preview.QueueRevision,
		preview.Slot.StartsAtUTC, preview.Slot.LocalDateTime, preview.Slot.TimeZone,
		preview.Slot.UTCOffsetMinutes, preview.NotBeforeUTC, preview.SearchUntilUTC,
		preview.CreatedAt, preview.ExpiresAt,
	)
	if err != nil {
		return fmt.Errorf("insert smart queue preview: %w", err)
	}
	return nil
}

func (repository *PostgresRepository) Confirm(
	ctx context.Context, request ConfirmRequest,
) (Confirmation, error) {
	transaction, err := repository.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return Confirmation{}, fmt.Errorf("begin smart queue confirmation: %w", err)
	}
	defer func() { _ = transaction.Rollback(ctx) }()

	existing, existingCommand, existingHash, found, err := findIdempotentConfirmation(
		ctx, transaction, request.Reservation.WorkspaceID, request.Reservation.IdempotencyKey,
	)
	if err != nil {
		return Confirmation{}, err
	}
	if found {
		if existingHash != request.ConfirmationHash {
			return Confirmation{}, ErrIdempotencyReplay
		}
		return Confirmation{
			Reservation: existing, SchedulingCommand: existingCommand,
		}, nil
	}

	preview, err := lockPreview(
		ctx, transaction, request.Preview.WorkspaceID,
		request.Preview.QueueID, request.Preview.Token,
	)
	if err != nil {
		return Confirmation{}, err
	}
	if preview.ConfirmedAt != nil {
		existing, existingCommand, existingHash, found, findErr := findIdempotentConfirmation(
			ctx, transaction, request.Reservation.WorkspaceID,
			request.Reservation.IdempotencyKey,
		)
		if findErr != nil {
			return Confirmation{}, findErr
		}
		if found {
			if existingHash != request.ConfirmationHash {
				return Confirmation{}, ErrIdempotencyReplay
			}
			return Confirmation{
				Reservation: existing, SchedulingCommand: existingCommand,
			}, nil
		}
		return Confirmation{}, ErrPreviewConsumed
	}
	if !request.Reservation.CreatedAt.Before(preview.ExpiresAt) {
		return Confirmation{}, ErrPreviewExpired
	}
	var queueRevision int64
	if err := transaction.QueryRow(ctx, `
		SELECT revision FROM f20_smart_queues
		WHERE workspace_id = $1 AND id = $2
		FOR SHARE
	`, preview.WorkspaceID, preview.QueueID).Scan(&queueRevision); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Confirmation{}, ErrNotFound
		}
		return Confirmation{}, fmt.Errorf("read smart queue revision: %w", err)
	}
	if queueRevision != preview.QueueRevision {
		return Confirmation{}, ErrQueueChanged
	}
	if err := lockWorkspace(ctx, transaction, preview.WorkspaceID); err != nil {
		return Confirmation{}, err
	}
	// A different preview using the same idempotency key may have committed
	// while this transaction waited for the workspace lock.
	existing, existingCommand, existingHash, found, err = findIdempotentConfirmation(
		ctx, transaction, request.Reservation.WorkspaceID,
		request.Reservation.IdempotencyKey,
	)
	if err != nil {
		return Confirmation{}, err
	}
	if found {
		if existingHash != request.ConfirmationHash {
			return Confirmation{}, ErrIdempotencyReplay
		}
		return Confirmation{
			Reservation: existing, SchedulingCommand: existingCommand,
		}, nil
	}
	var pending int
	if err := transaction.QueryRow(ctx, `
		SELECT count(*)
		FROM f20_scheduling_commands
		WHERE workspace_id = $1 AND state = 'pending'
	`, preview.WorkspaceID).Scan(&pending); err != nil {
		return Confirmation{}, fmt.Errorf("count pending smart queue reservations: %w", err)
	}
	if pending >= request.MaxPendingReservations {
		return Confirmation{}, ErrCapacityExceeded
	}

	reservation := cloneReservation(request.Reservation)
	reservation.Slot = preview.Slot
	var insertedID string
	err = transaction.QueryRow(ctx, `
		INSERT INTO f20_slot_reservations (
			id, workspace_id, queue_id, draft_id, channel_ids, starts_at_utc,
			local_date_time, time_zone, utc_offset_minutes, idempotency_key,
			confirmation_hash, created_by_account_id, created_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)
		ON CONFLICT DO NOTHING
		RETURNING id
	`, reservation.ID, reservation.WorkspaceID, reservation.QueueID,
		reservation.DraftID, reservation.ChannelIDs, reservation.Slot.StartsAtUTC,
		reservation.Slot.LocalDateTime, reservation.Slot.TimeZone,
		reservation.Slot.UTCOffsetMinutes, reservation.IdempotencyKey,
		request.ConfirmationHash, reservation.CreatedBy, reservation.CreatedAt,
	).Scan(&insertedID)
	if errors.Is(err, pgx.ErrNoRows) {
		return Confirmation{}, ErrSlotUnavailable
	}
	if err != nil {
		return Confirmation{}, fmt.Errorf("insert smart queue reservation: %w", err)
	}

	command := cloneSchedulingCommand(request.SchedulingCommand)
	command.ScheduledAt = preview.Slot
	_, err = transaction.Exec(ctx, `
		INSERT INTO f20_scheduling_commands (
			id, reservation_id, workspace_id, draft_id, channel_ids,
			starts_at_utc, local_date_time, time_zone, utc_offset_minutes,
			state, idempotency_key, created_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)
	`, command.ID, command.ReservationID, command.WorkspaceID, command.DraftID,
		command.ChannelIDs, command.ScheduledAt.StartsAtUTC,
		command.ScheduledAt.LocalDateTime, command.ScheduledAt.TimeZone,
		command.ScheduledAt.UTCOffsetMinutes, command.State,
		command.IdempotencyKey, command.CreatedAt,
	)
	if err != nil {
		return Confirmation{}, fmt.Errorf("insert F7 scheduling command: %w", err)
	}
	tag, err := transaction.Exec(ctx, `
		UPDATE f20_slot_previews
		SET confirmed_at = $4, reservation_id = $5,
			idempotency_key = $6, confirmation_hash = $7
		WHERE workspace_id = $1 AND queue_id = $2 AND token = $3
		  AND confirmed_at IS NULL
	`, preview.WorkspaceID, preview.QueueID, preview.Token,
		reservation.CreatedAt, reservation.ID, reservation.IdempotencyKey,
		request.ConfirmationHash,
	)
	if err != nil {
		return Confirmation{}, fmt.Errorf("consume smart queue preview: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return Confirmation{}, ErrPreviewConsumed
	}
	if err := transaction.Commit(ctx); err != nil {
		return Confirmation{}, fmt.Errorf("commit smart queue confirmation: %w", err)
	}
	return Confirmation{
		Reservation: reservation, SchedulingCommand: command,
	}, nil
}

func (repository *PostgresRepository) MarkSchedulingCommandSent(
	ctx context.Context, workspaceID, commandID string,
) error {
	tag, err := repository.pool.Exec(ctx, `
		UPDATE f20_scheduling_commands
		SET state = 'sent', sent_at = now()
		WHERE workspace_id = $1 AND id = $2 AND state = 'pending'
	`, workspaceID, commandID)
	if err != nil {
		return fmt.Errorf("mark F7 scheduling command sent: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return ErrConflict
	}
	return nil
}

const queueSelect = `
	SELECT id, workspace_id, name, time_zone, interval_minutes, horizon_days,
		windows, revision, created_by_account_id, created_at, updated_at
	FROM f20_smart_queues
`

type rowScanner interface {
	Scan(...any) error
}

func scanQueue(row rowScanner) (Queue, error) {
	var queue Queue
	var windows []byte
	err := row.Scan(
		&queue.ID, &queue.WorkspaceID, &queue.Name, &queue.TimeZone,
		&queue.IntervalMinutes, &queue.HorizonDays, &windows, &queue.Revision,
		&queue.CreatedBy, &queue.CreatedAt, &queue.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return Queue{}, ErrNotFound
	}
	if err != nil {
		return Queue{}, fmt.Errorf("scan smart queue: %w", err)
	}
	if err := json.Unmarshal(windows, &queue.Windows); err != nil {
		return Queue{}, fmt.Errorf("decode smart queue windows: %w", err)
	}
	queue.CreatedAt, queue.UpdatedAt = queue.CreatedAt.UTC(), queue.UpdatedAt.UTC()
	return queue, nil
}

func lockPreview(
	ctx context.Context,
	transaction pgx.Tx,
	workspaceID, queueID, token string,
) (Preview, error) {
	var preview Preview
	err := transaction.QueryRow(ctx, `
		SELECT token, workspace_id, queue_id, queue_revision, starts_at_utc,
			local_date_time, time_zone, utc_offset_minutes, not_before_utc,
			search_until_utc, created_at, expires_at, confirmed_at,
			coalesce(reservation_id, ''), coalesce(idempotency_key, ''),
			coalesce(confirmation_hash, '')
		FROM f20_slot_previews
		WHERE workspace_id = $1 AND queue_id = $2 AND token = $3
		FOR UPDATE
	`, workspaceID, queueID, token).Scan(
		&preview.Token, &preview.WorkspaceID, &preview.QueueID, &preview.QueueRevision,
		&preview.Slot.StartsAtUTC, &preview.Slot.LocalDateTime, &preview.Slot.TimeZone,
		&preview.Slot.UTCOffsetMinutes, &preview.NotBeforeUTC, &preview.SearchUntilUTC,
		&preview.CreatedAt, &preview.ExpiresAt, &preview.ConfirmedAt,
		&preview.ReservationID, &preview.IdempotencyKey, &preview.ConfirmationHash,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return Preview{}, ErrNotFound
	}
	if err != nil {
		return Preview{}, fmt.Errorf("lock smart queue preview: %w", err)
	}
	return preview, nil
}

func findIdempotentConfirmation(
	ctx context.Context,
	transaction pgx.Tx,
	workspaceID, idempotencyKey string,
) (Reservation, SchedulingCommand, string, bool, error) {
	var reservation Reservation
	var command SchedulingCommand
	var hash string
	err := transaction.QueryRow(ctx, `
		SELECT r.id, r.workspace_id, r.queue_id, r.draft_id, r.channel_ids,
			r.starts_at_utc, r.local_date_time, r.time_zone,
			r.utc_offset_minutes, r.idempotency_key, r.confirmation_hash,
			r.created_by_account_id, r.created_at,
			c.id, c.reservation_id, c.workspace_id, c.draft_id, c.channel_ids,
			c.starts_at_utc, c.local_date_time, c.time_zone,
			c.utc_offset_minutes, c.state, c.idempotency_key, c.created_at
		FROM f20_slot_reservations r
		JOIN f20_scheduling_commands c ON c.reservation_id = r.id
		WHERE r.workspace_id = $1 AND r.idempotency_key = $2
	`, workspaceID, idempotencyKey).Scan(
		&reservation.ID, &reservation.WorkspaceID, &reservation.QueueID,
		&reservation.DraftID, &reservation.ChannelIDs, &reservation.Slot.StartsAtUTC,
		&reservation.Slot.LocalDateTime, &reservation.Slot.TimeZone,
		&reservation.Slot.UTCOffsetMinutes, &reservation.IdempotencyKey, &hash,
		&reservation.CreatedBy, &reservation.CreatedAt,
		&command.ID, &command.ReservationID, &command.WorkspaceID,
		&command.DraftID, &command.ChannelIDs, &command.ScheduledAt.StartsAtUTC,
		&command.ScheduledAt.LocalDateTime, &command.ScheduledAt.TimeZone,
		&command.ScheduledAt.UTCOffsetMinutes, &command.State,
		&command.IdempotencyKey, &command.CreatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return Reservation{}, SchedulingCommand{}, "", false, nil
	}
	if err != nil {
		return Reservation{}, SchedulingCommand{}, "", false,
			fmt.Errorf("read idempotent smart queue confirmation: %w", err)
	}
	return reservation, command, hash, true, nil
}

func lockWorkspace(ctx context.Context, transaction pgx.Tx, workspaceID string) error {
	if _, err := transaction.Exec(
		ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 20))`, workspaceID,
	); err != nil {
		return fmt.Errorf("lock smart queue workspace: %w", err)
	}
	return nil
}
