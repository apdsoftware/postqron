package auth

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

func (store *PostgresStore) FreezeAccountAccess(
	ctx context.Context,
	accountID string,
	now time.Time,
) error {
	return store.changeAccountAccess(ctx, accountID, now)
}

func (store *PostgresStore) RestoreAccountAccess(
	ctx context.Context,
	accountID string,
	_ time.Time,
) error {
	transaction, state, err := store.lockAccountAccess(ctx, accountID)
	if err != nil {
		return err
	}
	defer transaction.Rollback()
	if state == AccountAccessFinalized {
		return ErrAccountAccessUnavailable
	}
	if state == AccountAccessFrozen {
		if _, err := transaction.ExecContext(ctx, `
			UPDATE auth_accounts
			SET access_state = 'active', access_frozen_at = NULL
			WHERE id = $1`,
			accountID,
		); err != nil {
			return err
		}
	}
	return classifyDatabaseError(transaction.Commit())
}

func (store *PostgresStore) FinalizeAccountAccess(
	ctx context.Context,
	accountID string,
	now time.Time,
) error {
	transaction, state, err := store.lockAccountAccess(ctx, accountID)
	if err != nil {
		return err
	}
	defer transaction.Rollback()
	if state == AccountAccessFinalized {
		return classifyDatabaseError(transaction.Commit())
	}
	if err := invalidatePostgresAccountArtifacts(
		ctx,
		transaction,
		accountID,
		now,
	); err != nil {
		return err
	}
	if _, err := transaction.ExecContext(ctx, `
		DELETE FROM auth_provider_identities WHERE account_id = $1`,
		accountID,
	); err != nil {
		return err
	}
	if _, err := transaction.ExecContext(ctx, `
		DELETE FROM auth_password_credentials WHERE account_id = $1`,
		accountID,
	); err != nil {
		return err
	}
	if _, err := transaction.ExecContext(ctx, `
		DELETE FROM auth_password_tokens WHERE account_id = $1`,
		accountID,
	); err != nil {
		return err
	}
	if _, err := transaction.ExecContext(ctx, `
		UPDATE auth_outbox_events SET payload = '{}'::jsonb
		WHERE aggregate_id = $1`,
		accountID,
	); err != nil {
		return err
	}
	digest := sha256.Sum256([]byte(accountID))
	finalizedEmail := fmt.Sprintf("finalized-%x@account.invalid", digest)
	if _, err := transaction.ExecContext(ctx, `
		UPDATE auth_accounts
		SET email = $2,
		    normalized_email = $2,
		    display_name = '',
		    email_verified_at = NULL,
		    access_state = 'finalized',
		    access_frozen_at = COALESCE(access_frozen_at, $3),
		    access_finalized_at = $3
		WHERE id = $1`,
		accountID,
		finalizedEmail,
		now,
	); err != nil {
		return classifyDatabaseError(err)
	}
	return classifyDatabaseError(transaction.Commit())
}

func (store *PostgresStore) changeAccountAccess(
	ctx context.Context,
	accountID string,
	now time.Time,
) error {
	transaction, state, err := store.lockAccountAccess(ctx, accountID)
	if err != nil {
		return err
	}
	defer transaction.Rollback()
	if state == AccountAccessFinalized {
		return ErrAccountAccessUnavailable
	}
	if state == AccountAccessActive {
		if _, err := transaction.ExecContext(ctx, `
			UPDATE auth_accounts
			SET access_state = 'frozen', access_frozen_at = $2
			WHERE id = $1`,
			accountID,
			now,
		); err != nil {
			return err
		}
	}
	if err := invalidatePostgresAccountArtifacts(
		ctx,
		transaction,
		accountID,
		now,
	); err != nil {
		return err
	}
	return classifyDatabaseError(transaction.Commit())
}

func (store *PostgresStore) lockAccountAccess(
	ctx context.Context,
	accountID string,
) (*sql.Tx, AccountAccessState, error) {
	transaction, err := store.database.BeginTx(ctx, &sql.TxOptions{
		Isolation: sql.LevelSerializable,
	})
	if err != nil {
		return nil, "", err
	}
	var state AccountAccessState
	err = transaction.QueryRowContext(ctx, `
		SELECT access_state
		FROM auth_accounts
		WHERE id = $1
		FOR UPDATE`,
		accountID,
	).Scan(&state)
	if errors.Is(err, sql.ErrNoRows) {
		transaction.Rollback()
		return nil, "", ErrAccountAccessUnavailable
	}
	if err != nil {
		transaction.Rollback()
		return nil, "", err
	}
	return transaction, state, nil
}

func invalidatePostgresAccountArtifacts(
	ctx context.Context,
	transaction *sql.Tx,
	accountID string,
	now time.Time,
) error {
	if _, err := transaction.ExecContext(ctx, `
		UPDATE auth_sessions
		SET revoked_at = $2
		WHERE account_id = $1 AND revoked_at IS NULL`,
		accountID,
		now,
	); err != nil {
		return err
	}
	if _, err := transaction.ExecContext(ctx, `
		UPDATE auth_password_tokens
		SET consumed_at = $2
		WHERE account_id = $1
		  AND consumed_at IS NULL`,
		accountID,
		now,
	); err != nil {
		return err
	}
	if _, err := transaction.ExecContext(ctx, `
		UPDATE auth_oauth_attempts
		SET status = 'failed',
		    completed_at = $2,
		    pkce_verifier_ciphertext = ''::bytea,
		    nonce_ciphertext = ''::bytea
		WHERE target_account_id = $1
		  AND status IN ('pending', 'claimed')`,
		accountID,
		now,
	); err != nil {
		return err
	}
	return nil
}
