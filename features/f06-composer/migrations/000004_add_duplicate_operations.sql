CREATE TABLE f06_composer_duplicate_operations (
    workspace_id text NOT NULL CHECK (length(btrim(workspace_id)) > 0),
    idempotency_key text NOT NULL CHECK (length(btrim(idempotency_key)) > 0),
    source_draft_id text NOT NULL CHECK (length(btrim(source_draft_id)) > 0),
    source_revision bigint NOT NULL CHECK (source_revision > 0),
    created_by_account_id text NOT NULL CHECK (length(btrim(created_by_account_id)) > 0),
    status text NOT NULL CHECK (status IN ('pending', 'completed')),
    clone_draft_id text,
    clone_draft_revision bigint CHECK (
        clone_draft_revision IS NULL OR clone_draft_revision > 0
    ),
    locked_until timestamptz,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    PRIMARY KEY (workspace_id, idempotency_key),
    CHECK (
        (status = 'pending' AND clone_draft_id IS NULL AND clone_draft_revision IS NULL)
        OR
        (status = 'completed' AND clone_draft_id IS NOT NULL AND clone_draft_revision IS NOT NULL)
    )
);

CREATE INDEX f06_composer_duplicate_operations_workspace_source_idx
    ON f06_composer_duplicate_operations (
        workspace_id,
        source_draft_id,
        source_revision,
        created_at DESC
    );
