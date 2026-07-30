package meta

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	publishing "github.com/apdsoftware/postqron/features/f08-publishing"
)

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
	provider, workspaceID, idempotencyKey string,
	payload json.RawMessage,
) (string, error) {
	provider = strings.TrimSpace(provider)
	workspaceID = strings.TrimSpace(workspaceID)
	idempotencyKey = strings.TrimSpace(idempotencyKey)
	if store == nil || store.database == nil || provider == "" ||
		workspaceID == "" || idempotencyKey == "" || !json.Valid(payload) {
		return "", publishing.ErrInvalidArgument
	}
	canonicalPayload, err := canonicalJSON(payload)
	if err != nil {
		return "", publishing.ErrInvalidArgument
	}
	fingerprint := sha256.Sum256(canonicalPayload)
	deliveryID := StableNotificationID(provider, idempotencyKey)
	transaction, err := store.database.BeginTx(ctx, nil)
	if err != nil {
		return "", fmt.Errorf("begin Meta notification: %w", err)
	}
	defer transaction.Rollback()
	var (
		persistedID          string
		persistedWorkspace   string
		persistedFingerprint string
	)
	err = transaction.QueryRowContext(ctx, `
		INSERT INTO f08_meta_notification_outbox (
			id, provider, workspace_id, idempotency_key, payload,
			payload_fingerprint, state, created_at
		) VALUES ($1, $2, $3, $4, $5::jsonb, $6, 'pending', $7)
		ON CONFLICT (provider, idempotency_key) DO UPDATE
		   SET provider = EXCLUDED.provider
		RETURNING id, workspace_id, payload_fingerprint`,
		deliveryID,
		provider,
		workspaceID,
		idempotencyKey,
		canonicalPayload,
		hex.EncodeToString(fingerprint[:]),
		store.clock().UTC(),
	).Scan(&persistedID, &persistedWorkspace, &persistedFingerprint)
	if err != nil {
		return "", fmt.Errorf("enqueue Meta notification: %w", err)
	}
	if persistedID != deliveryID || persistedWorkspace != workspaceID ||
		persistedFingerprint != hex.EncodeToString(fingerprint[:]) {
		return "", publishing.ErrConflict
	}
	if err = transaction.Commit(); err != nil {
		return "", fmt.Errorf("commit Meta notification: %w", err)
	}
	return persistedID, nil
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
