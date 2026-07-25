package prelaunch

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

type PostgresRepository struct {
	database *sql.DB
}

func NewPostgresRepository(database *sql.DB) (*PostgresRepository, error) {
	if database == nil {
		return nil, errors.New("prelaunch PostgreSQL database is required")
	}
	return &PostgresRepository{database: database}, nil
}

func (repository *PostgresRepository) Allow(
	ctx context.Context,
	key string,
	window time.Time,
	limit int,
) (bool, error) {
	var count int
	err := repository.database.QueryRowContext(
		ctx,
		`INSERT INTO f34_prelaunch_rate_limits (
			key_hash, window_started_at, request_count
		) VALUES ($1, $2, 1)
		ON CONFLICT (key_hash, window_started_at)
		DO UPDATE SET request_count =
			f34_prelaunch_rate_limits.request_count + 1
		RETURNING request_count`,
		key,
		window,
	).Scan(&count)
	if err != nil {
		return false, fmt.Errorf("increment prelaunch rate limit: %w", err)
	}
	return count <= limit, nil
}

func (repository *PostgresRepository) Submit(
	ctx context.Context,
	submission Submission,
) (SubmitResult, error) {
	transaction, err := repository.database.BeginTx(ctx, nil)
	if err != nil {
		return SubmitResult{}, err
	}
	defer transaction.Rollback()

	consentJSON, err := json.Marshal(submission.Consent)
	if err != nil {
		return SubmitResult{}, err
	}
	var requestID string
	err = transaction.QueryRowContext(
		ctx,
		`INSERT INTO f34_prelaunch_access_requests (
			id, email, email_hash, locale, consent_proof, marketing_consent,
			requested_at
		) VALUES ($1, $2, $3, $4, $5, FALSE, $6)
		ON CONFLICT (email_hash) DO NOTHING
		RETURNING id`,
		submission.ID,
		submission.Email,
		submission.EmailHash,
		submission.Locale,
		consentJSON,
		submission.RequestedAt,
	).Scan(&requestID)
	created := true
	if errors.Is(err, sql.ErrNoRows) {
		created = false
		err = transaction.QueryRowContext(
			ctx,
			`SELECT id
			 FROM f34_prelaunch_access_requests
			 WHERE email_hash = $1`,
			submission.EmailHash,
		).Scan(&requestID)
	}
	if err != nil {
		return SubmitResult{}, err
	}

	if created {
		commandJSON, marshalErr := json.Marshal(submission.Command)
		if marshalErr != nil {
			return SubmitResult{}, marshalErr
		}
		if _, err = transaction.ExecContext(
			ctx,
			`INSERT INTO f34_prelaunch_email_outbox (
				id, request_id, event_name, channel, template_id,
				idempotency_key, command, occurred_at
			) VALUES ($1, $2, $3, 'transactional', $4, $5, $6, $7)`,
			"email_"+submission.ID,
			requestID,
			submission.Command.Event,
			submission.Command.TemplateID,
			submission.Command.IdempotencyKey,
			commandJSON,
			submission.RequestedAt,
		); err != nil {
			return SubmitResult{}, err
		}
	}
	if err := transaction.Commit(); err != nil {
		return SubmitResult{}, err
	}
	return SubmitResult{RequestID: requestID, Created: created}, nil
}
