package accountprivacyruntime

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	accountprivacy "github.com/apdsoftware/postqron/features/f12-account-privacy"
)

const artifactRoutePrefix = "/api/v1/account/privacy-artifacts/"

type sqlExportQueue struct {
	database *sql.DB
	now      func() time.Time
}

func (queue sqlExportQueue) EnqueueExport(ctx context.Context, job accountprivacy.ExportJob) error {
	now := queue.now().UTC()
	_, err := queue.database.ExecContext(ctx, `
		INSERT INTO account_privacy_export_jobs (
			request_id, account_id, scope, workspace_id, available_at, created_at
		) VALUES ($1, $2, $3, NULLIF($4, ''), $5, $5)
		ON CONFLICT (request_id) DO NOTHING`,
		job.RequestID, job.AccountID, job.Scope, job.WorkspaceID, now)
	if err != nil {
		return fmt.Errorf("enqueue privacy export: %w", err)
	}
	return nil
}

type privateArtifactStore struct {
	root string
}

func newPrivateArtifactStore(root string) (privateArtifactStore, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return privateArtifactStore{}, errors.New("POSTQRON_PRIVACY_ARTIFACT_DIR is required")
	}
	absolute, err := filepath.Abs(root)
	if err != nil {
		return privateArtifactStore{}, fmt.Errorf("resolve privacy artifact directory: %w", err)
	}
	if err := os.MkdirAll(absolute, 0o700); err != nil {
		return privateArtifactStore{}, fmt.Errorf("create privacy artifact directory: %w", err)
	}
	if err := os.Chmod(absolute, 0o700); err != nil {
		return privateArtifactStore{}, fmt.Errorf("secure privacy artifact directory: %w", err)
	}
	return privateArtifactStore{root: absolute}, nil
}

func (store privateArtifactStore) path(objectKey string) (string, error) {
	if objectKey == "" || filepath.IsAbs(objectKey) || strings.Contains(objectKey, `\`) {
		return "", errors.New("invalid privacy artifact key")
	}
	clean := filepath.Clean(objectKey)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") || clean != objectKey {
		return "", errors.New("invalid privacy artifact key")
	}
	path := filepath.Join(store.root, clean)
	relative, err := filepath.Rel(store.root, path)
	if err != nil || relative != clean {
		return "", errors.New("privacy artifact escapes private root")
	}
	return path, nil
}

func (store privateArtifactStore) DeleteExport(_ context.Context, objectKey string) error {
	if objectKey == "" {
		return nil
	}
	path, err := store.path(objectKey)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("delete privacy artifact: %w", err)
	}
	return nil
}

type oneTimeDownloadSigner struct {
	database *sql.DB
	baseURL  string
	now      func() time.Time
}

func (signer oneTimeDownloadSigner) SignedDownloadURL(
	ctx context.Context,
	objectKey string,
	expiresAt time.Time,
) (string, error) {
	var exportID, accountID string
	err := signer.database.QueryRowContext(ctx, `
		SELECT id, account_id
		FROM account_privacy_export_requests
		WHERE object_key = $1 AND status = 'ready' AND expires_at > $2`,
		objectKey, signer.now().UTC()).Scan(&exportID, &accountID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", accountprivacy.ErrExportNotReady
	}
	if err != nil {
		return "", fmt.Errorf("resolve privacy artifact owner: %w", err)
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate download token: %w", err)
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	digest := sha256.Sum256([]byte(token))
	_, err = signer.database.ExecContext(ctx, `
		INSERT INTO account_privacy_download_tokens (
			token_hash, export_id, account_id, object_key, expires_at, created_at
		) VALUES ($1, $2, $3, $4, $5, $6)`,
		hex.EncodeToString(digest[:]), exportID, accountID, objectKey,
		expiresAt.UTC(), signer.now().UTC())
	if err != nil {
		return "", fmt.Errorf("persist one-time download token: %w", err)
	}
	return strings.TrimRight(signer.baseURL, "/") + artifactRoutePrefix + token, nil
}

type artifactDownloadHandler struct {
	database *sql.DB
	store    privateArtifactStore
	now      func() time.Time
}

func (handler artifactDownloadHandler) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		response.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	token := strings.TrimPrefix(request.URL.Path, artifactRoutePrefix)
	if token == request.URL.Path || token == "" || strings.Contains(token, "/") {
		http.NotFound(response, request)
		return
	}
	decoded, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(decoded) != 32 {
		http.NotFound(response, request)
		return
	}
	digest := sha256.Sum256([]byte(token))
	transaction, err := handler.database.BeginTx(request.Context(), &sql.TxOptions{})
	if err != nil {
		http.Error(response, "download unavailable", http.StatusServiceUnavailable)
		return
	}
	defer transaction.Rollback()
	var objectKey string
	err = transaction.QueryRowContext(request.Context(), `
		UPDATE account_privacy_download_tokens token
		SET consumed_at = $2
		FROM account_privacy_export_requests export
		WHERE token.token_hash = $1
		  AND token.export_id = export.id
		  AND token.account_id = export.account_id
		  AND token.object_key = export.object_key
		  AND token.consumed_at IS NULL
		  AND token.expires_at > $2
		  AND export.status = 'ready'
		  AND export.expires_at > $2
		RETURNING token.object_key`,
		hex.EncodeToString(digest[:]), handler.now().UTC()).Scan(&objectKey)
	if err != nil {
		http.NotFound(response, request)
		return
	}
	if err := transaction.Commit(); err != nil {
		http.Error(response, "download unavailable", http.StatusServiceUnavailable)
		return
	}
	path, err := handler.store.path(objectKey)
	if err != nil {
		http.NotFound(response, request)
		return
	}
	file, err := os.Open(path)
	if err != nil {
		http.NotFound(response, request)
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		http.NotFound(response, request)
		return
	}
	response.Header().Set("Content-Type", "application/zip")
	response.Header().Set("Content-Disposition", `attachment; filename="postqron-export.zip"`)
	response.Header().Set("Cache-Control", "private, no-store")
	response.Header().Set("X-Content-Type-Options", "nosniff")
	response.Header().Set("Content-Length", fmt.Sprintf("%d", info.Size()))
	_, _ = io.Copy(response, file)
}
