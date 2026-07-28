package auth

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

type PostgresPasswordRegistrationStore struct {
	database *sql.DB
}

func NewPostgresPasswordRegistrationStore(
	database *sql.DB,
) (*PostgresPasswordRegistrationStore, error) {
	if database == nil {
		return nil, errors.New("password registration database is required")
	}
	return &PostgresPasswordRegistrationStore{database: database}, nil
}

func (store *PostgresPasswordRegistrationStore) RegisterPasswordAccount(
	ctx context.Context,
	command RegisterPasswordAccountCommand,
) (RegisterPasswordAccountResult, error) {
	transaction, err := store.database.BeginTx(ctx, &sql.TxOptions{
		Isolation: sql.LevelSerializable,
	})
	if err != nil {
		return RegisterPasswordAccountResult{}, err
	}
	defer transaction.Rollback()

	if _, err := transaction.ExecContext(ctx, `
		INSERT INTO auth_accounts (
			id, email, normalized_email, display_name, contract_country, created_at
		) VALUES ($1, $2, $3, $4, $5, $6)`,
		command.Account.ID,
		command.Account.Email,
		command.Account.NormalizedEmail,
		command.Account.DisplayName,
		command.Account.ContractCountry,
		command.Account.CreatedAt,
	); err != nil {
		if errors.Is(classifyDatabaseError(err), errStoreConflict) {
			return RegisterPasswordAccountResult{}, nil
		}
		return RegisterPasswordAccountResult{}, err
	}
	if _, err := transaction.ExecContext(ctx, `
		INSERT INTO auth_password_credentials (
			account_id, password_hash, failed_attempts, locked_until,
			changed_at, created_at, updated_at
		) VALUES ($1, $2, 0, NULL, $3, $3, $3)`,
		command.Account.ID,
		command.PasswordHash,
		command.Now,
	); err != nil {
		return RegisterPasswordAccountResult{}, classifyDatabaseError(err)
	}
	for _, receipt := range command.Consents {
		if _, err := transaction.ExecContext(ctx, `
			INSERT INTO auth_consent_events (
				id, account_id, document_key, document_version,
				document_digest_sha256, action, purpose, locale, country,
				surface, control_text_id, correlation_id, occurred_at
			) VALUES (
				$1, $2, $3, $4, lower($5), $6, $7, $8, $9, $10, $11, $12, $13
			)`,
			secureEventID(),
			command.Account.ID,
			receipt.DocumentKey,
			receipt.Version,
			receipt.DigestSHA256,
			receipt.Action,
			receipt.Purpose,
			receipt.Locale,
			command.Account.ContractCountry,
			receipt.Surface,
			receipt.ControlTextID,
			command.CorrelationID,
			command.Now,
		); err != nil {
			return RegisterPasswordAccountResult{}, classifyDatabaseError(err)
		}
	}
	payload, err := buildOnboardingPayload(command.Account, command.Now)
	if err != nil {
		return RegisterPasswordAccountResult{}, err
	}
	if _, err := transaction.ExecContext(ctx, `
		INSERT INTO auth_outbox_events (
			id, event_type, event_version, aggregate_id, correlation_id,
			payload, occurred_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		command.OnboardingEventID,
		OnboardingEventType,
		OnboardingEventVersion,
		command.Account.ID,
		command.CorrelationID,
		payload,
		command.Now,
	); err != nil {
		return RegisterPasswordAccountResult{}, classifyDatabaseError(err)
	}
	if _, err := transaction.ExecContext(ctx, `
		INSERT INTO auth_password_tokens (
			id, account_id, purpose, token_hash, expires_at, created_at
		) VALUES ($1, $2, 'verify_email', $3, $4, $5)`,
		command.VerificationTokenID,
		command.Account.ID,
		command.VerificationHash,
		command.VerificationExpiry,
		command.Now,
	); err != nil {
		return RegisterPasswordAccountResult{}, classifyDatabaseError(err)
	}
	if _, err := transaction.ExecContext(ctx, `
		INSERT INTO auth_security_events (
			id, account_id, event_type, outcome, occurred_at
		) VALUES ($1, $2, 'email.verification_requested', 'succeeded', $3)`,
		command.SecurityEventID,
		command.Account.ID,
		command.Now,
	); err != nil {
		return RegisterPasswordAccountResult{}, classifyDatabaseError(err)
	}
	if err := transaction.Commit(); err != nil {
		return RegisterPasswordAccountResult{}, classifyDatabaseError(err)
	}
	return RegisterPasswordAccountResult{Created: true}, nil
}

func (store *PostgresPasswordRegistrationStore) VerifyPasswordEmail(
	ctx context.Context,
	command VerifyPasswordEmailCommand,
) (VerifyPasswordEmailResult, error) {
	transaction, err := store.database.BeginTx(ctx, &sql.TxOptions{
		Isolation: sql.LevelSerializable,
	})
	if err != nil {
		return VerifyPasswordEmailResult{}, err
	}
	defer transaction.Rollback()

	var tokenID string
	var accountID string
	var expiresAt time.Time
	var consumedAt sql.NullTime
	err = transaction.QueryRowContext(ctx, `
		SELECT id, account_id, expires_at, consumed_at
		FROM auth_password_tokens
		WHERE purpose = 'verify_email'
		  AND token_hash = $1
		FOR UPDATE`,
		command.TokenHash,
	).Scan(&tokenID, &accountID, &expiresAt, &consumedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return VerifyPasswordEmailResult{}, transaction.Commit()
	}
	if err != nil {
		return VerifyPasswordEmailResult{}, err
	}
	if consumedAt.Valid {
		return VerifyPasswordEmailResult{}, transaction.Commit()
	}
	if !command.Now.Before(expiresAt) {
		if _, err := transaction.ExecContext(ctx, `
			UPDATE auth_password_tokens
			SET consumed_at = $2
			WHERE id = $1`,
			tokenID,
			command.Now,
		); err != nil {
			return VerifyPasswordEmailResult{}, err
		}
		if err := transaction.Commit(); err != nil {
			return VerifyPasswordEmailResult{}, err
		}
		return VerifyPasswordEmailResult{Expired: true}, nil
	}
	var wasVerified bool
	err = transaction.QueryRowContext(ctx, `
		SELECT email_verified_at IS NOT NULL
		FROM auth_accounts
		WHERE id = $1
		FOR UPDATE`,
		accountID,
	).Scan(&wasVerified)
	if err != nil {
		return VerifyPasswordEmailResult{}, err
	}
	if _, err := transaction.ExecContext(ctx, `
		UPDATE auth_password_tokens
		SET consumed_at = $2
		WHERE account_id = $1
		  AND purpose = 'verify_email'
		  AND consumed_at IS NULL`,
		accountID,
		command.Now,
	); err != nil {
		return VerifyPasswordEmailResult{}, err
	}
	if _, err := transaction.ExecContext(ctx, `
		UPDATE auth_accounts
		SET email_verified_at = COALESCE(email_verified_at, $2)
		WHERE id = $1`,
		accountID,
		command.Now,
	); err != nil {
		return VerifyPasswordEmailResult{}, err
	}
	if !wasVerified {
		if _, err := transaction.ExecContext(ctx, `
			INSERT INTO auth_security_events (
				id, account_id, event_type, outcome, occurred_at
			) VALUES ($1, $2, 'email.verified', 'succeeded', $3)`,
			command.SecurityEventID,
			accountID,
			command.Now,
		); err != nil {
			return VerifyPasswordEmailResult{}, err
		}
	}
	if err := transaction.Commit(); err != nil {
		return VerifyPasswordEmailResult{}, err
	}
	return VerifyPasswordEmailResult{Verified: true}, nil
}

func (store *PostgresPasswordRegistrationStore) ResendPasswordVerification(
	ctx context.Context,
	command ResendPasswordVerificationCommand,
) (ResendPasswordVerificationResult, error) {
	transaction, err := store.database.BeginTx(ctx, &sql.TxOptions{
		Isolation: sql.LevelSerializable,
	})
	if err != nil {
		return ResendPasswordVerificationResult{}, err
	}
	defer transaction.Rollback()

	var accountID string
	var verified bool
	var hasCredential bool
	err = transaction.QueryRowContext(ctx, `
		SELECT
			account.id,
			account.email_verified_at IS NOT NULL,
			EXISTS (
				SELECT 1
				FROM auth_password_credentials credential
				WHERE credential.account_id = account.id
			)
		FROM auth_accounts account
		WHERE account.normalized_email = $1
		FOR UPDATE`,
		command.NormalizedEmail,
	).Scan(&accountID, &verified, &hasCredential)
	if errors.Is(err, sql.ErrNoRows) {
		return ResendPasswordVerificationResult{}, transaction.Commit()
	}
	if err != nil {
		return ResendPasswordVerificationResult{}, err
	}
	if verified || !hasCredential {
		return ResendPasswordVerificationResult{}, transaction.Commit()
	}

	var createdAt time.Time
	err = transaction.QueryRowContext(ctx, `
		SELECT created_at
		FROM auth_password_tokens
		WHERE account_id = $1
		  AND purpose = 'verify_email'
		  AND consumed_at IS NULL
		  AND expires_at > $2
		ORDER BY created_at DESC
		LIMIT 1`,
		accountID,
		command.Now,
	).Scan(&createdAt)
	if err == nil && !createdAt.Before(command.NotBefore) {
		if err := transaction.Commit(); err != nil {
			return ResendPasswordVerificationResult{}, err
		}
		return ResendPasswordVerificationResult{
			RateLimited: true,
			AccountID:   accountID,
		}, nil
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return ResendPasswordVerificationResult{}, err
	}

	if _, err := transaction.ExecContext(ctx, `
		UPDATE auth_password_tokens
		SET consumed_at = $2
		WHERE account_id = $1
		  AND purpose = 'verify_email'
		  AND consumed_at IS NULL`,
		accountID,
		command.Now,
	); err != nil {
		return ResendPasswordVerificationResult{}, err
	}
	if _, err := transaction.ExecContext(ctx, `
		INSERT INTO auth_password_tokens (
			id, account_id, purpose, token_hash, expires_at, created_at
		) VALUES ($1, $2, 'verify_email', $3, $4, $5)`,
		command.VerificationTokenID,
		accountID,
		command.VerificationHash,
		command.VerificationExpiry,
		command.Now,
	); err != nil {
		return ResendPasswordVerificationResult{}, classifyDatabaseError(err)
	}
	if _, err := transaction.ExecContext(ctx, `
		INSERT INTO auth_security_events (
			id, account_id, event_type, outcome, occurred_at
		) VALUES ($1, $2, 'email.verification_requested', 'succeeded', $3)`,
		command.SecurityEventID,
		accountID,
		command.Now,
	); err != nil {
		return ResendPasswordVerificationResult{}, classifyDatabaseError(err)
	}
	if err := transaction.Commit(); err != nil {
		return ResendPasswordVerificationResult{}, err
	}
	return ResendPasswordVerificationResult{
		Issued:    true,
		AccountID: accountID,
	}, nil
}
