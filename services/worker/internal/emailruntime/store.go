package emailruntime

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	email "github.com/apdsoftware/postqron/features/f14-email"
)

type sqlStore struct {
	database *sql.DB
}

const emailDeliveryLease = 2 * time.Minute

func (store *sqlStore) Enqueue(
	ctx context.Context,
	delivery email.Delivery,
) (email.EnqueueResult, error) {
	headers, err := json.Marshal(map[string]string{})
	if err != nil {
		return email.EnqueueResult{}, err
	}
	result, err := store.database.ExecContext(ctx, `
		INSERT INTO f14_email_deliveries (
			id, idempotency_key, channel, template_id, template_version,
			recipient_id, recipient_email, recipient_name, subject, preheader,
			html_body, text_body, locale, source_workspace_id, message_headers,
			state, attempt_count, max_attempts, next_attempt_at, created_at,
			updated_at
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14,
			$15, 'pending', 0, $16, $17, $18, $18
		)
		ON CONFLICT (channel, idempotency_key) DO NOTHING`,
		delivery.Message.ID,
		delivery.Message.IdempotencyKey,
		delivery.Message.Channel,
		delivery.Message.Template,
		delivery.Message.TemplateVersion,
		delivery.Rendered.Recipient.ID,
		delivery.Rendered.Recipient.Email,
		delivery.Rendered.Recipient.Name,
		delivery.Rendered.Subject,
		delivery.Rendered.Preheader,
		delivery.Rendered.HTML,
		delivery.Rendered.Text,
		delivery.Rendered.Locale,
		nullableWorkspaceID(delivery.Message.SourceWorkspaceID),
		headers,
		delivery.Message.MaxAttempts,
		delivery.NextAttemptAt,
		delivery.Message.CreatedAt,
	)
	if err != nil {
		return email.EnqueueResult{}, fmt.Errorf("insert email delivery: %w", err)
	}
	created := true
	if rows, rowsErr := result.RowsAffected(); rowsErr == nil && rows == 0 {
		created = false
	}
	var (
		id                string
		state             email.DeliveryState
		sourceWorkspaceID sql.NullString
		recipientID       string
		templateID        email.TemplateID
		templateVersion   string
	)
	err = store.database.QueryRowContext(ctx, `
		SELECT id, state, source_workspace_id, recipient_id,
		       template_id, template_version
		  FROM f14_email_deliveries
		 WHERE channel = $1 AND idempotency_key = $2`,
		delivery.Message.Channel,
		delivery.Message.IdempotencyKey,
	).Scan(
		&id,
		&state,
		&sourceWorkspaceID,
		&recipientID,
		&templateID,
		&templateVersion,
	)
	if err != nil {
		return email.EnqueueResult{}, fmt.Errorf("read email delivery: %w", err)
	}
	if sourceWorkspaceID.String != delivery.Message.SourceWorkspaceID ||
		recipientID != delivery.Rendered.Recipient.ID ||
		templateID != delivery.Message.Template ||
		templateVersion != delivery.Message.TemplateVersion {
		return email.EnqueueResult{}, errors.New(
			"email idempotency binding conflict",
		)
	}
	return email.EnqueueResult{ID: id, Created: created, State: state}, nil
}

func nullableWorkspaceID(workspaceID string) any {
	workspaceID = strings.TrimSpace(workspaceID)
	if workspaceID == "" {
		return nil
	}
	return workspaceID
}

func (store *sqlStore) ReconcileExpiredLeases(
	ctx context.Context,
	now time.Time,
) (int64, error) {
	result, err := store.database.ExecContext(ctx, `
		UPDATE f14_email_deliveries
		   SET state = 'failed',
		       last_diagnostic_code = CASE
		           WHEN provider_call_started_at IS NOT NULL
		           THEN 'ambiguous_delivery'
		           ELSE 'lease_attempts_exhausted'
		       END,
		       last_diagnostic_detail = '',
		       updated_at = $1,
		       retention_until = $2,
		       lease_token = NULL,
		       locked_until = NULL,
		       provider_call_started_at = NULL
		 WHERE state = 'sending'
		   AND locked_until <= $1
		   AND (
		       provider_call_started_at IS NOT NULL
		       OR attempt_count >= max_attempts
		   )`,
		now,
		now.AddDate(1, 0, 0),
	)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

func (store *sqlStore) ClaimDue(
	ctx context.Context,
	now time.Time,
) (email.Delivery, bool, error) {
	var random [18]byte
	if _, err := rand.Read(random[:]); err != nil {
		return email.Delivery{}, false, err
	}
	leaseToken := "email_lease_" +
		base64.RawURLEncoding.EncodeToString(random[:])
	row := store.database.QueryRowContext(ctx, `
		SELECT id,
		       idempotency_key,
		       channel,
		       template_id,
		       template_version,
		       recipient_id,
		       recipient_email,
		       recipient_name,
		       subject,
		       preheader,
		       html_body,
		       text_body,
		       locale,
		       state,
		       attempt_count,
		       max_attempts,
		       next_attempt_at,
		       created_at,
		       lease_token,
		       locked_until,
		       provider_call_started_at
		  FROM f14_claim_email_delivery_v2($1, $2, $3)`,
		now,
		leaseToken,
		now.Add(emailDeliveryLease),
	)
	delivery, err := scanDelivery(row)
	if errors.Is(err, sql.ErrNoRows) {
		return email.Delivery{}, false, nil
	}
	if err != nil {
		return email.Delivery{}, false, err
	}
	return delivery, true, nil
}

func (store *sqlStore) MarkProviderCallStarted(
	ctx context.Context,
	id, leaseToken string,
	now time.Time,
) error {
	result, err := store.database.ExecContext(ctx, `
		UPDATE f14_email_deliveries
		   SET attempt_count = attempt_count + 1,
		       provider_call_started_at = $3,
		       updated_at = $3
		 WHERE id = $1
		   AND state = 'sending'
		   AND lease_token = $2
		   AND locked_until > $3
		   AND provider_call_started_at IS NULL
		   AND attempt_count < max_attempts`,
		id,
		leaseToken,
		now,
	)
	if err != nil {
		return err
	}
	return requireOneEmailRow(result)
}

func (store *sqlStore) MarkAccepted(
	ctx context.Context,
	id, leaseToken, providerID string,
	now time.Time,
) error {
	result, err := store.database.ExecContext(ctx, `
		UPDATE f14_email_deliveries
		   SET state = 'accepted',
		       provider_message_id = $3,
		       accepted_at = $4,
		       updated_at = $4,
		       retention_until = $5,
		       lease_token = NULL,
		       locked_until = NULL,
		       provider_call_started_at = NULL
		 WHERE id = $1
		   AND state = 'sending'
		   AND lease_token = $2
		   AND provider_call_started_at IS NOT NULL`,
		id,
		leaseToken,
		providerID,
		now,
		now.AddDate(1, 0, 0),
	)
	if err != nil {
		return err
	}
	return requireOneEmailRow(result)
}

func (store *sqlStore) MarkRetry(
	ctx context.Context,
	id, leaseToken string,
	diagnostic email.Diagnostic,
	next time.Time,
) error {
	result, err := store.database.ExecContext(ctx, `
		UPDATE f14_email_deliveries
		   SET state = 'retry',
		       next_attempt_at = $2,
		       last_diagnostic_code = $3,
		       last_diagnostic_detail = $4,
		       updated_at = $5,
		       lease_token = NULL,
		       locked_until = NULL,
		       provider_call_started_at = NULL
		 WHERE id = $1
		   AND state = 'sending'
		   AND lease_token = $6
		   AND provider_call_started_at IS NOT NULL`,
		id,
		next,
		diagnostic.Code,
		diagnostic.Detail,
		diagnostic.At,
		leaseToken,
	)
	if err != nil {
		return err
	}
	return requireOneEmailRow(result)
}

func (store *sqlStore) MarkFailed(
	ctx context.Context,
	id, leaseToken string,
	diagnostic email.Diagnostic,
) error {
	result, err := store.database.ExecContext(ctx, `
		UPDATE f14_email_deliveries
		   SET state = 'failed',
		       last_diagnostic_code = $2,
		       last_diagnostic_detail = $3,
		       updated_at = $4,
		       retention_until = $5,
		       lease_token = NULL,
		       locked_until = NULL,
		       provider_call_started_at = NULL
		 WHERE id = $1
		   AND state = 'sending'
		   AND lease_token = $6
		   AND provider_call_started_at IS NOT NULL`,
		id,
		diagnostic.Code,
		diagnostic.Detail,
		diagnostic.At,
		diagnostic.At.AddDate(1, 0, 0),
		leaseToken,
	)
	if err != nil {
		return err
	}
	return requireOneEmailRow(result)
}

func scanDelivery(row *sql.Row) (email.Delivery, error) {
	var (
		delivery        email.Delivery
		channel         email.Channel
		templateID      email.TemplateID
		templateVersion string
		locale          email.Locale
		state           email.DeliveryState
		attemptCount    int
		providerCallAt  sql.NullTime
	)
	err := row.Scan(
		&delivery.Message.ID,
		&delivery.Message.IdempotencyKey,
		&channel,
		&templateID,
		&templateVersion,
		&delivery.Rendered.Recipient.ID,
		&delivery.Rendered.Recipient.Email,
		&delivery.Rendered.Recipient.Name,
		&delivery.Rendered.Subject,
		&delivery.Rendered.Preheader,
		&delivery.Rendered.HTML,
		&delivery.Rendered.Text,
		&locale,
		&state,
		&attemptCount,
		&delivery.Message.MaxAttempts,
		&delivery.NextAttemptAt,
		&delivery.Message.CreatedAt,
		&delivery.LeaseToken,
		&delivery.LockedUntil,
		&providerCallAt,
	)
	if err != nil {
		return email.Delivery{}, err
	}
	delivery.Message.Channel = channel
	delivery.Message.Template = templateID
	delivery.Message.TemplateVersion = templateVersion
	delivery.Rendered.Channel = channel
	delivery.Rendered.Template = templateID
	delivery.Rendered.TemplateVersion = templateVersion
	delivery.Rendered.Locale = locale
	delivery.State = state
	delivery.Attempt = attemptCount
	if providerCallAt.Valid {
		delivery.ProviderCallAt = providerCallAt.Time
	}
	return delivery, nil
}

func requireOneEmailRow(result sql.Result) error {
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows != 1 {
		return errors.New("email delivery state changed concurrently")
	}
	return nil
}
