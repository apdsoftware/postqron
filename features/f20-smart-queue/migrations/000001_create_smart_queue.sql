CREATE TABLE f20_smart_queues (
    id text PRIMARY KEY CHECK (id LIKE 'queue_%' AND length(id) BETWEEN 8 AND 96),
    workspace_id text NOT NULL CHECK (length(btrim(workspace_id)) > 0),
    name text NOT NULL CHECK (length(btrim(name)) BETWEEN 1 AND 120),
    time_zone text NOT NULL CHECK (
        length(btrim(time_zone)) > 0 AND time_zone <> 'Local'
    ),
    interval_minutes smallint NOT NULL CHECK (interval_minutes BETWEEN 5 AND 1440),
    horizon_days smallint NOT NULL CHECK (horizon_days BETWEEN 1 AND 366),
    windows jsonb NOT NULL CHECK (
        jsonb_typeof(windows) = 'array' AND jsonb_array_length(windows) BETWEEN 1 AND 64
    ),
    revision bigint NOT NULL CHECK (revision > 0),
    created_by_account_id text NOT NULL CHECK (length(btrim(created_by_account_id)) > 0),
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL CHECK (updated_at >= created_at)
);

CREATE INDEX f20_smart_queues_workspace_idx
    ON f20_smart_queues (workspace_id, id);

CREATE TABLE f20_slot_previews (
    token text PRIMARY KEY CHECK (
        token LIKE 'preview_%' AND length(token) BETWEEN 10 AND 128
    ),
    workspace_id text NOT NULL CHECK (length(btrim(workspace_id)) > 0),
    queue_id text NOT NULL REFERENCES f20_smart_queues (id),
    queue_revision bigint NOT NULL CHECK (queue_revision > 0),
    starts_at_utc timestamptz NOT NULL,
    local_date_time text NOT NULL CHECK (
        local_date_time ~ '^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}$'
    ),
    time_zone text NOT NULL CHECK (length(btrim(time_zone)) > 0),
    utc_offset_minutes smallint NOT NULL CHECK (
        utc_offset_minutes BETWEEN -1080 AND 1080
    ),
    not_before_utc timestamptz NOT NULL,
    search_until_utc timestamptz NOT NULL,
    created_at timestamptz NOT NULL,
    expires_at timestamptz NOT NULL,
    confirmed_at timestamptz,
    reservation_id text,
    idempotency_key text,
    confirmation_hash text,
    CHECK (search_until_utc >= not_before_utc),
    CHECK (expires_at > created_at),
    CHECK (
        (confirmed_at IS NULL AND reservation_id IS NULL
            AND idempotency_key IS NULL AND confirmation_hash IS NULL)
        OR
        (confirmed_at IS NOT NULL AND reservation_id IS NOT NULL
            AND idempotency_key IS NOT NULL AND confirmation_hash IS NOT NULL)
    )
);

CREATE INDEX f20_slot_previews_expiry_idx
    ON f20_slot_previews (expires_at)
    WHERE confirmed_at IS NULL;

CREATE TABLE f20_slot_reservations (
    id text PRIMARY KEY CHECK (
        id LIKE 'reservation_%' AND length(id) BETWEEN 14 AND 128
    ),
    workspace_id text NOT NULL CHECK (length(btrim(workspace_id)) > 0),
    queue_id text NOT NULL REFERENCES f20_smart_queues (id),
    draft_id text NOT NULL CHECK (length(btrim(draft_id)) > 0),
    channel_ids text[] NOT NULL CHECK (
        cardinality(channel_ids) > 0 AND array_position(channel_ids, NULL) IS NULL
    ),
    starts_at_utc timestamptz NOT NULL,
    local_date_time text NOT NULL,
    time_zone text NOT NULL CHECK (length(btrim(time_zone)) > 0),
    utc_offset_minutes smallint NOT NULL CHECK (
        utc_offset_minutes BETWEEN -1080 AND 1080
    ),
    idempotency_key text NOT NULL CHECK (length(idempotency_key) BETWEEN 1 AND 200),
    confirmation_hash text NOT NULL CHECK (length(confirmation_hash) = 64),
    created_by_account_id text NOT NULL CHECK (length(btrim(created_by_account_id)) > 0),
    created_at timestamptz NOT NULL,
    UNIQUE (queue_id, starts_at_utc),
    UNIQUE (workspace_id, idempotency_key)
);

CREATE INDEX f20_slot_reservations_search_idx
    ON f20_slot_reservations (workspace_id, queue_id, starts_at_utc, id);

ALTER TABLE f20_slot_previews
    ADD CONSTRAINT f20_slot_previews_reservation_fk
    FOREIGN KEY (reservation_id) REFERENCES f20_slot_reservations (id);

CREATE TABLE f20_scheduling_commands (
    id text PRIMARY KEY CHECK (
        id LIKE 'queuecmd_%' AND length(id) BETWEEN 10 AND 128
    ),
    reservation_id text NOT NULL UNIQUE REFERENCES f20_slot_reservations (id),
    workspace_id text NOT NULL CHECK (length(btrim(workspace_id)) > 0),
    draft_id text NOT NULL CHECK (length(btrim(draft_id)) > 0),
    channel_ids text[] NOT NULL CHECK (
        cardinality(channel_ids) > 0 AND array_position(channel_ids, NULL) IS NULL
    ),
    starts_at_utc timestamptz NOT NULL,
    local_date_time text NOT NULL,
    time_zone text NOT NULL CHECK (length(btrim(time_zone)) > 0),
    utc_offset_minutes smallint NOT NULL CHECK (
        utc_offset_minutes BETWEEN -1080 AND 1080
    ),
    state text NOT NULL CHECK (state IN ('pending', 'sent')),
    idempotency_key text NOT NULL UNIQUE,
    created_at timestamptz NOT NULL,
    sent_at timestamptz,
    CHECK (
        (state = 'pending' AND sent_at IS NULL)
        OR (state = 'sent' AND sent_at IS NOT NULL)
    )
);

CREATE INDEX f20_scheduling_commands_pending_idx
    ON f20_scheduling_commands (created_at, id)
    WHERE state = 'pending';

COMMENT ON TABLE f20_scheduling_commands IS
    'Transactional outbox consumed by the trusted F7 scheduling adapter.';
