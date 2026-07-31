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
	DeliverMetaNotification(context.Context, NotificationDelivery) error
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
	delivery, found, err := dispatcher.store.ClaimDue(
		ctx,
		now,
		now.Add(dispatcher.lease),
		"lease_"+base64.RawURLEncoding.EncodeToString(random[:]),
	)
	if err != nil || !found {
		return found, err
	}
	if err = dispatcher.sender.DeliverMetaNotification(ctx, delivery); err != nil {
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
			SELECT membership.account_id
			  FROM f04_memberships membership
			  JOIN auth_accounts account ON account.id = membership.account_id
			 WHERE membership.workspace_id = $3
			   AND membership.status = 'active'
			   AND membership.role::text = 'owner'
			   AND account.email_verified_at IS NOT NULL
			 ORDER BY membership.account_id
			 LIMIT 1
		)
		INSERT INTO f08_meta_notification_outbox (
			id, provider, workspace_id, post_id, channel_id, recipient_id,
			idempotency_key, payload, payload_fingerprint, state,
			next_attempt_at, created_at
		)
		SELECT $1, $2, $3, $4, $5, recipient.account_id,
		       $6, $7::jsonb, $8, 'pending', $9, $9
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
		canonicalPayload,
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
	return persistedID, state == "delivered", nil
}

type NotificationDelivery struct {
	ID             string
	Provider       string
	WorkspaceID    string
	PostID         string
	ChannelID      string
	RecipientID    string
	IdempotencyKey string
	Payload        json.RawMessage
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
		          recipient_id, idempotency_key, payload, attempt_count,
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
		&delivery.IdempotencyKey,
		&delivery.Payload,
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
		`state = 'delivered', delivered_at = $3, last_error_code = NULL`,
		now,
	)
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
