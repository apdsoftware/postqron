package auth

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

type PostgresStore struct {
	database *sql.DB
}

func NewPostgresStore(database *sql.DB) (*PostgresStore, error) {
	if database == nil {
		return nil, errors.New("auth database is required")
	}
	return &PostgresStore{database: database}, nil
}

func (s *PostgresStore) SaveAttempt(
	ctx context.Context,
	attempt OAuthAttempt,
) error {
	consents, err := json.Marshal(attempt.Consents)
	if err != nil {
		return fmt.Errorf("encode consent receipts: %w", err)
	}
	_, err = s.database.ExecContext(ctx, `
		INSERT INTO auth_oauth_attempts (
			id, state_hash, pkce_verifier_ciphertext, nonce_ciphertext,
			provider, intent, target_account_id, bound_session_token_hash,
			return_to, contract_country, consent_receipts, correlation_id,
			status, created_at, expires_at, claimed_at, completed_at
		) VALUES (
			$1, $2, $3, $4, $5, $6, NULLIF($7, ''), NULLIF($8, ''),
			$9, NULLIF($10, ''), $11, $12, $13, $14, $15, $16, $17
		)`,
		attempt.ID,
		attempt.StateHash,
		attempt.PKCEVerifierCiphertext,
		attempt.NonceCiphertext,
		attempt.Provider,
		attempt.Intent,
		attempt.TargetAccountID,
		attempt.BoundSessionTokenHash,
		attempt.ReturnTo,
		attempt.ContractCountry,
		consents,
		attempt.CorrelationID,
		attempt.Status,
		attempt.CreatedAt,
		attempt.ExpiresAt,
		attempt.ClaimedAt,
		attempt.CompletedAt,
	)
	return classifyDatabaseError(err)
}

func (s *PostgresStore) ClaimAttempt(
	ctx context.Context,
	stateHash string,
	now time.Time,
) (OAuthAttempt, error) {
	transaction, err := s.database.BeginTx(ctx, &sql.TxOptions{
		Isolation: sql.LevelReadCommitted,
	})
	if err != nil {
		return OAuthAttempt{}, err
	}
	defer transaction.Rollback()
	attempt, exists, err := selectAttempt(
		transaction.QueryRowContext(ctx, attemptSelect+`
			WHERE state_hash = $1
			FOR UPDATE`, stateHash),
	)
	if err != nil {
		return OAuthAttempt{}, err
	}
	if !exists {
		return OAuthAttempt{}, newError(
			CodeInvalidState,
			"Richiesta di accesso non valida. Riavvia il login.",
			false,
			nil,
		)
	}
	if !now.Before(attempt.ExpiresAt) {
		return OAuthAttempt{}, newError(
			CodeFlowExpired,
			"La richiesta di accesso è scaduta. Riprova.",
			true,
			nil,
		)
	}
	if attempt.Status != AttemptPending {
		return OAuthAttempt{}, newError(
			CodeInvalidState,
			"Richiesta di accesso già utilizzata. Riavvia il login.",
			false,
			nil,
		)
	}
	attempt.Status = AttemptClaimed
	attempt.ClaimedAt = timePointer(now)
	if _, err := transaction.ExecContext(ctx, `
		UPDATE auth_oauth_attempts
		SET status = $2, claimed_at = $3
		WHERE id = $1`,
		attempt.ID,
		attempt.Status,
		attempt.ClaimedAt,
	); err != nil {
		return OAuthAttempt{}, classifyDatabaseError(err)
	}
	if err := transaction.Commit(); err != nil {
		return OAuthAttempt{}, err
	}
	return attempt, nil
}

func (s *PostgresStore) ReleaseAttempt(ctx context.Context, id string) error {
	result, err := s.database.ExecContext(ctx, `
		UPDATE auth_oauth_attempts
		SET status = 'pending', claimed_at = NULL
		WHERE id = $1 AND status = 'claimed'`,
		id,
	)
	if err != nil {
		return classifyDatabaseError(err)
	}
	return requireOneRow(result, "claimed OAuth attempt not found")
}

func (s *PostgresStore) FailAttempt(
	ctx context.Context,
	id string,
	now time.Time,
) error {
	result, err := s.database.ExecContext(ctx, `
		UPDATE auth_oauth_attempts
		SET status = 'failed', completed_at = $2
		WHERE id = $1 AND status = 'claimed'`,
		id,
		now,
	)
	if err != nil {
		return classifyDatabaseError(err)
	}
	return requireOneRow(result, "claimed OAuth attempt not found")
}

func (s *PostgresStore) Transaction(
	ctx context.Context,
	operation func(Transaction) error,
) error {
	transaction, err := s.database.BeginTx(ctx, &sql.TxOptions{
		Isolation: sql.LevelSerializable,
	})
	if err != nil {
		return err
	}
	defer transaction.Rollback()
	if err := operation(&postgresTransaction{ctx: ctx, transaction: transaction}); err != nil {
		return err
	}
	return classifyDatabaseError(transaction.Commit())
}

type postgresTransaction struct {
	ctx         context.Context
	transaction *sql.Tx
}

const attemptSelect = `
	SELECT
		id, state_hash, pkce_verifier_ciphertext, nonce_ciphertext,
		provider, intent, target_account_id, bound_session_token_hash,
		return_to, contract_country, consent_receipts, correlation_id,
		status, created_at, expires_at, claimed_at, completed_at
	FROM auth_oauth_attempts
`

func (tx *postgresTransaction) Attempt(id string) (OAuthAttempt, bool, error) {
	return selectAttempt(
		tx.transaction.QueryRowContext(tx.ctx, attemptSelect+` WHERE id = $1 FOR UPDATE`, id),
	)
}

func (tx *postgresTransaction) UpdateAttempt(attempt OAuthAttempt) error {
	result, err := tx.transaction.ExecContext(tx.ctx, `
		UPDATE auth_oauth_attempts
		SET status = $2, claimed_at = $3, completed_at = $4
		WHERE id = $1`,
		attempt.ID,
		attempt.Status,
		attempt.ClaimedAt,
		attempt.CompletedAt,
	)
	if err != nil {
		return classifyDatabaseError(err)
	}
	return requireOneRow(result, "OAuth attempt not found")
}

func (tx *postgresTransaction) ProviderIdentity(
	provider Provider,
	subject string,
) (ProviderIdentity, bool, error) {
	return selectProviderIdentity(tx.transaction.QueryRowContext(tx.ctx, `
		SELECT
			provider, provider_subject, account_id, provider_email,
			COALESCE(revocation_token_ciphertext, ''::bytea), linked_at
		FROM auth_provider_identities
		WHERE provider = $1 AND provider_subject = $2`,
		provider,
		subject,
	))
}

func (tx *postgresTransaction) ProviderIdentities(
	accountID string,
) ([]ProviderIdentity, error) {
	rows, err := tx.transaction.QueryContext(tx.ctx, `
		SELECT
			provider, provider_subject, account_id, provider_email,
			COALESCE(revocation_token_ciphertext, ''::bytea), linked_at
		FROM auth_provider_identities
		WHERE account_id = $1
		ORDER BY provider`,
		accountID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var identities []ProviderIdentity
	for rows.Next() {
		var identity ProviderIdentity
		if err := rows.Scan(
			&identity.Provider,
			&identity.Subject,
			&identity.AccountID,
			&identity.Email,
			&identity.RevocationTokenCiphertext,
			&identity.LinkedAt,
		); err != nil {
			return nil, err
		}
		identities = append(identities, identity)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return identities, nil
}

func (tx *postgresTransaction) PutProviderIdentity(identity ProviderIdentity) error {
	_, err := tx.transaction.ExecContext(tx.ctx, `
		INSERT INTO auth_provider_identities (
			provider, provider_subject, account_id, provider_email,
			revocation_token_ciphertext, linked_at
		) VALUES ($1, $2, $3, $4, NULLIF($5, ''::bytea), $6)`,
		identity.Provider,
		identity.Subject,
		identity.AccountID,
		identity.Email,
		identity.RevocationTokenCiphertext,
		identity.LinkedAt,
	)
	return classifyDatabaseError(err)
}

func (tx *postgresTransaction) DeleteProviderIdentity(
	provider Provider,
	accountID string,
) error {
	result, err := tx.transaction.ExecContext(tx.ctx, `
		DELETE FROM auth_provider_identities
		WHERE provider = $1 AND account_id = $2`,
		provider,
		accountID,
	)
	if err != nil {
		return classifyDatabaseError(err)
	}
	return requireOneRow(result, "provider identity not found")
}

func (tx *postgresTransaction) Account(id string) (Account, bool, error) {
	return selectAccount(tx.transaction.QueryRowContext(tx.ctx, `
		SELECT id, email, normalized_email, display_name, contract_country, created_at
		FROM auth_accounts
		WHERE id = $1`,
		id,
	))
}

func (tx *postgresTransaction) AccountByVerifiedEmail(
	normalizedEmail string,
) (Account, bool, error) {
	return selectAccount(tx.transaction.QueryRowContext(tx.ctx, `
		SELECT id, email, normalized_email, display_name, contract_country, created_at
		FROM auth_accounts
		WHERE normalized_email = $1`,
		normalizedEmail,
	))
}

func (tx *postgresTransaction) PutAccount(account Account) error {
	_, err := tx.transaction.ExecContext(tx.ctx, `
		INSERT INTO auth_accounts (
			id, email, normalized_email, display_name, contract_country, created_at
		) VALUES ($1, $2, $3, $4, $5, $6)`,
		account.ID,
		account.Email,
		account.NormalizedEmail,
		account.DisplayName,
		account.ContractCountry,
		account.CreatedAt,
	)
	return classifyDatabaseError(err)
}

func (tx *postgresTransaction) SessionByTokenHash(
	tokenHash string,
) (Session, bool, error) {
	return selectSession(tx.transaction.QueryRowContext(tx.ctx, `
		SELECT
			id, account_id, token_hash, created_at, authenticated_at,
			expires_at, revoked_at
		FROM auth_sessions
		WHERE token_hash = $1`,
		tokenHash,
	))
}

func (tx *postgresTransaction) Sessions(accountID string) ([]Session, error) {
	rows, err := tx.transaction.QueryContext(tx.ctx, `
		SELECT
			id, account_id, token_hash, created_at, authenticated_at,
			expires_at, revoked_at
		FROM auth_sessions
		WHERE account_id = $1
		ORDER BY created_at`,
		accountID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var sessions []Session
	for rows.Next() {
		session, err := scanSession(rows)
		if err != nil {
			return nil, err
		}
		sessions = append(sessions, session)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return sessions, nil
}

func (tx *postgresTransaction) PutSession(session Session) error {
	_, err := tx.transaction.ExecContext(tx.ctx, `
		INSERT INTO auth_sessions (
			id, account_id, token_hash, created_at, authenticated_at,
			expires_at, revoked_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (id) DO UPDATE
		SET revoked_at = EXCLUDED.revoked_at`,
		session.ID,
		session.AccountID,
		session.TokenHash,
		session.CreatedAt,
		session.AuthenticatedAt,
		session.ExpiresAt,
		session.RevokedAt,
	)
	return classifyDatabaseError(err)
}

func (tx *postgresTransaction) ConsentExists(
	accountID string,
	receipt ConsentReceipt,
	correlationID string,
) (bool, error) {
	var exists bool
	err := tx.transaction.QueryRowContext(tx.ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM auth_consent_events
			WHERE account_id = $1
			  AND document_key = $2
			  AND document_version = $3
			  AND document_digest_sha256 = lower($4)
			  AND action = $5
			  AND purpose = $6
			  AND correlation_id = $7
		)`,
		accountID,
		receipt.DocumentKey,
		receipt.Version,
		receipt.DigestSHA256,
		receipt.Action,
		receipt.Purpose,
		correlationID,
	).Scan(&exists)
	return exists, err
}

func (tx *postgresTransaction) AppendConsent(event ConsentEvent) error {
	_, err := tx.transaction.ExecContext(tx.ctx, `
		INSERT INTO auth_consent_events (
			id, account_id, document_key, document_version,
			document_digest_sha256, action, purpose, locale, country,
			surface, control_text_id, correlation_id, occurred_at
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13
		)`,
		event.ID,
		event.AccountID,
		event.DocumentKey,
		event.Version,
		event.DigestSHA256,
		event.Action,
		event.Purpose,
		event.Locale,
		event.Country,
		event.Surface,
		event.ControlTextID,
		event.CorrelationID,
		event.OccurredAt,
	)
	return classifyDatabaseError(err)
}

func (tx *postgresTransaction) AppendOutbox(event OutboxEvent) error {
	_, err := tx.transaction.ExecContext(tx.ctx, `
		INSERT INTO auth_outbox_events (
			id, event_type, event_version, aggregate_id, correlation_id,
			payload, occurred_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		event.ID,
		event.Type,
		event.Version,
		event.AggregateID,
		event.CorrelationID,
		event.Payload,
		event.OccurredAt,
	)
	return classifyDatabaseError(err)
}

type rowScanner interface {
	Scan(...any) error
}

func selectAttempt(row rowScanner) (OAuthAttempt, bool, error) {
	var attempt OAuthAttempt
	var provider, intent, status string
	var targetAccountID, boundSessionHash, country sql.NullString
	var claimedAt, completedAt sql.NullTime
	var consentJSON []byte
	err := row.Scan(
		&attempt.ID,
		&attempt.StateHash,
		&attempt.PKCEVerifierCiphertext,
		&attempt.NonceCiphertext,
		&provider,
		&intent,
		&targetAccountID,
		&boundSessionHash,
		&attempt.ReturnTo,
		&country,
		&consentJSON,
		&attempt.CorrelationID,
		&status,
		&attempt.CreatedAt,
		&attempt.ExpiresAt,
		&claimedAt,
		&completedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return OAuthAttempt{}, false, nil
	}
	if err != nil {
		return OAuthAttempt{}, false, err
	}
	if err := json.Unmarshal(consentJSON, &attempt.Consents); err != nil {
		return OAuthAttempt{}, false, fmt.Errorf("decode consent receipts: %w", err)
	}
	attempt.Provider = Provider(provider)
	attempt.Intent = FlowIntent(intent)
	attempt.Status = AttemptStatus(status)
	attempt.TargetAccountID = targetAccountID.String
	attempt.BoundSessionTokenHash = boundSessionHash.String
	attempt.ContractCountry = country.String
	if claimedAt.Valid {
		attempt.ClaimedAt = timePointer(claimedAt.Time)
	}
	if completedAt.Valid {
		attempt.CompletedAt = timePointer(completedAt.Time)
	}
	return attempt, true, nil
}

func selectProviderIdentity(row rowScanner) (ProviderIdentity, bool, error) {
	var identity ProviderIdentity
	err := row.Scan(
		&identity.Provider,
		&identity.Subject,
		&identity.AccountID,
		&identity.Email,
		&identity.RevocationTokenCiphertext,
		&identity.LinkedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return ProviderIdentity{}, false, nil
	}
	return identity, err == nil, err
}

func selectAccount(row rowScanner) (Account, bool, error) {
	var account Account
	err := row.Scan(
		&account.ID,
		&account.Email,
		&account.NormalizedEmail,
		&account.DisplayName,
		&account.ContractCountry,
		&account.CreatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return Account{}, false, nil
	}
	return account, err == nil, err
}

func selectSession(row rowScanner) (Session, bool, error) {
	session, err := scanSession(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Session{}, false, nil
	}
	return session, err == nil, err
}

func scanSession(row rowScanner) (Session, error) {
	var session Session
	var revokedAt sql.NullTime
	err := row.Scan(
		&session.ID,
		&session.AccountID,
		&session.TokenHash,
		&session.CreatedAt,
		&session.AuthenticatedAt,
		&session.ExpiresAt,
		&revokedAt,
	)
	if err != nil {
		return Session{}, err
	}
	if revokedAt.Valid {
		session.RevokedAt = timePointer(revokedAt.Time)
	}
	return session, nil
}

func requireOneRow(result sql.Result, message string) error {
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows != 1 {
		return errors.New(message)
	}
	return nil
}

type sqlStateError interface {
	SQLState() string
}

func classifyDatabaseError(err error) error {
	if err == nil {
		return nil
	}
	var stateError sqlStateError
	if errors.As(err, &stateError) {
		switch stateError.SQLState() {
		case "23505", "40001":
			return fmt.Errorf("%w: %v", errStoreConflict, err)
		}
	}
	return err
}
