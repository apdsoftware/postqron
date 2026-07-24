CREATE TABLE f23_push_subscriptions (
    id text PRIMARY KEY CHECK (
        id LIKE 'subscription_%'
        AND length(id) BETWEEN 20 AND 96
    ),
    account_id text NOT NULL CHECK (length(btrim(account_id)) BETWEEN 1 AND 160),
    endpoint_hash char(64) NOT NULL UNIQUE CHECK (
        endpoint_hash ~ '^[0-9a-f]{64}$'
    ),
    key_id text NOT NULL CHECK (length(btrim(key_id)) > 0),
    endpoint_ciphertext bytea NOT NULL CHECK (octet_length(endpoint_ciphertext) > 16),
    p256dh_ciphertext bytea NOT NULL CHECK (octet_length(p256dh_ciphertext) > 16),
    auth_ciphertext bytea NOT NULL CHECK (octet_length(auth_ciphertext) > 16),
    expiration_time timestamptz,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    revoked_at timestamptz,
    CHECK (updated_at >= created_at),
    CHECK (revoked_at IS NULL OR revoked_at >= created_at)
);

CREATE TABLE f23_push_events (
    event_id text PRIMARY KEY CHECK (length(btrim(event_id)) BETWEEN 1 AND 160),
    fingerprint char(64) NOT NULL CHECK (fingerprint ~ '^[0-9a-f]{64}$'),
    kind text NOT NULL CHECK (
        kind IN (
            'publication_failed',
            'review_requested',
            'review_approved',
            'review_changes_requested'
        )
    ),
    workspace_id text NOT NULL CHECK (length(btrim(workspace_id)) > 0),
    resource_id text NOT NULL CHECK (length(btrim(resource_id)) > 0),
    title text NOT NULL CHECK (char_length(title) BETWEEN 1 AND 80),
    body text NOT NULL CHECK (char_length(body) <= 240),
    action_url text NOT NULL CHECK (
        action_url LIKE '/%'
        AND action_url NOT LIKE '//%'
        AND char_length(action_url) <= 1024
    ),
    occurred_at timestamptz NOT NULL,
    received_at timestamptz NOT NULL
);

CREATE TABLE f23_push_deliveries (
    id text PRIMARY KEY CHECK (
        id LIKE 'delivery_%'
        AND length(id) BETWEEN 20 AND 96
    ),
    source_event_id text NOT NULL REFERENCES f23_push_events (event_id),
    subscription_id text NOT NULL REFERENCES f23_push_subscriptions (id),
    state text NOT NULL CHECK (
        state IN ('pending', 'sending', 'retry', 'delivered', 'failed')
    ),
    attempt_count integer NOT NULL DEFAULT 0 CHECK (attempt_count >= 0),
    next_attempt_at timestamptz NOT NULL,
    lease_token text,
    locked_until timestamptz,
    created_at timestamptz NOT NULL,
    delivered_at timestamptz,
    failed_at timestamptz,
    UNIQUE (source_event_id, subscription_id),
    CHECK (
        (state = 'sending' AND lease_token IS NOT NULL AND locked_until IS NOT NULL)
        OR
        (state <> 'sending' AND lease_token IS NULL AND locked_until IS NULL)
    ),
    CHECK (
        (state = 'delivered' AND delivered_at IS NOT NULL AND failed_at IS NULL)
        OR
        (state = 'failed' AND failed_at IS NOT NULL AND delivered_at IS NULL)
        OR
        (state NOT IN ('delivered', 'failed') AND delivered_at IS NULL AND failed_at IS NULL)
    )
);

CREATE INDEX f23_active_subscriptions_account_idx
    ON f23_push_subscriptions (account_id, id)
    WHERE revoked_at IS NULL;

CREATE INDEX f23_due_deliveries_idx
    ON f23_push_deliveries (next_attempt_at, id)
    WHERE state IN ('pending', 'sending', 'retry');

COMMENT ON TABLE f23_push_subscriptions IS
    'One opt-in per browser device; endpoint and Web Push keys are encrypted at rest.';
COMMENT ON TABLE f23_push_events IS
    'Idempotency ledger for privacy-minimized, client-safe push payloads.';
COMMENT ON TABLE f23_push_deliveries IS
    'A unique delivery per source event and active device supports fan-out without duplicates.';
