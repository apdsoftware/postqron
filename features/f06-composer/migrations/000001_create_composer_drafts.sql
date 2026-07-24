CREATE TABLE f06_composer_drafts (
    id text PRIMARY KEY CHECK (
        id LIKE 'draft_%'
        AND length(id) BETWEEN 7 AND 96
    ),
    workspace_id text NOT NULL CHECK (length(btrim(workspace_id)) > 0),
    created_by_account_id text NOT NULL
        CHECK (length(btrim(created_by_account_id)) > 0),
    content jsonb NOT NULL CHECK (
        jsonb_typeof(content) = 'object'
        AND jsonb_typeof(content -> 'text') = 'string'
        AND jsonb_typeof(content -> 'media') = 'array'
        AND jsonb_typeof(content -> 'destinations') = 'array'
    ),
    revision bigint NOT NULL DEFAULT 1 CHECK (revision > 0),
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    CHECK (updated_at >= created_at)
);

CREATE INDEX f06_composer_drafts_workspace_updated_idx
    ON f06_composer_drafts (workspace_id, updated_at DESC, id);

COMMENT ON COLUMN f06_composer_drafts.content IS
    'Composer-owned text, media metadata, and destination overrides; never provider credentials.';
