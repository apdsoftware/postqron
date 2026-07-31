package scheduling

import (
	"context"
	"database/sql"
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
			channel_ids = $5,
			scheduled_for_utc = $6,
			scheduled_local = $7,
			scheduled_timezone = $8,
			scheduled_utc_offset_minutes = $9,
			revision = $10,
			active_command_id = $11,
			updated_at = $12
		WHERE workspace_id = $1 AND id = $2 AND revision = $3
	`,
		replacement.WorkspaceID,
		replacement.ID,
		expectedRevision,
		replacement.DraftID,
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
		channel_ids,
		generation,
		execute_at_utc,
		state,
		created_at,
		invalidated_at,
		invalidation_key
	FROM f07_publication_commands
`

func insertPost(ctx context.Context, transaction *sql.Tx, post ScheduledPost) error {
	_, err := transaction.ExecContext(ctx, `
		INSERT INTO f07_scheduled_posts (
			id,
			workspace_id,
			draft_id,
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
			$1, $2, $3, $4, $5, $6, $7, $8,
			$9, $10, $11, NULLIF($12, ''), $13, $14, $15, $16
		)
	`,
		post.ID,
		post.WorkspaceID,
		post.DraftID,
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
			channel_ids,
			generation,
			execute_at_utc,
			state,
			invalidation_key,
			created_at,
			invalidated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
	`,
		command.ID,
		command.WorkspaceID,
		command.PostID,
		command.DraftID,
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
