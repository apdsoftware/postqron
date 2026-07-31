CREATE TABLE f07_idempotency_operations (
    workspace_id text NOT NULL CHECK (length(btrim(workspace_id)) > 0),
    operation_kind text NOT NULL CHECK (
        operation_kind IN ('schedule', 'duplicate')
    ),
    idempotency_key text NOT NULL CHECK (
        length(idempotency_key) BETWEEN 1 AND 200
        AND idempotency_key = btrim(idempotency_key)
    ),
    payload_fingerprint text NOT NULL CHECK (
        payload_fingerprint ~ '^[0-9a-f]{64}$'
    ),
    actor_account_id text NOT NULL CHECK (length(btrim(actor_account_id)) > 0),
    state text NOT NULL CHECK (
        state IN ('reserved', 'prepared', 'clone_created', 'completed')
    ),
    post_id text NOT NULL CHECK (post_id LIKE 'post_%'),
    publication_command_id text NOT NULL CHECK (
        publication_command_id LIKE 'pubcmd_%'
    ),
    source_post_id text,
    source_post_revision bigint,
    source_draft_id text,
    source_draft_revision bigint,
    channel_ids text[],
    scheduled_for_utc timestamptz,
    scheduled_local text,
    scheduled_timezone text,
    scheduled_utc_offset_minutes smallint,
    clone_draft_id text,
    clone_draft_revision bigint,
    lease_generation bigint NOT NULL CHECK (lease_generation > 0),
    locked_until timestamptz,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL CHECK (updated_at >= created_at),
    completed_at timestamptz,
    PRIMARY KEY (workspace_id, operation_kind, idempotency_key),
    CHECK (
        (operation_kind = 'schedule' AND state IN ('reserved', 'completed'))
        OR operation_kind = 'duplicate'
    ),
    CHECK (
        operation_kind = 'schedule'
        OR state = 'reserved'
        OR (
            source_post_id IS NOT NULL
            AND source_post_revision > 0
            AND source_draft_id IS NOT NULL
            AND source_draft_revision > 0
            AND cardinality(channel_ids) > 0
            AND array_position(channel_ids, NULL) IS NULL
            AND scheduled_for_utc IS NOT NULL
            AND scheduled_local IS NOT NULL
            AND scheduled_timezone IS NOT NULL
            AND scheduled_utc_offset_minutes BETWEEN -1080 AND 1080
        )
    ),
    CHECK (
        state NOT IN ('clone_created', 'completed')
        OR operation_kind = 'schedule'
        OR (
            clone_draft_id IS NOT NULL
            AND clone_draft_revision > 0
        )
    ),
    CHECK (
        (state = 'completed' AND locked_until IS NULL AND completed_at IS NOT NULL)
        OR (state <> 'completed' AND locked_until IS NOT NULL AND completed_at IS NULL)
    )
);

CREATE UNIQUE INDEX f07_idempotency_operations_post_idx
    ON f07_idempotency_operations (post_id);

CREATE INDEX f07_idempotency_operations_recovery_idx
    ON f07_idempotency_operations (locked_until, workspace_id, operation_kind)
    WHERE state <> 'completed';

COMMENT ON TABLE f07_idempotency_operations IS
    'Durable HTTP idempotency reservations and fenced duplicate-draft recovery saga owned by F7.';
COMMENT ON COLUMN f07_idempotency_operations.payload_fingerprint IS
    'Canonical SHA-256 request fingerprint; a key cannot be rebound to different input.';
COMMENT ON COLUMN f07_idempotency_operations.lease_generation IS
    'Fencing token incremented on every expired-lease takeover.';
