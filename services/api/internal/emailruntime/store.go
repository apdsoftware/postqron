package emailruntime

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	email "github.com/apdsoftware/postqron/features/f14-email"
)

type sqlStore struct {
	database *sql.DB
}

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
			id,
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
			message_headers,
			state,
			attempt_count,
			max_attempts,
			next_attempt_at,
			created_at,
			updated_at
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14,
			'pending', 0, $15, $16, $17, $17
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
	row := store.database.QueryRowContext(ctx, `
		SELECT id, state
		  FROM f14_email_deliveries
		 WHERE channel = $1
		   AND idempotency_key = $2`,
		delivery.Message.Channel,
		delivery.Message.IdempotencyKey,
	)
	var id string
	var state email.DeliveryState
	if err := row.Scan(&id, &state); err != nil {
		return email.EnqueueResult{}, fmt.Errorf("read email delivery: %w", err)
	}
	return email.EnqueueResult{
		ID:      id,
		Created: created,
		State:   state,
	}, nil
}

func (store *sqlStore) ClaimDue(
	ctx context.Context,
	now time.Time,
) (email.Delivery, bool, error) {
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
		       created_at
		  FROM f14_claim_email_delivery($1)`,
		now,
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

func (store *sqlStore) MarkAccepted(
	ctx context.Context,
	id, providerID string,
	now time.Time,
) error {
	result, err := store.database.ExecContext(ctx, `
		UPDATE f14_email_deliveries
		   SET state = 'accepted',
		       provider_message_id = $2,
		       accepted_at = $3,
		       updated_at = $3
		 WHERE id = $1
		   AND state = 'sending'`,
		id,
		providerID,
		now,
	)
	if err != nil {
		return err
	}
	return requireOneEmailRow(result)
}

func (store *sqlStore) MarkRetry(
	ctx context.Context,
	id string,
	diagnostic email.Diagnostic,
	next time.Time,
) error {
	result, err := store.database.ExecContext(ctx, `
		UPDATE f14_email_deliveries
		   SET state = 'retry',
		       next_attempt_at = $2,
		       last_diagnostic_code = $3,
		       last_diagnostic_detail = $4,
		       updated_at = $5
		 WHERE id = $1
		   AND state = 'sending'`,
		id,
		next,
		diagnostic.Code,
		diagnostic.Detail,
		diagnostic.At,
	)
	if err != nil {
		return err
	}
	return requireOneEmailRow(result)
}

func (store *sqlStore) MarkFailed(
	ctx context.Context,
	id string,
	diagnostic email.Diagnostic,
) error {
	result, err := store.database.ExecContext(ctx, `
		UPDATE f14_email_deliveries
		   SET state = 'failed',
		       last_diagnostic_code = $2,
		       last_diagnostic_detail = $3,
		       updated_at = $4
		 WHERE id = $1
		   AND state = 'sending'`,
		id,
		diagnostic.Code,
		diagnostic.Detail,
		diagnostic.At,
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
