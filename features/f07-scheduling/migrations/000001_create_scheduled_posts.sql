CREATE TABLE f07_scheduled_posts (
    id text PRIMARY KEY CHECK (
        id LIKE 'post_%'
        AND length(id) BETWEEN 6 AND 96
    ),
    workspace_id text NOT NULL CHECK (length(btrim(workspace_id)) > 0),
    draft_id text NOT NULL CHECK (length(btrim(draft_id)) > 0),
    channel_ids text[] NOT NULL CHECK (
        cardinality(channel_ids) > 0
        AND array_position(channel_ids, NULL) IS NULL
    ),
    status text NOT NULL CHECK (
        status IN ('scheduled', 'publishing', 'published', 'failed', 'cancelled')
    ),
    scheduled_for_utc timestamptz NOT NULL,
    scheduled_local text NOT NULL CHECK (
        scheduled_local ~ '^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}$'
    ),
    scheduled_timezone text NOT NULL
        CHECK (length(btrim(scheduled_timezone)) > 0),
    scheduled_utc_offset_minutes smallint NOT NULL CHECK (
        scheduled_utc_offset_minutes BETWEEN -1080 AND 1080
    ),
    revision bigint NOT NULL DEFAULT 1 CHECK (revision > 0),
    active_command_id text,
    duplicated_from_post_id text REFERENCES f07_scheduled_posts (id),
    created_by_account_id text NOT NULL
        CHECK (length(btrim(created_by_account_id)) > 0),
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    cancelled_at timestamptz,
    CHECK (updated_at >= created_at),
    CHECK (
        (status = 'cancelled' AND active_command_id IS NULL AND cancelled_at IS NOT NULL)
        OR
        (status <> 'cancelled' AND cancelled_at IS NULL)
    )
);

CREATE TABLE f07_publication_commands (
    id text PRIMARY KEY CHECK (
        id LIKE 'pubcmd_%'
        AND length(id) BETWEEN 8 AND 96
    ),
    workspace_id text NOT NULL CHECK (length(btrim(workspace_id)) > 0),
    post_id text NOT NULL REFERENCES f07_scheduled_posts (id),
    draft_id text NOT NULL CHECK (length(btrim(draft_id)) > 0),
    channel_ids text[] NOT NULL CHECK (
        cardinality(channel_ids) > 0
        AND array_position(channel_ids, NULL) IS NULL
    ),
    generation bigint NOT NULL CHECK (generation > 0),
    execute_at_utc timestamptz NOT NULL,
    state text NOT NULL CHECK (state IN ('pending', 'invalidated')),
    invalidation_key text NOT NULL UNIQUE,
    created_at timestamptz NOT NULL,
    invalidated_at timestamptz,
    UNIQUE (post_id, generation),
    CHECK (
        (state = 'pending' AND invalidated_at IS NULL)
        OR
        (state = 'invalidated' AND invalidated_at IS NOT NULL)
    )
);

CREATE UNIQUE INDEX f07_publication_commands_one_pending_idx
    ON f07_publication_commands (post_id)
    WHERE state = 'pending';

CREATE INDEX f07_scheduled_posts_calendar_idx
    ON f07_scheduled_posts (
        workspace_id,
        scheduled_for_utc,
        status,
        id
    );

CREATE INDEX f07_scheduled_posts_channels_idx
    ON f07_scheduled_posts USING gin (channel_ids);

CREATE INDEX f07_publication_commands_due_idx
    ON f07_publication_commands (execute_at_utc, id)
    WHERE state = 'pending';

COMMENT ON COLUMN f07_scheduled_posts.scheduled_for_utc IS
    'Canonical publication instant; scheduled_local/timezone/offset preserve the user choice.';
COMMENT ON COLUMN f07_publication_commands.invalidation_key IS
    'Stable post:generation idempotency key consumed by the publishing worker.';
