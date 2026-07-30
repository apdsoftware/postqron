CREATE TABLE f06_composer_draft_revisions (
    draft_id text NOT NULL
        REFERENCES f06_composer_drafts(id) ON DELETE CASCADE,
    workspace_id text NOT NULL CHECK (length(btrim(workspace_id)) > 0),
    revision bigint NOT NULL CHECK (revision > 0),
    content jsonb NOT NULL CHECK (jsonb_typeof(content) = 'object'),
    autosave_key text,
    saved_at timestamptz NOT NULL,
    PRIMARY KEY (draft_id, revision)
);

CREATE UNIQUE INDEX f06_composer_revisions_autosave_key_idx
    ON f06_composer_draft_revisions (draft_id, autosave_key)
    WHERE autosave_key IS NOT NULL;

INSERT INTO f06_composer_draft_revisions (
    draft_id,
    workspace_id,
    revision,
    content,
    saved_at
)
SELECT
    id,
    workspace_id,
    revision,
    content,
    updated_at
FROM f06_composer_drafts
ON CONFLICT (draft_id, revision) DO NOTHING;

CREATE INDEX f06_composer_revisions_workspace_draft_idx
    ON f06_composer_draft_revisions (workspace_id, draft_id, revision DESC);

CREATE TABLE f06_composer_media (
    id text PRIMARY KEY CHECK (id LIKE 'media_%'),
    workspace_id text NOT NULL CHECK (length(btrim(workspace_id)) > 0),
    object_key text NOT NULL UNIQUE CHECK (
        object_key LIKE 'f06/tmp/%'
        AND length(object_key) <= 1024
    ),
    file_name text NOT NULL CHECK (length(btrim(file_name)) > 0),
    declared_content_type text NOT NULL,
    declared_size_bytes bigint NOT NULL CHECK (declared_size_bytes > 0),
    status text NOT NULL CHECK (status IN ('pending', 'ready', 'rejected')),
    inspected_metadata jsonb,
    attached_draft_id text
        REFERENCES f06_composer_drafts(id) ON DELETE SET NULL,
    created_at timestamptz NOT NULL,
    expires_at timestamptz,
    CHECK (
        (status = 'pending' AND inspected_metadata IS NULL)
        OR (status = 'ready' AND inspected_metadata IS NOT NULL)
        OR status = 'rejected'
    )
);

CREATE INDEX f06_composer_media_workspace_expiry_idx
    ON f06_composer_media (workspace_id, expires_at)
    WHERE attached_draft_id IS NULL;
