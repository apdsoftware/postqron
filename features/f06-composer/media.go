package composer

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"hash/crc32"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"time"
)

const (
	defaultMaximumUploadBytes int64 = 1024 * 1024 * 1024
	uploadLifetime                  = 24 * time.Hour
	signedURLLifetime               = 15 * time.Minute
	maximumImageHeaderBytes   int64 = 16 * 1024 * 1024
	mediaDeleteLease                = 2 * time.Minute
	mediaDeleteRecoveryBatch        = 16
)

type ObjectInfo struct {
	SizeBytes   int64
	ContentType string
}

type SignedObjectURL struct {
	URL       string
	Headers   map[string]string
	ExpiresAt time.Time
}

// ObjectStore is the only F6 boundary allowed to handle media bytes. The
// PostgreSQL store owns metadata and state, never object content.
type ObjectStore interface {
	AuthorizeUpload(
		context.Context,
		string,
		string,
		int64,
		time.Time,
	) (SignedObjectURL, error)
	AuthorizeDownload(context.Context, string, time.Time) (SignedObjectURL, error)
	Stat(context.Context, string) (ObjectInfo, error)
	Open(context.Context, string) (io.ReadCloser, error)
	Retain(context.Context, string) error
	MakeTemporary(context.Context, string) error
	Delete(context.Context, string) error
}

type MediaInspector interface {
	Inspect(context.Context, io.Reader, ObjectInfo) (Media, error)
}

type unavailableObjectStore struct{}

func (unavailableObjectStore) AuthorizeUpload(
	context.Context,
	string,
	string,
	int64,
	time.Time,
) (SignedObjectURL, error) {
	return SignedObjectURL{}, ErrStorageUnavailable
}

func (unavailableObjectStore) AuthorizeDownload(
	context.Context,
	string,
	time.Time,
) (SignedObjectURL, error) {
	return SignedObjectURL{}, ErrStorageUnavailable
}

func (unavailableObjectStore) Stat(context.Context, string) (ObjectInfo, error) {
	return ObjectInfo{}, ErrStorageUnavailable
}

func (unavailableObjectStore) Open(context.Context, string) (io.ReadCloser, error) {
	return nil, ErrStorageUnavailable
}

func (unavailableObjectStore) Retain(context.Context, string) error {
	return ErrStorageUnavailable
}

func (unavailableObjectStore) MakeTemporary(context.Context, string) error {
	return ErrStorageUnavailable
}

func (unavailableObjectStore) Delete(context.Context, string) error {
	return ErrStorageUnavailable
}

type PostgresMediaStore struct {
	database       *sql.DB
	objects        ObjectStore
	inspector      MediaInspector
	clock          func() time.Time
	random         func([]byte) error
	maxUploadBytes int64
}

func NewPostgresMediaStore(
	database *sql.DB,
	objects ObjectStore,
	inspector MediaInspector,
	clock func() time.Time,
	maxUploadBytes int64,
) (*PostgresMediaStore, error) {
	if database == nil {
		return nil, errors.New("composer media database is required")
	}
	if objects == nil {
		objects = unavailableObjectStore{}
	}
	if inspector == nil {
		inspector = StreamMediaInspector{}
	}
	if clock == nil {
		clock = time.Now
	}
	if maxUploadBytes < 1 {
		maxUploadBytes = defaultMaximumUploadBytes
	}
	return &PostgresMediaStore{
		database:       database,
		objects:        objects,
		inspector:      inspector,
		clock:          clock,
		maxUploadBytes: maxUploadBytes,
		random: func(destination []byte) error {
			_, err := rand.Read(destination)
			return err
		},
	}, nil
}

func (store *PostgresMediaStore) CreateUpload(
	ctx context.Context,
	workspaceID, _ string,
	request MediaUploadRequest,
) (MediaUpload, error) {
	request.FileName = strings.TrimSpace(filepath.Base(request.FileName))
	request.ContentType = normalizeContentType(request.ContentType)
	if request.FileName == "" || request.FileName == "." ||
		request.ContentType == "" ||
		request.SizeBytes < 1 ||
		request.SizeBytes > store.maxUploadBytes {
		return MediaUpload{}, &FieldRuleError{
			Field:   "body",
			Rule:    "valid_upload_declaration",
			Code:    "media_upload_invalid",
			Message: "File name, content type, and a supported positive size are required.",
		}
	}
	id, err := randomMediaID(store.random)
	if err != nil {
		return MediaUpload{}, err
	}
	objectKey := temporaryObjectKey(workspaceID, id, request.FileName)
	now := store.clock().UTC()
	expiresAt := now.Add(uploadLifetime)
	signed, err := store.objects.AuthorizeUpload(
		ctx,
		objectKey,
		request.ContentType,
		request.SizeBytes,
		now.Add(signedURLLifetime),
	)
	if err != nil {
		return MediaUpload{}, fmt.Errorf("authorize composer object upload: %w", err)
	}
	_, err = store.database.ExecContext(ctx, `
		INSERT INTO f06_composer_media (
			id, workspace_id, object_key, file_name,
			declared_content_type, declared_size_bytes, status,
			created_at, expires_at
		) VALUES ($1, $2, $3, $4, $5, $6, 'pending', $7, $8)`,
		id,
		workspaceID,
		objectKey,
		request.FileName,
		request.ContentType,
		request.SizeBytes,
		now,
		expiresAt,
	)
	if err != nil {
		_ = store.objects.Delete(ctx, objectKey)
		return MediaUpload{}, fmt.Errorf("record composer media upload: %w", err)
	}
	base := mediaBasePath(workspaceID, id)
	return MediaUpload{
		ID:            id,
		Status:        InspectionPending,
		UploadURL:     signed.URL,
		UploadHeaders: signed.Headers,
		CompleteURL:   base + "/complete",
		ExpiresAt:     expiresAt,
		MaxBytes:      request.SizeBytes,
	}, nil
}

func (store *PostgresMediaStore) CompleteUpload(
	ctx context.Context,
	workspaceID, mediaID string,
) (Media, error) {
	var objectKey, declaredType string
	var declaredSize int64
	var expiresAt time.Time
	err := store.database.QueryRowContext(ctx, `
		SELECT object_key, declared_content_type, declared_size_bytes, expires_at
		  FROM f06_composer_media
		 WHERE workspace_id = $1
		   AND id = $2
		   AND status = 'pending'
		   AND deleting_at IS NULL`,
		workspaceID,
		mediaID,
	).Scan(&objectKey, &declaredType, &declaredSize, &expiresAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Media{}, ErrNotFound
	}
	if err != nil {
		return Media{}, fmt.Errorf("read composer upload state: %w", err)
	}
	if !expiresAt.After(store.clock().UTC()) {
		_ = store.rejectAndDelete(ctx, workspaceID, mediaID, objectKey)
		return Media{}, &FieldRuleError{
			Field: "upload", Rule: "not_expired", Code: "media_upload_expired",
			Message: "The media upload authorization has expired.",
		}
	}
	info, err := store.objects.Stat(ctx, objectKey)
	if err != nil {
		return Media{}, fmt.Errorf("inspect composer object metadata: %w", err)
	}
	if info.SizeBytes != declaredSize {
		_ = store.rejectAndDelete(ctx, workspaceID, mediaID, objectKey)
		return Media{}, &FieldRuleError{
			Field: "upload", Rule: "declared_size", Code: "media_size_mismatch",
			Message: "The uploaded object size does not match the authorized size.",
		}
	}
	if normalizeContentType(info.ContentType) != declaredType {
		_ = store.rejectAndDelete(ctx, workspaceID, mediaID, objectKey)
		return Media{}, &FieldRuleError{
			Field: "upload", Rule: "declared_content_type",
			Code:    "media_content_type_mismatch",
			Message: "The uploaded object content type does not match the authorization.",
		}
	}
	body, err := store.objects.Open(ctx, objectKey)
	if err != nil {
		return Media{}, fmt.Errorf("open composer object for inspection: %w", err)
	}
	defer body.Close()
	media, inspectErr := store.inspector.Inspect(ctx, body, info)
	if inspectErr != nil {
		_ = store.rejectAndDelete(ctx, workspaceID, mediaID, objectKey)
		return Media{}, inspectErr
	}
	media.ID = mediaID
	media.URL = mediaBasePath(workspaceID, mediaID) + "/download"
	media.ExpiresAt = &expiresAt
	metadata, err := json.Marshal(media)
	if err != nil {
		return Media{}, fmt.Errorf("encode inspected media metadata: %w", err)
	}
	tag, err := store.database.ExecContext(ctx, `
		UPDATE f06_composer_media
		   SET status = 'ready', inspected_metadata = $3
		 WHERE workspace_id = $1
		   AND id = $2
		   AND status = 'pending'
		   AND deleting_at IS NULL`,
		workspaceID,
		mediaID,
		metadata,
	)
	if err != nil {
		return Media{}, fmt.Errorf("store inspected composer media metadata: %w", err)
	}
	if affected, _ := tag.RowsAffected(); affected != 1 {
		return Media{}, ErrConflict
	}
	return media, nil
}

func (store *PostgresMediaStore) Get(
	ctx context.Context,
	workspaceID, mediaID string,
) (Media, error) {
	var metadata []byte
	var expiresAt sql.NullTime
	var objectKey string
	err := store.database.QueryRowContext(ctx, `
		SELECT object_key, inspected_metadata, expires_at
		  FROM f06_composer_media
		 WHERE workspace_id = $1
		   AND id = $2
		   AND status = 'ready'
		   AND deleting_at IS NULL`,
		workspaceID,
		mediaID,
	).Scan(&objectKey, &metadata, &expiresAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Media{}, ErrNotFound
	}
	if err != nil {
		return Media{}, fmt.Errorf("read composer media: %w", err)
	}
	if expiresAt.Valid && !expiresAt.Time.After(store.clock().UTC()) {
		_ = store.rejectAndDelete(ctx, workspaceID, mediaID, objectKey)
		return Media{}, ErrNotFound
	}
	var media Media
	if err := json.Unmarshal(metadata, &media); err != nil {
		return Media{}, fmt.Errorf("decode inspected composer media: %w", err)
	}
	if expiresAt.Valid {
		value := expiresAt.Time.UTC()
		media.ExpiresAt = &value
	} else {
		media.ExpiresAt = nil
	}
	return media, nil
}

func (store *PostgresMediaStore) Download(
	ctx context.Context,
	workspaceID, mediaID string,
) (MediaDownload, error) {
	if _, err := store.Get(ctx, workspaceID, mediaID); err != nil {
		return MediaDownload{}, err
	}
	var objectKey string
	err := store.database.QueryRowContext(ctx, `
		SELECT object_key
		  FROM f06_composer_media
		 WHERE workspace_id = $1
		   AND id = $2
		   AND status = 'ready'
		   AND deleting_at IS NULL`,
		workspaceID,
		mediaID,
	).Scan(&objectKey)
	if err != nil {
		return MediaDownload{}, fmt.Errorf("read composer media object key: %w", err)
	}
	expiresAt := store.clock().UTC().Add(signedURLLifetime)
	signed, err := store.objects.AuthorizeDownload(ctx, objectKey, expiresAt)
	if err != nil {
		return MediaDownload{}, fmt.Errorf("authorize composer object download: %w", err)
	}
	return MediaDownload{URL: signed.URL, ExpiresAt: signed.ExpiresAt}, nil
}

func (store *PostgresMediaStore) Delete(
	ctx context.Context,
	workspaceID, mediaID string,
) error {
	recovered, err := store.recoverExpiredTargetDelete(ctx, workspaceID, mediaID)
	if err != nil {
		return err
	}
	if recovered {
		return nil
	}
	if err := store.recoverExpiredDeleteReservations(
		ctx,
		workspaceID,
		mediaID,
	); err != nil {
		return err
	}
	objectKey, reservedAt, err := store.claimDeleteReservation(
		ctx,
		workspaceID,
		mediaID,
	)
	if err != nil {
		return err
	}
	return store.finalizeDeleteReservation(
		ctx,
		workspaceID,
		mediaID,
		objectKey,
		reservedAt,
	)
}

func (store *PostgresMediaStore) claimDeleteReservation(
	ctx context.Context,
	workspaceID, mediaID string,
) (string, time.Time, error) {
	transaction, err := store.database.BeginTx(ctx, nil)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("begin composer media delete: %w", err)
	}
	defer transaction.Rollback()
	var objectKey string
	var deletingAt sql.NullTime
	err = transaction.QueryRowContext(ctx, `
		SELECT object_key, deleting_at
		  FROM f06_composer_media
		 WHERE workspace_id = $1
		   AND id = $2
		   AND attached_draft_id IS NULL
		 FOR UPDATE`,
		workspaceID,
		mediaID,
	).Scan(&objectKey, &deletingAt)
	if errors.Is(err, sql.ErrNoRows) {
		return "", time.Time{}, ErrNotFound
	}
	if err != nil {
		return "", time.Time{}, fmt.Errorf("read composer media for deletion: %w", err)
	}
	if deletingAt.Valid &&
		deletingAt.Time.After(store.clock().UTC().Add(-mediaDeleteLease)) {
		return "", time.Time{}, ErrConflict
	}
	reservedAt := store.clock().UTC()
	var tag sql.Result
	if deletingAt.Valid {
		tag, err = transaction.ExecContext(ctx, `
			UPDATE f06_composer_media
			   SET deleting_at = $3
			 WHERE workspace_id = $1
			   AND id = $2
			   AND attached_draft_id IS NULL
			   AND deleting_at = $4`,
			workspaceID,
			mediaID,
			reservedAt,
			deletingAt.Time.UTC(),
		)
	} else {
		tag, err = transaction.ExecContext(ctx, `
			UPDATE f06_composer_media
			   SET deleting_at = $3
			 WHERE workspace_id = $1
			   AND id = $2
			   AND attached_draft_id IS NULL
			   AND deleting_at IS NULL`,
			workspaceID,
			mediaID,
			reservedAt,
		)
	}
	if err != nil {
		return "", time.Time{}, fmt.Errorf("reserve composer media deletion: %w", err)
	}
	if affected, _ := tag.RowsAffected(); affected != 1 {
		return "", time.Time{}, ErrConflict
	}
	if err := transaction.Commit(); err != nil {
		return "", time.Time{}, fmt.Errorf(
			"commit composer media delete reservation: %w",
			err,
		)
	}
	return objectKey, reservedAt, nil
}

func (store *PostgresMediaStore) finalizeDeleteReservation(
	ctx context.Context,
	workspaceID, mediaID, objectKey string,
	reservedAt time.Time,
) error {
	if err := store.objects.Delete(ctx, objectKey); err != nil &&
		!errors.Is(err, ErrNotFound) {
		return fmt.Errorf("delete composer object: %w", err)
	}
	tag, err := store.database.ExecContext(ctx, `
		DELETE FROM f06_composer_media
		 WHERE workspace_id = $1
		   AND id = $2
		   AND attached_draft_id IS NULL
		   AND deleting_at = $3`,
		workspaceID,
		mediaID,
		reservedAt,
	)
	if err != nil {
		return fmt.Errorf("delete composer media metadata: %w", err)
	}
	if affected, _ := tag.RowsAffected(); affected != 1 {
		return ErrConflict
	}
	return nil
}

func (store *PostgresMediaStore) releaseDeleteReservation(
	ctx context.Context,
	workspaceID, mediaID string,
	reservedAt time.Time,
) {
	_, _ = store.database.ExecContext(ctx, `
		UPDATE f06_composer_media
		   SET deleting_at = NULL
		 WHERE workspace_id = $1
		   AND id = $2
		   AND attached_draft_id IS NULL
		   AND deleting_at = $3`,
		workspaceID,
		mediaID,
		reservedAt,
	)
}

func (store *PostgresMediaStore) recoverExpiredTargetDelete(
	ctx context.Context,
	workspaceID, mediaID string,
) (bool, error) {
	transaction, err := store.database.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("begin expired composer media delete recovery: %w", err)
	}
	defer transaction.Rollback()
	var objectKey string
	var deletingAt sql.NullTime
	err = transaction.QueryRowContext(ctx, `
		SELECT object_key, deleting_at
		  FROM f06_composer_media
		 WHERE workspace_id = $1
		   AND id = $2
		   AND attached_draft_id IS NULL
		 FOR UPDATE`,
		workspaceID,
		mediaID,
	).Scan(&objectKey, &deletingAt)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read expired composer media delete recovery: %w", err)
	}
	if !deletingAt.Valid ||
		deletingAt.Time.After(store.clock().UTC().Add(-mediaDeleteLease)) {
		return false, nil
	}
	reservedAt := store.clock().UTC()
	tag, err := transaction.ExecContext(ctx, `
		UPDATE f06_composer_media
		   SET deleting_at = $3
		 WHERE workspace_id = $1
		   AND id = $2
		   AND attached_draft_id IS NULL
		   AND deleting_at = $4`,
		workspaceID,
		mediaID,
		reservedAt,
		deletingAt.Time.UTC(),
	)
	if err != nil {
		return false, fmt.Errorf("claim expired composer media delete recovery: %w", err)
	}
	if affected, _ := tag.RowsAffected(); affected != 1 {
		return false, ErrConflict
	}
	if err := transaction.Commit(); err != nil {
		return false, fmt.Errorf("commit expired composer media delete recovery: %w", err)
	}
	return true, store.finalizeDeleteReservation(
		ctx,
		workspaceID,
		mediaID,
		objectKey,
		reservedAt,
	)
}

func (store *PostgresMediaStore) recoverExpiredDeleteReservations(
	ctx context.Context,
	workspaceID, skipMediaID string,
) error {
	var recoveryErrors []error
	for recovered := 0; recovered < mediaDeleteRecoveryBatch; recovered++ {
		mediaID, objectKey, reservedAt, ok, err :=
			store.claimNextExpiredDeleteReservation(ctx, workspaceID, skipMediaID)
		if err != nil {
			recoveryErrors = append(recoveryErrors, err)
			break
		}
		if !ok {
			break
		}
		if err := store.finalizeDeleteReservation(
			ctx,
			workspaceID,
			mediaID,
			objectKey,
			reservedAt,
		); err != nil {
			recoveryErrors = append(recoveryErrors, err)
		}
	}
	return errors.Join(recoveryErrors...)
}

func (store *PostgresMediaStore) claimNextExpiredDeleteReservation(
	ctx context.Context,
	workspaceID, skipMediaID string,
) (string, string, time.Time, bool, error) {
	transaction, err := store.database.BeginTx(ctx, nil)
	if err != nil {
		return "", "", time.Time{}, false, fmt.Errorf(
			"begin composer media delete sweep: %w",
			err,
		)
	}
	defer transaction.Rollback()
	var mediaID string
	var objectKey string
	var deletingAt time.Time
	err = transaction.QueryRowContext(ctx, `
		SELECT id, object_key, deleting_at
		  FROM f06_composer_media
		 WHERE workspace_id = $1
		   AND attached_draft_id IS NULL
		   AND deleting_at IS NOT NULL
		   AND deleting_at < $2
		   AND ($3 = '' OR id <> $3)
		 ORDER BY deleting_at, id
		 LIMIT 1
		 FOR UPDATE SKIP LOCKED`,
		workspaceID,
		store.clock().UTC().Add(-mediaDeleteLease),
		skipMediaID,
	).Scan(&mediaID, &objectKey, &deletingAt)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", time.Time{}, false, nil
	}
	if err != nil {
		return "", "", time.Time{}, false, fmt.Errorf(
			"scan composer media delete sweep: %w",
			err,
		)
	}
	reservedAt := store.clock().UTC()
	tag, err := transaction.ExecContext(ctx, `
		UPDATE f06_composer_media
		   SET deleting_at = $3
		 WHERE workspace_id = $1
		   AND id = $2
		   AND attached_draft_id IS NULL
		   AND deleting_at = $4`,
		workspaceID,
		mediaID,
		reservedAt,
		deletingAt.UTC(),
	)
	if err != nil {
		return "", "", time.Time{}, false, fmt.Errorf(
			"claim composer media delete sweep: %w",
			err,
		)
	}
	if affected, _ := tag.RowsAffected(); affected != 1 {
		return "", "", time.Time{}, false, ErrConflict
	}
	if err := transaction.Commit(); err != nil {
		return "", "", time.Time{}, false, fmt.Errorf(
			"commit composer media delete sweep: %w",
			err,
		)
	}
	return mediaID, objectKey, reservedAt, true, nil
}

func (store *PostgresMediaStore) Canonicalize(
	ctx context.Context,
	workspaceID, _ string,
	media []Media,
) ([]Media, error) {
	canonical := make([]Media, len(media))
	for index, candidate := range media {
		inspected, err := store.Get(ctx, workspaceID, strings.TrimSpace(candidate.ID))
		if err != nil {
			if errors.Is(err, ErrNotFound) {
				return nil, &FieldRuleError{
					Field:   fmt.Sprintf("media[%d].id", index),
					Rule:    "inspected_workspace_media",
					Code:    "media_not_ready",
					Message: "Media must belong to this workspace and pass server inspection.",
				}
			}
			return nil, err
		}
		canonical[index] = inspected
	}
	return canonical, nil
}

type mediaLifecycleLink struct {
	ID        string
	ObjectKey string
}

type mediaLinkMutation struct {
	workspaceID string
	draftID     string
	added       []mediaLifecycleLink
	removed     []mediaLifecycleLink
	retained    []mediaLifecycleLink
	expiresAt   time.Time
}

func (store *PostgresMediaStore) ReconcileLifecycle(
	ctx context.Context,
	workspaceID string,
) error {
	var retryErrors []error
	if err := store.recoverExpiredDeleteReservations(
		ctx,
		workspaceID,
		"",
	); err != nil {
		retryErrors = append(retryErrors, err)
	}
	rows, err := store.database.QueryContext(ctx, `
		SELECT id, object_key
		  FROM f06_composer_media
		 WHERE workspace_id = $1
		   AND lifecycle_state = 'temporary'
		   AND lifecycle_sync_pending
		   AND attached_draft_id IS NULL
		   AND deleting_at IS NULL
		 ORDER BY id
		 LIMIT 100`,
		workspaceID,
	)
	if err != nil {
		retryErrors = append(
			retryErrors,
			fmt.Errorf("list composer media lifecycle retries: %w", err),
		)
		return errors.Join(retryErrors...)
	}
	defer rows.Close()
	links := make([]mediaLifecycleLink, 0)
	for rows.Next() {
		var link mediaLifecycleLink
		if err := rows.Scan(&link.ID, &link.ObjectKey); err != nil {
			retryErrors = append(
				retryErrors,
				fmt.Errorf("scan composer media lifecycle retry: %w", err),
			)
			return errors.Join(retryErrors...)
		}
		links = append(links, link)
	}
	if err := rows.Err(); err != nil {
		retryErrors = append(
			retryErrors,
			fmt.Errorf("iterate composer media lifecycle retries: %w", err),
		)
		return errors.Join(retryErrors...)
	}
	if err := rows.Close(); err != nil {
		retryErrors = append(
			retryErrors,
			fmt.Errorf("close composer media lifecycle retries: %w", err),
		)
		return errors.Join(retryErrors...)
	}
	for _, link := range links {
		if err := store.makeTemporary(
			ctx,
			workspaceID,
			link,
			store.clock().UTC().Add(uploadLifetime),
		); err != nil {
			retryErrors = append(retryErrors, err)
		}
	}
	return errors.Join(retryErrors...)
}

func (store *PostgresMediaStore) prepareDraftMediaTx(
	ctx context.Context,
	transaction *sql.Tx,
	workspaceID, draftID string,
	currentIDs, desiredIDs []string,
	now time.Time,
) (mediaLinkMutation, error) {
	mutation := mediaLinkMutation{
		workspaceID: workspaceID,
		draftID:     draftID,
		expiresAt:   now.UTC().Add(uploadLifetime),
	}
	current := stringSet(currentIDs)
	desired := stringSet(desiredIDs)
	for _, mediaID := range desiredIDs {
		var link mediaLifecycleLink
		var status string
		var attachedDraft sql.NullString
		var expiresAt, deletingAt sql.NullTime
		link.ID = mediaID
		err := transaction.QueryRowContext(ctx, `
			SELECT object_key, status, attached_draft_id, expires_at, deleting_at
			  FROM f06_composer_media
			 WHERE workspace_id = $1 AND id = $2
			 FOR UPDATE`,
			workspaceID,
			mediaID,
		).Scan(&link.ObjectKey, &status, &attachedDraft, &expiresAt, &deletingAt)
		if errors.Is(err, sql.ErrNoRows) {
			return mutation, &FieldRuleError{
				Field: "media", Rule: "ready_workspace_media",
				Code:    "media_not_ready",
				Message: "Media must belong to this workspace and remain ready.",
			}
		}
		if err != nil {
			return mutation, fmt.Errorf("lock composer media attachment: %w", err)
		}
		if status != "ready" ||
			deletingAt.Valid ||
			(expiresAt.Valid && !expiresAt.Time.After(now) && !attachedDraft.Valid) {
			return mutation, &FieldRuleError{
				Field: "media", Rule: "ready_workspace_media",
				Code:    "media_not_ready",
				Message: "Media must belong to this workspace and remain ready.",
			}
		}
		if attachedDraft.Valid && attachedDraft.String != draftID {
			return mutation, ErrConflict
		}
		_, wasCurrent := current[mediaID]
		if !wasCurrent || !attachedDraft.Valid {
			mutation.added = append(mutation.added, link)
		}
	}
	for _, mediaID := range currentIDs {
		if _, remains := desired[mediaID]; remains {
			continue
		}
		var link mediaLifecycleLink
		var attachedDraft sql.NullString
		link.ID = mediaID
		err := transaction.QueryRowContext(ctx, `
			SELECT object_key, attached_draft_id
			  FROM f06_composer_media
			 WHERE workspace_id = $1 AND id = $2
			 FOR UPDATE`,
			workspaceID,
			mediaID,
		).Scan(&link.ObjectKey, &attachedDraft)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return mutation, fmt.Errorf("lock removed composer media: %w", err)
		}
		if attachedDraft.Valid && attachedDraft.String == draftID {
			mutation.removed = append(mutation.removed, link)
		}
	}
	for _, link := range mutation.added {
		mutation.retained = append(mutation.retained, link)
		if err := store.objects.Retain(ctx, link.ObjectKey); err != nil {
			return mutation, fmt.Errorf("retain composer object: %w", err)
		}
	}
	return mutation, nil
}

func (store *PostgresMediaStore) applyDraftMediaTx(
	ctx context.Context,
	transaction *sql.Tx,
	mutation mediaLinkMutation,
) error {
	for _, link := range mutation.added {
		tag, err := transaction.ExecContext(ctx, `
			UPDATE f06_composer_media
			   SET attached_draft_id = $3,
			       expires_at = NULL,
			       lifecycle_state = 'retained',
			       lifecycle_sync_pending = false
			 WHERE workspace_id = $1
			   AND id = $2
			   AND status = 'ready'
			   AND (attached_draft_id IS NULL OR attached_draft_id = $3)`,
			mutation.workspaceID,
			link.ID,
			mutation.draftID,
		)
		if err != nil {
			return fmt.Errorf("attach composer media metadata: %w", err)
		}
		if affected, _ := tag.RowsAffected(); affected != 1 {
			return ErrConflict
		}
	}
	for _, link := range mutation.removed {
		tag, err := transaction.ExecContext(ctx, `
			UPDATE f06_composer_media
			   SET attached_draft_id = NULL,
			       expires_at = $4,
			       lifecycle_state = 'temporary',
			       lifecycle_sync_pending = true
			 WHERE workspace_id = $1
			   AND id = $2
			   AND attached_draft_id = $3`,
			mutation.workspaceID,
			link.ID,
			mutation.draftID,
			mutation.expiresAt,
		)
		if err != nil {
			return fmt.Errorf("release composer media metadata: %w", err)
		}
		if affected, _ := tag.RowsAffected(); affected != 1 {
			return ErrConflict
		}
	}
	return nil
}

func (store *PostgresMediaStore) compensateRetained(
	ctx context.Context,
	mutation mediaLinkMutation,
) error {
	compensationContext, cancel := context.WithTimeout(
		context.WithoutCancel(ctx),
		10*time.Second,
	)
	defer cancel()
	var compensationErrors []error
	for _, link := range mutation.retained {
		var attachedDraft sql.NullString
		err := store.database.QueryRowContext(compensationContext, `
		 SELECT attached_draft_id
			  FROM f06_composer_media
			 WHERE workspace_id = $1
			   AND id = $2
			   AND deleting_at IS NULL`,
			mutation.workspaceID,
			link.ID,
		).Scan(&attachedDraft)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			compensationErrors = append(
				compensationErrors,
				fmt.Errorf("check retained composer media compensation: %w", err),
			)
			continue
		}
		if attachedDraft.Valid {
			continue
		}
		if err := store.makeTemporary(
			compensationContext,
			mutation.workspaceID,
			link,
			mutation.expiresAt,
		); err != nil {
			compensationErrors = append(compensationErrors, err)
		}
	}
	return errors.Join(compensationErrors...)
}

func (store *PostgresMediaStore) finalizeRemoved(
	ctx context.Context,
	mutation mediaLinkMutation,
) {
	finalizeContext, cancel := context.WithTimeout(
		context.WithoutCancel(ctx),
		10*time.Second,
	)
	defer cancel()
	for _, link := range mutation.removed {
		_ = store.makeTemporary(
			finalizeContext,
			mutation.workspaceID,
			link,
			mutation.expiresAt,
		)
	}
}

func (store *PostgresMediaStore) makeTemporary(
	ctx context.Context,
	workspaceID string,
	link mediaLifecycleLink,
	expiresAt time.Time,
) error {
	objectErr := store.objects.MakeTemporary(ctx, link.ObjectKey)
	pending := objectErr != nil
	_, databaseErr := store.database.ExecContext(ctx, `
		UPDATE f06_composer_media
		   SET lifecycle_state = 'temporary',
		       lifecycle_sync_pending = $3,
		       expires_at = GREATEST(COALESCE(expires_at, $4), $4)
		 WHERE workspace_id = $1
		   AND id = $2
		   AND attached_draft_id IS NULL
		   AND deleting_at IS NULL`,
		workspaceID,
		link.ID,
		pending,
		expiresAt.UTC(),
	)
	if databaseErr != nil {
		databaseErr = fmt.Errorf("record composer media lifecycle retry: %w", databaseErr)
	}
	if objectErr != nil {
		objectErr = fmt.Errorf("make composer object temporary: %w", objectErr)
	}
	return errors.Join(objectErr, databaseErr)
}

func stringSet(values []string) map[string]struct{} {
	result := make(map[string]struct{}, len(values))
	for _, value := range values {
		result[value] = struct{}{}
	}
	return result
}

func (store *PostgresMediaStore) rejectAndDelete(
	ctx context.Context,
	workspaceID, mediaID, objectKey string,
) error {
	_, databaseErr := store.database.ExecContext(ctx, `
		UPDATE f06_composer_media
		   SET status = 'rejected', inspected_metadata = NULL
		 WHERE workspace_id = $1 AND id = $2`,
		workspaceID,
		mediaID,
	)
	objectErr := store.objects.Delete(ctx, objectKey)
	return errors.Join(databaseErr, objectErr)
}

type StreamMediaInspector struct{}

func (StreamMediaInspector) Inspect(
	_ context.Context,
	source io.Reader,
	info ObjectInfo,
) (Media, error) {
	if source == nil || info.SizeBytes < 1 {
		return Media{}, invalidInspectedMedia("media_object_invalid")
	}
	limited := &io.LimitedReader{R: source, N: info.SizeBytes}
	buffered := bufio.NewReader(limited)
	peekBytes := int64(512)
	if info.SizeBytes < peekBytes {
		peekBytes = info.SizeBytes
	}
	header, err := buffered.Peek(int(peekBytes))
	if err != nil && !errors.Is(err, io.EOF) {
		return Media{}, invalidInspectedMedia("media_object_unreadable")
	}
	actualType := detectMediaContentType(header)
	if actualType != normalizeContentType(info.ContentType) {
		return Media{}, &FieldRuleError{
			Field: "upload", Rule: "content_type_matches_bytes",
			Code:    "media_content_type_mismatch",
			Message: "The object content type does not match its bytes.",
		}
	}
	media := Media{
		ContentType:      actualType,
		SizeBytes:        info.SizeBytes,
		InspectionStatus: InspectionReady,
	}
	switch {
	case strings.HasPrefix(actualType, "image/"):
		var decodedPrefix bytes.Buffer
		config, _, err := image.DecodeConfig(io.LimitReader(
			io.TeeReader(buffered, &decodedPrefix),
			minimumInt64(info.SizeBytes, maximumImageHeaderBytes),
		))
		if err != nil || config.Width < 1 || config.Height < 1 {
			return Media{}, &FieldRuleError{
				Field: "upload", Rule: "decodable_image",
				Code:    "media_image_invalid",
				Message: "The uploaded image could not be decoded safely.",
			}
		}
		fullImage := io.MultiReader(bytes.NewReader(decodedPrefix.Bytes()), buffered)
		if err := verifyImageContainer(actualType, fullImage, info.SizeBytes); err != nil {
			return Media{}, &FieldRuleError{
				Field: "upload", Rule: "complete_image_container",
				Code:    "media_image_invalid",
				Message: "The uploaded image container is truncated or corrupt.",
			}
		}
		media.Kind = MediaImage
		media.Width = config.Width
		media.Height = config.Height
	case actualType == "video/mp4":
		mp4, err := inspectMP4Metadata(buffered, info.SizeBytes)
		if err != nil {
			return Media{}, err
		}
		media.Kind = MediaVideo
		media.Width = mp4.Width
		media.Height = mp4.Height
		media.DurationSeconds = mp4.DurationSeconds
		media.VideoCodec = mp4.VideoCodec
		media.AudioCodec = mp4.AudioCodec
		media.HasAudio = mp4.HasAudio
		media.MoovBeforeMediaData = mp4.MoovBeforeMediaData
	default:
		return Media{}, &FieldRuleError{
			Field: "upload", Rule: "inspectable_media_type",
			Code:    "media_type_unsupported",
			Message: "The server cannot inspect this media type.",
		}
	}
	return media, nil
}

func verifyImageContainer(contentType string, reader io.Reader, size int64) error {
	switch contentType {
	case "image/png":
		return verifyPNG(reader, size)
	case "image/jpeg":
		return verifyTerminalBytes(reader, size, []byte{0xff, 0xd9})
	case "image/gif":
		return verifyTerminalBytes(reader, size, []byte{0x3b})
	default:
		return errors.New("unsupported image container")
	}
}

func verifyPNG(reader io.Reader, totalSize int64) error {
	const pngSignature = "\x89PNG\r\n\x1a\n"
	if totalSize < int64(len(pngSignature))+12 {
		return io.ErrUnexpectedEOF
	}
	signature := make([]byte, len(pngSignature))
	if _, err := io.ReadFull(reader, signature); err != nil ||
		string(signature) != pngSignature {
		return errors.New("invalid PNG signature")
	}
	remaining := totalSize - int64(len(signature))
	firstChunk := true
	header := make([]byte, 8)
	checksumBytes := make([]byte, 4)
	for remaining > 0 {
		if remaining < 12 {
			return io.ErrUnexpectedEOF
		}
		if _, err := io.ReadFull(reader, header); err != nil {
			return err
		}
		length := int64(binary.BigEndian.Uint32(header[:4]))
		chunkType := string(header[4:8])
		if length > remaining-12 {
			return io.ErrUnexpectedEOF
		}
		if firstChunk && (chunkType != "IHDR" || length != 13) {
			return errors.New("invalid first PNG chunk")
		}
		checksum := crc32.NewIEEE()
		_, _ = checksum.Write(header[4:8])
		if _, err := io.CopyN(checksum, reader, length); err != nil {
			return err
		}
		if _, err := io.ReadFull(reader, checksumBytes); err != nil {
			return err
		}
		if binary.BigEndian.Uint32(checksumBytes) != checksum.Sum32() {
			return errors.New("invalid PNG chunk checksum")
		}
		remaining -= length + 12
		firstChunk = false
		if chunkType == "IEND" {
			if length != 0 || remaining != 0 {
				return errors.New("invalid PNG terminator")
			}
			return nil
		}
	}
	return io.ErrUnexpectedEOF
}

func verifyTerminalBytes(reader io.Reader, totalSize int64, terminal []byte) error {
	if totalSize < int64(len(terminal)) {
		return io.ErrUnexpectedEOF
	}
	tail := make([]byte, len(terminal))
	buffer := make([]byte, 32*1024)
	var consumed int64
	for {
		count, err := reader.Read(buffer)
		if count > 0 {
			consumed += int64(count)
			if count >= len(tail) {
				copy(tail, buffer[count-len(tail):count])
			} else {
				copy(tail, tail[count:])
				copy(tail[len(tail)-count:], buffer[:count])
			}
		}
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}
	}
	if consumed != totalSize || !bytes.Equal(tail, terminal) {
		return io.ErrUnexpectedEOF
	}
	return nil
}

func inspectMP4(reader io.Reader, totalSize int64) (bool, error) {
	remaining := totalSize
	first := true
	foundMoov := false
	foundMediaData := false
	moovBeforeMedia := false
	header := make([]byte, 16)
	for remaining > 0 {
		if remaining < 8 {
			return false, invalidInspectedMedia("media_video_invalid")
		}
		if _, err := io.ReadFull(reader, header[:8]); err != nil {
			return false, invalidInspectedMedia("media_video_invalid")
		}
		boxSize := int64(binary.BigEndian.Uint32(header[:4]))
		boxType := string(header[4:8])
		headerSize := int64(8)
		if boxSize == 1 {
			if remaining < 16 {
				return false, invalidInspectedMedia("media_video_invalid")
			}
			if _, err := io.ReadFull(reader, header[8:16]); err != nil {
				return false, invalidInspectedMedia("media_video_invalid")
			}
			boxSize = int64(binary.BigEndian.Uint64(header[8:16]))
			headerSize = 16
		} else if boxSize == 0 {
			boxSize = remaining
		}
		if boxSize < headerSize || boxSize > remaining {
			return false, invalidInspectedMedia("media_video_invalid")
		}
		if first && boxType != "ftyp" {
			return false, invalidInspectedMedia("media_video_invalid")
		}
		first = false
		if boxType == "moov" {
			foundMoov = true
			moovBeforeMedia = !foundMediaData
		}
		if boxType == "mdat" {
			foundMediaData = true
		}
		if _, err := io.CopyN(io.Discard, reader, boxSize-headerSize); err != nil {
			return false, invalidInspectedMedia("media_video_invalid")
		}
		remaining -= boxSize
	}
	if !foundMoov || !foundMediaData {
		return false, invalidInspectedMedia("media_video_invalid")
	}
	return moovBeforeMedia, nil
}

func detectMediaContentType(header []byte) string {
	if len(header) >= 12 && string(header[4:8]) == "ftyp" {
		return "video/mp4"
	}
	return normalizeContentType(http.DetectContentType(header))
}

func invalidInspectedMedia(code string) error {
	return &FieldRuleError{
		Field: "upload", Rule: "valid_media_object", Code: code,
		Message: "The uploaded object failed media inspection.",
	}
}

func normalizeContentType(value string) string {
	return strings.ToLower(strings.TrimSpace(strings.Split(value, ";")[0]))
}

func minimumInt64(left, right int64) int64 {
	if left < right {
		return left
	}
	return right
}

func randomMediaID(random func([]byte) error) (string, error) {
	randomBytes := make([]byte, 18)
	if err := random(randomBytes); err != nil {
		return "", fmt.Errorf("generate media id: %w", err)
	}
	return "media_" + base64.RawURLEncoding.EncodeToString(randomBytes), nil
}

func temporaryObjectKey(workspaceID, mediaID, fileName string) string {
	return "f06/tmp/" + workspaceID + "/" + mediaID + "/" + fileName
}

func mediaBasePath(workspaceID, mediaID string) string {
	return "/api/v1/workspaces/" + workspaceID + "/composer/media/" + mediaID
}
