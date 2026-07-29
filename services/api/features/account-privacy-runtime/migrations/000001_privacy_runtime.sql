CREATE TABLE account_privacy_export_jobs (
    request_id text PRIMARY KEY
        REFERENCES account_privacy_export_requests(id) ON DELETE CASCADE,
    account_id text NOT NULL,
    scope text NOT NULL CHECK (scope IN ('account', 'workspace')),
    workspace_id text,
    state text NOT NULL DEFAULT 'queued'
        CHECK (state IN ('queued', 'processing', 'completed', 'failed')),
    attempts integer NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    available_at timestamptz NOT NULL,
    claimed_at timestamptz,
    claim_token text,
    last_error_code text,
    created_at timestamptz NOT NULL,
    completed_at timestamptz,
    CHECK (
        (scope = 'account' AND workspace_id IS NULL) OR
        (scope = 'workspace' AND workspace_id IS NOT NULL)
    )
);

CREATE INDEX account_privacy_export_jobs_claim_idx
    ON account_privacy_export_jobs (available_at, created_at, request_id)
    WHERE state IN ('queued', 'processing');

CREATE TABLE account_privacy_download_tokens (
    token_hash char(64) PRIMARY KEY CHECK (token_hash ~ '^[0-9a-f]{64}$'),
    export_id text NOT NULL
        REFERENCES account_privacy_export_requests(id) ON DELETE CASCADE,
    account_id text NOT NULL,
    object_key text NOT NULL,
    expires_at timestamptz NOT NULL,
    consumed_at timestamptz,
    created_at timestamptz NOT NULL
);

CREATE INDEX account_privacy_download_tokens_expiry_idx
    ON account_privacy_download_tokens (expires_at)
    WHERE consumed_at IS NULL;

CREATE TABLE account_privacy_cancel_capabilities (
    token_hash char(64) PRIMARY KEY CHECK (token_hash ~ '^[0-9a-f]{64}$'),
    account_id text NOT NULL,
    expires_at timestamptz NOT NULL,
    consumed_at timestamptz,
    created_at timestamptz NOT NULL
);

CREATE INDEX account_privacy_cancel_capabilities_expiry_idx
    ON account_privacy_cancel_capabilities (expires_at)
    WHERE consumed_at IS NULL;

CREATE TABLE account_privacy_runtime_audit (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    target_id text NOT NULL,
    event_type text NOT NULL,
    outcome text NOT NULL CHECK (outcome IN ('succeeded', 'failed')),
    error_code text,
    occurred_at timestamptz NOT NULL
);

CREATE INDEX account_privacy_runtime_audit_time_idx
    ON account_privacy_runtime_audit (occurred_at);
