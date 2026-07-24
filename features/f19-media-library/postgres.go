package medialibrary

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type PostgresRepository struct {
	pool *pgxpool.Pool
}

func NewPostgresRepository(pool *pgxpool.Pool) (*PostgresRepository, error) {
	if pool == nil {
		return nil, fmt.Errorf("%w: postgres pool is required", ErrInvalidArgument)
	}
	return &PostgresRepository{pool: pool}, nil
}

func (repository *PostgresRepository) CreateUpload(
	ctx context.Context,
	upload Upload,
) (Upload, bool, error) {
	tag, err := repository.pool.Exec(ctx, `
		INSERT INTO f19_media_uploads (
			id, asset_id, workspace_id, created_by_account_id, storage_key,
			original_name, declared_content_type, reserved_size_bytes,
			idempotency_key, status, expires_at, created_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
		ON CONFLICT (workspace_id, idempotency_key) DO NOTHING
	`,
		upload.ID, upload.AssetID, upload.WorkspaceID, upload.CreatedBy,
		upload.StorageKey, upload.OriginalName, upload.DeclaredType,
		upload.ReservedSizeBytes, upload.IdempotencyKey, upload.Status,
		upload.ExpiresAt, upload.CreatedAt,
	)
	if err != nil {
		if isPostgresUniqueViolation(err) {
			return Upload{}, false, ErrConflict
		}
		return Upload{}, false, fmt.Errorf("create media upload: %w", err)
	}
	if tag.RowsAffected() == 1 {
		return upload, true, nil
	}
	existing, err := scanUpload(repository.pool.QueryRow(ctx, `
		SELECT
			id, asset_id, workspace_id, created_by_account_id, storage_key,
			original_name, declared_content_type, reserved_size_bytes,
			idempotency_key, status, expires_at, created_at, completed_at
		FROM f19_media_uploads
		WHERE workspace_id = $1 AND idempotency_key = $2
	`, upload.WorkspaceID, upload.IdempotencyKey))
	return existing, false, err
}

func (repository *PostgresRepository) GetUpload(
	ctx context.Context,
	workspaceID, uploadID string,
) (Upload, error) {
	return scanUpload(repository.pool.QueryRow(ctx, `
		SELECT
			id, asset_id, workspace_id, created_by_account_id, storage_key,
			original_name, declared_content_type, reserved_size_bytes,
			idempotency_key, status, expires_at, created_at, completed_at
		FROM f19_media_uploads
		WHERE workspace_id = $1 AND id = $2
	`, workspaceID, uploadID))
}

func (repository *PostgresRepository) CancelUpload(
	ctx context.Context,
	workspaceID, uploadID string,
) error {
	tag, err := repository.pool.Exec(ctx, `
		UPDATE f19_media_uploads
		SET status = 'canceled'
		WHERE workspace_id = $1 AND id = $2 AND status = 'pending'
	`, workspaceID, uploadID)
	if err != nil {
		return fmt.Errorf("cancel media upload: %w", err)
	}
	if tag.RowsAffected() == 1 {
		return nil
	}
	upload, getErr := repository.GetUpload(ctx, workspaceID, uploadID)
	if getErr != nil {
		return getErr
	}
	if upload.Status == UploadCompleted {
		return ErrConflict
	}
	return nil
}

func (repository *PostgresRepository) CompleteUpload(
	ctx context.Context,
	upload Upload,
	asset Asset,
) (result Asset, created bool, err error) {
	err = pgx.BeginFunc(ctx, repository.pool, func(tx pgx.Tx) error {
		current, scanErr := scanUpload(tx.QueryRow(ctx, `
			SELECT
				id, asset_id, workspace_id, created_by_account_id, storage_key,
				original_name, declared_content_type, reserved_size_bytes,
				idempotency_key, status, expires_at, created_at, completed_at
			FROM f19_media_uploads
			WHERE workspace_id = $1 AND id = $2
			FOR UPDATE
		`, upload.WorkspaceID, upload.ID))
		if scanErr != nil {
			return scanErr
		}
		if current.Status == UploadCompleted {
			existing, getErr := scanAsset(tx.QueryRow(ctx, assetSelect+`
				WHERE workspace_id = $1 AND id = $2
			`, upload.WorkspaceID, upload.AssetID))
			if getErr != nil {
				return getErr
			}
			result = existing
			return nil
		}
		if current.Status != UploadPending {
			return ErrConflict
		}
		_, insertErr := tx.Exec(ctx, `
			INSERT INTO f19_media_assets (
				id, workspace_id, created_by_account_id, storage_key,
				original_name, kind, content_type, size_bytes, width, height,
				color_space, video_codec, audio_codec, audio_sample_rate,
				frames_per_second, video_bitrate, audio_bitrate,
				duration_seconds, has_audio, has_edit_list,
				moov_before_media_data, checksum_sha256, alt_text, tags,
				status, revision, created_at, updated_at
			) VALUES (
				$1, $2, $3, $4, $5, $6, $7, $8, $9, $10,
				$11, $12, $13, $14, $15, $16, $17, $18, $19, $20,
				$21, $22, $23, $24, $25, $26, $27, $28
			)
		`,
			asset.ID, asset.WorkspaceID, asset.CreatedBy, asset.StorageKey,
			asset.OriginalName, asset.Kind, asset.ContentType, asset.SizeBytes,
			asset.Width, asset.Height, asset.ColorSpace, asset.VideoCodec,
			asset.AudioCodec, asset.AudioSampleRate, asset.FramesPerSecond,
			asset.VideoBitrate, asset.AudioBitrate, asset.DurationSeconds,
			asset.HasAudio, asset.HasEditList, asset.MoovBeforeMediaData,
			asset.ChecksumSHA256, asset.AltText, asset.Tags, asset.Status,
			asset.Revision, asset.CreatedAt, asset.UpdatedAt,
		)
		if insertErr != nil {
			if isPostgresUniqueViolation(insertErr) {
				return ErrConflict
			}
			return fmt.Errorf("create media asset: %w", insertErr)
		}
		_, updateErr := tx.Exec(ctx, `
			UPDATE f19_media_uploads
			SET status = 'completed', completed_at = $3
			WHERE workspace_id = $1 AND id = $2
		`, upload.WorkspaceID, upload.ID, asset.CreatedAt)
		if updateErr != nil {
			return fmt.Errorf("complete media upload: %w", updateErr)
		}
		result = cloneAsset(asset)
		created = true
		return nil
	})
	return result, created, err
}

const assetSelect = `
	SELECT
		id, workspace_id, created_by_account_id, storage_key, original_name,
		kind, content_type, size_bytes, width, height, color_space,
		video_codec, audio_codec, audio_sample_rate, frames_per_second,
		video_bitrate, audio_bitrate, duration_seconds, has_audio,
		has_edit_list, moov_before_media_data, checksum_sha256, alt_text,
		tags, status, revision, created_at, updated_at, archived_at, purged_at
	FROM f19_media_assets
`

func (repository *PostgresRepository) GetAsset(
	ctx context.Context,
	workspaceID, assetID string,
) (Asset, error) {
	return scanAsset(repository.pool.QueryRow(ctx, assetSelect+`
		WHERE workspace_id = $1 AND id = $2
	`, workspaceID, assetID))
}

func (repository *PostgresRepository) Search(
	ctx context.Context,
	workspaceID string,
	query SearchQuery,
) ([]Asset, error) {
	rows, err := repository.pool.Query(ctx, assetSelect+`
		WHERE workspace_id = $1
		  AND status = 'ready'
		  AND ($2 = '' OR kind = $2)
		  AND (
			$3 = ''
			OR original_name ILIKE '%' || $3 || '%'
			OR alt_text ILIKE '%' || $3 || '%'
			OR array_to_string(tags, ' ') ILIKE '%' || $3 || '%'
		  )
		  AND ($4::text[] <@ tags)
		ORDER BY updated_at DESC, id
		LIMIT $5
	`, workspaceID, query.Kind, query.Text, query.Tags, query.Limit)
	if err != nil {
		return nil, fmt.Errorf("search media assets: %w", err)
	}
	defer rows.Close()
	assets := make([]Asset, 0)
	for rows.Next() {
		asset, scanErr := scanAsset(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		assets = append(assets, asset)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate media assets: %w", err)
	}
	return assets, nil
}

func (repository *PostgresRepository) UpdateMetadata(
	ctx context.Context,
	asset Asset,
	expectedRevision int64,
) (Asset, error) {
	updated, err := scanAsset(repository.pool.QueryRow(ctx, assetSelectFromUpdate(`
		UPDATE f19_media_assets
		SET original_name = $4,
			alt_text = $5,
			tags = $6,
			revision = revision + 1,
			updated_at = $7
		WHERE workspace_id = $1 AND id = $2 AND revision = $3
		  AND status = 'ready'
	`),
		asset.WorkspaceID, asset.ID, expectedRevision, asset.OriginalName,
		asset.AltText, asset.Tags, asset.UpdatedAt,
	))
	if !errors.Is(err, ErrNotFound) {
		return updated, err
	}
	return Asset{}, repository.classifyAssetMiss(
		ctx, asset.WorkspaceID, asset.ID, expectedRevision,
	)
}

func (repository *PostgresRepository) Archive(
	ctx context.Context,
	workspaceID, assetID string,
	expectedRevision int64,
	now time.Time,
) (Asset, error) {
	asset, err := scanAsset(repository.pool.QueryRow(ctx, assetSelectFromUpdate(`
		UPDATE f19_media_assets
		SET status = 'archived',
			revision = revision + 1,
			updated_at = $4,
			archived_at = $4
		WHERE workspace_id = $1 AND id = $2 AND revision = $3
		  AND status = 'ready'
	`), workspaceID, assetID, expectedRevision, now))
	if !errors.Is(err, ErrNotFound) {
		return asset, err
	}
	return Asset{}, repository.classifyAssetMiss(ctx, workspaceID, assetID, expectedRevision)
}

func (repository *PostgresRepository) MarkPurged(
	ctx context.Context,
	workspaceID, assetID string,
	expectedRevision int64,
	now time.Time,
) error {
	tag, err := repository.pool.Exec(ctx, `
		UPDATE f19_media_assets
		SET status = 'purged',
			revision = revision + 1,
			updated_at = $4,
			purged_at = $4
		WHERE workspace_id = $1 AND id = $2 AND revision = $3
		  AND status = 'archived'
	`, workspaceID, assetID, expectedRevision, now)
	if err != nil {
		return fmt.Errorf("purge media asset: %w", err)
	}
	if tag.RowsAffected() == 1 {
		return nil
	}
	return repository.classifyAssetMiss(ctx, workspaceID, assetID, expectedRevision)
}

func (repository *PostgresRepository) classifyAssetMiss(
	ctx context.Context,
	workspaceID, assetID string,
	expectedRevision int64,
) error {
	asset, err := repository.GetAsset(ctx, workspaceID, assetID)
	if err != nil {
		return err
	}
	if asset.Revision != expectedRevision {
		return ErrConflict
	}
	if asset.Status != StatusReady {
		return ErrAssetArchived
	}
	return ErrConflict
}

func assetSelectFromUpdate(update string) string {
	return update + `
		RETURNING
			id, workspace_id, created_by_account_id, storage_key, original_name,
			kind, content_type, size_bytes, width, height, color_space,
			video_codec, audio_codec, audio_sample_rate, frames_per_second,
			video_bitrate, audio_bitrate, duration_seconds, has_audio,
			has_edit_list, moov_before_media_data, checksum_sha256, alt_text,
			tags, status, revision, created_at, updated_at, archived_at, purged_at
	`
}

type postgresRow interface {
	Scan(...any) error
}

func scanUpload(row postgresRow) (Upload, error) {
	var upload Upload
	err := row.Scan(
		&upload.ID, &upload.AssetID, &upload.WorkspaceID, &upload.CreatedBy,
		&upload.StorageKey, &upload.OriginalName, &upload.DeclaredType,
		&upload.ReservedSizeBytes, &upload.IdempotencyKey, &upload.Status, &upload.ExpiresAt,
		&upload.CreatedAt, &upload.CompletedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return Upload{}, ErrNotFound
	}
	if err != nil {
		return Upload{}, fmt.Errorf("scan media upload: %w", err)
	}
	return upload, nil
}

func scanAsset(row postgresRow) (Asset, error) {
	var asset Asset
	err := row.Scan(
		&asset.ID, &asset.WorkspaceID, &asset.CreatedBy, &asset.StorageKey,
		&asset.OriginalName, &asset.Kind, &asset.ContentType, &asset.SizeBytes,
		&asset.Width, &asset.Height, &asset.ColorSpace, &asset.VideoCodec,
		&asset.AudioCodec, &asset.AudioSampleRate, &asset.FramesPerSecond,
		&asset.VideoBitrate, &asset.AudioBitrate, &asset.DurationSeconds,
		&asset.HasAudio, &asset.HasEditList, &asset.MoovBeforeMediaData,
		&asset.ChecksumSHA256, &asset.AltText, &asset.Tags, &asset.Status,
		&asset.Revision, &asset.CreatedAt, &asset.UpdatedAt, &asset.ArchivedAt,
		&asset.PurgedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return Asset{}, ErrNotFound
	}
	if err != nil {
		return Asset{}, fmt.Errorf("scan media asset: %w", err)
	}
	return asset, nil
}

func isPostgresUniqueViolation(err error) bool {
	var postgresError interface{ SQLState() string }
	return errors.As(err, &postgresError) && postgresError.SQLState() == "23505"
}
