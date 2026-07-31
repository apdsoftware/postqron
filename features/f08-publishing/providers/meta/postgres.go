package meta

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	publishing "github.com/apdsoftware/postqron/features/f08-publishing"
)

type NotificationSender interface {
	DeliverMetaNotification(context.Context, NotificationDelivery) (string, error)
}

const notificationMaxAttempts = 6

func notificationRetentionUntil(now time.Time) time.Time {
	return now.AddDate(1, 0, 0)
}

type NotificationDispatcher struct {
	store  *PostgresNotificationStore
	sender NotificationSender
	lease  time.Duration
}

func NewNotificationDispatcher(
	store *PostgresNotificationStore,
	sender NotificationSender,
	lease time.Duration,
) (*NotificationDispatcher, error) {
	if store == nil || sender == nil || lease <= 0 {
		return nil, publishing.ErrInvalidArgument
	}
	return &NotificationDispatcher{store: store, sender: sender, lease: lease}, nil
}

func (dispatcher *NotificationDispatcher) DispatchOne(
	ctx context.Context,
) (bool, error) {
	if dispatcher == nil || dispatcher.store == nil || dispatcher.sender == nil {
		return false, publishing.ErrProviderUnavailable
	}
	var random [18]byte
	if _, err := rand.Read(random[:]); err != nil {
		return false, fmt.Errorf("create Meta notification lease: %w", err)
	}
	now := dispatcher.store.clock().UTC()
	if _, err := dispatcher.store.PurgeExpired(ctx, now); err != nil {
		return false, err
	}
	delivery, found, err := dispatcher.store.ClaimDue(
		ctx,
		now,
		now.Add(dispatcher.lease),
		"lease_"+base64.RawURLEncoding.EncodeToString(random[:]),
	)
	if err != nil || !found {
		return found, err
	}
	emailDeliveryID, deliveryErr := dispatcher.sender.DeliverMetaNotification(
		ctx,
		delivery,
	)
	if strings.TrimSpace(emailDeliveryID) != "" {
		if err = dispatcher.store.RecordEmailDelivery(
			ctx,
			delivery.ID,
			delivery.LeaseToken,
			emailDeliveryID,
		); err != nil {
			return true, fmt.Errorf("record Meta notification email: %w", err)
		}
	}
	if err = deliveryErr; err != nil {
		var providerError *publishing.ProviderError
		failureCode := "notification_delivery_failed"
		if errors.As(err, &providerError) &&
			providerError.Code == "notification_not_delivered" {
			failureCode = providerError.Code
		}
		if (errors.As(err, &providerError) && !providerError.Retryable) ||
			delivery.AttemptCount >= notificationMaxAttempts {
			if markErr := dispatcher.store.MarkPermanentFailure(
				ctx,
				delivery.ID,
				delivery.LeaseToken,
				failureCode,
				now,
			); markErr != nil {
				return true, errors.Join(err, markErr)
			}
			return true, fmt.Errorf("permanently fail Meta notification: %w", err)
		}
		exponent := delivery.AttemptCount - 1
		if exponent < 0 {
			exponent = 0
		}
		if exponent > 8 {
			exponent = 8
		}
		next := now.Add(time.Minute * time.Duration(1<<exponent))
		if markErr := dispatcher.store.MarkRetry(
			ctx,
			delivery.ID,
			delivery.LeaseToken,
			"notification_delivery_failed",
			next,
		); markErr != nil {
			return true, errors.Join(err, markErr)
		}
		return true, fmt.Errorf("deliver Meta notification: %w", err)
	}
	if err = dispatcher.store.MarkDelivered(
		ctx,
		delivery.ID,
		delivery.LeaseToken,
		now,
	); err != nil {
		return true, fmt.Errorf("mark Meta notification delivered: %w", err)
	}
	return true, nil
}

func (store *PostgresNotificationStore) RecordEmailDelivery(
	ctx context.Context,
	id, leaseToken, emailDeliveryID string,
) error {
	result, err := store.database.ExecContext(ctx, `
		UPDATE f08_meta_notification_outbox
		   SET email_delivery_id = COALESCE(email_delivery_id, $3)
		 WHERE id = $1
		   AND state = 'sending'
		   AND lease_token = $2
		   AND (
		       email_delivery_id IS NULL
		       OR email_delivery_id = $3
		   )`,
		id,
		leaseToken,
		strings.TrimSpace(emailDeliveryID),
	)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows != 1 {
		return publishing.ErrConflict
	}
	return nil
}

type PostgresNotificationStore struct {
	database *sql.DB
	clock    func() time.Time
}

func NewPostgresNotificationStore(
	database *sql.DB,
	clock func() time.Time,
) (*PostgresNotificationStore, error) {
	if database == nil {
		return nil, publishing.ErrInvalidArgument
	}
	if clock == nil {
		clock = time.Now
	}
	return &PostgresNotificationStore{database: database, clock: clock}, nil
}

func (store *PostgresNotificationStore) PutIfAbsent(
	ctx context.Context,
	provider, workspaceID, postID, channelID, idempotencyKey string,
	payload json.RawMessage,
) (string, bool, error) {
	provider = strings.TrimSpace(provider)
	workspaceID = strings.TrimSpace(workspaceID)
	postID = strings.TrimSpace(postID)
	channelID = strings.TrimSpace(channelID)
	idempotencyKey = strings.TrimSpace(idempotencyKey)
	if store == nil || store.database == nil || provider == "" ||
		workspaceID == "" || postID == "" || channelID == "" ||
		idempotencyKey == "" || !json.Valid(payload) {
		return "", false, publishing.ErrInvalidArgument
	}
	canonicalPayload, err := canonicalJSON(payload)
	if err != nil {
		return "", false, publishing.ErrInvalidArgument
	}
	fingerprint := sha256.Sum256(canonicalPayload)
	deliveryID := StableNotificationID(provider, idempotencyKey)
	transaction, err := store.database.BeginTx(ctx, nil)
	if err != nil {
		return "", false, fmt.Errorf("begin Meta notification: %w", err)
	}
	defer transaction.Rollback()
	if _, err = transaction.ExecContext(
		ctx,
		`SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`,
		deliveryID,
	); err != nil {
		return "", false, fmt.Errorf("lock Meta notification idempotency: %w", err)
	}
	var tombstoneProvider, tombstoneFingerprint string
	err = transaction.QueryRowContext(ctx, `
		SELECT provider, payload_fingerprint
		  FROM f08_meta_notification_tombstones
		 WHERE id = $1
		   AND expires_at > $2`,
		deliveryID,
		store.clock().UTC(),
	).Scan(&tombstoneProvider, &tombstoneFingerprint)
	if err == nil {
		if tombstoneProvider != provider ||
			tombstoneFingerprint != hex.EncodeToString(fingerprint[:]) {
			return "", false, publishing.ErrConflict
		}
		if err = transaction.Commit(); err != nil {
			return "", false, fmt.Errorf(
				"commit Meta notification tombstone: %w",
				err,
			)
		}
		return deliveryID, false, &publishing.ProviderError{
			Code:   "notification_permanent_failure",
			Detail: "manual publishing notification was not delivered",
		}
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", false, fmt.Errorf("read Meta notification tombstone: %w", err)
	}
	var (
		persistedID          string
		persistedWorkspace   string
		persistedPost        string
		persistedChannel     string
		persistedFingerprint string
		state                string
	)
	err = transaction.QueryRowContext(ctx, `
		WITH recipient AS (
			SELECT membership.account_id,
			       CASE
			           WHEN lower(split_part(COALESCE(profile.locale, ''), '-', 1))
			               IN ('en', 'it', 'es', 'fr', 'de')
			           THEN lower(split_part(profile.locale, '-', 1))
			           ELSE 'en'
			       END AS locale
			  FROM f04_memberships membership
			  JOIN auth_accounts account ON account.id = membership.account_id
			  LEFT JOIN account_privacy_profiles profile
			    ON profile.account_id = membership.account_id
			 WHERE membership.workspace_id = $3
			   AND membership.status = 'active'
			   AND membership.role::text = 'owner'
			   AND account.email_verified_at IS NOT NULL
			 ORDER BY membership.account_id
			 LIMIT 1
		)
		INSERT INTO f08_meta_notification_outbox (
			id, provider, workspace_id, post_id, channel_id, recipient_id,
			locale, template_id, idempotency_key, payload_fingerprint, state,
			next_attempt_at, created_at
		)
		SELECT $1, $2, $3, $4, $5, recipient.account_id, recipient.locale,
		       CASE $2
		           WHEN 'facebook_groups' THEN 'facebook_group_manual_publish'
		           ELSE 'instagram_personal_manual_publish'
		       END,
		       $6, $7, 'pending', $8, $8
		  FROM recipient
		ON CONFLICT (provider, idempotency_key) DO UPDATE
		   SET provider = EXCLUDED.provider
		RETURNING id, workspace_id, post_id, channel_id,
		          payload_fingerprint, state`,
		deliveryID,
		provider,
		workspaceID,
		postID,
		channelID,
		idempotencyKey,
		hex.EncodeToString(fingerprint[:]),
		store.clock().UTC(),
	).Scan(
		&persistedID,
		&persistedWorkspace,
		&persistedPost,
		&persistedChannel,
		&persistedFingerprint,
		&state,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", false, fmt.Errorf(
				"%w: Meta notification recipient is unavailable",
				publishing.ErrProviderUnavailable,
			)
		}
		return "", false, fmt.Errorf("enqueue Meta notification: %w", err)
	}
	if persistedID != deliveryID || persistedWorkspace != workspaceID ||
		persistedPost != postID || persistedChannel != channelID ||
		persistedFingerprint != hex.EncodeToString(fingerprint[:]) {
		return "", false, publishing.ErrConflict
	}
	if err = transaction.QueryRowContext(
		ctx,
		`SELECT state FROM f08_meta_notification_outbox WHERE id = $1`,
		persistedID,
	).Scan(&state); err != nil {
		return "", false, fmt.Errorf("read Meta notification state: %w", err)
	}
	if err = transaction.Commit(); err != nil {
		return "", false, fmt.Errorf("commit Meta notification: %w", err)
	}
	if state == "permanent_failure" {
		return persistedID, false, &publishing.ProviderError{
			Code:   "notification_permanent_failure",
			Detail: "manual publishing notification was not delivered",
		}
	}
	return persistedID, state == "delivered", nil
}

type NotificationDelivery struct {
	ID             string
	Provider       string
	WorkspaceID    string
	PostID         string
	ChannelID      string
	RecipientID    string
	Locale         string
	TemplateID     string
	IdempotencyKey string
	AttemptCount   int
	LeaseToken     string
}

func (store *PostgresNotificationStore) ClaimDue(
	ctx context.Context,
	now, lockedUntil time.Time,
	leaseToken string,
) (NotificationDelivery, bool, error) {
	var delivery NotificationDelivery
	err := store.database.QueryRowContext(ctx, `
		UPDATE f08_meta_notification_outbox
		   SET state = 'sending',
		       attempt_count = attempt_count + 1,
		       lease_token = $3,
		       locked_until = $2
		 WHERE id = (
			SELECT id
			  FROM f08_meta_notification_outbox
			 WHERE next_attempt_at <= $1
			   AND (
			       state IN ('pending', 'retry')
			       OR (state = 'sending' AND locked_until <= $1)
			   )
			 ORDER BY next_attempt_at, id
			 FOR UPDATE SKIP LOCKED
			 LIMIT 1
		 )
		RETURNING id, provider, workspace_id, post_id, channel_id,
		          recipient_id, locale, template_id, idempotency_key, attempt_count,
		          lease_token`,
		now,
		lockedUntil,
		strings.TrimSpace(leaseToken),
	).Scan(
		&delivery.ID,
		&delivery.Provider,
		&delivery.WorkspaceID,
		&delivery.PostID,
		&delivery.ChannelID,
		&delivery.RecipientID,
		&delivery.Locale,
		&delivery.TemplateID,
		&delivery.IdempotencyKey,
		&delivery.AttemptCount,
		&delivery.LeaseToken,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return NotificationDelivery{}, false, nil
	}
	if err != nil {
		return NotificationDelivery{}, false, fmt.Errorf(
			"claim Meta notification: %w",
			err,
		)
	}
	return delivery, true, nil
}

func (store *PostgresNotificationStore) MarkDelivered(
	ctx context.Context,
	id, leaseToken string,
	now time.Time,
) error {
	return store.transitionDelivery(
		ctx,
		id,
		leaseToken,
		`state = 'delivered', delivered_at = $3, last_error_code = NULL,
		 retention_until = $4`,
		now,
		notificationRetentionUntil(now),
	)
}

func (store *PostgresNotificationStore) MarkPermanentFailure(
	ctx context.Context,
	id, leaseToken, code string,
	now time.Time,
) error {
	return store.transitionDelivery(
		ctx,
		id,
		leaseToken,
		`state = 'permanent_failure', permanent_failed_at = $3,
		 last_error_code = $4, retention_until = $5`,
		now,
		strings.TrimSpace(code),
		notificationRetentionUntil(now),
	)
}

func (store *PostgresNotificationStore) PurgeExpired(
	ctx context.Context,
	now time.Time,
) (int64, error) {
	transaction, err := store.database.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin Meta notification purge: %w", err)
	}
	defer transaction.Rollback()
	if _, err = transaction.ExecContext(ctx, `
		DELETE FROM f08_meta_notification_tombstones
		 WHERE expires_at <= $1`,
		now.UTC(),
	); err != nil {
		return 0, fmt.Errorf("purge Meta notification tombstones: %w", err)
	}
	rows, err := transaction.QueryContext(ctx, `
		SELECT id
		  FROM f08_meta_notification_outbox
		 WHERE state IN ('delivered', 'permanent_failure')
		   AND retention_until <= $1
		 ORDER BY retention_until, id
		 LIMIT 100`,
		now.UTC(),
	)
	if err != nil {
		return 0, fmt.Errorf("list expired Meta notification audit: %w", err)
	}
	var ids []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return 0, err
		}
		ids = append(ids, id)
	}
	if err = rows.Close(); err != nil {
		return 0, err
	}
	var purged int64
	for _, id := range ids {
		if _, err = transaction.ExecContext(ctx, `
			SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`,
			id,
		); err != nil {
			return 0, err
		}
		result, purgeErr := transaction.ExecContext(ctx, `
			WITH expired AS (
				DELETE FROM f08_meta_notification_outbox
				 WHERE id = $1
				   AND state IN ('delivered', 'permanent_failure')
				   AND retention_until <= $2
				RETURNING id, provider, payload_fingerprint, retention_until
			)
			INSERT INTO f08_meta_notification_tombstones (
				id, provider, payload_fingerprint, outcome, expires_at
			)
			SELECT id, provider, payload_fingerprint, 'permanent_failure',
			       retention_until + INTERVAL '12 months'
			  FROM expired
			ON CONFLICT (id) DO UPDATE
			   SET expires_at = GREATEST(
			           f08_meta_notification_tombstones.expires_at,
			           EXCLUDED.expires_at
			       )`,
			id,
			now.UTC(),
		)
		if purgeErr != nil {
			return 0, fmt.Errorf("purge Meta notification audit: %w", purgeErr)
		}
		if affected, rowsErr := result.RowsAffected(); rowsErr != nil {
			return 0, rowsErr
		} else {
			purged += affected
		}
	}
	if err = transaction.Commit(); err != nil {
		return 0, fmt.Errorf("commit Meta notification purge: %w", err)
	}
	return purged, nil
}

func (store *PostgresNotificationStore) MarkRetry(
	ctx context.Context,
	id, leaseToken, code string,
	next time.Time,
) error {
	return store.transitionDelivery(
		ctx,
		id,
		leaseToken,
		`state = 'retry', next_attempt_at = $3, last_error_code = $4`,
		next,
		strings.TrimSpace(code),
	)
}

func (store *PostgresNotificationStore) transitionDelivery(
	ctx context.Context,
	id, leaseToken, assignment string,
	value time.Time,
	extra ...any,
) error {
	arguments := []any{id, leaseToken, value}
	arguments = append(arguments, extra...)
	result, err := store.database.ExecContext(
		ctx,
		`UPDATE f08_meta_notification_outbox SET `+assignment+`,
		        lease_token = NULL, locked_until = NULL
		  WHERE id = $1 AND state = 'sending' AND lease_token = $2`,
		arguments...,
	)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows != 1 {
		return publishing.ErrConflict
	}
	return nil
}

func canonicalJSON(payload json.RawMessage) ([]byte, error) {
	var value any
	if err := json.Unmarshal(payload, &value); err != nil {
		return nil, err
	}
	canonical, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	if len(canonical) == 0 {
		return nil, errors.New("empty JSON payload")
	}
	return canonical, nil
}

var _ NotificationStore = (*PostgresNotificationStore)(nil)
