package auth

import (
	"context"
	"crypto/subtle"
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

func (store *PostgresPasswordStore) PasswordSession(
	ctx context.Context,
	tokenHash string,
	now time.Time,
) (PasswordSessionContext, bool, error) {
	var session PasswordSessionContext
	err := store.database.QueryRowContext(ctx, `
		SELECT
			session.account_id,
			credential.password_hash,
			session.authenticated_at,
			credential.password_change_locked_until
		FROM auth_sessions session
		JOIN auth_password_credentials credential
		  ON credential.account_id = session.account_id
		WHERE session.token_hash = $1
		  AND session.revoked_at IS NULL
		  AND session.expires_at > $2`,
		tokenHash,
		now,
	).Scan(
		&session.AccountID,
		&session.PasswordHash,
		&session.AuthenticatedAt,
		&session.ChangeLockedUntil,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return PasswordSessionContext{}, false, nil
	}
	if err != nil {
		return PasswordSessionContext{}, false, fmt.Errorf(
			"read password session: %w",
			err,
		)
	}
	return session, true, nil
}

func (store *PostgresPasswordStore) RecordPasswordChangeFailure(
	ctx context.Context,
	accountID, eventID string,
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
		SET password_change_failed_attempts =
		        password_change_failed_attempts + 1,
		    password_change_locked_until = CASE
		        WHEN password_change_failed_attempts + 1 >= 10
		            THEN $2 + interval '30 minutes'
		        WHEN password_change_failed_attempts + 1 >= 5
		            THEN $2 + interval '5 minutes'
		        ELSE password_change_locked_until
		    END,
		    updated_at = $2
		WHERE account_id = $1`,
		accountID,
		now,
	)
	if err != nil {
		return err
	}
	if rows, rowsErr := result.RowsAffected(); rowsErr != nil || rows != 1 {
		return errors.New("password credential disappeared during change failure")
	}
	if _, err := transaction.ExecContext(ctx, `
		INSERT INTO auth_security_events (
			id, account_id, event_type, outcome, occurred_at
		) VALUES ($1, $2, 'password.change_failed', 'rejected', $3)`,
		eventID,
		accountID,
		now,
	); err != nil {
		return err
	}
	return transaction.Commit()
}

func (store *PostgresPasswordStore) CompletePasswordChange(
	ctx context.Context,
	change PasswordChange,
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

	var persistedHash string
	err = transaction.QueryRowContext(ctx, `
		SELECT password_hash
		FROM auth_password_credentials
		WHERE account_id = $1
		FOR UPDATE`,
		change.AccountID,
	).Scan(&persistedHash)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrPasswordChangeConflict
	}
	if err != nil {
		return err
	}
	if len(persistedHash) != len(change.CurrentPasswordHash) ||
		subtle.ConstantTimeCompare(
			[]byte(persistedHash),
			[]byte(change.CurrentPasswordHash),
		) != 1 {
		return ErrPasswordChangeConflict
	}

	var sessionAccountID string
	err = transaction.QueryRowContext(ctx, `
		SELECT account_id
		FROM auth_sessions
		WHERE token_hash = $1
		  AND revoked_at IS NULL
		  AND expires_at > $2
		FOR UPDATE`,
		change.CurrentSessionTokenHash,
		now,
	).Scan(&sessionAccountID)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrPasswordChangeConflict
	}
	if err != nil {
		return err
	}
	if sessionAccountID != change.AccountID {
		return ErrPasswordChangeConflict
	}

	result, err := transaction.ExecContext(ctx, `
		UPDATE auth_password_credentials
		SET password_hash = $2,
		    failed_attempts = 0,
		    locked_until = NULL,
		    password_change_failed_attempts = 0,
		    password_change_locked_until = NULL,
		    changed_at = $3,
		    updated_at = $3
		WHERE account_id = $1
		  AND password_hash = $4`,
		change.AccountID,
		change.NewPasswordHash,
		now,
		change.CurrentPasswordHash,
	)
	if err != nil {
		return err
	}
	if rows, rowsErr := result.RowsAffected(); rowsErr != nil || rows != 1 {
		return ErrPasswordChangeConflict
	}
	if _, err := transaction.ExecContext(ctx, `
		UPDATE auth_sessions
		SET revoked_at = $2
		WHERE account_id = $1
		  AND revoked_at IS NULL`,
		change.AccountID,
		now,
	); err != nil {
		return err
	}
	if _, err := transaction.ExecContext(ctx, `
		INSERT INTO auth_sessions (
			id, account_id, token_hash, created_at,
			authenticated_at, expires_at, revoked_at
		) VALUES ($1, $2, $3, $4, $5, $6, NULL)`,
		change.NewSession.ID,
		change.NewSession.AccountID,
		change.NewSession.TokenHash,
		change.NewSession.CreatedAt,
		change.NewSession.AuthenticatedAt,
		change.NewSession.ExpiresAt,
	); err != nil {
		return err
	}
	if _, err := transaction.ExecContext(ctx, `
		INSERT INTO auth_security_events (
			id, account_id, event_type, outcome, occurred_at
		) VALUES ($1, $2, 'password.changed', 'succeeded', $3)`,
		eventID,
		change.AccountID,
		now,
	); err != nil {
		return err
	}
	return transaction.Commit()
}

func (store *PostgresPasswordStore) RevokePasswordSession(
	ctx context.Context,
	tokenHash, eventID string,
	now time.Time,
) error {
	transaction, err := store.database.BeginTx(ctx, &sql.TxOptions{
		Isolation: sql.LevelSerializable,
	})
	if err != nil {
		return err
	}
	defer transaction.Rollback()
	var accountID string
	err = transaction.QueryRowContext(ctx, `
		UPDATE auth_sessions
		SET revoked_at = $2
		WHERE token_hash = $1
		  AND revoked_at IS NULL
		RETURNING account_id`,
		tokenHash,
		now,
	).Scan(&accountID)
	if errors.Is(err, sql.ErrNoRows) {
		return transaction.Commit()
	}
	if err != nil {
		return err
	}
	if _, err := transaction.ExecContext(ctx, `
		INSERT INTO auth_security_events (
			id, account_id, event_type, outcome, occurred_at
		) VALUES ($1, $2, 'session.logged_out', 'succeeded', $3)`,
		eventID,
		accountID,
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
