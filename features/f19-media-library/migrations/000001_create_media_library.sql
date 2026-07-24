CREATE TABLE f19_media_uploads (
    id text PRIMARY KEY CHECK (
        id LIKE 'upload_%' AND length(id) BETWEEN 8 AND 96
    ),
    asset_id text NOT NULL UNIQUE CHECK (
        asset_id LIKE 'media_%' AND length(asset_id) BETWEEN 7 AND 96
    ),
    workspace_id text NOT NULL CHECK (length(btrim(workspace_id)) > 0),
    created_by_account_id text NOT NULL
        CHECK (length(btrim(created_by_account_id)) > 0),
    storage_key text NOT NULL UNIQUE
        CHECK (length(btrim(storage_key)) > 0),
    original_name text NOT NULL
        CHECK (length(btrim(original_name)) BETWEEN 1 AND 255),
    declared_content_type text NOT NULL CHECK (
        declared_content_type LIKE 'image/%'
        OR declared_content_type LIKE 'video/%'
    ),
    reserved_size_bytes bigint NOT NULL
        CHECK (reserved_size_bytes BETWEEN 1 AND 5368709120),
    idempotency_key text NOT NULL
        CHECK (length(idempotency_key) BETWEEN 1 AND 200),
    status text NOT NULL CHECK (
        status IN ('pending', 'completed', 'canceled')
    ),
    expires_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL,
    completed_at timestamptz,
    CHECK (expires_at > created_at),
    CHECK (
        (status = 'completed' AND completed_at IS NOT NULL)
        OR (status <> 'completed' AND completed_at IS NULL)
    )
);

CREATE UNIQUE INDEX f19_media_uploads_workspace_request_idx
    ON f19_media_uploads (workspace_id, idempotency_key);

CREATE INDEX f19_media_uploads_workspace_status_idx
    ON f19_media_uploads (workspace_id, status, expires_at);

CREATE TABLE f19_media_assets (
    id text PRIMARY KEY CHECK (
        id LIKE 'media_%' AND length(id) BETWEEN 7 AND 96
    ),
    workspace_id text NOT NULL CHECK (length(btrim(workspace_id)) > 0),
    created_by_account_id text NOT NULL
        CHECK (length(btrim(created_by_account_id)) > 0),
    storage_key text NOT NULL UNIQUE
        CHECK (length(btrim(storage_key)) > 0),
    original_name text NOT NULL
        CHECK (length(btrim(original_name)) BETWEEN 1 AND 255),
    kind text NOT NULL CHECK (kind IN ('image', 'video')),
    content_type text NOT NULL CHECK (
        content_type LIKE 'image/%' OR content_type LIKE 'video/%'
    ),
    size_bytes bigint NOT NULL
        CHECK (size_bytes BETWEEN 1 AND 5368709120),
    width integer NOT NULL CHECK (width > 0),
    height integer NOT NULL CHECK (height > 0),
    color_space text NOT NULL DEFAULT '',
    video_codec text NOT NULL DEFAULT '',
    audio_codec text NOT NULL DEFAULT '',
    audio_sample_rate integer NOT NULL DEFAULT 0
        CHECK (audio_sample_rate >= 0),
    frames_per_second double precision NOT NULL DEFAULT 0
        CHECK (frames_per_second >= 0),
    video_bitrate bigint NOT NULL DEFAULT 0 CHECK (video_bitrate >= 0),
    audio_bitrate bigint NOT NULL DEFAULT 0 CHECK (audio_bitrate >= 0),
    duration_seconds double precision NOT NULL DEFAULT 0
        CHECK (duration_seconds >= 0),
    has_audio boolean NOT NULL DEFAULT false,
    has_edit_list boolean NOT NULL DEFAULT false,
    moov_before_media_data boolean NOT NULL DEFAULT false,
    checksum_sha256 text NOT NULL CHECK (
        checksum_sha256 ~ '^[0-9a-f]{64}$'
    ),
    alt_text text NOT NULL DEFAULT '',
    tags text[] NOT NULL DEFAULT '{}',
    status text NOT NULL CHECK (status IN ('ready', 'archived', 'purged')),
    revision bigint NOT NULL DEFAULT 1 CHECK (revision > 0),
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    archived_at timestamptz,
    purged_at timestamptz,
    CHECK (updated_at >= created_at),
    CHECK (
        (status = 'ready' AND archived_at IS NULL AND purged_at IS NULL)
        OR (status = 'archived' AND archived_at IS NOT NULL AND purged_at IS NULL)
        OR (status = 'purged' AND archived_at IS NOT NULL AND purged_at IS NOT NULL)
    )
);

CREATE INDEX f19_media_assets_workspace_updated_idx
    ON f19_media_assets (workspace_id, status, updated_at DESC, id);

CREATE INDEX f19_media_assets_tags_idx
    ON f19_media_assets USING gin (tags);

COMMENT ON TABLE f19_media_assets IS
    'Trusted inspected metadata for F6 reuse; archived rows protect existing drafts.';

COMMENT ON COLUMN f19_media_assets.storage_key IS
    'Opaque object key only. Signed URLs and provider credentials are never persisted.';
