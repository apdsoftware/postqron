CREATE TABLE account_privacy_profiles (
    account_id text PRIMARY KEY,
    display_name text NOT NULL CHECK (char_length(display_name) BETWEEN 1 AND 100),
    locale text NOT NULL CHECK (char_length(locale) BETWEEN 2 AND 35),
    timezone text NOT NULL,
    updated_at timestamptz NOT NULL
);

CREATE TABLE account_privacy_export_requests (
    id text PRIMARY KEY,
    account_id text NOT NULL,
    scope text NOT NULL CHECK (scope IN ('account', 'workspace')),
    workspace_id text,
    status text NOT NULL CHECK (status IN ('queued', 'ready', 'failed', 'expired')),
    object_key text,
    sha256 text CHECK (sha256 IS NULL OR sha256 ~ '^[0-9a-f]{64}$'),
    size_bytes bigint CHECK (size_bytes IS NULL OR size_bytes > 0),
    requested_at timestamptz NOT NULL,
    ready_at timestamptz,
    expires_at timestamptz NOT NULL,
    CHECK (
        (scope = 'account' AND workspace_id IS NULL) OR
        (scope = 'workspace' AND workspace_id IS NOT NULL)
    ),
    CHECK (
        status <> 'ready' OR
        (object_key IS NOT NULL AND sha256 IS NOT NULL AND size_bytes IS NOT NULL AND ready_at IS NOT NULL)
    )
);

CREATE INDEX account_privacy_exports_account_idx
    ON account_privacy_export_requests (account_id, requested_at DESC);

CREATE INDEX account_privacy_exports_expiry_idx
    ON account_privacy_export_requests (expires_at)
    WHERE status = 'ready';

CREATE TABLE account_privacy_deletion_requests (
    id text PRIMARY KEY,
    account_id text NOT NULL,
    scope text NOT NULL CHECK (scope IN ('account', 'workspace')),
    workspace_id text,
    status text NOT NULL CHECK (
        status IN (
            'deactivating',
            'grace_period',
            'finalizing',
            'completed',
            'cancelled',
            'deactivation_failed',
            'finalization_failed'
        )
    ),
    requested_at timestamptz NOT NULL,
    grace_ends_at timestamptz NOT NULL,
    immediate boolean NOT NULL DEFAULT false,
    ownership_plan jsonb NOT NULL DEFAULT '{"actions":[]}'::jsonb,
    failure_code text,
    completed_at timestamptz,
    tombstone_id text,
    tombstone_expires_at timestamptz,
    CHECK (
        (scope = 'account' AND workspace_id IS NULL) OR
        (scope = 'workspace' AND workspace_id IS NOT NULL)
    ),
    CHECK (
        status <> 'completed' OR
        (completed_at IS NOT NULL AND tombstone_id IS NOT NULL AND tombstone_expires_at IS NOT NULL)
    )
);

CREATE UNIQUE INDEX account_privacy_active_account_deletion_idx
    ON account_privacy_deletion_requests (account_id)
    WHERE scope = 'account' AND status IN (
        'deactivating',
        'grace_period',
        'finalizing',
        'deactivation_failed',
        'finalization_failed'
    );

CREATE UNIQUE INDEX account_privacy_active_workspace_deletion_idx
    ON account_privacy_deletion_requests (workspace_id)
    WHERE scope = 'workspace' AND status IN (
        'deactivating',
        'grace_period',
        'finalizing',
        'deactivation_failed',
        'finalization_failed'
    );

CREATE INDEX account_privacy_due_deletions_idx
    ON account_privacy_deletion_requests (grace_ends_at)
    WHERE status IN ('grace_period', 'finalization_failed');

CREATE TABLE account_privacy_tombstones (
    id text PRIMARY KEY,
    deletion_request_id text NOT NULL UNIQUE,
    finalized_at timestamptz NOT NULL,
    expires_at timestamptz NOT NULL
);

CREATE INDEX account_privacy_tombstones_expiry_idx
    ON account_privacy_tombstones (expires_at);

CREATE TABLE account_privacy_audit_events (
    id text PRIMARY KEY,
    account_id text,
    target_id text NOT NULL,
    event_type text NOT NULL,
    outcome text NOT NULL,
    metadata jsonb NOT NULL DEFAULT '{}'::jsonb,
    occurred_at timestamptz NOT NULL
);

CREATE INDEX account_privacy_audit_retention_idx
    ON account_privacy_audit_events (occurred_at);
