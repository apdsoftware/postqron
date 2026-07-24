package collaboration

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

func (repository *PostgresRepository) CreateComment(
	ctx context.Context,
	comment Comment,
	audit AuditEvent,
	event Event,
) (Comment, error) {
	err := repository.transaction(ctx, func(transaction pgx.Tx) error {
		_, err := transaction.Exec(ctx, `
			INSERT INTO f17_collaboration_comments (
				id, workspace_id, draft_id, author_account_id, body, created_at
			) VALUES ($1, $2, $3, $4, $5, $6)
		`,
			comment.ID,
			comment.WorkspaceID,
			comment.DraftID,
			comment.AuthorID,
			comment.Body,
			comment.CreatedAt,
		)
		if err != nil {
			if postgresUniqueViolation(err) {
				return ErrConflict
			}
			return fmt.Errorf("insert collaboration comment: %w", err)
		}
		return insertRecords(ctx, transaction, audit, event)
	})
	if err != nil {
		return Comment{}, err
	}
	return cloneComment(comment), nil
}

func (repository *PostgresRepository) ListComments(
	ctx context.Context,
	workspaceID, draftID string,
) ([]Comment, error) {
	rows, err := repository.pool.Query(ctx, `
		SELECT
			id,
			workspace_id,
			draft_id,
			author_account_id,
			body,
			created_at,
			resolved_by_account_id,
			resolved_at
		FROM f17_collaboration_comments
		WHERE workspace_id = $1 AND draft_id = $2
		ORDER BY created_at, id
	`, workspaceID, draftID)
	if err != nil {
		return nil, fmt.Errorf("list collaboration comments: %w", err)
	}
	defer rows.Close()
	comments := make([]Comment, 0)
	for rows.Next() {
		comment, scanErr := scanComment(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		comments = append(comments, comment)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate collaboration comments: %w", err)
	}
	return comments, nil
}

func (repository *PostgresRepository) ResolveComment(
	ctx context.Context,
	workspaceID, draftID, commentID, actorID string,
	now time.Time,
	audit AuditEvent,
	event Event,
) (Comment, error) {
	var result Comment
	err := repository.transaction(ctx, func(transaction pgx.Tx) error {
		comment, err := scanComment(transaction.QueryRow(ctx, `
			SELECT
				id,
				workspace_id,
				draft_id,
				author_account_id,
				body,
				created_at,
				resolved_by_account_id,
				resolved_at
			FROM f17_collaboration_comments
			WHERE workspace_id = $1 AND draft_id = $2 AND id = $3
			FOR UPDATE
		`, workspaceID, draftID, commentID))
		if err != nil {
			return err
		}
		if comment.ResolvedAt != nil {
			result = comment
			return nil
		}
		_, err = transaction.Exec(ctx, `
			UPDATE f17_collaboration_comments
			SET resolved_by_account_id = $4, resolved_at = $5
			WHERE workspace_id = $1 AND draft_id = $2 AND id = $3
		`, workspaceID, draftID, commentID, actorID, now)
		if err != nil {
			return fmt.Errorf("resolve collaboration comment: %w", err)
		}
		comment.ResolvedBy = actorID
		comment.ResolvedAt = timePointer(now)
		result = comment
		return insertRecords(ctx, transaction, audit, event)
	})
	if err != nil {
		return Comment{}, err
	}
	return result, nil
}

func (repository *PostgresRepository) RequestReview(
	ctx context.Context,
	review Review,
	audit AuditEvent,
	event Event,
) (Review, bool, error) {
	var result Review
	created := false
	err := repository.transaction(ctx, func(transaction pgx.Tx) error {
		current, err := scanReview(transaction.QueryRow(ctx, `
			SELECT
				id,
				workspace_id,
				draft_id,
				draft_revision,
				status,
				requested_by_account_id,
				requested_at,
				decided_by_account_id,
				decided_at,
				decision_note
			FROM f17_collaboration_reviews
			WHERE workspace_id = $1 AND draft_id = $2
			ORDER BY sequence DESC
			LIMIT 1
			FOR UPDATE
		`, review.WorkspaceID, review.DraftID))
		if err != nil && !errors.Is(err, ErrNotFound) {
			return err
		}
		if err == nil && current.Status == ReviewPending {
			if current.DraftRevision == review.DraftRevision &&
				current.RequestedBy == review.RequestedBy {
				result = current
				return nil
			}
			return ErrReviewPending
		}
		_, err = transaction.Exec(ctx, `
			INSERT INTO f17_collaboration_reviews (
				id,
				workspace_id,
				draft_id,
				draft_revision,
				status,
				requested_by_account_id,
				requested_at
			) VALUES ($1, $2, $3, $4, $5, $6, $7)
		`,
			review.ID,
			review.WorkspaceID,
			review.DraftID,
			review.DraftRevision,
			review.Status,
			review.RequestedBy,
			review.RequestedAt,
		)
		if err != nil {
			if postgresUniqueViolation(err) {
				return ErrReviewPending
			}
			return fmt.Errorf("insert collaboration review: %w", err)
		}
		if err := insertRecords(ctx, transaction, audit, event); err != nil {
			return err
		}
		result = review
		created = true
		return nil
	})
	if err != nil {
		if errors.Is(err, ErrReviewPending) {
			current, latestErr := repository.LatestReview(
				ctx,
				review.WorkspaceID,
				review.DraftID,
			)
			if latestErr == nil &&
				current.Status == ReviewPending &&
				current.DraftRevision == review.DraftRevision &&
				current.RequestedBy == review.RequestedBy {
				return current, false, nil
			}
		}
		return Review{}, false, err
	}
	return result, created, nil
}

func (repository *PostgresRepository) LatestReview(
	ctx context.Context,
	workspaceID, draftID string,
) (Review, error) {
	return scanReview(repository.pool.QueryRow(ctx, `
		SELECT
			id,
			workspace_id,
			draft_id,
			draft_revision,
			status,
			requested_by_account_id,
			requested_at,
			decided_by_account_id,
			decided_at,
			decision_note
		FROM f17_collaboration_reviews
		WHERE workspace_id = $1 AND draft_id = $2
		ORDER BY sequence DESC
		LIMIT 1
	`, workspaceID, draftID))
}

func (repository *PostgresRepository) DecideReview(
	ctx context.Context,
	workspaceID, draftID, reviewID string,
	decision ReviewDecision,
	note string,
	now time.Time,
	audit AuditEvent,
	event Event,
) (Review, error) {
	var result Review
	err := repository.transaction(ctx, func(transaction pgx.Tx) error {
		review, err := scanReview(transaction.QueryRow(ctx, `
			SELECT
				id,
				workspace_id,
				draft_id,
				draft_revision,
				status,
				requested_by_account_id,
				requested_at,
				decided_by_account_id,
				decided_at,
				decision_note
			FROM f17_collaboration_reviews
			WHERE workspace_id = $1 AND draft_id = $2
			ORDER BY sequence DESC
			LIMIT 1
			FOR UPDATE
		`, workspaceID, draftID))
		if err != nil {
			return err
		}
		if review.ID != reviewID || review.Status != ReviewPending {
			return ErrReviewNotPending
		}
		switch decision {
		case DecisionApprove:
			var unresolved bool
			if err := transaction.QueryRow(ctx, `
				SELECT EXISTS (
					SELECT 1
					FROM f17_collaboration_comments
					WHERE workspace_id = $1
					  AND draft_id = $2
					  AND resolved_at IS NULL
				)
			`, workspaceID, draftID).Scan(&unresolved); err != nil {
				return fmt.Errorf("check unresolved collaboration comments: %w", err)
			}
			if unresolved {
				return ErrUnresolvedComment
			}
			review.Status = ReviewApproved
		case DecisionRequestChanges:
			review.Status = ReviewChangesRequested
		default:
			return ErrInvalidArgument
		}
		row := transaction.QueryRow(ctx, `
			UPDATE f17_collaboration_reviews
			SET
				status = $4,
				decided_by_account_id = $5,
				decided_at = $6,
				decision_note = $7
			WHERE workspace_id = $1 AND draft_id = $2 AND id = $3 AND status = 'pending'
			RETURNING
				id,
				workspace_id,
				draft_id,
				draft_revision,
				status,
				requested_by_account_id,
				requested_at,
				decided_by_account_id,
				decided_at,
				decision_note
		`,
			workspaceID,
			draftID,
			reviewID,
			review.Status,
			audit.ActorID,
			now,
			note,
		)
		result, err = scanReview(row)
		if err != nil {
			return err
		}
		return insertRecords(ctx, transaction, audit, event)
	})
	if err != nil {
		if postgresCheckViolation(err) {
			return Review{}, ErrConflict
		}
		return Review{}, err
	}
	return result, nil
}

func (repository *PostgresRepository) RecordSchedulingBlocked(
	ctx context.Context,
	audit AuditEvent,
	event Event,
) error {
	return repository.transaction(ctx, func(transaction pgx.Tx) error {
		return insertRecords(ctx, transaction, audit, event)
	})
}

func (repository *PostgresRepository) PendingEvents(
	ctx context.Context,
	limit int,
) ([]Event, error) {
	rows, err := repository.pool.Query(ctx, `
		SELECT
			id,
			event_type,
			workspace_id,
			actor_account_id,
			draft_id,
			correlation_id,
			occurred_at,
			data,
			published_at
		FROM f17_collaboration_outbox
		WHERE published_at IS NULL
		ORDER BY occurred_at, id
		LIMIT $1
	`, limit)
	if err != nil {
		return nil, fmt.Errorf("list collaboration outbox: %w", err)
	}
	defer rows.Close()
	events := make([]Event, 0)
	for rows.Next() {
		event, scanErr := scanEvent(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate collaboration outbox: %w", err)
	}
	return events, nil
}

func (repository *PostgresRepository) MarkEventPublished(
	ctx context.Context,
	eventID string,
	publishedAt time.Time,
) error {
	tag, err := repository.pool.Exec(ctx, `
		UPDATE f17_collaboration_outbox
		SET published_at = COALESCE(published_at, $2)
		WHERE id = $1
	`, eventID, publishedAt)
	if err != nil {
		return fmt.Errorf("mark collaboration event published: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (repository *PostgresRepository) transaction(
	ctx context.Context,
	operation func(pgx.Tx) error,
) error {
	transaction, err := repository.pool.BeginTx(ctx, pgx.TxOptions{
		IsoLevel: pgx.Serializable,
	})
	if err != nil {
		return fmt.Errorf("begin collaboration transaction: %w", err)
	}
	defer func() { _ = transaction.Rollback(ctx) }()
	if err := operation(transaction); err != nil {
		return err
	}
	if err := transaction.Commit(ctx); err != nil {
		if postgresSerializationFailure(err) {
			return ErrConflict
		}
		return fmt.Errorf("commit collaboration transaction: %w", err)
	}
	return nil
}

func insertRecords(
	ctx context.Context,
	transaction pgx.Tx,
	audit AuditEvent,
	event Event,
) error {
	_, err := transaction.Exec(ctx, `
		INSERT INTO f17_collaboration_audit_events (
			id,
			workspace_id,
			actor_account_id,
			target_type,
			target_id,
			action,
			outcome,
			occurred_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
	`,
		audit.ID,
		audit.WorkspaceID,
		nullableString(audit.ActorID),
		audit.TargetType,
		audit.TargetID,
		audit.Action,
		audit.Outcome,
		audit.OccurredAt,
	)
	if err != nil {
		return fmt.Errorf("insert collaboration audit event: %w", err)
	}
	data, err := json.Marshal(event.Data)
	if err != nil {
		return fmt.Errorf("encode collaboration event: %w", err)
	}
	_, err = transaction.Exec(ctx, `
		INSERT INTO f17_collaboration_outbox (
			id,
			event_type,
			workspace_id,
			actor_account_id,
			draft_id,
			correlation_id,
			occurred_at,
			data
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
	`,
		event.ID,
		event.Type,
		event.WorkspaceID,
		nullableString(event.ActorID),
		event.DraftID,
		event.CorrelationID,
		event.OccurredAt,
		data,
	)
	if err != nil {
		return fmt.Errorf("insert collaboration outbox event: %w", err)
	}
	return nil
}

type postgresRow interface {
	Scan(...any) error
}

func scanComment(row postgresRow) (Comment, error) {
	var comment Comment
	var resolvedBy *string
	err := row.Scan(
		&comment.ID,
		&comment.WorkspaceID,
		&comment.DraftID,
		&comment.AuthorID,
		&comment.Body,
		&comment.CreatedAt,
		&resolvedBy,
		&comment.ResolvedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return Comment{}, ErrNotFound
	}
	if err != nil {
		return Comment{}, fmt.Errorf("scan collaboration comment: %w", err)
	}
	if resolvedBy != nil {
		comment.ResolvedBy = *resolvedBy
	}
	return comment, nil
}

func scanReview(row postgresRow) (Review, error) {
	var review Review
	var decidedBy *string
	err := row.Scan(
		&review.ID,
		&review.WorkspaceID,
		&review.DraftID,
		&review.DraftRevision,
		&review.Status,
		&review.RequestedBy,
		&review.RequestedAt,
		&decidedBy,
		&review.DecidedAt,
		&review.DecisionNote,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return Review{}, ErrNotFound
	}
	if err != nil {
		return Review{}, fmt.Errorf("scan collaboration review: %w", err)
	}
	if decidedBy != nil {
		review.DecidedBy = *decidedBy
	}
	return review, nil
}

func scanEvent(row postgresRow) (Event, error) {
	var event Event
	var actorID *string
	var data []byte
	err := row.Scan(
		&event.ID,
		&event.Type,
		&event.WorkspaceID,
		&actorID,
		&event.DraftID,
		&event.CorrelationID,
		&event.OccurredAt,
		&data,
		&event.PublishedAt,
	)
	if err != nil {
		return Event{}, fmt.Errorf("scan collaboration event: %w", err)
	}
	if actorID != nil {
		event.ActorID = *actorID
	}
	if err := json.Unmarshal(data, &event.Data); err != nil {
		return Event{}, fmt.Errorf("decode collaboration event: %w", err)
	}
	return event, nil
}

func nullableString(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func postgresUniqueViolation(err error) bool {
	return postgresState(err) == "23505"
}

func postgresCheckViolation(err error) bool {
	return postgresState(err) == "23514"
}

func postgresSerializationFailure(err error) bool {
	return postgresState(err) == "40001"
}

func postgresState(err error) string {
	var postgresError interface{ SQLState() string }
	if errors.As(err, &postgresError) {
		return postgresError.SQLState()
	}
	return ""
}
