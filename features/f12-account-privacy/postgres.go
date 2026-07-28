package accountprivacy

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

type providerProjectionReader interface {
	Providers(context.Context, string) ([]Provider, error)
	Provider(context.Context, string, string) (Provider, error)
}

type workspaceProjectionReader interface {
	Workspaces(context.Context, string) ([]WorkspaceRef, error)
}

type PostgresRepository struct {
	database   *sql.DB
	providers  providerProjectionReader
	workspaces workspaceProjectionReader
}

func NewPostgresRepository(
	database *sql.DB,
	providers providerProjectionReader,
	workspaces workspaceProjectionReader,
) (*PostgresRepository, error) {
	if database == nil {
		return nil, fmt.Errorf("%w: database is required", ErrInvalidArgument)
	}
	if providers == nil {
		providers = emptyProviderProjection{}
	}
	if workspaces == nil {
		workspaces = emptyWorkspaceProjection{}
	}
	return &PostgresRepository{
		database:   database,
		providers:  providers,
		workspaces: workspaces,
	}, nil
}

func (repository *PostgresRepository) Profile(
	ctx context.Context,
	accountID string,
) (Profile, error) {
	profile, err := scanProfile(repository.database.QueryRowContext(ctx, `
		SELECT account_id, display_name, locale, timezone, updated_at
		FROM account_privacy_profiles
		WHERE account_id = $1
	`, accountID))
	if errors.Is(err, sql.ErrNoRows) {
		return Profile{}, ErrNotFound
	}
	if err != nil {
		return Profile{}, fmt.Errorf("read account privacy profile: %w", err)
	}
	return profile, nil
}

func (repository *PostgresRepository) UpdateProfile(
	ctx context.Context,
	command ProfileUpdateCommand,
) (Profile, error) {
	row := repository.database.QueryRowContext(ctx, `
		UPDATE account_privacy_profiles
		SET display_name = $2, locale = $3, timezone = $4, updated_at = $5
		WHERE account_id = $1
		RETURNING account_id, display_name, locale, timezone, updated_at
	`, command.AccountID, command.DisplayName, command.Locale, command.Timezone, command.Now)
	profile, err := scanProfile(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Profile{}, ErrNotFound
	}
	if err != nil {
		return Profile{}, fmt.Errorf("update account privacy profile: %w", err)
	}
	if err := insertAuditEvent(
		ctx,
		repository.database,
		command.AccountID,
		command.AccountID,
		"profile.updated",
		"success",
		command.Now,
		nil,
	); err != nil {
		return Profile{}, err
	}
	return profile, nil
}

func (repository *PostgresRepository) Providers(
	ctx context.Context,
	accountID string,
) ([]Provider, error) {
	return repository.providers.Providers(ctx, accountID)
}

func (repository *PostgresRepository) Provider(
	ctx context.Context,
	accountID, providerID string,
) (Provider, error) {
	return repository.providers.Provider(ctx, accountID, providerID)
}

func (repository *PostgresRepository) Workspaces(
	ctx context.Context,
	accountID string,
) ([]WorkspaceRef, error) {
	return repository.workspaces.Workspaces(ctx, accountID)
}

func (repository *PostgresRepository) CreateExport(
	ctx context.Context,
	request ExportRequest,
) error {
	transaction, err := repository.database.BeginTx(ctx, &sql.TxOptions{
		Isolation: sql.LevelReadCommitted,
	})
	if err != nil {
		return fmt.Errorf("begin account export transaction: %w", err)
	}
	defer transaction.Rollback()
	_, err = transaction.ExecContext(ctx, `
		INSERT INTO account_privacy_export_requests (
			id, account_id, scope, workspace_id, status, requested_at, expires_at
		) VALUES ($1, $2, $3, NULLIF($4, ''), $5, $6, $7)
	`, request.ID, request.AccountID, request.Scope, request.WorkspaceID, request.Status, request.RequestedAt, request.ExpiresAt)
	if isUniqueViolation(err) {
		return ErrConflict
	}
	if err != nil {
		return fmt.Errorf("insert account export request: %w", err)
	}
	if err := insertAuditEvent(
		ctx,
		transaction,
		request.AccountID,
		request.ID,
		"export.requested",
		"queued",
		request.RequestedAt,
		nil,
	); err != nil {
		return err
	}
	if err := transaction.Commit(); err != nil {
		return fmt.Errorf("commit account export request: %w", err)
	}
	return nil
}

func (repository *PostgresRepository) Export(
	ctx context.Context,
	requestID string,
) (ExportRequest, error) {
	request, err := scanExportRequest(repository.database.QueryRowContext(ctx, `
		SELECT id, account_id, scope, COALESCE(workspace_id, ''), status,
		       requested_at, ready_at, expires_at, COALESCE(object_key, ''),
		       COALESCE(sha256, ''), COALESCE(size_bytes, 0)
		FROM account_privacy_export_requests
		WHERE id = $1
	`, requestID))
	if errors.Is(err, sql.ErrNoRows) {
		return ExportRequest{}, ErrNotFound
	}
	if err != nil {
		return ExportRequest{}, fmt.Errorf("read account export request: %w", err)
	}
	return request, nil
}

func (repository *PostgresRepository) ActiveExport(
	ctx context.Context,
	accountID string,
	scope ExportScope,
	workspaceID string,
) (ExportRequest, bool, error) {
	request, err := scanExportRequest(repository.database.QueryRowContext(ctx, `
		SELECT id, account_id, scope, COALESCE(workspace_id, ''), status,
		       requested_at, ready_at, expires_at, COALESCE(object_key, ''),
		       COALESCE(sha256, ''), COALESCE(size_bytes, 0)
		FROM account_privacy_export_requests
		WHERE account_id = $1
		  AND scope = $2
		  AND COALESCE(workspace_id, '') = $3
		  AND status IN ('queued', 'ready')
		ORDER BY requested_at DESC, id DESC
		LIMIT 1
	`, accountID, scope, workspaceID))
	if errors.Is(err, sql.ErrNoRows) {
		return ExportRequest{}, false, nil
	}
	if err != nil {
		return ExportRequest{}, false, fmt.Errorf("read active account export: %w", err)
	}
	return request, true, nil
}

func (repository *PostgresRepository) MarkExportReady(
	ctx context.Context,
	command ExportReadyCommand,
) error {
	transaction, err := repository.database.BeginTx(ctx, &sql.TxOptions{
		Isolation: sql.LevelReadCommitted,
	})
	if err != nil {
		return fmt.Errorf("begin export ready transaction: %w", err)
	}
	defer transaction.Rollback()
	tag, err := transaction.ExecContext(ctx, `
		UPDATE account_privacy_export_requests
		SET status = 'ready',
		    object_key = $2,
		    sha256 = $3,
		    size_bytes = $4,
		    ready_at = $5
		WHERE id = $1
		  AND status = 'queued'
	`, command.RequestID, command.ObjectKey, command.SHA256, command.SizeBytes, command.ReadyAt)
	if err != nil {
		return fmt.Errorf("mark export ready: %w", err)
	}
	if rows, _ := tag.RowsAffected(); rows != 1 {
		return repository.exportStateError(ctx, command.RequestID)
	}
	accountID, err := exportAccountID(ctx, transaction, command.RequestID)
	if err != nil {
		return err
	}
	if err := insertAuditEvent(
		ctx,
		transaction,
		accountID,
		command.RequestID,
		"export.ready",
		"success",
		command.ReadyAt,
		nil,
	); err != nil {
		return err
	}
	if err := transaction.Commit(); err != nil {
		return fmt.Errorf("commit export ready transaction: %w", err)
	}
	return nil
}

func (repository *PostgresRepository) MarkExportFailed(
	ctx context.Context,
	requestID string,
	now time.Time,
) error {
	transaction, err := repository.database.BeginTx(ctx, &sql.TxOptions{
		Isolation: sql.LevelReadCommitted,
	})
	if err != nil {
		return fmt.Errorf("begin export failure transaction: %w", err)
	}
	defer transaction.Rollback()
	tag, err := transaction.ExecContext(ctx, `
		UPDATE account_privacy_export_requests
		SET status = 'failed'
		WHERE id = $1
	`, requestID)
	if err != nil {
		return fmt.Errorf("mark export failed: %w", err)
	}
	if rows, _ := tag.RowsAffected(); rows != 1 {
		return ErrNotFound
	}
	accountID, err := exportAccountID(ctx, transaction, requestID)
	if err != nil {
		return err
	}
	if err := insertAuditEvent(
		ctx,
		transaction,
		accountID,
		requestID,
		"export.failed",
		"failed",
		now,
		nil,
	); err != nil {
		return err
	}
	if err := transaction.Commit(); err != nil {
		return fmt.Errorf("commit export failure transaction: %w", err)
	}
	return nil
}

func (repository *PostgresRepository) ExpiredExports(
	ctx context.Context,
	now time.Time,
	limit int,
) ([]ExportRequest, error) {
	rows, err := repository.database.QueryContext(ctx, `
		SELECT id, account_id, scope, COALESCE(workspace_id, ''), status,
		       requested_at, ready_at, expires_at, COALESCE(object_key, ''),
		       COALESCE(sha256, ''), COALESCE(size_bytes, 0)
		FROM account_privacy_export_requests
		WHERE status = 'ready' AND expires_at <= $1
		ORDER BY expires_at, id
		LIMIT $2
	`, now, limit)
	if err != nil {
		return nil, fmt.Errorf("list expired exports: %w", err)
	}
	defer rows.Close()
	requests := make([]ExportRequest, 0)
	for rows.Next() {
		request, scanErr := scanExportRequest(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		requests = append(requests, request)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate expired exports: %w", err)
	}
	return requests, nil
}

func (repository *PostgresRepository) MarkExportExpired(
	ctx context.Context,
	requestID string,
	now time.Time,
) error {
	transaction, err := repository.database.BeginTx(ctx, &sql.TxOptions{
		Isolation: sql.LevelReadCommitted,
	})
	if err != nil {
		return fmt.Errorf("begin export expiry transaction: %w", err)
	}
	defer transaction.Rollback()
	tag, err := transaction.ExecContext(ctx, `
		UPDATE account_privacy_export_requests
		SET status = 'expired',
		    object_key = NULL,
		    sha256 = NULL,
		    size_bytes = NULL
		WHERE id = $1
		  AND status IN ('queued', 'ready')
		  AND expires_at <= $2
	`, requestID, now)
	if err != nil {
		return fmt.Errorf("mark export expired: %w", err)
	}
	if rows, _ := tag.RowsAffected(); rows != 1 {
		return repository.exportStateError(ctx, requestID)
	}
	accountID, err := exportAccountID(ctx, transaction, requestID)
	if err != nil {
		return err
	}
	if err := insertAuditEvent(
		ctx,
		transaction,
		accountID,
		requestID,
		"export.expired",
		"success",
		now,
		nil,
	); err != nil {
		return err
	}
	if err := transaction.Commit(); err != nil {
		return fmt.Errorf("commit export expiry transaction: %w", err)
	}
	return nil
}

func (repository *PostgresRepository) CreateDeletion(
	ctx context.Context,
	request DeletionRequest,
) error {
	transaction, err := repository.database.BeginTx(ctx, &sql.TxOptions{
		Isolation: sql.LevelReadCommitted,
	})
	if err != nil {
		return fmt.Errorf("begin deletion request transaction: %w", err)
	}
	defer transaction.Rollback()
	ownershipJSON, err := json.Marshal(request.Ownership)
	if err != nil {
		return fmt.Errorf("marshal ownership plan: %w", err)
	}
	_, err = transaction.ExecContext(ctx, `
		INSERT INTO account_privacy_deletion_requests (
			id, account_id, scope, workspace_id, status, requested_at,
			grace_ends_at, ownership_plan
		) VALUES ($1, $2, $3, NULLIF($4, ''), $5, $6, $7, $8::jsonb)
	`, request.ID, request.AccountID, request.Scope, request.WorkspaceID, request.Status, request.RequestedAt, request.GraceEndsAt, string(ownershipJSON))
	if isUniqueViolation(err) {
		return ErrConflict
	}
	if err != nil {
		return fmt.Errorf("insert deletion request: %w", err)
	}
	if err := insertAuditEvent(
		ctx,
		transaction,
		request.AccountID,
		request.ID,
		"deletion.requested",
		"deactivating",
		request.RequestedAt,
		nil,
	); err != nil {
		return err
	}
	if err := transaction.Commit(); err != nil {
		return fmt.Errorf("commit deletion request transaction: %w", err)
	}
	return nil
}

func (repository *PostgresRepository) MarkGracePeriod(
	ctx context.Context,
	requestID string,
	now time.Time,
) error {
	return repository.updateDeletionStatus(ctx, requestID, now, `
		UPDATE account_privacy_deletion_requests
		SET status = 'grace_period', failure_code = NULL
		WHERE id = $1 AND status = 'deactivating'
	`, "deletion.deactivated", "success", requestID)
}

func (repository *PostgresRepository) MarkDeactivationFailed(
	ctx context.Context,
	requestID, code string,
	now time.Time,
) error {
	return repository.updateDeletionStatus(ctx, requestID, now, `
		UPDATE account_privacy_deletion_requests
		SET status = 'deactivation_failed', failure_code = $2
		WHERE id = $1
	`, "deletion.deactivated", "failed", requestID, code)
}

func (repository *PostgresRepository) Deletion(
	ctx context.Context,
	requestID string,
) (DeletionRequest, error) {
	request, err := scanDeletionRequest(repository.database.QueryRowContext(ctx, `
		SELECT id, account_id, scope, COALESCE(workspace_id, ''), status,
		       requested_at, grace_ends_at, ownership_plan, COALESCE(failure_code, ''),
		       completed_at, COALESCE(tombstone_id, ''), tombstone_expires_at
		FROM account_privacy_deletion_requests
		WHERE id = $1
	`, requestID))
	if errors.Is(err, sql.ErrNoRows) {
		return DeletionRequest{}, ErrNotFound
	}
	if err != nil {
		return DeletionRequest{}, fmt.Errorf("read deletion request: %w", err)
	}
	return request, nil
}

func (repository *PostgresRepository) CancelDeletion(
	ctx context.Context,
	requestID string,
	now time.Time,
) error {
	return repository.updateDeletionStatus(ctx, requestID, now, `
		UPDATE account_privacy_deletion_requests
		SET status = 'cancelled'
		WHERE id = $1
		  AND status = 'grace_period'
		  AND grace_ends_at > $2
	`, "deletion.cancelled", "success", requestID, now)
}

func (repository *PostgresRepository) ClaimDueDeletions(
	ctx context.Context,
	now time.Time,
	limit int,
) ([]DeletionRequest, error) {
	rows, err := repository.database.QueryContext(ctx, `
		WITH due AS (
			SELECT id
			FROM account_privacy_deletion_requests
			WHERE status IN ('grace_period', 'finalization_failed')
			  AND grace_ends_at <= $1
			ORDER BY grace_ends_at, id
			LIMIT $2
			FOR UPDATE SKIP LOCKED
		)
		UPDATE account_privacy_deletion_requests request
		SET status = 'finalizing', failure_code = NULL
		FROM due
		WHERE request.id = due.id
		RETURNING request.id, request.account_id, request.scope,
		          COALESCE(request.workspace_id, ''), request.status,
		          request.requested_at, request.grace_ends_at, request.ownership_plan,
		          COALESCE(request.failure_code, ''), request.completed_at,
		          COALESCE(request.tombstone_id, ''), request.tombstone_expires_at
	`, now, limit)
	if err != nil {
		return nil, fmt.Errorf("claim due deletions: %w", err)
	}
	defer rows.Close()
	requests := make([]DeletionRequest, 0)
	for rows.Next() {
		request, scanErr := scanDeletionRequest(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		requests = append(requests, request)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate due deletions: %w", err)
	}
	return requests, nil
}

func (repository *PostgresRepository) CompleteDeletion(
	ctx context.Context,
	command DeletionCompleteCommand,
) error {
	transaction, err := repository.database.BeginTx(ctx, &sql.TxOptions{
		Isolation: sql.LevelReadCommitted,
	})
	if err != nil {
		return fmt.Errorf("begin deletion completion transaction: %w", err)
	}
	defer transaction.Rollback()
	tag, err := transaction.ExecContext(ctx, `
		UPDATE account_privacy_deletion_requests
		SET status = 'completed',
		    completed_at = $2,
		    tombstone_id = $3,
		    tombstone_expires_at = $4
		WHERE id = $1 AND status = 'finalizing'
	`, command.RequestID, command.CompletedAt, command.TombstoneID, command.TombstoneExpiresAt)
	if err != nil {
		return fmt.Errorf("complete deletion request: %w", err)
	}
	if rows, _ := tag.RowsAffected(); rows != 1 {
		return repository.deletionStateError(ctx, command.RequestID)
	}
	_, err = transaction.ExecContext(ctx, `
		INSERT INTO account_privacy_tombstones (
			id, deletion_request_id, finalized_at, expires_at
		) VALUES ($1, $2, $3, $4)
	`, command.TombstoneID, command.RequestID, command.CompletedAt, command.TombstoneExpiresAt)
	if err != nil {
		return fmt.Errorf("insert account privacy tombstone: %w", err)
	}
	accountID, err := deletionAccountID(ctx, transaction, command.RequestID)
	if err != nil {
		return err
	}
	if err := insertAuditEvent(
		ctx,
		transaction,
		accountID,
		command.RequestID,
		"deletion.completed",
		"success",
		command.CompletedAt,
		nil,
	); err != nil {
		return err
	}
	if err := transaction.Commit(); err != nil {
		return fmt.Errorf("commit deletion completion transaction: %w", err)
	}
	return nil
}

func (repository *PostgresRepository) MarkFinalizationFailed(
	ctx context.Context,
	requestID, code string,
	now time.Time,
) error {
	return repository.updateDeletionStatus(ctx, requestID, now, `
		UPDATE account_privacy_deletion_requests
		SET status = 'finalization_failed', failure_code = $2
		WHERE id = $1
	`, "deletion.completed", "failed", requestID, code)
}

func (repository *PostgresRepository) updateDeletionStatus(
	ctx context.Context,
	requestID string,
	now time.Time,
	query string,
	eventType string,
	outcome string,
	execArgs ...any,
) error {
	transaction, err := repository.database.BeginTx(ctx, &sql.TxOptions{
		Isolation: sql.LevelReadCommitted,
	})
	if err != nil {
		return fmt.Errorf("begin deletion state transaction: %w", err)
	}
	defer transaction.Rollback()
	tag, err := transaction.ExecContext(ctx, query, execArgs...)
	if err != nil {
		return fmt.Errorf("update deletion state: %w", err)
	}
	if rows, _ := tag.RowsAffected(); rows != 1 {
		return repository.deletionStateError(ctx, requestID)
	}
	accountID, err := deletionAccountID(ctx, transaction, requestID)
	if err != nil {
		return err
	}
	if err := insertAuditEvent(
		ctx,
		transaction,
		accountID,
		requestID,
		eventType,
		outcome,
		now,
		nil,
	); err != nil {
		return err
	}
	if err := transaction.Commit(); err != nil {
		return fmt.Errorf("commit deletion state transaction: %w", err)
	}
	return nil
}

func (repository *PostgresRepository) exportStateError(
	ctx context.Context,
	requestID string,
) error {
	var status string
	err := repository.database.QueryRowContext(ctx, `
		SELECT status FROM account_privacy_export_requests WHERE id = $1
	`, requestID).Scan(&status)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("read export status: %w", err)
	}
	return ErrConflict
}

func (repository *PostgresRepository) deletionStateError(
	ctx context.Context,
	requestID string,
) error {
	var status string
	err := repository.database.QueryRowContext(ctx, `
		SELECT status FROM account_privacy_deletion_requests WHERE id = $1
	`, requestID).Scan(&status)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("read deletion status: %w", err)
	}
	if status != string(DeletionGracePeriod) {
		return ErrConflict
	}
	return ErrDeletionInactive
}

func scanProfile(scanner interface{ Scan(...any) error }) (Profile, error) {
	var profile Profile
	err := scanner.Scan(
		&profile.AccountID,
		&profile.DisplayName,
		&profile.Locale,
		&profile.Timezone,
		&profile.UpdatedAt,
	)
	return profile, err
}

func scanExportRequest(scanner interface{ Scan(...any) error }) (ExportRequest, error) {
	var (
		request ExportRequest
		readyAt sql.NullTime
	)
	err := scanner.Scan(
		&request.ID,
		&request.AccountID,
		&request.Scope,
		&request.WorkspaceID,
		&request.Status,
		&request.RequestedAt,
		&readyAt,
		&request.ExpiresAt,
		&request.ObjectKey,
		&request.SHA256,
		&request.SizeBytes,
	)
	if err != nil {
		return ExportRequest{}, err
	}
	if readyAt.Valid {
		request.ReadyAt = timePointer(readyAt.Time.UTC())
	}
	request.RequestedAt = request.RequestedAt.UTC()
	request.ExpiresAt = request.ExpiresAt.UTC()
	return request, nil
}

func scanDeletionRequest(scanner interface{ Scan(...any) error }) (DeletionRequest, error) {
	var (
		request            DeletionRequest
		ownershipJSON      []byte
		failureCode        string
		completedAt        sql.NullTime
		tombstoneID        string
		tombstoneExpiresAt sql.NullTime
	)
	err := scanner.Scan(
		&request.ID,
		&request.AccountID,
		&request.Scope,
		&request.WorkspaceID,
		&request.Status,
		&request.RequestedAt,
		&request.GraceEndsAt,
		&ownershipJSON,
		&failureCode,
		&completedAt,
		&tombstoneID,
		&tombstoneExpiresAt,
	)
	if err != nil {
		return DeletionRequest{}, err
	}
	if len(ownershipJSON) > 0 {
		if err := json.Unmarshal(ownershipJSON, &request.Ownership); err != nil {
			return DeletionRequest{}, fmt.Errorf("decode ownership plan: %w", err)
		}
	}
	request.RequestedAt = request.RequestedAt.UTC()
	request.GraceEndsAt = request.GraceEndsAt.UTC()
	if failureCode != "" {
		request.FailureCode = failureCode
	}
	if completedAt.Valid {
		request.CompletedAt = timePointer(completedAt.Time.UTC())
	}
	if tombstoneID != "" {
		request.TombstoneID = tombstoneID
	}
	if tombstoneExpiresAt.Valid {
		request.TombstoneExpiresAt = timePointer(tombstoneExpiresAt.Time.UTC())
	}
	return request, nil
}

func exportAccountID(
	ctx context.Context,
	scanner interface {
		QueryRowContext(context.Context, string, ...any) *sql.Row
	},
	requestID string,
) (string, error) {
	var accountID string
	err := scanner.QueryRowContext(
		ctx,
		`SELECT account_id FROM account_privacy_export_requests WHERE id = $1`,
		requestID,
	).Scan(&accountID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("read export account id: %w", err)
	}
	return accountID, nil
}

func deletionAccountID(
	ctx context.Context,
	scanner interface {
		QueryRowContext(context.Context, string, ...any) *sql.Row
	},
	requestID string,
) (string, error) {
	var accountID string
	err := scanner.QueryRowContext(
		ctx,
		`SELECT account_id FROM account_privacy_deletion_requests WHERE id = $1`,
		requestID,
	).Scan(&accountID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("read deletion account id: %w", err)
	}
	return accountID, nil
}

type sqlExecutor interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

func insertAuditEvent(
	ctx context.Context,
	executor sqlExecutor,
	accountID string,
	targetID string,
	eventType string,
	outcome string,
	occurredAt time.Time,
	metadata map[string]any,
) error {
	payload := "{}"
	if metadata != nil {
		encoded, err := json.Marshal(metadata)
		if err != nil {
			return fmt.Errorf("marshal audit metadata: %w", err)
		}
		payload = string(encoded)
	}
	auditID, err := newPostgresOpaqueID()
	if err != nil {
		return err
	}
	if _, err := executor.ExecContext(ctx, `
		INSERT INTO account_privacy_audit_events (
			id, account_id, target_id, event_type, outcome, metadata, occurred_at
		) VALUES ($1, NULLIF($2, ''), $3, $4, $5, $6::jsonb, $7)
	`, auditID, accountID, targetID, eventType, outcome, payload, occurredAt); err != nil {
		return fmt.Errorf("insert audit event: %w", err)
	}
	return nil
}

func newPostgresOpaqueID() (string, error) {
	bytes := make([]byte, 18)
	if _, err := rand.Read(bytes); err != nil {
		return "", fmt.Errorf("generate repository identifier: %w", err)
	}
	return hex.EncodeToString(bytes), nil
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

type emptyProviderProjection struct{}

func (emptyProviderProjection) Providers(context.Context, string) ([]Provider, error) {
	return []Provider{}, nil
}

func (emptyProviderProjection) Provider(context.Context, string, string) (Provider, error) {
	return Provider{}, ErrNotFound
}

type emptyWorkspaceProjection struct{}

func (emptyWorkspaceProjection) Workspaces(context.Context, string) ([]WorkspaceRef, error) {
	return []WorkspaceRef{}, nil
}
