package auth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

type PostgresPasswordStore struct {
	database *sql.DB
}

func NewPostgresPasswordStore(database *sql.DB) (*PostgresPasswordStore, error) {
	if database == nil {
		return nil, errors.New("password database is required")
	}
	return &PostgresPasswordStore{database: database}, nil
}

func (store *PostgresPasswordStore) CredentialByEmail(
	ctx context.Context,
	email string,
) (PasswordCredential, bool, error) {
	var credential PasswordCredential
	err := store.database.QueryRowContext(ctx, `
		SELECT
			credential.account_id,
			credential.password_hash,
			credential.failed_attempts,
			credential.locked_until
		FROM auth_password_credentials credential
		JOIN auth_accounts account ON account.id = credential.account_id
		WHERE account.normalized_email = $1
		  AND account.email_verified_at IS NOT NULL`,
		email,
	).Scan(
		&credential.AccountID,
		&credential.PasswordHash,
		&credential.FailedAttempts,
		&credential.LockedUntil,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return PasswordCredential{}, false, nil
	}
	if err != nil {
		return PasswordCredential{}, false, fmt.Errorf("read password credential: %w", err)
	}
	return credential, true, nil
}

func (store *PostgresPasswordStore) RecordPasswordFailure(
	ctx context.Context,
	accountID string,
	now time.Time,
) error {
	transaction, err := store.database.BeginTx(ctx, &sql.TxOptions{
		Isolation: sql.LevelReadCommitted,
	})
	if err != nil {
		return err
	}
	defer transaction.Rollback()
	if _, err := transaction.ExecContext(ctx, `
		UPDATE auth_password_credentials
		SET failed_attempts = failed_attempts + 1,
		    locked_until = CASE
		        WHEN failed_attempts + 1 >= 10 THEN $2 + interval '30 minutes'
		        WHEN failed_attempts + 1 >= 5 THEN $2 + interval '5 minutes'
		        ELSE locked_until
		    END,
		    updated_at = $2
		WHERE account_id = $1`,
		accountID,
		now,
	); err != nil {
		return err
	}
	if _, err := transaction.ExecContext(ctx, `
		INSERT INTO auth_security_events (
			id, account_id, event_type, outcome, occurred_at
		) VALUES ($1, $2, 'password.login_failed', 'rejected', $3)`,
		secureEventID(),
		accountID,
		now,
	); err != nil {
		return err
	}
	return transaction.Commit()
}

func (store *PostgresPasswordStore) CompletePasswordLogin(
	ctx context.Context,
	session PasswordSession,
	eventID string,
	now time.Time,
) error {
	transaction, err := store.database.BeginTx(ctx, &sql.TxOptions{
		Isolation: sql.LevelSerializable,
	})
	if err != nil {
		return err
	}
	defer transaction.Rollback()
	result, err := transaction.ExecContext(ctx, `
		UPDATE auth_password_credentials
		SET failed_attempts = 0,
		    locked_until = NULL,
		    updated_at = $2
		WHERE account_id = $1`,
		session.AccountID,
		now,
	)
	if err != nil {
		return err
	}
	if rows, rowsErr := result.RowsAffected(); rowsErr != nil || rows != 1 {
		return errors.New("password credential disappeared during login")
	}
	if _, err := transaction.ExecContext(ctx, `
		INSERT INTO auth_sessions (
			id, account_id, token_hash, created_at,
			authenticated_at, expires_at, revoked_at
		) VALUES ($1, $2, $3, $4, $5, $6, NULL)`,
		session.ID,
		session.AccountID,
		session.TokenHash,
		session.CreatedAt,
		session.AuthenticatedAt,
		session.ExpiresAt,
	); err != nil {
		return err
	}
	if _, err := transaction.ExecContext(ctx, `
		INSERT INTO auth_security_events (
			id, account_id, event_type, outcome, occurred_at
		) VALUES ($1, $2, 'password.login_succeeded', 'succeeded', $3)`,
		eventID,
		session.AccountID,
		now,
	); err != nil {
		return err
	}
	return transaction.Commit()
}

func BootstrapPasswordAccount(
	ctx context.Context,
	database *sql.DB,
	email, encodedHash string,
	now time.Time,
) error {
	normalized, err := normalizePasswordEmail(email)
	if err != nil {
		return err
	}
	if _, _, _, err := parsePasswordHash(encodedHash); err != nil {
		return err
	}
	transaction, err := database.BeginTx(ctx, &sql.TxOptions{
		Isolation: sql.LevelSerializable,
	})
	if err != nil {
		return err
	}
	defer transaction.Rollback()
	accountID := secureEventID()
	if err := transaction.QueryRowContext(ctx, `
		INSERT INTO auth_accounts (
			id, email, normalized_email, display_name,
			contract_country, created_at, email_verified_at
		) VALUES ($1, $2, $2, 'Postqron Administrator', 'IT', $3, $3)
		ON CONFLICT (normalized_email) DO UPDATE
		    SET email_verified_at = COALESCE(
		        auth_accounts.email_verified_at,
		        EXCLUDED.email_verified_at
		    )
		RETURNING id`,
		accountID,
		normalized,
		now,
	).Scan(&accountID); err != nil {
		return fmt.Errorf("bootstrap password account: %w", err)
	}
	result, err := transaction.ExecContext(ctx, `
		INSERT INTO auth_password_credentials (
			account_id, password_hash, failed_attempts, locked_until,
			changed_at, created_at, updated_at
		) VALUES ($1, $2, 0, NULL, $3, $3, $3)
		ON CONFLICT (account_id) DO NOTHING`,
		accountID,
		encodedHash,
		now,
	)
	if err != nil {
		return fmt.Errorf("bootstrap password credential: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 1 {
		if _, err := transaction.ExecContext(ctx, `
			INSERT INTO auth_security_events (
				id, account_id, event_type, outcome, occurred_at
			) VALUES ($1, $2, 'password.bootstrap', 'succeeded', $3)`,
			secureEventID(),
			accountID,
			now,
		); err != nil {
			return err
		}
	}
	return transaction.Commit()
}
