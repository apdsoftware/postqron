package pwa

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
)

type PostgresRepository struct {
	database *sql.DB
	cipher   CredentialCipher
}

func NewPostgresRepository(
	database *sql.DB,
	cipher CredentialCipher,
) (*PostgresRepository, error) {
	if database == nil || cipher == nil {
		return nil, fmt.Errorf(
			"%w: push database and credential cipher are required",
			ErrInvalidArgument,
		)
	}
	return &PostgresRepository{database: database, cipher: cipher}, nil
}

func (repository *PostgresRepository) UpsertSubscription(
	ctx context.Context,
	subscription Subscription,
) (Subscription, bool, error) {
	endpoint, p256dh, auth, err := repository.sealSubscription(subscription)
	if err != nil {
		return Subscription{}, false, err
	}
	var created bool
	err = repository.database.QueryRowContext(ctx, `
		INSERT INTO f23_push_subscriptions (
			id, account_id, endpoint_hash, key_id, endpoint_ciphertext,
			p256dh_ciphertext, auth_ciphertext, expiration_time,
			created_at, updated_at, revoked_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, NULL)
		ON CONFLICT (id) DO UPDATE SET
			key_id = EXCLUDED.key_id,
			endpoint_ciphertext = EXCLUDED.endpoint_ciphertext,
			p256dh_ciphertext = EXCLUDED.p256dh_ciphertext,
			auth_ciphertext = EXCLUDED.auth_ciphertext,
			expiration_time = EXCLUDED.expiration_time,
			updated_at = EXCLUDED.updated_at,
			revoked_at = NULL
		WHERE f23_push_subscriptions.account_id = EXCLUDED.account_id
			AND f23_push_subscriptions.endpoint_hash = EXCLUDED.endpoint_hash
		RETURNING (xmax = 0), created_at`,
		subscription.ID,
		subscription.AccountID,
		digest(subscription.Endpoint),
		endpoint.KeyID,
		endpoint.Data,
		p256dh.Data,
		auth.Data,
		subscription.ExpirationTime,
		subscription.CreatedAt,
		subscription.UpdatedAt,
	).Scan(&created, &subscription.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Subscription{}, false, ErrConflict
	}
	if err != nil {
		return Subscription{}, false, err
	}
	return subscription, created, nil
}

func (repository *PostgresRepository) RevokeSubscription(
	ctx context.Context,
	accountID, endpoint string,
	now time.Time,
) (bool, error) {
	result, err := repository.database.ExecContext(ctx, `
		UPDATE f23_push_subscriptions
		SET revoked_at = $3, updated_at = $3
		WHERE account_id = $1 AND endpoint_hash = $2 AND revoked_at IS NULL`,
		accountID,
		digest(endpoint),
		now,
	)
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	return affected > 0, err
}

func (repository *PostgresRepository) ActiveSubscriptions(
	ctx context.Context,
	accountIDs []string,
	now time.Time,
) ([]Subscription, error) {
	if len(accountIDs) == 0 {
		return []Subscription{}, nil
	}
	placeholders := make([]string, len(accountIDs))
	arguments := make([]any, 0, len(accountIDs)+1)
	for index, accountID := range accountIDs {
		placeholders[index] = fmt.Sprintf("$%d", index+1)
		arguments = append(arguments, accountID)
	}
	arguments = append(arguments, now)
	query := `
		SELECT
			id, account_id, key_id, endpoint_ciphertext,
			p256dh_ciphertext, auth_ciphertext, expiration_time,
			created_at, updated_at, revoked_at
		FROM f23_push_subscriptions
		WHERE account_id IN (` + strings.Join(placeholders, ", ") + `)
			AND revoked_at IS NULL
			AND (expiration_time IS NULL OR expiration_time > $` +
		fmt.Sprint(len(arguments)) + `)
		ORDER BY id`
	rows, err := repository.database.QueryContext(ctx, query, arguments...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]Subscription, 0)
	for rows.Next() {
		subscription, scanErr := repository.scanSubscription(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		result = append(result, subscription)
	}
	return result, rows.Err()
}

func (repository *PostgresRepository) EnqueueDeliveries(
	ctx context.Context,
	event PushEvent,
	subscriptions []Subscription,
	now time.Time,
) (int, error) {
	transaction, err := repository.database.BeginTx(ctx, &sql.TxOptions{
		Isolation: sql.LevelReadCommitted,
	})
	if err != nil {
		return 0, err
	}
	defer transaction.Rollback()

	fingerprint := digest(eventFingerprint(event))
	result, err := transaction.ExecContext(ctx, `
		INSERT INTO f23_push_events (
			event_id, fingerprint, kind, workspace_id, resource_id,
			title, body, action_url, occurred_at, received_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		ON CONFLICT (event_id) DO NOTHING`,
		event.EventID,
		fingerprint,
		event.Kind,
		event.WorkspaceID,
		event.ResourceID,
		event.Title,
		event.Body,
		event.ActionURL,
		event.OccurredAt,
		now,
	)
	if err != nil {
		return 0, err
	}
	inserted, err := result.RowsAffected()
	if err != nil {
		return 0, err
	}
	if inserted == 0 {
		var existing string
		if err := transaction.QueryRowContext(ctx, `
			SELECT fingerprint FROM f23_push_events WHERE event_id = $1`,
			event.EventID,
		).Scan(&existing); err != nil {
			return 0, err
		}
		if existing != fingerprint {
			return 0, ErrConflict
		}
		return 0, transaction.Commit()
	}

	created := 0
	for _, subscription := range subscriptions {
		result, err = transaction.ExecContext(ctx, `
			INSERT INTO f23_push_deliveries (
				id, source_event_id, subscription_id, state,
				attempt_count, next_attempt_at, created_at
			) VALUES ($1, $2, $3, 'pending', 0, $4, $4)
			ON CONFLICT (source_event_id, subscription_id) DO NOTHING`,
			stableID("delivery", event.EventID, subscription.ID),
			event.EventID,
			subscription.ID,
			now,
		)
		if err != nil {
			return 0, err
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return 0, err
		}
		created += int(affected)
	}
	if err := transaction.Commit(); err != nil {
		return 0, err
	}
	return created, nil
}

func (repository *PostgresRepository) ClaimDelivery(
	ctx context.Context,
	now, lockedUntil time.Time,
	leaseToken string,
) (Delivery, Subscription, bool, error) {
	transaction, err := repository.database.BeginTx(ctx, &sql.TxOptions{
		Isolation: sql.LevelReadCommitted,
	})
	if err != nil {
		return Delivery{}, Subscription{}, false, err
	}
	defer transaction.Rollback()

	var delivery Delivery
	var subscription Subscription
	var kind string
	var keyID string
	var endpoint, p256dh, auth []byte
	var expiration, revoked sql.NullTime
	err = transaction.QueryRowContext(ctx, `
		SELECT
			d.id, d.source_event_id, d.subscription_id, d.state,
			d.attempt_count, d.next_attempt_at, d.created_at,
			e.kind, e.workspace_id, e.resource_id, e.title, e.body,
			e.action_url, e.occurred_at,
			s.id, s.account_id, s.key_id, s.endpoint_ciphertext,
			s.p256dh_ciphertext, s.auth_ciphertext, s.expiration_time,
			s.created_at, s.updated_at, s.revoked_at
		FROM f23_push_deliveries d
		JOIN f23_push_events e ON e.event_id = d.source_event_id
		JOIN f23_push_subscriptions s ON s.id = d.subscription_id
		WHERE (
				d.state IN ('pending', 'retry')
				OR (
					d.state = 'sending'
					AND d.locked_until IS NOT NULL
					AND d.locked_until <= $1
				)
			)
			AND d.next_attempt_at <= $1
			AND s.revoked_at IS NULL
		ORDER BY d.next_attempt_at, d.id
		FOR UPDATE OF d SKIP LOCKED
		LIMIT 1`,
		now,
	).Scan(
		&delivery.ID,
		&delivery.SourceEventID,
		&delivery.SubscriptionID,
		&delivery.State,
		&delivery.AttemptCount,
		&delivery.NextAttemptAt,
		&delivery.CreatedAt,
		&kind,
		&delivery.Event.WorkspaceID,
		&delivery.Event.ResourceID,
		&delivery.Event.Title,
		&delivery.Event.Body,
		&delivery.Event.ActionURL,
		&delivery.Event.OccurredAt,
		&subscription.ID,
		&subscription.AccountID,
		&keyID,
		&endpoint,
		&p256dh,
		&auth,
		&expiration,
		&subscription.CreatedAt,
		&subscription.UpdatedAt,
		&revoked,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return Delivery{}, Subscription{}, false, nil
	}
	if err != nil {
		return Delivery{}, Subscription{}, false, err
	}
	delivery.Event.EventID = delivery.SourceEventID
	delivery.Event.Kind = EventKind(kind)
	if expiration.Valid {
		subscription.ExpirationTime = &expiration.Time
	}
	if err := repository.openSubscription(
		&subscription,
		keyID,
		endpoint,
		p256dh,
		auth,
	); err != nil {
		return Delivery{}, Subscription{}, false, err
	}
	if _, err = transaction.ExecContext(ctx, `
		UPDATE f23_push_deliveries
		SET state = 'sending', lease_token = $2, locked_until = $3
		WHERE id = $1`,
		delivery.ID,
		leaseToken,
		lockedUntil,
	); err != nil {
		return Delivery{}, Subscription{}, false, err
	}
	if err := transaction.Commit(); err != nil {
		return Delivery{}, Subscription{}, false, err
	}
	delivery.State = DeliverySending
	delivery.LeaseToken = leaseToken
	delivery.LockedUntil = &lockedUntil
	return delivery, subscription, true, nil
}

func (repository *PostgresRepository) MarkDelivered(
	ctx context.Context,
	id, leaseToken string,
	now time.Time,
) error {
	return repository.complete(ctx, id, leaseToken, `
		state = 'delivered', delivered_at = $3`, now)
}

func (repository *PostgresRepository) MarkRetry(
	ctx context.Context,
	id, leaseToken string,
	attempt int,
	nextAttemptAt time.Time,
) error {
	result, err := repository.database.ExecContext(ctx, `
		UPDATE f23_push_deliveries
		SET state = 'retry', attempt_count = $3, next_attempt_at = $4,
			lease_token = NULL, locked_until = NULL
		WHERE id = $1 AND state = 'sending' AND lease_token = $2`,
		id,
		leaseToken,
		attempt,
		nextAttemptAt,
	)
	return requireChanged(result, err)
}

func (repository *PostgresRepository) MarkFailed(
	ctx context.Context,
	id, leaseToken string,
	now time.Time,
) error {
	return repository.complete(ctx, id, leaseToken, `
		state = 'failed', failed_at = $3`, now)
}

func (repository *PostgresRepository) complete(
	ctx context.Context,
	id, leaseToken, assignment string,
	now time.Time,
) error {
	result, err := repository.database.ExecContext(ctx, `
		UPDATE f23_push_deliveries SET `+assignment+`,
			lease_token = NULL, locked_until = NULL
		WHERE id = $1 AND state = 'sending' AND lease_token = $2`,
		id,
		leaseToken,
		now,
	)
	return requireChanged(result, err)
}

func (repository *PostgresRepository) ExpireSubscription(
	ctx context.Context,
	id string,
	now time.Time,
) error {
	result, err := repository.database.ExecContext(ctx, `
		UPDATE f23_push_subscriptions
		SET revoked_at = COALESCE(revoked_at, $2), updated_at = $2
		WHERE id = $1`,
		id,
		now,
	)
	return requireChanged(result, err)
}

type rowScanner interface {
	Scan(...any) error
}

func (repository *PostgresRepository) scanSubscription(
	row rowScanner,
) (Subscription, error) {
	var subscription Subscription
	var keyID string
	var endpoint, p256dh, auth []byte
	var expiration, revoked sql.NullTime
	err := row.Scan(
		&subscription.ID,
		&subscription.AccountID,
		&keyID,
		&endpoint,
		&p256dh,
		&auth,
		&expiration,
		&subscription.CreatedAt,
		&subscription.UpdatedAt,
		&revoked,
	)
	if err != nil {
		return Subscription{}, err
	}
	if expiration.Valid {
		subscription.ExpirationTime = &expiration.Time
	}
	if revoked.Valid {
		subscription.RevokedAt = &revoked.Time
	}
	if err := repository.openSubscription(
		&subscription,
		keyID,
		endpoint,
		p256dh,
		auth,
	); err != nil {
		return Subscription{}, err
	}
	return subscription, nil
}

func (repository *PostgresRepository) sealSubscription(
	subscription Subscription,
) (Ciphertext, Ciphertext, Ciphertext, error) {
	values := []string{
		subscription.Endpoint,
		subscription.P256DH,
		subscription.Auth,
	}
	fields := []string{"endpoint", "p256dh", "auth"}
	result := make([]Ciphertext, len(values))
	for index, value := range values {
		sealed, err := repository.cipher.Seal(
			[]byte(value),
			credentialAAD(subscription.ID, fields[index]),
		)
		if err != nil {
			return Ciphertext{}, Ciphertext{}, Ciphertext{}, err
		}
		if index > 0 && sealed.KeyID != result[0].KeyID {
			return Ciphertext{}, Ciphertext{}, Ciphertext{}, errors.New(
				"push credentials must use one encryption key",
			)
		}
		result[index] = sealed
	}
	return result[0], result[1], result[2], nil
}

func (repository *PostgresRepository) openSubscription(
	subscription *Subscription,
	keyID string,
	endpoint, p256dh, auth []byte,
) error {
	destinations := []*string{
		&subscription.Endpoint,
		&subscription.P256DH,
		&subscription.Auth,
	}
	fields := []string{"endpoint", "p256dh", "auth"}
	values := [][]byte{endpoint, p256dh, auth}
	for index, value := range values {
		plaintext, err := repository.cipher.Open(
			Ciphertext{KeyID: keyID, Data: value},
			credentialAAD(subscription.ID, fields[index]),
		)
		if err != nil {
			return fmt.Errorf("decrypt push %s: %w", fields[index], err)
		}
		*destinations[index] = string(plaintext)
	}
	return nil
}

func credentialAAD(subscriptionID, field string) []byte {
	return []byte(FeatureID + "\x00" + subscriptionID + "\x00" + field)
}

func digest(value string) string {
	hashed := sha256.Sum256([]byte(value))
	return hex.EncodeToString(hashed[:])
}

func requireChanged(result sql.Result, err error) error {
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected != 1 {
		return ErrConflict
	}
	return nil
}
