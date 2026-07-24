package socialconnections

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
		return nil, fmt.Errorf("%w: social connections database is required", ErrInvalidArgument)
	}
	return &PostgresRepository{database: database}, nil
}

func (repository *PostgresRepository) CreateAttempt(
	ctx context.Context,
	attempt OAuthAttempt,
) error {
	_, err := repository.database.ExecContext(ctx, `
		INSERT INTO f05_oauth_attempts (
			id, state_hash, workspace_id, actor_account_id, provider,
			pkce_verifier_key_id, pkce_verifier_ciphertext,
			created_at, expires_at, consumed_at
		) VALUES (
			$1, $2, $3, $4, $5, NULLIF($6, ''), $7, $8, $9, $10
		)`,
		attempt.ID,
		attempt.StateHash,
		attempt.WorkspaceID,
		attempt.ActorID,
		attempt.Provider,
		attempt.PKCEVerifierCiphertext.KeyID,
		nullBytes(attempt.PKCEVerifierCiphertext.Data),
		attempt.CreatedAt,
		attempt.ExpiresAt,
		attempt.ConsumedAt,
	)
	return err
}

func (repository *PostgresRepository) ConsumeAttempt(
	ctx context.Context,
	stateHash string,
	now time.Time,
) (OAuthAttempt, error) {
	transaction, err := repository.database.BeginTx(ctx, &sql.TxOptions{
		Isolation: sql.LevelReadCommitted,
	})
	if err != nil {
		return OAuthAttempt{}, err
	}
	defer transaction.Rollback()
	var attempt OAuthAttempt
	var keyID sql.NullString
	var ciphertext []byte
	var consumed sql.NullTime
	err = transaction.QueryRowContext(ctx, `
		SELECT
			id, state_hash, workspace_id, actor_account_id, provider,
			pkce_verifier_key_id, pkce_verifier_ciphertext,
			created_at, expires_at, consumed_at
		FROM f05_oauth_attempts
		WHERE state_hash = $1
		FOR UPDATE`,
		stateHash,
	).Scan(
		&attempt.ID,
		&attempt.StateHash,
		&attempt.WorkspaceID,
		&attempt.ActorID,
		&attempt.Provider,
		&keyID,
		&ciphertext,
		&attempt.CreatedAt,
		&attempt.ExpiresAt,
		&consumed,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return OAuthAttempt{}, ErrInvalidState
	}
	if err != nil {
		return OAuthAttempt{}, err
	}
	if consumed.Valid {
		return OAuthAttempt{}, ErrInvalidState
	}
	if !now.Before(attempt.ExpiresAt) {
		return OAuthAttempt{}, ErrFlowExpired
	}
	attempt.PKCEVerifierCiphertext = Ciphertext{
		KeyID: keyID.String,
		Data:  append([]byte(nil), ciphertext...),
	}
	attempt.ConsumedAt = cloneTimePointer(&now)
	if _, err = transaction.ExecContext(ctx, `
		UPDATE f05_oauth_attempts
		SET consumed_at = $2
		WHERE id = $1`,
		attempt.ID,
		now,
	); err != nil {
		return OAuthAttempt{}, err
	}
	if err = transaction.Commit(); err != nil {
		return OAuthAttempt{}, err
	}
	return attempt, nil
}

func (repository *PostgresRepository) SaveSelection(
	ctx context.Context,
	selection StoredSelection,
) error {
	transaction, err := repository.database.BeginTx(ctx, &sql.TxOptions{
		Isolation: sql.LevelSerializable,
	})
	if err != nil {
		return err
	}
	defer transaction.Rollback()
	if _, err = transaction.ExecContext(ctx, `
		INSERT INTO f05_resource_selections (
			id, workspace_id, actor_account_id, provider, created_at, expires_at
		) VALUES ($1, $2, $3, $4, $5, $6)`,
		selection.ID,
		selection.WorkspaceID,
		selection.ActorID,
		selection.Provider,
		selection.CreatedAt,
		selection.ExpiresAt,
	); err != nil {
		return err
	}
	for _, resource := range selection.Resources {
		scopes, marshalErr := json.Marshal(resource.Candidate.Scopes)
		if marshalErr != nil {
			return fmt.Errorf("encode social candidate scopes: %w", marshalErr)
		}
		if _, err = transaction.ExecContext(ctx, `
			INSERT INTO f05_selection_resources (
				selection_id, remote_id, resource_type, account_type,
				display_name, handle, picture_url, scopes,
				access_token_key_id, access_token_ciphertext,
				refresh_token_key_id, refresh_token_ciphertext,
				token_expires_at
			) VALUES (
				$1, $2, $3, $4, $5, $6, $7, $8,
				$9, $10, NULLIF($11, ''), $12, $13
			)`,
			selection.ID,
			resource.Candidate.RemoteID,
			resource.Candidate.ResourceType,
			resource.Candidate.AccountType,
			resource.Candidate.DisplayName,
			resource.Candidate.Handle,
			resource.Candidate.PictureURL,
			scopes,
			resource.AccessTokenCiphertext.KeyID,
			resource.AccessTokenCiphertext.Data,
			resource.RefreshTokenCiphertext.KeyID,
			nullBytes(resource.RefreshTokenCiphertext.Data),
			resource.TokenExpiresAt,
		); err != nil {
			return err
		}
	}
	return transaction.Commit()
}

func (repository *PostgresRepository) Connect(
	ctx context.Context,
	command ConnectCommand,
) (Connection, bool, error) {
	transaction, err := repository.database.BeginTx(ctx, &sql.TxOptions{
		Isolation: sql.LevelSerializable,
	})
	if err != nil {
		return Connection{}, false, err
	}
	defer transaction.Rollback()
	var selection StoredSelection
	err = transaction.QueryRowContext(ctx, `
		SELECT id, workspace_id, actor_account_id, provider, created_at, expires_at
		FROM f05_resource_selections
		WHERE id = $1
		FOR UPDATE`,
		command.SelectionID,
	).Scan(
		&selection.ID,
		&selection.WorkspaceID,
		&selection.ActorID,
		&selection.Provider,
		&selection.CreatedAt,
		&selection.ExpiresAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return Connection{}, false, ErrResourceNotFound
	}
	if err != nil {
		return Connection{}, false, err
	}
	if selection.WorkspaceID != command.WorkspaceID ||
		selection.ActorID != command.ActorID {
		return Connection{}, false, ErrResourceNotFound
	}
	if !command.Now.Before(selection.ExpiresAt) {
		return Connection{}, false, ErrFlowExpired
	}
	resource, selectedAt, err := selectSelectionResource(
		transaction.QueryRowContext(ctx, `
			SELECT
				remote_id, resource_type, account_type, display_name, handle,
				picture_url, scopes, access_token_key_id,
				access_token_ciphertext, refresh_token_key_id,
				refresh_token_ciphertext, token_expires_at, selected_at
			FROM f05_selection_resources
			WHERE selection_id = $1 AND remote_id = $2
			FOR UPDATE`,
			command.SelectionID,
			command.RemoteID,
		),
	)
	if errors.Is(err, sql.ErrNoRows) || selectedAt.Valid {
		return Connection{}, false, ErrResourceNotFound
	}
	if err != nil {
		return Connection{}, false, err
	}

	existing, exists, err := selectCredential(
		transaction.QueryRowContext(ctx, credentialSelect+`
			WHERE workspace_id = $1 AND provider = $2 AND remote_id = $3
			FOR UPDATE`,
			command.WorkspaceID,
			selection.Provider,
			command.RemoteID,
		),
	)
	if err != nil {
		return Connection{}, false, err
	}
	if exists && existing.Status == StatusConnected {
		return Connection{}, false, ErrResourceAlreadyUsed
	}
	scopes, err := json.Marshal(resource.Candidate.Scopes)
	if err != nil {
		return Connection{}, false, err
	}
	reconnected := exists
	var connection Connection
	if exists {
		_, err = transaction.ExecContext(ctx, `
			UPDATE f05_social_connections
			SET
				resource_type = $2, account_type = $3, display_name = $4,
				handle = $5, picture_url = $6, scopes = $7,
				status = 'connected', reconnect_reason = '',
				access_token_key_id = $8, access_token_ciphertext = $9,
				refresh_token_key_id = NULLIF($10, ''),
				refresh_token_ciphertext = $11, token_expires_at = $12,
				refresh_locked_until = NULL, last_verified_at = $13,
				connected_by_actor_id = $14, updated_at = $13,
				revoked_at = NULL
			WHERE id = $1`,
			existing.ID,
			resource.Candidate.ResourceType,
			resource.Candidate.AccountType,
			resource.Candidate.DisplayName,
			resource.Candidate.Handle,
			resource.Candidate.PictureURL,
			scopes,
			resource.AccessTokenCiphertext.KeyID,
			resource.AccessTokenCiphertext.Data,
			resource.RefreshTokenCiphertext.KeyID,
			nullBytes(resource.RefreshTokenCiphertext.Data),
			resource.TokenExpiresAt,
			command.Now,
			command.ActorID,
		)
		if err != nil {
			return Connection{}, false, err
		}
		connection, _, err = selectConnectionByID(ctx, transaction, existing.ID)
	} else {
		_, err = transaction.ExecContext(ctx, `
			INSERT INTO f05_social_connections (
				id, workspace_id, provider, remote_id, resource_type,
				account_type, display_name, handle, picture_url, scopes,
				status, access_token_key_id, access_token_ciphertext,
				refresh_token_key_id, refresh_token_ciphertext,
				token_expires_at, last_verified_at, connected_by_actor_id,
				created_at, updated_at
			) VALUES (
				$1, $2, $3, $4, $5, $6, $7, $8, $9, $10,
				'connected', $11, $12, NULLIF($13, ''), $14, $15,
				$16, $17, $16, $16
			)`,
			command.NewConnectionID,
			command.WorkspaceID,
			selection.Provider,
			resource.Candidate.RemoteID,
			resource.Candidate.ResourceType,
			resource.Candidate.AccountType,
			resource.Candidate.DisplayName,
			resource.Candidate.Handle,
			resource.Candidate.PictureURL,
			scopes,
			resource.AccessTokenCiphertext.KeyID,
			resource.AccessTokenCiphertext.Data,
			resource.RefreshTokenCiphertext.KeyID,
			nullBytes(resource.RefreshTokenCiphertext.Data),
			resource.TokenExpiresAt,
			command.Now,
			command.ActorID,
		)
		if err != nil {
			return Connection{}, false, err
		}
		connection, _, err = selectConnectionByID(ctx, transaction, command.NewConnectionID)
	}
	if err != nil {
		return Connection{}, false, err
	}
	if _, err = transaction.ExecContext(ctx, `
		UPDATE f05_selection_resources
		SET selected_at = $3
		WHERE selection_id = $1 AND remote_id = $2`,
		command.SelectionID,
		command.RemoteID,
		command.Now,
	); err != nil {
		return Connection{}, false, err
	}
	event := command.Event
	event.ConnectionID = connection.ID
	event.Provider = connection.Provider
	event.RemoteID = connection.RemoteID
	event.OccurredAt = command.Now
	if reconnected {
		event.Type = EventReconnected
	}
	if err = insertEvent(ctx, transaction, event); err != nil {
		return Connection{}, false, err
	}
	if err = transaction.Commit(); err != nil {
		return Connection{}, false, err
	}
	return connection, reconnected, nil
}

func (repository *PostgresRepository) ListConnections(
	ctx context.Context,
	workspaceID string,
) ([]Connection, error) {
	rows, err := repository.database.QueryContext(ctx, credentialSelect+`
		WHERE workspace_id = $1
		ORDER BY provider, display_name, id`,
		workspaceID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var connections []Connection
	for rows.Next() {
		stored, exists, scanErr := selectCredential(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		if exists {
			connections = append(connections, stored.Connection)
		}
	}
	return connections, rows.Err()
}

func (repository *PostgresRepository) GetCredential(
	ctx context.Context,
	workspaceID, connectionID string,
) (StoredCredential, error) {
	stored, exists, err := selectCredential(
		repository.database.QueryRowContext(ctx, credentialSelect+`
			WHERE workspace_id = $1 AND id = $2`,
			workspaceID,
			connectionID,
		),
	)
	if err != nil {
		return StoredCredential{}, err
	}
	if !exists {
		return StoredCredential{}, ErrResourceNotFound
	}
	return stored, nil
}

func (repository *PostgresRepository) ClaimRefresh(
	ctx context.Context,
	workspaceID, connectionID string,
	now, refreshAt time.Time,
	lockTTL time.Duration,
) (StoredCredential, bool, error) {
	transaction, err := repository.database.BeginTx(ctx, &sql.TxOptions{
		Isolation: sql.LevelReadCommitted,
	})
	if err != nil {
		return StoredCredential{}, false, err
	}
	defer transaction.Rollback()
	stored, exists, err := selectCredential(
		transaction.QueryRowContext(ctx, credentialSelect+`
			WHERE workspace_id = $1 AND id = $2
			FOR UPDATE`,
			workspaceID,
			connectionID,
		),
	)
	if err != nil {
		return StoredCredential{}, false, err
	}
	if !exists {
		return StoredCredential{}, false, ErrResourceNotFound
	}
	if stored.Status == StatusReconnectRequired {
		return StoredCredential{}, false, ErrReconnectRequired
	}
	if stored.Status == StatusRevoked {
		return StoredCredential{}, false, ErrConnectionRevoked
	}
	if stored.TokenExpiresAt == nil || stored.TokenExpiresAt.After(refreshAt) {
		return stored, false, nil
	}
	if stored.RefreshLockedUntil != nil && stored.RefreshLockedUntil.After(now) {
		return StoredCredential{}, false, ErrRefreshInProgress
	}
	lockedUntil := now.Add(lockTTL)
	if _, err = transaction.ExecContext(ctx, `
		UPDATE f05_social_connections
		SET refresh_locked_until = $2
		WHERE id = $1`,
		connectionID,
		lockedUntil,
	); err != nil {
		return StoredCredential{}, false, err
	}
	if err = transaction.Commit(); err != nil {
		return StoredCredential{}, false, err
	}
	stored.RefreshLockedUntil = &lockedUntil
	return stored, true, nil
}

func (repository *PostgresRepository) CompleteRefresh(
	ctx context.Context,
	command RefreshCommand,
) (Connection, error) {
	transaction, err := repository.database.BeginTx(ctx, &sql.TxOptions{
		Isolation: sql.LevelSerializable,
	})
	if err != nil {
		return Connection{}, err
	}
	defer transaction.Rollback()
	scopes, err := json.Marshal(command.Scopes)
	if err != nil {
		return Connection{}, err
	}
	result, err := transaction.ExecContext(ctx, `
		UPDATE f05_social_connections
		SET
			access_token_key_id = $2, access_token_ciphertext = $3,
			refresh_token_key_id = NULLIF($4, ''),
			refresh_token_ciphertext = $5, scopes = $6,
			token_expires_at = $7, last_verified_at = $8,
			refresh_locked_until = NULL, updated_at = $9
		WHERE id = $1 AND status = 'connected'
			AND refresh_locked_until IS NOT NULL`,
		command.ConnectionID,
		command.AccessTokenCiphertext.KeyID,
		command.AccessTokenCiphertext.Data,
		command.RefreshTokenCiphertext.KeyID,
		nullBytes(command.RefreshTokenCiphertext.Data),
		scopes,
		command.ExpiresAt,
		command.VerifiedAt,
		command.Now,
	)
	if err != nil {
		return Connection{}, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return Connection{}, err
	}
	if affected != 1 {
		return Connection{}, ErrRefreshInProgress
	}
	if err = insertEvent(ctx, transaction, command.Event); err != nil {
		return Connection{}, err
	}
	connection, exists, err := selectConnectionByID(
		ctx,
		transaction,
		command.ConnectionID,
	)
	if err != nil {
		return Connection{}, err
	}
	if !exists {
		return Connection{}, ErrResourceNotFound
	}
	if err = transaction.Commit(); err != nil {
		return Connection{}, err
	}
	return connection, nil
}

func (repository *PostgresRepository) ReleaseRefresh(
	ctx context.Context,
	workspaceID, connectionID string,
) error {
	result, err := repository.database.ExecContext(ctx, `
		UPDATE f05_social_connections
		SET refresh_locked_until = NULL
		WHERE workspace_id = $1 AND id = $2`,
		workspaceID,
		connectionID,
	)
	if err != nil {
		return err
	}
	return requireAffected(result)
}

func (repository *PostgresRepository) MarkReconnectRequired(
	ctx context.Context,
	workspaceID, connectionID, reason string,
	now time.Time,
	event Event,
) (Connection, bool, error) {
	transaction, err := repository.database.BeginTx(ctx, &sql.TxOptions{
		Isolation: sql.LevelSerializable,
	})
	if err != nil {
		return Connection{}, false, err
	}
	defer transaction.Rollback()
	stored, exists, err := selectCredential(
		transaction.QueryRowContext(ctx, credentialSelect+`
			WHERE workspace_id = $1 AND id = $2
			FOR UPDATE`,
			workspaceID,
			connectionID,
		),
	)
	if err != nil {
		return Connection{}, false, err
	}
	if !exists {
		return Connection{}, false, ErrResourceNotFound
	}
	if stored.Status == StatusRevoked {
		return Connection{}, false, ErrConnectionRevoked
	}
	if stored.Status == StatusReconnectRequired {
		return stored.Connection, false, nil
	}
	if _, err = transaction.ExecContext(ctx, `
		UPDATE f05_social_connections
		SET
			status = 'reconnect_required', reconnect_reason = $2,
			access_token_key_id = NULL, access_token_ciphertext = NULL,
			refresh_token_key_id = NULL, refresh_token_ciphertext = NULL,
			token_expires_at = NULL, refresh_locked_until = NULL,
			updated_at = $3
		WHERE id = $1`,
		connectionID,
		reason,
		now,
	); err != nil {
		return Connection{}, false, err
	}
	if err = insertEvent(ctx, transaction, event); err != nil {
		return Connection{}, false, err
	}
	connection, _, err := selectConnectionByID(ctx, transaction, connectionID)
	if err != nil {
		return Connection{}, false, err
	}
	if err = transaction.Commit(); err != nil {
		return Connection{}, false, err
	}
	return connection, true, nil
}

func (repository *PostgresRepository) Revoke(
	ctx context.Context,
	workspaceID, connectionID string,
	now time.Time,
	event Event,
) (Connection, bool, error) {
	transaction, err := repository.database.BeginTx(ctx, &sql.TxOptions{
		Isolation: sql.LevelSerializable,
	})
	if err != nil {
		return Connection{}, false, err
	}
	defer transaction.Rollback()
	stored, exists, err := selectCredential(
		transaction.QueryRowContext(ctx, credentialSelect+`
			WHERE workspace_id = $1 AND id = $2
			FOR UPDATE`,
			workspaceID,
			connectionID,
		),
	)
	if err != nil {
		return Connection{}, false, err
	}
	if !exists {
		return Connection{}, false, ErrResourceNotFound
	}
	if stored.Status == StatusRevoked {
		return stored.Connection, false, nil
	}
	if _, err = transaction.ExecContext(ctx, `
		UPDATE f05_social_connections
		SET
			status = 'revoked', reconnect_reason = '',
			access_token_key_id = NULL, access_token_ciphertext = NULL,
			refresh_token_key_id = NULL, refresh_token_ciphertext = NULL,
			token_expires_at = NULL, refresh_locked_until = NULL,
			revoked_at = $2, updated_at = $2
		WHERE id = $1`,
		connectionID,
		now,
	); err != nil {
		return Connection{}, false, err
	}
	if err = insertEvent(ctx, transaction, event); err != nil {
		return Connection{}, false, err
	}
	connection, _, err := selectConnectionByID(ctx, transaction, connectionID)
	if err != nil {
		return Connection{}, false, err
	}
	if err = transaction.Commit(); err != nil {
		return Connection{}, false, err
	}
	return connection, true, nil
}

const credentialSelect = `
	SELECT
		id, workspace_id, provider, remote_id, resource_type, account_type,
		display_name, handle, picture_url, scopes, status, reconnect_reason,
		access_token_key_id, access_token_ciphertext,
		refresh_token_key_id, refresh_token_ciphertext,
		token_expires_at, refresh_locked_until, last_verified_at,
		connected_by_actor_id, created_at, updated_at, revoked_at
	FROM f05_social_connections
`

type rowScanner interface {
	Scan(...any) error
}

func selectCredential(scanner rowScanner) (StoredCredential, bool, error) {
	var stored StoredCredential
	var scopes []byte
	var accessKey, refreshKey sql.NullString
	var accessCiphertext, refreshCiphertext []byte
	var tokenExpires, refreshLocked, verified, revoked sql.NullTime
	err := scanner.Scan(
		&stored.ID,
		&stored.WorkspaceID,
		&stored.Provider,
		&stored.RemoteID,
		&stored.ResourceType,
		&stored.AccountType,
		&stored.DisplayName,
		&stored.Handle,
		&stored.PictureURL,
		&scopes,
		&stored.Status,
		&stored.ReconnectReason,
		&accessKey,
		&accessCiphertext,
		&refreshKey,
		&refreshCiphertext,
		&tokenExpires,
		&refreshLocked,
		&verified,
		&stored.ConnectedByActorID,
		&stored.CreatedAt,
		&stored.UpdatedAt,
		&revoked,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return StoredCredential{}, false, nil
	}
	if err != nil {
		return StoredCredential{}, false, err
	}
	if err = json.Unmarshal(scopes, &stored.Scopes); err != nil {
		return StoredCredential{}, false, fmt.Errorf("decode social scopes: %w", err)
	}
	stored.AccessTokenCiphertext = Ciphertext{
		KeyID: accessKey.String,
		Data:  append([]byte(nil), accessCiphertext...),
	}
	stored.RefreshTokenCiphertext = Ciphertext{
		KeyID: refreshKey.String,
		Data:  append([]byte(nil), refreshCiphertext...),
	}
	stored.TokenExpiresAt = nullableTimePointer(tokenExpires)
	stored.RefreshLockedUntil = nullableTimePointer(refreshLocked)
	stored.LastVerifiedAt = nullableTimePointer(verified)
	stored.RevokedAt = nullableTimePointer(revoked)
	return stored, true, nil
}

func selectSelectionResource(
	scanner rowScanner,
) (StoredResource, sql.NullTime, error) {
	var resource StoredResource
	var scopes []byte
	var refreshKey sql.NullString
	var refreshCiphertext []byte
	var tokenExpires, selectedAt sql.NullTime
	err := scanner.Scan(
		&resource.Candidate.RemoteID,
		&resource.Candidate.ResourceType,
		&resource.Candidate.AccountType,
		&resource.Candidate.DisplayName,
		&resource.Candidate.Handle,
		&resource.Candidate.PictureURL,
		&scopes,
		&resource.AccessTokenCiphertext.KeyID,
		&resource.AccessTokenCiphertext.Data,
		&refreshKey,
		&refreshCiphertext,
		&tokenExpires,
		&selectedAt,
	)
	if err != nil {
		return StoredResource{}, sql.NullTime{}, err
	}
	if err = json.Unmarshal(scopes, &resource.Candidate.Scopes); err != nil {
		return StoredResource{}, sql.NullTime{}, err
	}
	resource.RefreshTokenCiphertext = Ciphertext{
		KeyID: refreshKey.String,
		Data:  append([]byte(nil), refreshCiphertext...),
	}
	resource.TokenExpiresAt = nullableTimePointer(tokenExpires)
	return resource, selectedAt, nil
}

func selectConnectionByID(
	ctx context.Context,
	transaction *sql.Tx,
	connectionID string,
) (Connection, bool, error) {
	stored, exists, err := selectCredential(
		transaction.QueryRowContext(ctx, credentialSelect+` WHERE id = $1`, connectionID),
	)
	return stored.Connection, exists, err
}

func insertEvent(
	ctx context.Context,
	transaction *sql.Tx,
	event Event,
) error {
	_, err := transaction.ExecContext(ctx, `
		INSERT INTO f05_social_outbox (
			id, event_type, event_version, workspace_id, connection_id,
			provider, remote_id, actor_account_id, reason, correlation_id,
			occurred_at
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, NULLIF($8, ''), $9, $10, $11
		)`,
		event.ID,
		event.Type,
		event.Version,
		event.WorkspaceID,
		event.ConnectionID,
		event.Provider,
		event.RemoteID,
		event.ActorID,
		event.Reason,
		event.CorrelationID,
		event.OccurredAt,
	)
	return err
}

func nullableTimePointer(value sql.NullTime) *time.Time {
	if !value.Valid {
		return nil
	}
	return cloneTimePointer(&value.Time)
}

func nullBytes(value []byte) any {
	if len(value) == 0 {
		return nil
	}
	return value
}

func requireAffected(result sql.Result) error {
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected != 1 {
		return ErrResourceNotFound
	}
	return nil
}
