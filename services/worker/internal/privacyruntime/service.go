package privacyruntime

import (
	"archive/zip"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	auth "github.com/apdsoftware/postqron/features/f03-auth"
)

const (
	maxAttempts = 5
	claimLease  = 10 * time.Minute
)

type Service struct {
	database    *sql.DB
	root        string
	now         func() time.Time
	logger      *slog.Logger
	access      AccountAccessBoundary
	artifactKey [32]byte
}

type AccountAccessBoundary interface {
	Finalize(context.Context, string) error
}

func New(
	database *sql.DB,
	root string,
	now func() time.Time,
	logger *slog.Logger,
) (*Service, error) {
	if database == nil {
		return nil, errors.New("privacy runtime database is required")
	}
	store, err := auth.NewPostgresStore(database)
	if err != nil {
		return nil, fmt.Errorf("create F3 account access store: %w", err)
	}
	boundary, err := auth.NewAccountAccessBoundary(store, now)
	if err != nil {
		return nil, fmt.Errorf("create F3 account access boundary: %w", err)
	}
	return NewWithAccountAccess(database, root, now, logger, boundary)
}

func NewWithAccountAccess(
	database *sql.DB,
	root string,
	now func() time.Time,
	logger *slog.Logger,
	access AccountAccessBoundary,
) (*Service, error) {
	if database == nil {
		return nil, errors.New("privacy runtime database is required")
	}
	if access == nil {
		return nil, errors.New("F3 account access boundary is required")
	}
	root = strings.TrimSpace(root)
	if root == "" {
		root = filepath.Join(os.TempDir(), "postqron-privacy-artifacts")
	}
	absolute, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(absolute, 0o700); err != nil {
		return nil, err
	}
	if err := os.Chmod(absolute, 0o700); err != nil {
		return nil, err
	}
	if now == nil {
		now = time.Now
	}
	if logger == nil {
		logger = slog.Default()
	}
	artifactKey, err := artifactKeyFromEnv()
	if err != nil {
		return nil, err
	}
	return &Service{
		database:    database,
		root:        absolute,
		now:         now,
		logger:      logger,
		access:      access,
		artifactKey: artifactKey,
	}, nil
}

func (service *Service) Tick(ctx context.Context) {
	if processed, err := service.processExport(ctx); err != nil {
		service.logger.Error("privacy export processing failed", "error_code", errorCode(err))
	} else if processed {
		service.logger.Info("privacy export processing completed")
	}
	if count, err := service.purgeExpired(ctx, 25); err != nil {
		service.logger.Error("privacy export purge failed", "error_code", errorCode(err))
	} else if count > 0 {
		service.logger.Info("privacy export purge completed", "count", count)
	}
	if processed, err := service.processDeletion(ctx); err != nil {
		service.logger.Error("privacy deletion finalization failed", "error_code", errorCode(err))
	} else if processed {
		service.logger.Info("privacy deletion finalization completed")
	}
}

type deletionJob struct {
	ID          string
	AccountID   string
	Scope       string
	WorkspaceID string
	Ownership   struct {
		Actions []struct {
			WorkspaceID       string `json:"workspace_id"`
			Action            string `json:"action"`
			TransferAccountID string `json:"transfer_account_id"`
		} `json:"actions"`
	}
}

func (service *Service) processDeletion(ctx context.Context) (bool, error) {
	now := service.now().UTC()
	row := service.database.QueryRowContext(ctx, `
		WITH candidate AS (
			SELECT id FROM account_privacy_deletion_requests
			WHERE status IN ('grace_period', 'finalization_failed')
			  AND grace_ends_at <= $1
			ORDER BY grace_ends_at, id
			FOR UPDATE SKIP LOCKED
			LIMIT 1
		)
		UPDATE account_privacy_deletion_requests request
		SET status = 'finalizing', failure_code = NULL
		FROM candidate
		WHERE request.id = candidate.id
		RETURNING request.id, request.account_id, request.scope,
		          COALESCE(request.workspace_id, ''), request.ownership_plan`,
		now)
	var job deletionJob
	var ownership []byte
	if err := row.Scan(&job.ID, &job.AccountID, &job.Scope, &job.WorkspaceID, &ownership); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	if err := json.Unmarshal(ownership, &job.Ownership); err != nil {
		return true, service.failDeletion(ctx, job.ID, "invalid_ownership_plan")
	}
	if err := service.finalizeDeletion(ctx, job, now); err != nil {
		return true, errors.Join(err, service.failDeletion(ctx, job.ID, "erasure_failed"))
	}
	return true, nil
}

func (service *Service) finalizeDeletion(
	ctx context.Context,
	job deletionJob,
	now time.Time,
) error {
	if job.Scope == "account" {
		rows, err := service.database.QueryContext(ctx,
			`SELECT COALESCE(object_key, '') FROM account_privacy_export_requests WHERE account_id = $1`,
			job.AccountID)
		if err != nil {
			return err
		}
		for rows.Next() {
			var key string
			if err := rows.Scan(&key); err != nil {
				rows.Close()
				return err
			}
			if key != "" && filepath.Base(key) == key {
				if err := os.Remove(filepath.Join(service.root, key)); err != nil && !errors.Is(err, os.ErrNotExist) {
					rows.Close()
					return err
				}
			}
		}
		if err := rows.Close(); err != nil {
			return err
		}
		if err := service.finalizeAccountAccess(ctx, job.AccountID); err != nil {
			return err
		}
	}
	transaction, err := service.database.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return err
	}
	defer transaction.Rollback()
	for _, action := range job.Ownership.Actions {
		switch action.Action {
		case "transfer":
			if _, err := transaction.ExecContext(ctx, `
				UPDATE f04_memberships SET role = 'member', updated_at = $3
				WHERE workspace_id = $1 AND account_id = $2;
				UPDATE f04_memberships SET role = 'owner', updated_at = $3
				WHERE workspace_id = $1 AND account_id = $4`,
				action.WorkspaceID, job.AccountID, now, action.TransferAccountID); err != nil {
				return err
			}
		case "delete":
			if err := eraseWorkspace(ctx, transaction, action.WorkspaceID); err != nil {
				return err
			}
		default:
			return errors.New("unsupported ownership action")
		}
	}
	if job.Scope == "account" {
		anonymous := "deleted:" + job.ID
		if _, err := transaction.ExecContext(ctx, `
			UPDATE f06_composer_drafts SET created_by_account_id = $2 WHERE created_by_account_id = $1;
			UPDATE f07_scheduled_posts SET created_by_account_id = $2 WHERE created_by_account_id = $1;
			UPDATE f08_publication_dead_letters SET retried_by_account_id = NULL WHERE retried_by_account_id = $1;
			DELETE FROM f08_meta_notification_outbox WHERE recipient_id = $1;
			UPDATE f09_manual_retry_outbox SET actor_id = $2 WHERE actor_id = $1;
			DELETE FROM f09_notification_outbox WHERE account_id = $1;
			DELETE FROM f04_memberships WHERE account_id = $1;
			DELETE FROM account_privacy_profiles WHERE account_id = $1;
			DELETE FROM account_privacy_export_requests WHERE account_id = $1;
			DELETE FROM account_privacy_cancel_capabilities WHERE account_id = $1;
			UPDATE account_privacy_audit_events SET account_id = NULL WHERE account_id = $1`,
			job.AccountID, anonymous); err != nil {
			return err
		}
	}
	tombstoneBytes := make([]byte, 18)
	if _, err := rand.Read(tombstoneBytes); err != nil {
		return err
	}
	tombstoneID := hex.EncodeToString(tombstoneBytes)
	expiresAt := now.Add(45 * 24 * time.Hour)
	result, err := transaction.ExecContext(ctx, `
		UPDATE account_privacy_deletion_requests
		SET status = 'completed', completed_at = $2, tombstone_id = $3,
		    tombstone_expires_at = $4, account_id = ''
		WHERE id = $1 AND status = 'finalizing'`,
		job.ID, now, tombstoneID, expiresAt)
	if err != nil {
		return err
	}
	if rows, _ := result.RowsAffected(); rows != 1 {
		return errors.New("deletion finalization claim lost")
	}
	if _, err := transaction.ExecContext(ctx, `
		INSERT INTO account_privacy_tombstones
			(id, deletion_request_id, finalized_at, expires_at)
		VALUES ($1, $2, $3, $4);
		INSERT INTO account_privacy_runtime_audit
			(target_id, event_type, outcome, occurred_at)
		VALUES ($2, 'deletion.completed', 'succeeded', $3)`,
		tombstoneID, job.ID, now, expiresAt); err != nil {
		return err
	}
	return transaction.Commit()
}

func (service *Service) finalizeAccountAccess(ctx context.Context, accountID string) error {
	return service.access.Finalize(ctx, accountID)
}

func (service *Service) failDeletion(ctx context.Context, requestID, code string) error {
	now := service.now().UTC()
	_, err := service.database.ExecContext(ctx, `
		UPDATE account_privacy_deletion_requests
		SET status = 'finalization_failed', failure_code = $2
		WHERE id = $1 AND status = 'finalizing';
		INSERT INTO account_privacy_runtime_audit
			(target_id, event_type, outcome, error_code, occurred_at)
		VALUES ($1, 'deletion.attempt', 'failed', $2, $3)`,
		requestID, code, now)
	return err
}

func eraseWorkspace(ctx context.Context, transaction *sql.Tx, workspaceID string) error {
	queries := []string{
		`DELETE FROM f09_manual_retry_outbox WHERE workspace_id = $1`,
		`DELETE FROM f09_notification_outbox WHERE workspace_id = $1`,
		`DELETE FROM f09_publication_status_events WHERE workspace_id = $1`,
		`DELETE FROM f09_destination_status WHERE workspace_id = $1`,
		`DELETE FROM f09_post_status WHERE workspace_id = $1`,
		`DELETE FROM f08_meta_notification_outbox WHERE workspace_id = $1`,
		`DELETE FROM f08_publication_dead_letters WHERE job_id IN (SELECT id FROM f08_publication_jobs WHERE workspace_id = $1)`,
		`DELETE FROM f08_publication_attempts WHERE destination_id IN (SELECT id FROM f08_publication_destinations WHERE workspace_id = $1)`,
		`DELETE FROM f08_publication_destinations WHERE workspace_id = $1`,
		`DELETE FROM f08_publication_jobs WHERE workspace_id = $1`,
		`DELETE FROM f07_publication_commands WHERE workspace_id = $1`,
		`DELETE FROM f07_scheduled_posts WHERE workspace_id = $1`,
		`DELETE FROM f06_composer_drafts WHERE workspace_id = $1`,
		`DELETE FROM f05_social_outbox WHERE workspace_id = $1`,
		`DELETE FROM f05_social_connections WHERE workspace_id = $1`,
		`DELETE FROM f05_selection_resources WHERE selection_id IN (SELECT id FROM f05_resource_selections WHERE workspace_id = $1)`,
		`DELETE FROM f05_resource_selections WHERE workspace_id = $1`,
		`DELETE FROM f05_oauth_attempts WHERE workspace_id = $1`,
		`DELETE FROM f04_workspaces WHERE id = $1`,
	}
	for _, query := range queries {
		if _, err := transaction.ExecContext(ctx, query, workspaceID); err != nil {
			return err
		}
	}
	return nil
}

type exportJob struct {
	RequestID   string
	AccountID   string
	Scope       string
	WorkspaceID string
	Attempts    int
	ClaimToken  string
}

func (service *Service) processExport(ctx context.Context) (bool, error) {
	job, found, err := service.claimExport(ctx)
	if err != nil || !found {
		return found, err
	}
	objectKey := job.RequestID + ".zip"
	path := filepath.Join(service.root, objectKey)
	checksum, size, err := service.writeExport(ctx, job, path)
	if err != nil {
		_ = os.Remove(path)
		return true, errors.Join(err, service.retryExport(ctx, job, "artifact_generation_failed"))
	}
	now := service.now().UTC()
	transaction, err := service.database.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		_ = os.Remove(path)
		return true, err
	}
	defer transaction.Rollback()
	result, err := transaction.ExecContext(ctx, `
		UPDATE account_privacy_export_jobs
		SET state = 'completed', completed_at = $3, claim_token = NULL,
		    claimed_at = NULL, last_error_code = NULL
		WHERE request_id = $1 AND state = 'processing' AND claim_token = $2`,
		job.RequestID, job.ClaimToken, now)
	if err != nil {
		_ = os.Remove(path)
		return true, err
	}
	if rows, _ := result.RowsAffected(); rows != 1 {
		_ = os.Remove(path)
		return true, errors.New("export claim lost")
	}
	result, err = transaction.ExecContext(ctx, `
		UPDATE account_privacy_export_requests
		SET status = 'ready', object_key = $2, sha256 = $3,
		    size_bytes = $4, ready_at = $5
		WHERE id = $1 AND status = 'queued' AND expires_at > $5`,
		job.RequestID, objectKey, checksum, size, now)
	if err != nil {
		_ = os.Remove(path)
		return true, err
	}
	if rows, _ := result.RowsAffected(); rows != 1 {
		_ = os.Remove(path)
		return true, errors.New("export request no longer completable")
	}
	_, err = transaction.ExecContext(ctx, `
		INSERT INTO account_privacy_runtime_audit
			(target_id, event_type, outcome, occurred_at)
		VALUES ($1, 'export.completed', 'succeeded', $2)`, job.RequestID, now)
	if err != nil {
		_ = os.Remove(path)
		return true, err
	}
	if err := transaction.Commit(); err != nil {
		_ = os.Remove(path)
		return true, err
	}
	return true, nil
}

func (service *Service) claimExport(ctx context.Context) (exportJob, bool, error) {
	tokenBytes := make([]byte, 18)
	if _, err := rand.Read(tokenBytes); err != nil {
		return exportJob{}, false, err
	}
	token := hex.EncodeToString(tokenBytes)
	now := service.now().UTC()
	row := service.database.QueryRowContext(ctx, `
		WITH candidate AS (
			SELECT request_id
			FROM account_privacy_export_jobs
			WHERE attempts < $1
			  AND available_at <= $2
			  AND (
				state = 'queued'
				OR (state = 'processing' AND claimed_at <= $3)
			  )
			ORDER BY available_at, created_at, request_id
			FOR UPDATE SKIP LOCKED
			LIMIT 1
		)
		UPDATE account_privacy_export_jobs job
		SET state = 'processing', attempts = attempts + 1,
		    claimed_at = $2, claim_token = $4, last_error_code = NULL
		FROM candidate
		WHERE job.request_id = candidate.request_id
		RETURNING job.request_id, job.account_id, job.scope,
		          COALESCE(job.workspace_id, ''), job.attempts, job.claim_token`,
		maxAttempts, now, now.Add(-claimLease), token)
	var job exportJob
	if err := row.Scan(&job.RequestID, &job.AccountID, &job.Scope,
		&job.WorkspaceID, &job.Attempts, &job.ClaimToken); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return exportJob{}, false, nil
		}
		return exportJob{}, false, err
	}
	return job, true, nil
}

func (service *Service) retryExport(ctx context.Context, job exportJob, code string) error {
	now := service.now().UTC()
	state := "queued"
	if job.Attempts >= maxAttempts {
		state = "failed"
	}
	delay := time.Duration(1<<min(job.Attempts, 6)) * time.Minute
	transaction, err := service.database.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return err
	}
	defer transaction.Rollback()
	_, err = transaction.ExecContext(ctx, `
		UPDATE account_privacy_export_jobs
		SET state = $3, available_at = $4, claim_token = NULL,
		    claimed_at = NULL, last_error_code = $5
		WHERE request_id = $1 AND claim_token = $2`,
		job.RequestID, job.ClaimToken, state, now.Add(delay), code)
	if err != nil {
		return err
	}
	if state == "failed" {
		_, err = transaction.ExecContext(ctx,
			`UPDATE account_privacy_export_requests SET status = 'failed' WHERE id = $1 AND status = 'queued'`,
			job.RequestID)
		if err != nil {
			return err
		}
	}
	_, err = transaction.ExecContext(ctx, `
		INSERT INTO account_privacy_runtime_audit
			(target_id, event_type, outcome, error_code, occurred_at)
		VALUES ($1, 'export.attempt', 'failed', $2, $3)`,
		job.RequestID, code, now)
	if err != nil {
		return err
	}
	return transaction.Commit()
}

func (service *Service) writeExport(
	ctx context.Context,
	job exportJob,
	finalPath string,
) (string, int64, error) {
	temp, err := os.CreateTemp(service.root, ".privacy-export-*")
	if err != nil {
		return "", 0, err
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if err := temp.Chmod(0o600); err != nil {
		temp.Close()
		return "", 0, err
	}
	encryption, err := newEncryptedArtifactWriter(temp, service.artifactKey)
	if err != nil {
		temp.Close()
		return "", 0, err
	}
	hash := sha256.New()
	counting := &countWriter{writer: io.MultiWriter(encryption, hash)}
	archive := zip.NewWriter(counting)
	entries, err := service.exportEntries(ctx, job)
	if err == nil {
		for name, payload := range entries {
			var writer io.Writer
			writer, err = archive.CreateHeader(&zip.FileHeader{
				Name: name, Method: zip.Deflate,
			})
			if err == nil {
				_, err = writer.Write(payload)
			}
			if err != nil {
				break
			}
		}
	}
	closeErr := archive.Close()
	encryptionErr := encryption.Close()
	fileErr := temp.Close()
	if err == nil {
		err = errors.Join(closeErr, encryptionErr, fileErr)
	}
	if err != nil {
		return "", 0, err
	}
	if err := os.Rename(tempPath, finalPath); err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(hash.Sum(nil)), counting.count, nil
}

func (service *Service) exportEntries(ctx context.Context, job exportJob) (map[string][]byte, error) {
	entries := map[string][]byte{}
	manifest, _ := json.MarshalIndent(map[string]any{
		"format_version": 1, "scope": job.Scope, "generated_at": service.now().UTC(),
	}, "", "  ")
	entries["manifest.json"] = manifest
	if job.Scope == "account" {
		payload, err := queryJSON(ctx, service.database, `
			SELECT jsonb_build_object(
				'account', jsonb_build_object(
					'id', account.id, 'email', account.email,
					'display_name', account.display_name,
					'contract_country', account.contract_country,
					'created_at', account.created_at
				),
				'profile', COALESCE(to_jsonb(profile), '{}'::jsonb),
				'workspaces', COALESCE((
					SELECT jsonb_agg(jsonb_build_object(
						'id', workspace.id, 'name', workspace.name,
						'role', membership.role, 'status', workspace.status
					) ORDER BY workspace.id)
					FROM f04_memberships membership
					JOIN f04_workspaces workspace ON workspace.id = membership.workspace_id
					WHERE membership.account_id = account.id
				), '[]'::jsonb),
				'consents', COALESCE((
					SELECT jsonb_agg(jsonb_build_object(
						'document_key', document_key, 'document_version', document_version,
						'action', action, 'purpose', purpose, 'locale', locale,
						'country', country, 'occurred_at', occurred_at
					) ORDER BY occurred_at)
					FROM auth_consent_events WHERE account_id = account.id
				), '[]'::jsonb)
			)
			FROM auth_accounts account
			LEFT JOIN account_privacy_profiles profile ON profile.account_id = account.id
			WHERE account.id = $1`, job.AccountID)
		if err != nil {
			return nil, err
		}
		entries["account.json"] = payload
	} else {
		var authorized bool
		if err := service.database.QueryRowContext(ctx, `
			SELECT EXISTS (
				SELECT 1 FROM f04_memberships
				WHERE account_id = $1 AND workspace_id = $2
				  AND role = 'owner' AND status = 'active'
			)`, job.AccountID, job.WorkspaceID).Scan(&authorized); err != nil || !authorized {
			if err == nil {
				err = errors.New("workspace export authorization changed")
			}
			return nil, err
		}
	}
	for name, query := range map[string]string{
		"drafts.json": `SELECT COALESCE(jsonb_agg(jsonb_build_object(
			'id', id, 'workspace_id', workspace_id, 'content', content,
			'revision', revision, 'created_at', created_at, 'updated_at', updated_at
		) ORDER BY id), '[]'::jsonb) FROM f06_composer_drafts
		WHERE workspace_id = ANY($1)`,
		"scheduled-posts.json": `SELECT COALESCE(jsonb_agg(jsonb_build_object(
			'id', id, 'workspace_id', workspace_id, 'draft_id', draft_id,
			'channel_ids', channel_ids, 'status', status,
			'scheduled_for_utc', scheduled_for_utc,
			'scheduled_timezone', scheduled_timezone,
			'created_at', created_at, 'updated_at', updated_at
		) ORDER BY id), '[]'::jsonb) FROM f07_scheduled_posts
		WHERE workspace_id = ANY($1)`,
	} {
		workspaceIDs, err := service.workspaceIDs(ctx, job)
		if err != nil {
			return nil, err
		}
		payload, err := queryJSON(ctx, service.database, query, workspaceIDs)
		if err != nil {
			return nil, err
		}
		entries[name] = payload
	}
	return entries, nil
}

func (service *Service) workspaceIDs(ctx context.Context, job exportJob) ([]string, error) {
	if job.Scope == "workspace" {
		return []string{job.WorkspaceID}, nil
	}
	rows, err := service.database.QueryContext(ctx,
		`SELECT workspace_id FROM f04_memberships WHERE account_id = $1 ORDER BY workspace_id`,
		job.AccountID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func queryJSON(ctx context.Context, database *sql.DB, query string, args ...any) ([]byte, error) {
	var raw []byte
	if err := database.QueryRowContext(ctx, query, args...).Scan(&raw); err != nil {
		return nil, err
	}
	var compact any
	if err := json.Unmarshal(raw, &compact); err != nil {
		return nil, err
	}
	return json.MarshalIndent(compact, "", "  ")
}

func (service *Service) purgeExpired(ctx context.Context, limit int) (int, error) {
	if _, err := service.database.ExecContext(ctx, `
		DELETE FROM account_privacy_cancel_capabilities
		WHERE expires_at <= $1 OR consumed_at IS NOT NULL;
		DELETE FROM account_privacy_download_tokens
		WHERE expires_at <= $1 OR consumed_at IS NOT NULL`,
		service.now().UTC()); err != nil {
		return 0, err
	}
	rows, err := service.database.QueryContext(ctx, `
		SELECT id, COALESCE(object_key, '')
		FROM account_privacy_export_requests
		WHERE status IN ('queued', 'ready', 'failed') AND expires_at <= $1
		ORDER BY expires_at, id
		LIMIT $2`, service.now().UTC(), limit)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	type expired struct{ id, key string }
	var items []expired
	for rows.Next() {
		var item expired
		if err := rows.Scan(&item.id, &item.key); err != nil {
			return 0, err
		}
		items = append(items, item)
	}
	for _, item := range items {
		if item.key != "" {
			if filepath.Base(item.key) != item.key {
				return 0, errors.New("invalid stored artifact key")
			}
			if err := os.Remove(filepath.Join(service.root, item.key)); err != nil && !errors.Is(err, os.ErrNotExist) {
				return 0, err
			}
		}
		_, err := service.database.ExecContext(ctx, `
			UPDATE account_privacy_export_requests
			SET status = 'expired', object_key = NULL, sha256 = NULL, size_bytes = NULL
			WHERE id = $1 AND expires_at <= $2;
			DELETE FROM account_privacy_download_tokens WHERE export_id = $1`,
			item.id, service.now().UTC())
		if err != nil {
			return 0, err
		}
	}
	return len(items), rows.Err()
}

type countWriter struct {
	writer io.Writer
	count  int64
}

func (writer *countWriter) Write(payload []byte) (int, error) {
	n, err := writer.writer.Write(payload)
	writer.count += int64(n)
	return n, err
}

func errorCode(err error) string {
	if err == nil {
		return ""
	}
	return "privacy_runtime_failed"
}
