ALTER TABLE f07_idempotency_operations
    DROP COLUMN actor_account_id,
    ADD COLUMN downstream_idempotency_key text,
    ADD COLUMN response_snapshot jsonb,
    ADD COLUMN response_snapshot_status text NOT NULL DEFAULT 'pending';

-- Preserve the exact F6 key used by duplicate sagas created under 000003.
-- This must run while the legacy raw HTTP key is still available. PostgreSQL
-- text cannot contain NUL, so the legacy Go byte sequence is reproduced with
-- bytea concatenation before the raw key is minimized below.
UPDATE f07_idempotency_operations
SET downstream_idempotency_key = 'f07_' || 'duplicate_' || encode(
    sha256(
        convert_to(workspace_id, 'UTF8') || decode('00', 'hex') ||
        convert_to(operation_kind, 'UTF8') || decode('00', 'hex') ||
        convert_to(idempotency_key, 'UTF8') || decode('00', 'hex') ||
        convert_to(payload_fingerprint, 'UTF8')
    ),
    'hex'
)
WHERE operation_kind = 'duplicate';

UPDATE f07_idempotency_operations
SET idempotency_key = encode(
    sha256(convert_to(idempotency_key, 'UTF8')),
    'hex'
);

ALTER TABLE f07_idempotency_operations
    DROP CONSTRAINT f07_idempotency_operations_idempotency_key_check,
    ADD CONSTRAINT f07_idempotency_operations_idempotency_key_digest_check CHECK (
        idempotency_key ~ '^[0-9a-f]{64}$'
    );

DELETE FROM f07_idempotency_operations operation
WHERE NOT EXISTS (
    SELECT 1
    FROM f04_workspaces workspace
    WHERE workspace.id = operation.workspace_id
);

-- 000003 never captured the 201 response. Current post state cannot recreate
-- that historical response after edit, reschedule, cancellation, publication,
-- or deletion, so legacy completions deliberately fail closed on replay.
UPDATE f07_idempotency_operations
SET response_snapshot_status = 'legacy_unavailable'
WHERE state = 'completed';

ALTER TABLE f07_idempotency_operations
    ADD CONSTRAINT f07_idempotency_operations_downstream_key_check CHECK (
        (operation_kind = 'schedule' AND downstream_idempotency_key IS NULL)
        OR (operation_kind = 'duplicate'
            AND downstream_idempotency_key ~ '^f07_duplicate_[0-9a-f]{64}$')
    ),
    ADD CONSTRAINT f07_idempotency_operations_response_snapshot_status_check CHECK (
        response_snapshot_status IN ('pending', 'available', 'legacy_unavailable')
    ),
    ADD CONSTRAINT f07_idempotency_operations_response_snapshot_check CHECK (
        (state = 'completed' AND response_snapshot_status = 'available'
            AND response_snapshot IS NOT NULL
            AND jsonb_typeof(response_snapshot) = 'object')
        OR (state = 'completed' AND response_snapshot_status = 'legacy_unavailable'
            AND response_snapshot IS NULL)
        OR (state <> 'completed' AND response_snapshot_status = 'pending'
            AND response_snapshot IS NULL)
    );

ALTER TABLE f07_idempotency_operations
    ADD CONSTRAINT f07_idempotency_operations_workspace_fk
    FOREIGN KEY (workspace_id) REFERENCES f04_workspaces(id) ON DELETE CASCADE;

COMMENT ON COLUMN f07_idempotency_operations.response_snapshot IS
    'Canonical browser-safe 201 response captured atomically at completion and used for immutable replay.';
COMMENT ON COLUMN f07_idempotency_operations.response_snapshot_status IS
    'available for immutable snapshots captured by F7; legacy_unavailable fails closed when 000003 never captured the original response.';
COMMENT ON COLUMN f07_idempotency_operations.downstream_idempotency_key IS
    'Stable non-PII F6 duplicate key, materialized before raw HTTP key minimization so prepared sagas survive upgrades.';
COMMENT ON COLUMN f07_idempotency_operations.idempotency_key IS
    'SHA-256 digest of the client key; raw potentially identifying key material is never retained.';
COMMENT ON CONSTRAINT f07_idempotency_operations_workspace_fk
    ON f07_idempotency_operations IS
    'Workspace privacy erasure cascades to every F7 idempotency reservation.';
