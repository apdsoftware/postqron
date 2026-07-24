package statusnotifications

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

type SQLRepository struct {
	database *sql.DB
}

func NewSQLRepository(database *sql.DB) (*SQLRepository, error) {
	if database == nil {
		return nil, fmt.Errorf("%w: database is required", ErrInvalidArgument)
	}
	return &SQLRepository{database: database}, nil
}

func (repository *SQLRepository) ApplyLifecycle(
	ctx context.Context,
	event LifecycleEvent,
) (ApplyResult, error) {
	if err := validateLifecycleEvent(event); err != nil {
		return ApplyResult{}, err
	}
	transaction, err := repository.database.BeginTx(ctx, nil)
	if err != nil {
		return ApplyResult{}, fmt.Errorf("begin lifecycle status transaction: %w", err)
	}
	defer func() { _ = transaction.Rollback() }()

	first, err := recordStatusEvent(
		ctx,
		transaction,
		event.EventID,
		lifecycleFingerprint(event),
		"lifecycle",
		event.WorkspaceID,
		event.PostID,
		"",
		event.OccurredAt,
	)
	if err != nil {
		return ApplyResult{}, err
	}
	if !first {
		view, err := getPostTx(ctx, transaction, event.WorkspaceID, event.PostID)
		if err != nil {
			return ApplyResult{}, err
		}
		if err := transaction.Commit(); err != nil {
			return ApplyResult{}, err
		}
		return ApplyResult{View: view}, nil
	}

	var revision int64
	var lastEventAt sql.NullTime
	var currentStatus PostStatus
	var projectionUpdatedAt time.Time
	lockErr := transaction.QueryRowContext(
		ctx,
		`SELECT lifecycle_revision, last_lifecycle_event_at, status, updated_at
		 FROM f09_post_status
		 WHERE workspace_id = $1 AND post_id = $2
		 FOR UPDATE`,
		event.WorkspaceID,
		event.PostID,
	).Scan(&revision, &lastEventAt, &currentStatus, &projectionUpdatedAt)
	exists := lockErr == nil
	if lockErr != nil && !errors.Is(lockErr, sql.ErrNoRows) {
		return ApplyResult{}, fmt.Errorf("lock lifecycle projection: %w", lockErr)
	}
	stale := exists &&
		(currentStatus == StatusPublished ||
			currentStatus == StatusCancelled ||
			event.OccurredAt.Before(projectionUpdatedAt) ||
			event.Revision < revision ||
			event.Revision == revision &&
				lastEventAt.Valid &&
				!event.OccurredAt.After(lastEventAt.Time))
	if stale {
		view, err := getPostTx(ctx, transaction, event.WorkspaceID, event.PostID)
		if err != nil {
			return ApplyResult{}, err
		}
		if err := transaction.Commit(); err != nil {
			return ApplyResult{}, err
		}
		return ApplyResult{FirstDelivery: true, View: view}, nil
	}

	occurredAt := event.OccurredAt.UTC()
	_, err = transaction.ExecContext(
		ctx,
		`INSERT INTO f09_post_status (
		     workspace_id, post_id, draft_id, status, lifecycle_revision,
		     last_lifecycle_event_id, last_lifecycle_event_at, created_at, updated_at
		 )
		 VALUES ($1, $2, NULLIF($3, ''), $4, $5, $6, $7, $7, $7)
		 ON CONFLICT (workspace_id, post_id) DO UPDATE SET
		     draft_id = EXCLUDED.draft_id,
		     status = EXCLUDED.status,
		     lifecycle_revision = EXCLUDED.lifecycle_revision,
		     last_lifecycle_event_id = EXCLUDED.last_lifecycle_event_id,
		     last_lifecycle_event_at = EXCLUDED.last_lifecycle_event_at,
		     updated_at = EXCLUDED.updated_at`,
		event.WorkspaceID,
		event.PostID,
		event.DraftID,
		event.Status,
		event.Revision,
		event.EventID,
		occurredAt,
	)
	if err != nil {
		return ApplyResult{}, fmt.Errorf("upsert lifecycle projection: %w", err)
	}
	if _, err := transaction.ExecContext(
		ctx,
		`DELETE FROM f09_destination_status
		 WHERE workspace_id = $1 AND post_id = $2`,
		event.WorkspaceID,
		event.PostID,
	); err != nil {
		return ApplyResult{}, fmt.Errorf("replace lifecycle destinations: %w", err)
	}
	for _, destination := range event.Destinations {
		_, err := transaction.ExecContext(
			ctx,
			`INSERT INTO f09_destination_status (
			     workspace_id, post_id, destination_id, channel_id, status,
			     last_event_id, updated_at
			 )
			 VALUES ($1, $2, $3, $4, $5, $6, $7)`,
			event.WorkspaceID,
			event.PostID,
			destination.ID,
			destination.ChannelID,
			destinationStatusForPost(event.Status),
			event.EventID,
			occurredAt,
		)
		if err != nil {
			return ApplyResult{}, fmt.Errorf("insert lifecycle destination: %w", err)
		}
	}
	view, err := getPostTx(ctx, transaction, event.WorkspaceID, event.PostID)
	if err != nil {
		return ApplyResult{}, err
	}
	if err := transaction.Commit(); err != nil {
		return ApplyResult{}, fmt.Errorf("commit lifecycle projection: %w", err)
	}
	return ApplyResult{
		FirstDelivery: true,
		StateChanged:  true,
		View:          view,
	}, nil
}

func (repository *SQLRepository) ApplyPublication(
	ctx context.Context,
	event PublicationEvent,
) (ApplyResult, error) {
	if err := validatePublicationEvent(event); err != nil {
		return ApplyResult{}, err
	}
	transaction, err := repository.database.BeginTx(ctx, nil)
	if err != nil {
		return ApplyResult{}, fmt.Errorf("begin publication status transaction: %w", err)
	}
	defer func() { _ = transaction.Rollback() }()

	first, err := recordStatusEvent(
		ctx,
		transaction,
		event.EventID,
		publicationFingerprint(event),
		"publication",
		event.WorkspaceID,
		event.PostID,
		event.DestinationID,
		event.OccurredAt,
	)
	if err != nil {
		return ApplyResult{}, err
	}
	if !first {
		view, err := getPostTx(ctx, transaction, event.WorkspaceID, event.PostID)
		if err != nil {
			return ApplyResult{}, err
		}
		if err := transaction.Commit(); err != nil {
			return ApplyResult{}, err
		}
		return ApplyResult{View: view}, nil
	}

	occurredAt := event.OccurredAt.UTC()
	if _, err := transaction.ExecContext(
		ctx,
		`INSERT INTO f09_post_status (
		     workspace_id, post_id, status, created_at, updated_at
		 )
		 VALUES ($1, $2, 'scheduled', $3, $3)
		 ON CONFLICT (workspace_id, post_id) DO NOTHING`,
		event.WorkspaceID,
		event.PostID,
		occurredAt,
	); err != nil {
		return ApplyResult{}, fmt.Errorf("ensure publication projection: %w", err)
	}

	current, found, err := lockDestination(
		ctx,
		transaction,
		event.WorkspaceID,
		event.DestinationID,
	)
	if err != nil {
		return ApplyResult{}, err
	}
	incoming := destinationStatusFromPublication(event.Status)
	changed := !found || publicationEventWins(current, incoming, occurredAt)
	if changed {
		diagnostic := Diagnostic{}
		if incoming == DestinationFailed {
			diagnostic = ClientDiagnostic(event.Diagnostic, occurredAt)
		}
		_, err = transaction.ExecContext(
			ctx,
			`INSERT INTO f09_destination_status (
			     workspace_id, post_id, destination_id, channel_id, status,
			     remote_id, diagnostic_code, diagnostic_message,
			     diagnostic_retryable, diagnostic_at, last_event_id, updated_at
			 )
			 VALUES (
			     $1, $2, $3, $4, $5, NULLIF($6, ''), NULLIF($7, ''),
			     NULLIF($8, ''), $9, $10, $11, $12
			 )
			 ON CONFLICT (workspace_id, destination_id) DO UPDATE SET
			     post_id = EXCLUDED.post_id,
			     channel_id = EXCLUDED.channel_id,
			     status = EXCLUDED.status,
			     remote_id = EXCLUDED.remote_id,
			     diagnostic_code = EXCLUDED.diagnostic_code,
			     diagnostic_message = EXCLUDED.diagnostic_message,
			     diagnostic_retryable = EXCLUDED.diagnostic_retryable,
			     diagnostic_at = EXCLUDED.diagnostic_at,
			     last_event_id = EXCLUDED.last_event_id,
			     updated_at = EXCLUDED.updated_at`,
			event.WorkspaceID,
			event.PostID,
			event.DestinationID,
			event.ChannelID,
			incoming,
			event.RemoteID,
			diagnostic.Code,
			diagnostic.Message,
			diagnostic.Retryable,
			nullTime(diagnostic.At),
			event.EventID,
			occurredAt,
		)
		if err != nil {
			return ApplyResult{}, fmt.Errorf("upsert publication destination: %w", err)
		}
		aggregate, err := aggregateStatusTx(
			ctx,
			transaction,
			event.WorkspaceID,
			event.PostID,
		)
		if err != nil {
			return ApplyResult{}, err
		}
		if _, err := transaction.ExecContext(
			ctx,
			`UPDATE f09_post_status
			 SET status = $3, updated_at = GREATEST(updated_at, $4)
			 WHERE workspace_id = $1 AND post_id = $2`,
			event.WorkspaceID,
			event.PostID,
			aggregate,
			occurredAt,
		); err != nil {
			return ApplyResult{}, fmt.Errorf("update aggregate publication status: %w", err)
		}
	}
	view, err := getPostTx(ctx, transaction, event.WorkspaceID, event.PostID)
	if err != nil {
		return ApplyResult{}, err
	}
	if err := transaction.Commit(); err != nil {
		return ApplyResult{}, fmt.Errorf("commit publication projection: %w", err)
	}
	return ApplyResult{
		FirstDelivery: true,
		StateChanged:  changed,
		View:          view,
	}, nil
}

func (repository *SQLRepository) GetPost(
	ctx context.Context,
	workspaceID, postID string,
) (PostView, error) {
	return getPostQuery(ctx, repository.database, workspaceID, postID)
}

func (repository *SQLRepository) EnqueueNotification(
	ctx context.Context,
	notification Notification,
) (EnqueueResult, error) {
	if err := validateNotification(notification); err != nil {
		return EnqueueResult{}, err
	}
	transaction, err := repository.database.BeginTx(ctx, nil)
	if err != nil {
		return EnqueueResult{}, err
	}
	defer func() { _ = transaction.Rollback() }()
	var result EnqueueResult
	err = transaction.QueryRowContext(
		ctx,
		`INSERT INTO f09_notification_outbox (
		     id, source_event_id, kind, account_id, workspace_id, post_id,
		     destination_id, subject, detail, action_label, action_url,
		     idempotency_key, state, next_attempt_at, created_at
		 )
		 VALUES (
		     $1, $2, $3, NULLIF($4, ''), NULLIF($5, ''), NULLIF($6, ''),
		     NULLIF($7, ''), $8, $9, $10, $11, $12, $13, $14, $15
		 )
		 ON CONFLICT DO NOTHING
		 RETURNING id, state`,
		notification.ID,
		notification.SourceEventID,
		notification.Kind,
		notification.AccountID,
		notification.WorkspaceID,
		notification.PostID,
		notification.DestinationID,
		notification.Subject,
		notification.Detail,
		notification.ActionLabel,
		notification.ActionURL,
		notification.IdempotencyKey,
		notification.State,
		notification.NextAttemptAt,
		notification.CreatedAt,
	).Scan(&result.ID, &result.State)
	if err == nil {
		result.Created = true
	} else if errors.Is(err, sql.ErrNoRows) {
		err = transaction.QueryRowContext(
			ctx,
			`SELECT id, state
			 FROM f09_notification_outbox
			 WHERE (kind = $1 AND source_event_id = $2)
			    OR idempotency_key = $3
			 ORDER BY (kind = $1 AND source_event_id = $2) DESC
			 LIMIT 1`,
			notification.Kind,
			notification.SourceEventID,
			notification.IdempotencyKey,
		).Scan(&result.ID, &result.State)
	}
	if err != nil {
		return EnqueueResult{}, fmt.Errorf("enqueue notification: %w", err)
	}
	if err := transaction.Commit(); err != nil {
		return EnqueueResult{}, err
	}
	return result, nil
}

func (repository *SQLRepository) ClaimNotification(
	ctx context.Context,
	now, lockedUntil time.Time,
	leaseToken string,
) (Notification, bool, error) {
	row := repository.database.QueryRowContext(
		ctx,
		`UPDATE f09_notification_outbox
		 SET state = 'sending',
		     attempt_count = attempt_count + 1,
		     lease_token = $3,
		     locked_until = $2
		 WHERE id = (
		     SELECT id
		     FROM f09_notification_outbox
		     WHERE next_attempt_at <= $1
		       AND (
		           state IN ('pending', 'retry')
		           OR (state = 'sending' AND locked_until <= $1)
		       )
		     ORDER BY next_attempt_at, id
		     FOR UPDATE SKIP LOCKED
		     LIMIT 1
		 )
		 RETURNING id, source_event_id, kind, COALESCE(account_id, ''),
		     COALESCE(workspace_id, ''), COALESCE(post_id, ''),
		     COALESCE(destination_id, ''), subject, detail, action_label,
		     action_url, idempotency_key, state, attempt_count,
		     next_attempt_at, lease_token, locked_until, created_at`,
		now,
		lockedUntil,
		leaseToken,
	)
	item, err := scanNotification(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Notification{}, false, nil
	}
	if err != nil {
		return Notification{}, false, fmt.Errorf("claim notification: %w", err)
	}
	return item, true, nil
}

func (repository *SQLRepository) MarkNotificationDelivered(
	ctx context.Context,
	id, leaseToken string,
	deliveredAt time.Time,
) error {
	return markQueueDelivered(
		ctx,
		repository.database,
		"f09_notification_outbox",
		id,
		leaseToken,
		deliveredAt,
	)
}

func (repository *SQLRepository) MarkNotificationRetry(
	ctx context.Context,
	id, leaseToken string,
	next time.Time,
) error {
	return markQueueRetry(
		ctx,
		repository.database,
		"f09_notification_outbox",
		id,
		leaseToken,
		next,
	)
}

func (repository *SQLRepository) EnqueueManualRetry(
	ctx context.Context,
	retry ManualRetry,
) (EnqueueResult, error) {
	if err := validateManualRetry(retry); err != nil {
		return EnqueueResult{}, err
	}
	transaction, err := repository.database.BeginTx(ctx, nil)
	if err != nil {
		return EnqueueResult{}, err
	}
	defer func() { _ = transaction.Rollback() }()

	var status DestinationStatus
	var failureEventID string
	if err := transaction.QueryRowContext(
		ctx,
		`SELECT status, last_event_id
		 FROM f09_destination_status
		 WHERE workspace_id = $1 AND post_id = $2 AND destination_id = $3
		 FOR UPDATE`,
		retry.WorkspaceID,
		retry.PostID,
		retry.DestinationID,
	).Scan(&status, &failureEventID); errors.Is(err, sql.ErrNoRows) {
		return EnqueueResult{}, ErrNotFound
	} else if err != nil {
		return EnqueueResult{}, fmt.Errorf("lock failed destination: %w", err)
	}
	if status != DestinationFailed || failureEventID != retry.FailureEventID {
		return EnqueueResult{}, ErrConflict
	}

	var result EnqueueResult
	err = transaction.QueryRowContext(
		ctx,
		`INSERT INTO f09_manual_retry_outbox (
		     id, workspace_id, post_id, destination_id, failure_event_id,
		     actor_id, idempotency_key, state, next_attempt_at, created_at
		 )
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		 ON CONFLICT DO NOTHING
		 RETURNING id, state`,
		retry.ID,
		retry.WorkspaceID,
		retry.PostID,
		retry.DestinationID,
		retry.FailureEventID,
		retry.ActorID,
		retry.IdempotencyKey,
		retry.State,
		retry.NextAttemptAt,
		retry.CreatedAt,
	).Scan(&result.ID, &result.State)
	if err == nil {
		result.Created = true
	} else if errors.Is(err, sql.ErrNoRows) {
		// The same client key or another key for this failure cycle is a no-op.
		err = transaction.QueryRowContext(
			ctx,
			`SELECT id, state
			 FROM f09_manual_retry_outbox
			 WHERE workspace_id = $1
			   AND (
			       idempotency_key = $2
			       OR (destination_id = $3 AND failure_event_id = $4)
			   )
			 ORDER BY (idempotency_key = $2) DESC
			 LIMIT 1`,
			retry.WorkspaceID,
			retry.IdempotencyKey,
			retry.DestinationID,
			retry.FailureEventID,
		).Scan(&result.ID, &result.State)
	}
	if err != nil {
		return EnqueueResult{}, fmt.Errorf("enqueue manual retry: %w", err)
	}
	if err := transaction.Commit(); err != nil {
		return EnqueueResult{}, err
	}
	return result, nil
}

func (repository *SQLRepository) ClaimManualRetry(
	ctx context.Context,
	now, lockedUntil time.Time,
	leaseToken string,
) (ManualRetry, bool, error) {
	row := repository.database.QueryRowContext(
		ctx,
		`UPDATE f09_manual_retry_outbox
		 SET state = 'sending',
		     attempt_count = attempt_count + 1,
		     lease_token = $3,
		     locked_until = $2
		 WHERE id = (
		     SELECT id
		     FROM f09_manual_retry_outbox
		     WHERE next_attempt_at <= $1
		       AND (
		           state IN ('pending', 'retry')
		           OR (state = 'sending' AND locked_until <= $1)
		       )
		     ORDER BY next_attempt_at, id
		     FOR UPDATE SKIP LOCKED
		     LIMIT 1
		 )
		 RETURNING id, workspace_id, post_id, destination_id,
		     failure_event_id, actor_id, idempotency_key, state,
		     attempt_count, next_attempt_at, lease_token, locked_until,
		     created_at`,
		now,
		lockedUntil,
		leaseToken,
	)
	item, err := scanManualRetry(row)
	if errors.Is(err, sql.ErrNoRows) {
		return ManualRetry{}, false, nil
	}
	if err != nil {
		return ManualRetry{}, false, fmt.Errorf("claim manual retry: %w", err)
	}
	return item, true, nil
}

func (repository *SQLRepository) MarkManualRetryDelivered(
	ctx context.Context,
	id, leaseToken string,
	deliveredAt time.Time,
) error {
	return markQueueDelivered(
		ctx,
		repository.database,
		"f09_manual_retry_outbox",
		id,
		leaseToken,
		deliveredAt,
	)
}

func (repository *SQLRepository) MarkManualRetryRetry(
	ctx context.Context,
	id, leaseToken string,
	next time.Time,
) error {
	return markQueueRetry(
		ctx,
		repository.database,
		"f09_manual_retry_outbox",
		id,
		leaseToken,
		next,
	)
}

type rowScanner interface {
	Scan(...any) error
}

type queryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func recordStatusEvent(
	ctx context.Context,
	transaction *sql.Tx,
	eventID, fingerprint, kind, workspaceID, postID, destinationID string,
	occurredAt time.Time,
) (bool, error) {
	var inserted string
	err := transaction.QueryRowContext(
		ctx,
		`INSERT INTO f09_publication_status_events (
		     event_id, fingerprint, event_kind, workspace_id, post_id,
		     destination_id, occurred_at, received_at
		 )
		 VALUES ($1, $2, $3, $4, $5, NULLIF($6, ''), $7, CURRENT_TIMESTAMP)
		 ON CONFLICT (event_id) DO NOTHING
		 RETURNING event_id`,
		eventID,
		fingerprint,
		kind,
		workspaceID,
		postID,
		destinationID,
		occurredAt.UTC(),
	).Scan(&inserted)
	if err == nil {
		return true, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return false, fmt.Errorf("record status event: %w", err)
	}
	var existing string
	if err := transaction.QueryRowContext(
		ctx,
		`SELECT fingerprint
		 FROM f09_publication_status_events
		 WHERE event_id = $1`,
		eventID,
	).Scan(&existing); err != nil {
		return false, fmt.Errorf("read recorded status event: %w", err)
	}
	if existing != fingerprint {
		return false, ErrConflict
	}
	return false, nil
}

func getPostTx(
	ctx context.Context,
	transaction *sql.Tx,
	workspaceID, postID string,
) (PostView, error) {
	return getPostQuery(ctx, transaction, workspaceID, postID)
}

func getPostQuery(
	ctx context.Context,
	query queryer,
	workspaceID, postID string,
) (PostView, error) {
	var view PostView
	var draftID sql.NullString
	err := query.QueryRowContext(
		ctx,
		`SELECT workspace_id, post_id, draft_id, status, created_at, updated_at
		 FROM f09_post_status
		 WHERE workspace_id = $1 AND post_id = $2`,
		workspaceID,
		postID,
	).Scan(
		&view.WorkspaceID,
		&view.PostID,
		&draftID,
		&view.Status,
		&view.CreatedAt,
		&view.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return PostView{}, ErrNotFound
	}
	if err != nil {
		return PostView{}, fmt.Errorf("read post status: %w", err)
	}
	view.DraftID = draftID.String
	view.Destinations = []DestinationView{}
	rows, err := query.QueryContext(
		ctx,
		`SELECT destination_id, channel_id, status, COALESCE(remote_id, ''),
		     COALESCE(diagnostic_code, ''), COALESCE(diagnostic_message, ''),
		     diagnostic_retryable, diagnostic_at, last_event_id, updated_at
		 FROM f09_destination_status
		 WHERE workspace_id = $1 AND post_id = $2
		 ORDER BY destination_id`,
		workspaceID,
		postID,
	)
	if err != nil {
		return PostView{}, fmt.Errorf("list destination status: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var destination DestinationView
		var diagnosticAt sql.NullTime
		if err := rows.Scan(
			&destination.ID,
			&destination.ChannelID,
			&destination.Status,
			&destination.RemoteID,
			&destination.Diagnostic.Code,
			&destination.Diagnostic.Message,
			&destination.Diagnostic.Retryable,
			&diagnosticAt,
			&destination.LastEventID,
			&destination.UpdatedAt,
		); err != nil {
			return PostView{}, fmt.Errorf("scan destination status: %w", err)
		}
		if diagnosticAt.Valid {
			destination.Diagnostic.At = diagnosticAt.Time
		}
		view.Destinations = append(view.Destinations, destination)
	}
	if err := rows.Err(); err != nil {
		return PostView{}, fmt.Errorf("iterate destination status: %w", err)
	}
	return view, nil
}

func lockDestination(
	ctx context.Context,
	transaction *sql.Tx,
	workspaceID, destinationID string,
) (DestinationView, bool, error) {
	var destination DestinationView
	var remoteID, code, message sql.NullString
	var diagnosticAt sql.NullTime
	err := transaction.QueryRowContext(
		ctx,
		`SELECT destination_id, channel_id, status, remote_id,
		     diagnostic_code, diagnostic_message, diagnostic_retryable,
		     diagnostic_at, last_event_id, updated_at
		 FROM f09_destination_status
		 WHERE workspace_id = $1 AND destination_id = $2
		 FOR UPDATE`,
		workspaceID,
		destinationID,
	).Scan(
		&destination.ID,
		&destination.ChannelID,
		&destination.Status,
		&remoteID,
		&code,
		&message,
		&destination.Diagnostic.Retryable,
		&diagnosticAt,
		&destination.LastEventID,
		&destination.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return DestinationView{}, false, nil
	}
	if err != nil {
		return DestinationView{}, false, fmt.Errorf("lock destination status: %w", err)
	}
	destination.RemoteID = remoteID.String
	destination.Diagnostic.Code = code.String
	destination.Diagnostic.Message = message.String
	if diagnosticAt.Valid {
		destination.Diagnostic.At = diagnosticAt.Time
	}
	return destination, true, nil
}

func aggregateStatusTx(
	ctx context.Context,
	transaction *sql.Tx,
	workspaceID, postID string,
) (PostStatus, error) {
	rows, err := transaction.QueryContext(
		ctx,
		`SELECT status
		 FROM f09_destination_status
		 WHERE workspace_id = $1 AND post_id = $2`,
		workspaceID,
		postID,
	)
	if err != nil {
		return "", fmt.Errorf("read aggregate destinations: %w", err)
	}
	defer rows.Close()
	var destinations []DestinationView
	for rows.Next() {
		var status DestinationStatus
		if err := rows.Scan(&status); err != nil {
			return "", err
		}
		destinations = append(destinations, DestinationView{Status: status})
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	return aggregateStatus(destinations), nil
}

func scanNotification(row rowScanner) (Notification, error) {
	var item Notification
	var lockedUntil sql.NullTime
	err := row.Scan(
		&item.ID,
		&item.SourceEventID,
		&item.Kind,
		&item.AccountID,
		&item.WorkspaceID,
		&item.PostID,
		&item.DestinationID,
		&item.Subject,
		&item.Detail,
		&item.ActionLabel,
		&item.ActionURL,
		&item.IdempotencyKey,
		&item.State,
		&item.AttemptCount,
		&item.NextAttemptAt,
		&item.LeaseToken,
		&lockedUntil,
		&item.CreatedAt,
	)
	if lockedUntil.Valid {
		item.LockedUntil = &lockedUntil.Time
	}
	return item, err
}

func scanManualRetry(row rowScanner) (ManualRetry, error) {
	var item ManualRetry
	var lockedUntil sql.NullTime
	err := row.Scan(
		&item.ID,
		&item.WorkspaceID,
		&item.PostID,
		&item.DestinationID,
		&item.FailureEventID,
		&item.ActorID,
		&item.IdempotencyKey,
		&item.State,
		&item.AttemptCount,
		&item.NextAttemptAt,
		&item.LeaseToken,
		&lockedUntil,
		&item.CreatedAt,
	)
	if lockedUntil.Valid {
		item.LockedUntil = &lockedUntil.Time
	}
	return item, err
}

func markQueueDelivered(
	ctx context.Context,
	database *sql.DB,
	table, id, leaseToken string,
	deliveredAt time.Time,
) error {
	query := `UPDATE ` + table + `
		SET state = 'delivered', lease_token = NULL, locked_until = NULL,
		    delivered_at = $3
		WHERE id = $1 AND state = 'sending' AND lease_token = $2`
	result, err := database.ExecContext(ctx, query, id, leaseToken, deliveredAt)
	if err != nil {
		return fmt.Errorf("mark queue item delivered: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected != 1 {
		return ErrLeaseLost
	}
	return nil
}

func markQueueRetry(
	ctx context.Context,
	database *sql.DB,
	table, id, leaseToken string,
	next time.Time,
) error {
	query := `UPDATE ` + table + `
		SET state = 'retry', lease_token = NULL, locked_until = NULL,
		    next_attempt_at = $3
		WHERE id = $1 AND state = 'sending' AND lease_token = $2`
	result, err := database.ExecContext(ctx, query, id, leaseToken, next)
	if err != nil {
		return fmt.Errorf("mark queue item for retry: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected != 1 {
		return ErrLeaseLost
	}
	return nil
}

func nullTime(value time.Time) any {
	if value.IsZero() {
		return nil
	}
	return value
}
