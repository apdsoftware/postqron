CREATE TABLE f18_analytics_targets (
    id text PRIMARY KEY CHECK (
        id LIKE 'anltgt_%'
        AND length(id) = 71
    ),
    workspace_id text NOT NULL CHECK (length(btrim(workspace_id)) > 0),
    content_id text NOT NULL CHECK (length(btrim(content_id)) > 0),
    channel_id text NOT NULL CHECK (length(btrim(channel_id)) > 0),
    channel_type text NOT NULL CHECK (
        channel_type IN ('facebook_page', 'instagram_professional')
    ),
    provider text NOT NULL CHECK (provider = 'meta'),
    connection_id text NOT NULL CHECK (length(btrim(connection_id)) > 0),
    remote_id text NOT NULL CHECK (length(btrim(remote_id)) > 0),
    published_at timestamptz NOT NULL,
    cursor text NOT NULL DEFAULT '',
    state text NOT NULL CHECK (
        state IN (
            'pending',
            'syncing',
            'retry_wait',
            'current',
            'unavailable',
            'permission_missing',
            'failed'
        )
    ),
    attempt_count integer NOT NULL DEFAULT 0 CHECK (attempt_count >= 0),
    consecutive_failures integer NOT NULL DEFAULT 0
        CHECK (consecutive_failures >= 0),
    next_sync_at timestamptz NOT NULL,
    lease_token text,
    locked_until timestamptz,
    last_error_code text,
    last_error_at timestamptz,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    UNIQUE (provider, connection_id, remote_id),
    CHECK (
        (state = 'syncing' AND lease_token IS NOT NULL AND locked_until IS NOT NULL)
        OR
        (state <> 'syncing' AND lease_token IS NULL AND locked_until IS NULL)
    ),
    CHECK (
        (last_error_code IS NULL AND last_error_at IS NULL)
        OR
        (last_error_code IS NOT NULL AND last_error_at IS NOT NULL)
    ),
    CHECK (updated_at >= created_at)
);

CREATE TABLE f18_analytics_observations (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    target_id text NOT NULL REFERENCES f18_analytics_targets (id),
    metric text NOT NULL CHECK (
        metric IN (
            'reach',
            'likes',
            'comments',
            'shares',
            'saved',
            'views',
            'plays'
        )
    ),
    original_name text NOT NULL CHECK (length(btrim(original_name)) > 0),
    period text NOT NULL CHECK (length(btrim(period)) > 0),
    observed_at timestamptz NOT NULL,
    value bigint,
    state text NOT NULL CHECK (
        state IN (
            'available',
            'unavailable',
            'permission_missing',
            'failed'
        )
    ),
    api_version text NOT NULL DEFAULT '',
    reason_code text NOT NULL DEFAULT '',
    UNIQUE (target_id, metric, original_name, period, observed_at),
    CHECK (
        (state = 'available' AND value IS NOT NULL AND value >= 0)
        OR
        (state <> 'available' AND value IS NULL)
    ),
    CHECK (
        (
            state IN ('available', 'unavailable')
            AND length(btrim(api_version)) > 0
            AND reason_code = ''
        )
        OR
        (
            state IN ('permission_missing', 'failed')
            AND api_version = ''
            AND length(btrim(reason_code)) > 0
        )
    )
);

CREATE INDEX f18_analytics_due_idx
    ON f18_analytics_targets (next_sync_at, id)
    WHERE state <> 'failed';

CREATE INDEX f18_analytics_overview_idx
    ON f18_analytics_targets (workspace_id, published_at, channel_id);

CREATE INDEX f18_analytics_latest_observation_idx
    ON f18_analytics_observations (target_id, metric, observed_at DESC);

COMMENT ON COLUMN f18_analytics_targets.cursor IS
    'Opaque provider cursor persisted after every successful incremental batch.';
COMMENT ON COLUMN f18_analytics_observations.value IS
    'Nullable by design: available zero is 0; unavailable, missing permission and failure are NULL.';
COMMENT ON COLUMN f18_analytics_observations.original_name IS
    'Version-sensitive provider metric name retained alongside the normalized metric.';
