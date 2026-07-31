package scheduling

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

type PostgresRepository struct {
	database *sql.DB
}

func NewPostgresRepository(database *sql.DB) (*PostgresRepository, error) {
	if database == nil {
		return nil, fmt.Errorf("%w: postgres database is required", ErrInvalidArgument)
	}
	return &PostgresRepository{database: database}, nil
}

func (repository *PostgresRepository) ReserveOperation(
	ctx context.Context,
	candidate IdempotencyOperation,
	now time.Time,
) (IdempotencyOperation, error) {
	if candidate.ResponseSnapshotStatus == "" {
		candidate.ResponseSnapshotStatus = ResponseSnapshotPending
	}
	if candidate.Kind == OperationDuplicate && candidate.DownstreamIdempotencyKey == "" {
		candidate.DownstreamIdempotencyKey = deriveComposerDuplicateIdempotencyKey(candidate)
	}
	transaction, err := repository.database.BeginTx(ctx, nil)
	if err != nil {
		return IdempotencyOperation{}, fmt.Errorf("begin idempotency reservation: %w", err)
	}
	defer func() { _ = transaction.Rollback() }()
	result, err := transaction.ExecContext(ctx, `
		INSERT INTO f07_idempotency_operations (
			workspace_id, operation_kind, idempotency_key, payload_fingerprint,
			downstream_idempotency_key, response_snapshot_status,
			state, post_id, publication_command_id,
			lease_generation, locked_until, created_at, updated_at
		) VALUES ($1, $2, $3, $4, NULLIF($5, ''), $6, 'reserved', $7, $8, 1, $9, $10, $10)
		ON CONFLICT (workspace_id, operation_kind, idempotency_key) DO NOTHING
	`, candidate.WorkspaceID, string(candidate.Kind), candidate.IdempotencyKey,
		candidate.PayloadFingerprint, candidate.DownstreamIdempotencyKey,
		string(candidate.ResponseSnapshotStatus), candidate.PostID,
		candidate.PublicationCommandID, now.Add(operationLease), now)
	if err != nil {
		return IdempotencyOperation{}, fmt.Errorf("insert idempotency reservation: %w", err)
	}
	inserted, _ := result.RowsAffected()
	stored, err := lockOperation(ctx, transaction, candidate.WorkspaceID, candidate.Kind, candidate.IdempotencyKey)
	if err != nil {
		return IdempotencyOperation{}, err
	}
	if stored.PayloadFingerprint != candidate.PayloadFingerprint {
		return IdempotencyOperation{}, ErrIdempotencyMismatch
	}
	if stored.State == OperationCompleted {
		if err := transaction.Commit(); err != nil {
			return IdempotencyOperation{}, fmt.Errorf("commit idempotency replay: %w", err)
		}
		return stored, nil
	}
	if inserted == 0 {
		if stored.LockedUntil.After(now) {
			return IdempotencyOperation{}, ErrOperationInProgress
		}
		stored.LeaseGeneration++
		stored.LockedUntil = now.Add(operationLease)
		stored.UpdatedAt = now
		result, err = transaction.ExecContext(ctx, `
			UPDATE f07_idempotency_operations
			SET lease_generation = $4, locked_until = $5, updated_at = $6
			WHERE workspace_id = $1 AND operation_kind = $2 AND idempotency_key = $3
			  AND state <> 'completed'
		`, stored.WorkspaceID, string(stored.Kind), stored.IdempotencyKey,
			stored.LeaseGeneration, stored.LockedUntil, now)
		if err != nil {
			return IdempotencyOperation{}, fmt.Errorf("claim idempotency reservation: %w", err)
		}
		if affected, _ := result.RowsAffected(); affected != 1 {
			return IdempotencyOperation{}, ErrOperationInProgress
		}
	}
	if err := transaction.Commit(); err != nil {
		return IdempotencyOperation{}, fmt.Errorf("commit idempotency reservation: %w", err)
	}
	return stored, nil
}

func (repository *PostgresRepository) ReleaseOperation(
	ctx context.Context,
	operation IdempotencyOperation,
	now time.Time,
) error {
	_, err := repository.database.ExecContext(ctx, `
		UPDATE f07_idempotency_operations
		SET locked_until = $5, updated_at = $5
		WHERE workspace_id = $1 AND operation_kind = $2 AND idempotency_key = $3
		  AND lease_generation = $4 AND state <> 'completed'
	`, operation.WorkspaceID, string(operation.Kind), operation.IdempotencyKey,
		operation.LeaseGeneration, now)
	if err != nil {
		return fmt.Errorf("release idempotency reservation: %w", err)
	}
	return nil
}

func (repository *PostgresRepository) PrepareDuplicateOperation(
	ctx context.Context,
	operation IdempotencyOperation,
	source ScheduledPost,
	schedule resolvedSchedule,
	now time.Time,
) (IdempotencyOperation, error) {
	transaction, err := repository.database.BeginTx(ctx, nil)
	if err != nil {
		return IdempotencyOperation{}, fmt.Errorf("begin duplicate preparation: %w", err)
	}
	defer func() { _ = transaction.Rollback() }()
	stored, err := lockOperation(
		ctx,
		transaction,
		operation.WorkspaceID,
		operation.Kind,
		operation.IdempotencyKey,
	)
	if err != nil {
		return IdempotencyOperation{}, err
	}
	if stored.State != OperationReserved ||
		stored.LeaseGeneration != operation.LeaseGeneration {
		return IdempotencyOperation{}, ErrOperationInProgress
	}
	current, err := lockPost(ctx, transaction, operation.WorkspaceID, source.ID)
	if err != nil {
		return IdempotencyOperation{}, err
	}
	if current.Revision != source.Revision || current.Status != StatusScheduled {
		return IdempotencyOperation{}, ErrConflict
	}
	result, err := transaction.ExecContext(ctx, `
		UPDATE f07_idempotency_operations
		SET state = 'prepared', source_post_id = $5, source_post_revision = $6,
			source_draft_id = $7, source_draft_revision = $8, channel_ids = $9,
			scheduled_for_utc = $10, scheduled_local = $11,
			scheduled_timezone = $12, scheduled_utc_offset_minutes = $13,
			updated_at = $14
		WHERE workspace_id = $1 AND operation_kind = $2 AND idempotency_key = $3
		  AND lease_generation = $4 AND state = 'reserved'
	`, operation.WorkspaceID, string(operation.Kind), operation.IdempotencyKey,
		operation.LeaseGeneration, current.ID, current.Revision, current.DraftID,
		current.DraftRevision, current.ChannelIDs, schedule.utc, schedule.local,
		schedule.timeZone, schedule.offsetMinutes, now)
	if err != nil {
		return IdempotencyOperation{}, fmt.Errorf("prepare duplicate operation: %w", err)
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return IdempotencyOperation{}, ErrOperationInProgress
	}
	stored.State = OperationPrepared
	stored.SourcePostID = current.ID
	stored.SourcePostRevision = current.Revision
	stored.SourceDraftID = current.DraftID
	stored.SourceDraftRevision = current.DraftRevision
	stored.ChannelIDs = append([]string(nil), current.ChannelIDs...)
	stored.Schedule = schedule
	stored.UpdatedAt = now
	if err := transaction.Commit(); err != nil {
		return IdempotencyOperation{}, fmt.Errorf("commit duplicate preparation: %w", err)
	}
	return stored, nil
}

func (repository *PostgresRepository) RecordDuplicateClone(
	ctx context.Context,
	operation IdempotencyOperation,
	clone DuplicatedDraft,
	now time.Time,
) (IdempotencyOperation, error) {
	result, err := repository.database.ExecContext(ctx, `
		UPDATE f07_idempotency_operations
		SET state = 'clone_created', clone_draft_id = $5,
			clone_draft_revision = $6, updated_at = $7
		WHERE workspace_id = $1 AND operation_kind = $2 AND idempotency_key = $3
		  AND lease_generation = $4 AND state = 'prepared'
	`, operation.WorkspaceID, string(operation.Kind), operation.IdempotencyKey,
		operation.LeaseGeneration, clone.DraftID, clone.DraftRevision, now)
	if err != nil {
		return IdempotencyOperation{}, fmt.Errorf("record duplicated draft recovery point: %w", err)
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return IdempotencyOperation{}, ErrOperationInProgress
	}
	operation.State = OperationCloneCreated
	operation.CloneDraftID = clone.DraftID
	operation.CloneDraftRevision = clone.DraftRevision
	operation.UpdatedAt = now
	return operation, nil
}

func (repository *PostgresRepository) CompleteScheduleOperation(
	ctx context.Context,
	operation IdempotencyOperation,
	post ScheduledPost,
	command PublicationCommand,
	now time.Time,
) (ScheduledPost, error) {
	return repository.completeOperation(ctx, operation, OperationReserved, post, command, now)
}

func (repository *PostgresRepository) CompleteDuplicateOperation(
	ctx context.Context,
	operation IdempotencyOperation,
	post ScheduledPost,
	command PublicationCommand,
	now time.Time,
) (ScheduledPost, error) {
	return repository.completeOperation(ctx, operation, OperationCloneCreated, post, command, now)
}

func (repository *PostgresRepository) completeOperation(
	ctx context.Context,
	operation IdempotencyOperation,
	wantState OperationState,
	post ScheduledPost,
	command PublicationCommand,
	now time.Time,
) (ScheduledPost, error) {
	if err := validateAtomicPair(post, command); err != nil {
		return ScheduledPost{}, err
	}
	transaction, err := repository.database.BeginTx(ctx, nil)
	if err != nil {
		return ScheduledPost{}, fmt.Errorf("begin idempotent scheduling completion: %w", err)
	}
	defer func() { _ = transaction.Rollback() }()
	stored, err := lockOperation(ctx, transaction, operation.WorkspaceID, operation.Kind, operation.IdempotencyKey)
	if err != nil {
		return ScheduledPost{}, err
	}
	if stored.State != wantState || stored.LeaseGeneration != operation.LeaseGeneration {
		return ScheduledPost{}, ErrOperationInProgress
	}
	if post.ID != stored.PostID || command.ID != stored.PublicationCommandID {
		return ScheduledPost{}, ErrInvalidArgument
	}
	if wantState == OperationCloneCreated &&
		(post.DuplicatedFromPostID != stored.SourcePostID ||
			post.DraftID != stored.CloneDraftID ||
			post.DraftRevision != stored.CloneDraftRevision ||
			!equalStrings(post.ChannelIDs, stored.ChannelIDs) ||
			!post.ScheduledForUTC.Equal(stored.Schedule.utc)) {
		return ScheduledPost{}, ErrInvalidArgument
	}
	if wantState == OperationCloneCreated {
		source, sourceErr := lockPost(
			ctx,
			transaction,
			stored.WorkspaceID,
			stored.SourcePostID,
		)
		if sourceErr != nil {
			return ScheduledPost{}, sourceErr
		}
		if source.Revision != stored.SourcePostRevision ||
			source.Status != StatusScheduled {
			return ScheduledPost{}, ErrConflict
		}
	}
	if err := insertPost(ctx, transaction, post); err != nil {
		return ScheduledPost{}, err
	}
	if err := insertPublicationCommand(ctx, transaction, command); err != nil {
		return ScheduledPost{}, err
	}
	created, err := scanScheduledPost(transaction.QueryRowContext(ctx, postSelect+`
		WHERE workspace_id = $1 AND id = $2
	`, post.WorkspaceID, post.ID))
	if err != nil {
		return ScheduledPost{}, err
	}
	responseSnapshot, err := json.Marshal(scheduledPostView(created))
	if err != nil {
		return ScheduledPost{}, fmt.Errorf("encode idempotency response snapshot: %w", err)
	}
	result, err := transaction.ExecContext(ctx, `
		UPDATE f07_idempotency_operations
		SET state = 'completed', locked_until = NULL, updated_at = $5,
			completed_at = $5, response_snapshot = $7::jsonb,
			response_snapshot_status = 'available'
		WHERE workspace_id = $1 AND operation_kind = $2 AND idempotency_key = $3
		  AND lease_generation = $4 AND state = $6
	`, operation.WorkspaceID, string(operation.Kind), operation.IdempotencyKey,
		operation.LeaseGeneration, now, string(wantState), responseSnapshot)
	if err != nil {
		return ScheduledPost{}, fmt.Errorf("complete idempotency reservation: %w", err)
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return ScheduledPost{}, ErrOperationInProgress
	}
	if err := transaction.Commit(); err != nil {
		return ScheduledPost{}, fmt.Errorf("commit idempotent scheduling completion: %w", err)
	}
	return clonePost(created), nil
}

func (repository *PostgresRepository) Create(
	ctx context.Context,
	post ScheduledPost,
	command PublicationCommand,
) (ScheduledPost, error) {
	if err := validateAtomicPair(post, command); err != nil {
		return ScheduledPost{}, err
	}
	transaction, err := repository.database.BeginTx(ctx, nil)
	if err != nil {
		return ScheduledPost{}, fmt.Errorf("begin schedule transaction: %w", err)
	}
	defer func() {
		_ = transaction.Rollback()
	}()
	if err := insertPost(ctx, transaction, post); err != nil {
		return ScheduledPost{}, err
	}
	if err := insertPublicationCommand(ctx, transaction, command); err != nil {
		return ScheduledPost{}, err
	}
	created, err := scanScheduledPost(transaction.QueryRowContext(ctx, postSelect+`
		WHERE workspace_id = $1 AND id = $2
	`, post.WorkspaceID, post.ID))
	if err != nil {
		return ScheduledPost{}, err
	}
	if err := transaction.Commit(); err != nil {
		return ScheduledPost{}, fmt.Errorf("commit schedule transaction: %w", err)
	}
	return clonePost(created), nil
}

func (repository *PostgresRepository) Get(
	ctx context.Context,
	workspaceID, postID string,
) (ScheduledPost, error) {
	return scanScheduledPost(repository.database.QueryRowContext(ctx, postSelect+`
		WHERE workspace_id = $1 AND id = $2
	`, workspaceID, postID))
}

func (repository *PostgresRepository) List(
	ctx context.Context,
	workspaceID string,
	filter CalendarFilter,
) ([]ScheduledPost, error) {
	rows, err := repository.database.QueryContext(ctx, postSelect+`
		WHERE workspace_id = $1
		  AND scheduled_for_utc >= $2
		  AND scheduled_for_utc < $3
		  AND ($4 = '' OR status = $4)
		  AND ($5 = '' OR $5 = ANY(channel_ids))
		ORDER BY scheduled_for_utc, id
	`,
		workspaceID,
		filter.FromUTC,
		filter.UntilUTC,
		string(filter.Status),
		filter.ChannelID,
	)
	if err != nil {
		return nil, fmt.Errorf("list scheduling calendar: %w", err)
	}
	defer rows.Close()

	posts := make([]ScheduledPost, 0)
	for rows.Next() {
		post, scanErr := scanScheduledPost(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		posts = append(posts, post)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate scheduling calendar: %w", err)
	}
	return posts, nil
}

func (repository *PostgresRepository) Replace(
	ctx context.Context,
	replacement ScheduledPost,
	expectedRevision int64,
	command PublicationCommand,
) (ScheduledPost, error) {
	if err := validateAtomicPair(replacement, command); err != nil {
		return ScheduledPost{}, err
	}
	transaction, err := repository.database.BeginTx(ctx, nil)
	if err != nil {
		return ScheduledPost{}, fmt.Errorf("begin replace schedule transaction: %w", err)
	}
	defer func() {
		_ = transaction.Rollback()
	}()
	current, err := lockPost(ctx, transaction, replacement.WorkspaceID, replacement.ID)
	if err != nil {
		return ScheduledPost{}, err
	}
	if current.Revision != expectedRevision {
		return ScheduledPost{}, ErrConflict
	}
	if current.Status != StatusScheduled {
		return ScheduledPost{}, ErrImmutable
	}
	if replacement.Revision != expectedRevision+1 ||
		!replacement.CreatedAt.Equal(current.CreatedAt) ||
		replacement.CreatedBy != current.CreatedBy ||
		replacement.DuplicatedFromPostID != current.DuplicatedFromPostID {
		return ScheduledPost{}, ErrInvalidArgument
	}
	if err := invalidatePostCommand(
		ctx,
		transaction,
		current.ActiveCommandID,
		replacement.UpdatedAt,
	); err != nil {
		return ScheduledPost{}, err
	}
	result, err := transaction.ExecContext(ctx, `
		UPDATE f07_scheduled_posts
		SET draft_id = $4,
			draft_revision = $5,
			channel_ids = $6,
			scheduled_for_utc = $7,
			scheduled_local = $8,
			scheduled_timezone = $9,
			scheduled_utc_offset_minutes = $10,
			revision = $11,
			active_command_id = $12,
			updated_at = $13
		WHERE workspace_id = $1 AND id = $2 AND revision = $3
	`,
		replacement.WorkspaceID,
		replacement.ID,
		expectedRevision,
		replacement.DraftID,
		replacement.DraftRevision,
		replacement.ChannelIDs,
		replacement.ScheduledForUTC,
		replacement.ScheduledLocal,
		replacement.TimeZone,
		replacement.UTCOffsetMinutes,
		replacement.Revision,
		replacement.ActiveCommandID,
		replacement.UpdatedAt,
	)
	if err != nil {
		return ScheduledPost{}, fmt.Errorf("replace scheduled post: %w", err)
	}
	rowsAffected, rowsErr := result.RowsAffected()
	if rowsErr != nil {
		return ScheduledPost{}, fmt.Errorf("count replaced scheduled posts: %w", rowsErr)
	}
	if rowsAffected != 1 {
		return ScheduledPost{}, ErrConflict
	}
	if err := insertPublicationCommand(ctx, transaction, command); err != nil {
		return ScheduledPost{}, err
	}
	if err := transaction.Commit(); err != nil {
		return ScheduledPost{}, fmt.Errorf("commit replace schedule transaction: %w", err)
	}
	return clonePost(replacement), nil
}

func (repository *PostgresRepository) Cancel(
	ctx context.Context,
	workspaceID, postID string,
	expectedRevision int64,
	now time.Time,
) (ScheduledPost, error) {
	transaction, err := repository.database.BeginTx(ctx, nil)
	if err != nil {
		return ScheduledPost{}, fmt.Errorf("begin cancel schedule transaction: %w", err)
	}
	defer func() {
		_ = transaction.Rollback()
	}()
	current, err := lockPost(ctx, transaction, workspaceID, postID)
	if err != nil {
		return ScheduledPost{}, err
	}
	if current.Revision != expectedRevision {
		return ScheduledPost{}, ErrConflict
	}
	if current.Status != StatusScheduled {
		return ScheduledPost{}, ErrImmutable
	}
	if err := invalidatePostCommand(
		ctx,
		transaction,
		current.ActiveCommandID,
		now,
	); err != nil {
		return ScheduledPost{}, err
	}
	updated := clonePost(current)
	updated.Status = StatusCancelled
	updated.Revision++
	updated.ActiveCommandID = ""
	updated.UpdatedAt = now
	cancelledAt := now
	updated.CancelledAt = &cancelledAt
	result, err := transaction.ExecContext(ctx, `
		UPDATE f07_scheduled_posts
		SET status = 'cancelled',
			revision = revision + 1,
			active_command_id = NULL,
			updated_at = $4,
			cancelled_at = $4
		WHERE workspace_id = $1 AND id = $2 AND revision = $3
	`, workspaceID, postID, expectedRevision, now)
	if err != nil {
		return ScheduledPost{}, fmt.Errorf("cancel scheduled post: %w", err)
	}
	rowsAffected, rowsErr := result.RowsAffected()
	if rowsErr != nil {
		return ScheduledPost{}, fmt.Errorf("count cancelled scheduled posts: %w", rowsErr)
	}
	if rowsAffected != 1 {
		return ScheduledPost{}, ErrConflict
	}
	if err := transaction.Commit(); err != nil {
		return ScheduledPost{}, fmt.Errorf("commit cancel schedule transaction: %w", err)
	}
	return updated, nil
}

func (repository *PostgresRepository) Duplicate(
	ctx context.Context,
	workspaceID, sourcePostID string,
	expectedRevision int64,
	duplicate ScheduledPost,
	command PublicationCommand,
) (ScheduledPost, error) {
	if err := validateAtomicPair(duplicate, command); err != nil {
		return ScheduledPost{}, err
	}
	transaction, err := repository.database.BeginTx(ctx, nil)
	if err != nil {
		return ScheduledPost{}, fmt.Errorf("begin duplicate schedule transaction: %w", err)
	}
	defer func() {
		_ = transaction.Rollback()
	}()
	source, err := lockPost(ctx, transaction, workspaceID, sourcePostID)
	if err != nil {
		return ScheduledPost{}, err
	}
	if source.Revision != expectedRevision {
		return ScheduledPost{}, ErrConflict
	}
	if source.Status != StatusScheduled {
		return ScheduledPost{}, ErrImmutable
	}
	if duplicate.DuplicatedFromPostID != source.ID {
		return ScheduledPost{}, ErrInvalidArgument
	}
	if err := insertPost(ctx, transaction, duplicate); err != nil {
		return ScheduledPost{}, err
	}
	if err := insertPublicationCommand(ctx, transaction, command); err != nil {
		return ScheduledPost{}, err
	}
	if err := transaction.Commit(); err != nil {
		return ScheduledPost{}, fmt.Errorf("commit duplicate schedule transaction: %w", err)
	}
	return clonePost(duplicate), nil
}

func (repository *PostgresRepository) GetPublicationCommand(
	ctx context.Context,
	workspaceID, commandID string,
) (PublicationCommand, error) {
	return scanPublicationCommand(repository.database.QueryRowContext(ctx, commandSelect+`
		WHERE workspace_id = $1 AND id = $2
	`, workspaceID, commandID))
}

func (repository *PostgresRepository) ListPublicationCommands(
	ctx context.Context,
	workspaceID, postID string,
) ([]PublicationCommand, error) {
	rows, err := repository.database.QueryContext(ctx, commandSelect+`
		WHERE workspace_id = $1 AND post_id = $2
		ORDER BY generation
	`, workspaceID, postID)
	if err != nil {
		return nil, fmt.Errorf("list publication commands: %w", err)
	}
	defer rows.Close()
	commands := make([]PublicationCommand, 0)
	for rows.Next() {
		command, scanErr := scanPublicationCommand(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		commands = append(commands, command)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate publication commands: %w", err)
	}
	return commands, nil
}

const postSelect = `
	SELECT
		id,
		workspace_id,
		draft_id,
		draft_revision,
		channel_ids,
		status,
		scheduled_for_utc,
		scheduled_local,
		scheduled_timezone,
		scheduled_utc_offset_minutes,
		revision,
		COALESCE(active_command_id, ''),
		COALESCE(duplicated_from_post_id, ''),
		created_by_account_id,
		created_at,
		updated_at,
		cancelled_at
	FROM f07_scheduled_posts
`

const commandSelect = `
	SELECT
		id,
		workspace_id,
		post_id,
		draft_id,
		draft_revision,
		channel_ids,
		generation,
		execute_at_utc,
		state,
		created_at,
		invalidated_at,
		invalidation_key
	FROM f07_publication_commands
`

const operationSelect = `
	SELECT workspace_id, operation_kind, idempotency_key, payload_fingerprint,
		COALESCE(downstream_idempotency_key, ''),
		state, post_id, publication_command_id,
		COALESCE(source_post_id, ''), COALESCE(source_post_revision, 0),
		COALESCE(source_draft_id, ''), COALESCE(source_draft_revision, 0),
		COALESCE(channel_ids, ARRAY[]::text[]), scheduled_for_utc,
		COALESCE(scheduled_local, ''), COALESCE(scheduled_timezone, ''),
		COALESCE(scheduled_utc_offset_minutes, 0),
		COALESCE(clone_draft_id, ''), COALESCE(clone_draft_revision, 0),
		lease_generation, locked_until, created_at, updated_at, completed_at,
		response_snapshot_status, response_snapshot
	FROM f07_idempotency_operations
`

func lockOperation(
	ctx context.Context,
	transaction *sql.Tx,
	workspaceID string,
	kind OperationKind,
	idempotencyKey string,
) (IdempotencyOperation, error) {
	return scanOperation(transaction.QueryRowContext(ctx, operationSelect+`
		WHERE workspace_id = $1 AND operation_kind = $2 AND idempotency_key = $3
		FOR UPDATE
	`, workspaceID, string(kind), idempotencyKey))
}

func scanOperation(row schedulingRow) (IdempotencyOperation, error) {
	var operation IdempotencyOperation
	var channelIDs postgresTextArray
	var scheduledUTC, lockedUntil, completedAt sql.NullTime
	var responseSnapshot []byte
	err := row.Scan(
		&operation.WorkspaceID,
		&operation.Kind,
		&operation.IdempotencyKey,
		&operation.PayloadFingerprint,
		&operation.DownstreamIdempotencyKey,
		&operation.State,
		&operation.PostID,
		&operation.PublicationCommandID,
		&operation.SourcePostID,
		&operation.SourcePostRevision,
		&operation.SourceDraftID,
		&operation.SourceDraftRevision,
		&channelIDs,
		&scheduledUTC,
		&operation.Schedule.local,
		&operation.Schedule.timeZone,
		&operation.Schedule.offsetMinutes,
		&operation.CloneDraftID,
		&operation.CloneDraftRevision,
		&operation.LeaseGeneration,
		&lockedUntil,
		&operation.CreatedAt,
		&operation.UpdatedAt,
		&completedAt,
		&operation.ResponseSnapshotStatus,
		&responseSnapshot,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return IdempotencyOperation{}, ErrNotFound
	}
	if err != nil {
		return IdempotencyOperation{}, fmt.Errorf("scan idempotency operation: %w", err)
	}
	operation.ChannelIDs = append([]string(nil), channelIDs...)
	if scheduledUTC.Valid {
		operation.Schedule.utc = scheduledUTC.Time.UTC()
	}
	if lockedUntil.Valid {
		operation.LockedUntil = lockedUntil.Time.UTC()
	}
	if completedAt.Valid {
		value := completedAt.Time.UTC()
		operation.CompletedAt = &value
	}
	if len(responseSnapshot) > 0 {
		var view ScheduledPostView
		if err := json.Unmarshal(responseSnapshot, &view); err != nil {
			return IdempotencyOperation{}, fmt.Errorf("decode idempotency response snapshot: %w", err)
		}
		operation.ResponseSnapshot = &view
	}
	return operation, nil
}

func insertPost(ctx context.Context, transaction *sql.Tx, post ScheduledPost) error {
	_, err := transaction.ExecContext(ctx, `
		INSERT INTO f07_scheduled_posts (
			id,
			workspace_id,
			draft_id,
			draft_revision,
			channel_ids,
			status,
			scheduled_for_utc,
			scheduled_local,
			scheduled_timezone,
			scheduled_utc_offset_minutes,
			revision,
			active_command_id,
			duplicated_from_post_id,
			created_by_account_id,
			created_at,
			updated_at,
			cancelled_at
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8, $9,
			$10, $11, $12, NULLIF($13, ''), $14, $15, $16, $17
		)
	`,
		post.ID,
		post.WorkspaceID,
		post.DraftID,
		post.DraftRevision,
		post.ChannelIDs,
		string(post.Status),
		post.ScheduledForUTC,
		post.ScheduledLocal,
		post.TimeZone,
		post.UTCOffsetMinutes,
		post.Revision,
		post.ActiveCommandID,
		post.DuplicatedFromPostID,
		post.CreatedBy,
		post.CreatedAt,
		post.UpdatedAt,
		post.CancelledAt,
	)
	if err != nil {
		if isPostgresConflict(err) {
			return ErrConflict
		}
		return fmt.Errorf("insert scheduled post: %w", err)
	}
	return nil
}

func insertPublicationCommand(
	ctx context.Context,
	transaction *sql.Tx,
	command PublicationCommand,
) error {
	_, err := transaction.ExecContext(ctx, `
		INSERT INTO f07_publication_commands (
			id,
			workspace_id,
			post_id,
			draft_id,
			draft_revision,
			channel_ids,
			generation,
			execute_at_utc,
			state,
			invalidation_key,
			created_at,
			invalidated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
	`,
		command.ID,
		command.WorkspaceID,
		command.PostID,
		command.DraftID,
		command.DraftRevision,
		command.ChannelIDs,
		command.Generation,
		command.ExecuteAtUTC,
		string(command.State),
		command.InvalidationKey,
		command.CreatedAt,
		command.InvalidatedAt,
	)
	if err != nil {
		if isPostgresConflict(err) {
			return ErrConflict
		}
		return fmt.Errorf("insert publication command: %w", err)
	}
	return nil
}

func lockPost(
	ctx context.Context,
	transaction *sql.Tx,
	workspaceID, postID string,
) (ScheduledPost, error) {
	return scanScheduledPost(transaction.QueryRowContext(ctx, postSelect+`
		WHERE workspace_id = $1 AND id = $2
		FOR UPDATE
	`, workspaceID, postID))
}

func invalidatePostCommand(
	ctx context.Context,
	transaction *sql.Tx,
	commandID string,
	now time.Time,
) error {
	result, err := transaction.ExecContext(ctx, `
		UPDATE f07_publication_commands
		SET state = 'invalidated', invalidated_at = $2
		WHERE id = $1 AND state = 'pending'
	`, commandID, now)
	if err != nil {
		return fmt.Errorf("invalidate publication command: %w", err)
	}
	rowsAffected, rowsErr := result.RowsAffected()
	if rowsErr != nil {
		return fmt.Errorf("count invalidated publication commands: %w", rowsErr)
	}
	if rowsAffected != 1 {
		return ErrConflict
	}
	return nil
}

type schedulingRow interface {
	Scan(...any) error
}

func scanScheduledPost(row schedulingRow) (ScheduledPost, error) {
	var post ScheduledPost
	var channelIDs postgresTextArray
	err := row.Scan(
		&post.ID,
		&post.WorkspaceID,
		&post.DraftID,
		&post.DraftRevision,
		&channelIDs,
		&post.Status,
		&post.ScheduledForUTC,
		&post.ScheduledLocal,
		&post.TimeZone,
		&post.UTCOffsetMinutes,
		&post.Revision,
		&post.ActiveCommandID,
		&post.DuplicatedFromPostID,
		&post.CreatedBy,
		&post.CreatedAt,
		&post.UpdatedAt,
		&post.CancelledAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return ScheduledPost{}, ErrNotFound
	}
	if err != nil {
		return ScheduledPost{}, fmt.Errorf("scan scheduled post: %w", err)
	}
	post.ChannelIDs = append([]string(nil), channelIDs...)
	return post, nil
}

func scanPublicationCommand(row schedulingRow) (PublicationCommand, error) {
	var command PublicationCommand
	var channelIDs postgresTextArray
	err := row.Scan(
		&command.ID,
		&command.WorkspaceID,
		&command.PostID,
		&command.DraftID,
		&command.DraftRevision,
		&channelIDs,
		&command.Generation,
		&command.ExecuteAtUTC,
		&command.State,
		&command.CreatedAt,
		&command.InvalidatedAt,
		&command.InvalidationKey,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return PublicationCommand{}, ErrNotFound
	}
	if err != nil {
		return PublicationCommand{}, fmt.Errorf("scan publication command: %w", err)
	}
	command.ChannelIDs = append([]string(nil), channelIDs...)
	return command, nil
}

type postgresTextArray []string

func (array *postgresTextArray) Scan(source any) error {
	var encoded []byte
	switch value := source.(type) {
	case string:
		encoded = []byte(value)
	case []byte:
		encoded = append([]byte(nil), value...)
	default:
		return fmt.Errorf("scan postgres text array from %T", source)
	}
	var decoded []string
	if err := pgtype.NewMap().Scan(
		pgtype.TextArrayOID,
		pgx.TextFormatCode,
		encoded,
		&decoded,
	); err != nil {
		return fmt.Errorf("decode postgres text array: %w", err)
	}
	*array = append((*array)[:0], decoded...)
	return nil
}

func isPostgresConflict(err error) bool {
	var postgresError interface{ SQLState() string }
	return errors.As(err, &postgresError) && postgresError.SQLState() == "23505"
}
