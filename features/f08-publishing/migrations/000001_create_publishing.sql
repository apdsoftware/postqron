CREATE TABLE f08_publication_jobs (
    id text PRIMARY KEY CHECK (
        id LIKE 'pubjob_%'
        AND length(id) BETWEEN 8 AND 96
    ),
    command_id text NOT NULL UNIQUE CHECK (length(btrim(command_id)) > 0),
    workspace_id text NOT NULL CHECK (length(btrim(workspace_id)) > 0),
    post_id text NOT NULL CHECK (length(btrim(post_id)) > 0),
    draft_id text NOT NULL CHECK (length(btrim(draft_id)) > 0),
    generation bigint NOT NULL CHECK (generation > 0),
    invalidation_key text NOT NULL UNIQUE
        CHECK (length(btrim(invalidation_key)) > 0),
    status text NOT NULL CHECK (
        status IN (
            'queued',
            'publishing',
            'published',
            'partially_failed',
            'failed',
            'cancelled'
        )
    ),
    execute_at_utc timestamptz NOT NULL,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    CHECK (updated_at >= created_at)
);

CREATE TABLE f08_publication_destinations (
    id text PRIMARY KEY CHECK (
        id LIKE 'pubdst_%'
        AND length(id) BETWEEN 8 AND 96
    ),
    job_id text NOT NULL REFERENCES f08_publication_jobs (id),
    command_id text NOT NULL CHECK (length(btrim(command_id)) > 0),
    workspace_id text NOT NULL CHECK (length(btrim(workspace_id)) > 0),
    post_id text NOT NULL CHECK (length(btrim(post_id)) > 0),
    generation bigint NOT NULL CHECK (generation > 0),
    channel_id text NOT NULL CHECK (length(btrim(channel_id)) > 0),
    provider text NOT NULL CHECK (provider ~ '^[a-z][a-z0-9_-]{0,63}$'),
    connection_id text NOT NULL CHECK (length(btrim(connection_id)) > 0),
    payload jsonb NOT NULL CHECK (jsonb_typeof(payload) = 'object'),
    idempotency_key text NOT NULL UNIQUE CHECK (
        idempotency_key LIKE 'publish_%'
        AND length(idempotency_key) = 72
    ),
    status text NOT NULL CHECK (
        status IN (
            'pending',
            'publishing',
            'retry_wait',
            'published',
            'dead_letter',
            'cancelled'
        )
    ),
    attempt_count integer NOT NULL DEFAULT 0 CHECK (attempt_count >= 0),
    cycle_attempt_count integer NOT NULL DEFAULT 0
        CHECK (cycle_attempt_count >= 0),
    max_attempts integer NOT NULL CHECK (max_attempts > 0),
    next_attempt_at timestamptz NOT NULL,
    lease_token text,
    locked_until timestamptz,
    remote_id text,
    error_code text,
    error_detail text,
    error_retryable boolean NOT NULL DEFAULT false,
    error_at timestamptz,
    published_at timestamptz,
    dead_lettered_at timestamptz,
    cancelled_at timestamptz,
    manual_retry_count integer NOT NULL DEFAULT 0
        CHECK (manual_retry_count >= 0),
    UNIQUE (job_id, channel_id),
    CHECK (
        (status = 'publishing' AND lease_token IS NOT NULL AND locked_until IS NOT NULL)
        OR
        (status <> 'publishing' AND lease_token IS NULL AND locked_until IS NULL)
    ),
    CHECK (
        (status = 'published' AND remote_id IS NOT NULL AND published_at IS NOT NULL)
        OR
        (status <> 'published' AND remote_id IS NULL AND published_at IS NULL)
    ),
    CHECK (
        (status = 'dead_letter' AND dead_lettered_at IS NOT NULL)
        OR
        (status <> 'dead_letter' AND dead_lettered_at IS NULL)
    ),
    CHECK (
        (status = 'cancelled' AND cancelled_at IS NOT NULL)
        OR
        (status <> 'cancelled' AND cancelled_at IS NULL)
    )
);

CREATE TABLE f08_publication_attempts (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    destination_id text NOT NULL
        REFERENCES f08_publication_destinations (id),
    attempt_number integer NOT NULL CHECK (attempt_number > 0),
    lease_token text NOT NULL UNIQUE,
    started_at timestamptz NOT NULL,
    completed_at timestamptz,
    outcome text NOT NULL CHECK (
        outcome IN (
            'in_progress',
            'published',
            'retry',
            'dead_letter',
            'cancelled'
        )
    ),
    error_code text,
    error_detail text,
    remote_id text,
    UNIQUE (destination_id, attempt_number)
);

CREATE TABLE f08_publication_dead_letters (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    destination_id text NOT NULL
        REFERENCES f08_publication_destinations (id),
    job_id text NOT NULL REFERENCES f08_publication_jobs (id),
    diagnostic_code text NOT NULL CHECK (length(btrim(diagnostic_code)) > 0),
    diagnostic_detail text NOT NULL,
    failed_at timestamptz NOT NULL,
    resolved_at timestamptz,
    retried_by_account_id text,
    CHECK (
        (resolved_at IS NULL AND retried_by_account_id IS NULL)
        OR
        (resolved_at IS NOT NULL AND retried_by_account_id IS NOT NULL)
    )
);

CREATE UNIQUE INDEX f08_dead_letters_one_open_idx
    ON f08_publication_dead_letters (destination_id)
    WHERE resolved_at IS NULL;

CREATE UNIQUE INDEX f08_destinations_remote_id_idx
    ON f08_publication_destinations (provider, connection_id, remote_id)
    WHERE remote_id IS NOT NULL;

CREATE INDEX f08_destinations_due_idx
    ON f08_publication_destinations (next_attempt_at, id)
    WHERE status IN ('pending', 'retry_wait', 'publishing');

CREATE INDEX f08_destinations_job_idx
    ON f08_publication_destinations (job_id, id);

CREATE INDEX f08_attempts_destination_idx
    ON f08_publication_attempts (destination_id, attempt_number);

COMMENT ON COLUMN f08_publication_destinations.idempotency_key IS
    'Stable key required at the provider boundary; replays must return the same remote publication.';
COMMENT ON COLUMN f08_publication_destinations.locked_until IS
    'Persistent worker lease. Expired publishing rows are safely reclaimable.';
COMMENT ON TABLE f08_publication_dead_letters IS
    'Terminal publication failures. Manual retry resolves the open row and requeues the destination.';
