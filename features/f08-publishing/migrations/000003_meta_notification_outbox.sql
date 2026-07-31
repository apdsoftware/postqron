CREATE TABLE f08_meta_notification_outbox (
    id text PRIMARY KEY CHECK (
        id ~ '^meta_notification_[0-9a-f]{32}$'
    ),
    provider text NOT NULL CHECK (
        provider IN ('facebook_groups', 'instagram_personal')
    ),
    workspace_id text NOT NULL CHECK (length(btrim(workspace_id)) > 0),
    post_id text NOT NULL CHECK (length(btrim(post_id)) > 0),
    channel_id text NOT NULL CHECK (length(btrim(channel_id)) > 0),
    recipient_id text NOT NULL CHECK (length(btrim(recipient_id)) > 0),
    idempotency_key text NOT NULL CHECK (
        length(btrim(idempotency_key)) BETWEEN 1 AND 255
    ),
    payload jsonb NOT NULL,
    payload_fingerprint text NOT NULL CHECK (
        payload_fingerprint ~ '^[0-9a-f]{64}$'
    ),
    state text NOT NULL CHECK (
        state IN ('pending', 'sending', 'retry', 'delivered')
    ),
    attempt_count integer NOT NULL DEFAULT 0 CHECK (attempt_count >= 0),
    next_attempt_at timestamptz NOT NULL,
    lease_token text,
    locked_until timestamptz,
    last_error_code text CHECK (
        last_error_code IS NULL
        OR last_error_code ~ '^[a-z0-9_-]{1,64}$'
    ),
    created_at timestamptz NOT NULL,
    delivered_at timestamptz,
    UNIQUE (provider, idempotency_key),
    CHECK (
        (state = 'delivered' AND delivered_at IS NOT NULL)
        OR
        (state <> 'delivered' AND delivered_at IS NULL)
    ),
    CHECK (
        (state = 'sending' AND lease_token IS NOT NULL AND locked_until IS NOT NULL)
        OR
        (state <> 'sending' AND lease_token IS NULL AND locked_until IS NULL)
    )
);

CREATE INDEX f08_meta_notification_pending_idx
    ON f08_meta_notification_outbox (next_attempt_at, id)
    WHERE state IN ('pending', 'retry', 'sending');

COMMENT ON TABLE f08_meta_notification_outbox IS
    'Durable, idempotent user notification requests for Meta destinations that forbid automatic publishing.';
