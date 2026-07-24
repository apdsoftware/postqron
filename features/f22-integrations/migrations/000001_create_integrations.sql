CREATE SCHEMA IF NOT EXISTS integrations;

-- Raw public API credentials are shown once and never persisted. Authentication
-- compares a SHA-256 digest through the F3 adapter.
CREATE TABLE integrations.api_credentials (
    credential_id text PRIMARY KEY,
    workspace_id uuid NOT NULL,
    name text NOT NULL,
    token_digest bytea NOT NULL UNIQUE,
    scopes text[] NOT NULL,
    expires_at timestamptz NOT NULL,
    revoked_at timestamptz,
    created_by_account_id text NOT NULL,
    created_at timestamptz NOT NULL,
    last_used_at timestamptz,
    CHECK (length(credential_id) BETWEEN 1 AND 128),
    CHECK (length(name) BETWEEN 1 AND 100),
    CHECK (octet_length(token_digest) = 32),
    CHECK (
        scopes <@ ARRAY[
            'posts:read',
            'posts:write',
            'webhooks:read',
            'webhooks:write'
        ]::text[]
    ),
    CHECK (cardinality(scopes) > 0),
    CHECK (expires_at > created_at),
    CHECK (revoked_at IS NULL OR revoked_at >= created_at)
);

CREATE INDEX api_credentials_workspace_active_idx
    ON integrations.api_credentials (workspace_id, expires_at)
    WHERE revoked_at IS NULL;

-- The application serializes on this primary key. A matching fingerprint
-- replays the stored response; a mismatch is a 409 conflict.
CREATE TABLE integrations.idempotency_responses (
    workspace_id uuid NOT NULL,
    credential_id text NOT NULL,
    operation text NOT NULL,
    idempotency_key text NOT NULL,
    request_fingerprint bytea NOT NULL,
    response_status integer NOT NULL,
    response_headers jsonb NOT NULL,
    response_body bytea NOT NULL,
    created_at timestamptz NOT NULL,
    expires_at timestamptz NOT NULL,
    PRIMARY KEY (
        workspace_id,
        credential_id,
        operation,
        idempotency_key
    ),
    CHECK (length(operation) BETWEEN 1 AND 100),
    CHECK (length(idempotency_key) BETWEEN 16 AND 128),
    CHECK (octet_length(request_fingerprint) = 32),
    CHECK (response_status BETWEEN 200 AND 299),
    CHECK (expires_at > created_at)
);

CREATE INDEX idempotency_responses_expiry_idx
    ON integrations.idempotency_responses (expires_at);

-- Signing secrets are envelope-encrypted outside PostgreSQL. No provider token
-- or OAuth credential belongs in this schema.
CREATE TABLE integrations.webhook_subscriptions (
    subscription_id text PRIMARY KEY,
    workspace_id uuid NOT NULL,
    endpoint text NOT NULL,
    event_types text[] NOT NULL,
    event_version text NOT NULL,
    signing_secret_ciphertext bytea NOT NULL,
    signing_secret_key_id text NOT NULL,
    active boolean NOT NULL DEFAULT true,
    created_by_account_id text NOT NULL,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    disabled_at timestamptz,
    CHECK (length(subscription_id) BETWEEN 1 AND 128),
    CHECK (endpoint ~ '^https://'),
    CHECK (length(endpoint) <= 2048),
    CHECK (cardinality(event_types) > 0),
    CHECK (event_version = '2026-07-01'),
    CHECK (octet_length(signing_secret_ciphertext) > 32),
    CHECK (length(signing_secret_key_id) BETWEEN 1 AND 128),
    CHECK (
        (active AND disabled_at IS NULL)
        OR (NOT active AND disabled_at IS NOT NULL)
    )
);

CREATE INDEX webhook_subscriptions_workspace_active_idx
    ON integrations.webhook_subscriptions (workspace_id)
    WHERE active;

CREATE TABLE integrations.webhook_events (
    event_id text PRIMARY KEY,
    workspace_id uuid NOT NULL,
    event_type text NOT NULL,
    event_version text NOT NULL,
    occurred_at timestamptz NOT NULL,
    payload jsonb NOT NULL,
    enqueued_at timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz NOT NULL,
    CHECK (length(event_id) BETWEEN 1 AND 128),
    CHECK (event_type ~ '^[a-z0-9_]+([.][a-z0-9_]+)+$'),
    CHECK (event_version = '2026-07-01'),
    CHECK (jsonb_typeof(payload) = 'object'),
    CHECK (expires_at > enqueued_at)
);

CREATE INDEX webhook_events_expiry_idx
    ON integrations.webhook_events (expires_at);

CREATE TABLE integrations.webhook_deliveries (
    delivery_id text PRIMARY KEY,
    event_id text NOT NULL
        REFERENCES integrations.webhook_events(event_id),
    subscription_id text NOT NULL
        REFERENCES integrations.webhook_subscriptions(subscription_id),
    workspace_id uuid NOT NULL,
    state text NOT NULL
        CHECK (state IN ('pending', 'leased', 'delivered', 'dead_lettered')),
    attempt_count integer NOT NULL DEFAULT 0
        CHECK (attempt_count BETWEEN 0 AND 8),
    next_attempt_at timestamptz NOT NULL,
    locked_until timestamptz,
    delivered_at timestamptz,
    last_status_code integer,
    last_error_code text,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (event_id, subscription_id),
    CHECK (
        last_status_code IS NULL
        OR last_status_code BETWEEN 100 AND 599
    ),
    CHECK (
        (state = 'delivered' AND delivered_at IS NOT NULL)
        OR (state <> 'delivered' AND delivered_at IS NULL)
    )
);

CREATE INDEX webhook_deliveries_due_idx
    ON integrations.webhook_deliveries (next_attempt_at, delivery_id)
    WHERE state IN ('pending', 'leased');

CREATE TABLE integrations.webhook_dead_letters (
    delivery_id text PRIMARY KEY
        REFERENCES integrations.webhook_deliveries(delivery_id),
    workspace_id uuid NOT NULL,
    event_id text NOT NULL,
    subscription_id text NOT NULL,
    attempt_count integer NOT NULL,
    last_status_code integer,
    error_code text NOT NULL,
    failed_at timestamptz NOT NULL,
    expires_at timestamptz NOT NULL,
    CHECK (attempt_count BETWEEN 1 AND 8),
    CHECK (length(error_code) BETWEEN 1 AND 64),
    CHECK (expires_at > failed_at)
);

CREATE INDEX webhook_dead_letters_expiry_idx
    ON integrations.webhook_dead_letters (expires_at);

COMMENT ON COLUMN integrations.webhook_events.payload IS
    'Validated public event data; credential-like field names are rejected.';
COMMENT ON COLUMN integrations.webhook_dead_letters.error_code IS
    'Bounded diagnostic category only; never a response body, secret, or URL.';

REVOKE ALL ON SCHEMA integrations FROM PUBLIC;
REVOKE ALL ON ALL TABLES IN SCHEMA integrations FROM PUBLIC;
