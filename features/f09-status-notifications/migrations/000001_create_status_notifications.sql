CREATE TABLE f09_publication_status_events (
    event_id text PRIMARY KEY CHECK (length(btrim(event_id)) BETWEEN 1 AND 160),
    fingerprint text NOT NULL CHECK (fingerprint ~ '^[0-9a-f]{64}$'),
    event_kind text NOT NULL CHECK (
        event_kind IN ('lifecycle', 'publication')
    ),
    workspace_id text NOT NULL CHECK (length(btrim(workspace_id)) > 0),
    post_id text NOT NULL CHECK (length(btrim(post_id)) > 0),
    destination_id text,
    occurred_at timestamptz NOT NULL,
    received_at timestamptz NOT NULL
);

CREATE TABLE f09_post_status (
    workspace_id text NOT NULL CHECK (length(btrim(workspace_id)) > 0),
    post_id text NOT NULL CHECK (length(btrim(post_id)) > 0),
    draft_id text,
    status text NOT NULL CHECK (
        status IN (
            'draft',
            'scheduled',
            'publishing',
            'published',
            'failed',
            'cancelled'
        )
    ),
    lifecycle_revision bigint NOT NULL DEFAULT 0
        CHECK (lifecycle_revision >= 0),
    last_lifecycle_event_id text,
    last_lifecycle_event_at timestamptz,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    PRIMARY KEY (workspace_id, post_id),
    CHECK (updated_at >= created_at)
);

CREATE TABLE f09_destination_status (
    workspace_id text NOT NULL,
    post_id text NOT NULL,
    destination_id text NOT NULL CHECK (length(btrim(destination_id)) > 0),
    channel_id text NOT NULL CHECK (length(btrim(channel_id)) > 0),
    status text NOT NULL CHECK (
        status IN (
            'draft',
            'scheduled',
            'publishing',
            'published',
            'failed',
            'cancelled'
        )
    ),
    remote_id text,
    diagnostic_code text CHECK (
        diagnostic_code IS NULL
        OR diagnostic_code ~ '^[a-z0-9_-]{1,64}$'
    ),
    diagnostic_message text CHECK (
        diagnostic_message IS NULL
        OR length(diagnostic_message) <= 320
    ),
    diagnostic_retryable boolean NOT NULL DEFAULT false,
    diagnostic_at timestamptz,
    last_event_id text NOT NULL,
    updated_at timestamptz NOT NULL,
    PRIMARY KEY (workspace_id, destination_id),
    FOREIGN KEY (workspace_id, post_id)
        REFERENCES f09_post_status (workspace_id, post_id),
    CHECK (
        (status = 'published' AND remote_id IS NOT NULL)
        OR
        (status <> 'published' AND remote_id IS NULL)
    ),
    CHECK (
        (status = 'failed' AND diagnostic_code IS NOT NULL)
        OR
        (
            status <> 'failed'
            AND diagnostic_code IS NULL
            AND diagnostic_message IS NULL
            AND diagnostic_at IS NULL
        )
    )
);

CREATE TABLE f09_notification_outbox (
    id text PRIMARY KEY CHECK (
        id LIKE 'notification_%'
        AND length(id) BETWEEN 20 AND 96
    ),
    source_event_id text NOT NULL CHECK (length(btrim(source_event_id)) > 0),
    kind text NOT NULL CHECK (
        kind IN (
            'welcome',
            'plan_changed',
            'publication_failed',
            'security_alert'
        )
    ),
    account_id text,
    workspace_id text,
    post_id text,
    destination_id text,
    subject text NOT NULL DEFAULT '',
    detail text NOT NULL DEFAULT '',
    action_label text NOT NULL DEFAULT '',
    action_url text NOT NULL DEFAULT '',
    idempotency_key text NOT NULL UNIQUE
        CHECK (length(idempotency_key) BETWEEN 1 AND 255),
    state text NOT NULL CHECK (
        state IN ('pending', 'sending', 'retry', 'delivered')
    ),
    attempt_count integer NOT NULL DEFAULT 0 CHECK (attempt_count >= 0),
    next_attempt_at timestamptz NOT NULL,
    lease_token text,
    locked_until timestamptz,
    created_at timestamptz NOT NULL,
    delivered_at timestamptz,
    UNIQUE (kind, source_event_id),
    CHECK (
        (state = 'sending' AND lease_token IS NOT NULL AND locked_until IS NOT NULL)
        OR
        (state <> 'sending' AND lease_token IS NULL AND locked_until IS NULL)
    ),
    CHECK (
        (state = 'delivered' AND delivered_at IS NOT NULL)
        OR
        (state <> 'delivered' AND delivered_at IS NULL)
    ),
    CHECK (
        (kind IN ('welcome', 'security_alert') AND account_id IS NOT NULL)
        OR
        (kind IN ('plan_changed', 'publication_failed') AND workspace_id IS NOT NULL)
    )
);

CREATE TABLE f09_manual_retry_outbox (
    id text PRIMARY KEY CHECK (
        id LIKE 'manual_retry_%'
        AND length(id) BETWEEN 20 AND 96
    ),
    workspace_id text NOT NULL CHECK (length(btrim(workspace_id)) > 0),
    post_id text NOT NULL CHECK (length(btrim(post_id)) > 0),
    destination_id text NOT NULL CHECK (length(btrim(destination_id)) > 0),
    failure_event_id text NOT NULL CHECK (length(btrim(failure_event_id)) > 0),
    actor_id text NOT NULL CHECK (length(btrim(actor_id)) > 0),
    idempotency_key text NOT NULL
        CHECK (length(idempotency_key) BETWEEN 1 AND 255),
    state text NOT NULL CHECK (
        state IN ('pending', 'sending', 'retry', 'delivered')
    ),
    attempt_count integer NOT NULL DEFAULT 0 CHECK (attempt_count >= 0),
    next_attempt_at timestamptz NOT NULL,
    lease_token text,
    locked_until timestamptz,
    created_at timestamptz NOT NULL,
    delivered_at timestamptz,
    UNIQUE (workspace_id, idempotency_key),
    UNIQUE (workspace_id, destination_id, failure_event_id),
    FOREIGN KEY (workspace_id, destination_id)
        REFERENCES f09_destination_status (workspace_id, destination_id),
    CHECK (
        (state = 'sending' AND lease_token IS NOT NULL AND locked_until IS NOT NULL)
        OR
        (state <> 'sending' AND lease_token IS NULL AND locked_until IS NULL)
    ),
    CHECK (
        (state = 'delivered' AND delivered_at IS NOT NULL)
        OR
        (state <> 'delivered' AND delivered_at IS NULL)
    )
);

CREATE INDEX f09_status_events_post_idx
    ON f09_publication_status_events (workspace_id, post_id, occurred_at);

CREATE INDEX f09_destination_status_post_idx
    ON f09_destination_status (workspace_id, post_id, destination_id);

CREATE INDEX f09_notification_due_idx
    ON f09_notification_outbox (next_attempt_at, id)
    WHERE state IN ('pending', 'sending', 'retry');

CREATE INDEX f09_manual_retry_due_idx
    ON f09_manual_retry_outbox (next_attempt_at, id)
    WHERE state IN ('pending', 'sending', 'retry');

COMMENT ON TABLE f09_publication_status_events IS
    'Idempotency ledger; an event ID cannot be reused with another fingerprint.';
COMMENT ON COLUMN f09_destination_status.diagnostic_message IS
    'Client-safe, redacted detail only; raw provider errors are never stored.';
COMMENT ON TABLE f09_manual_retry_outbox IS
    'One retry command per destination failure cycle, independent of client key.';
