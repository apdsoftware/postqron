CREATE TABLE f34_prelaunch_access_requests (
    id TEXT PRIMARY KEY,
    email TEXT NOT NULL,
    email_hash CHAR(64) NOT NULL UNIQUE,
    locale TEXT NOT NULL
        CHECK (locale IN ('en', 'it', 'es', 'fr', 'de')),
    consent_proof JSONB NOT NULL,
    marketing_consent BOOLEAN NOT NULL DEFAULT FALSE
        CHECK (marketing_consent = FALSE),
    requested_at TIMESTAMPTZ NOT NULL,
    CONSTRAINT f34_prelaunch_email_length
        CHECK (char_length(email) BETWEEN 3 AND 254),
    CONSTRAINT f34_prelaunch_consent_shape
        CHECK (
            consent_proof ->> 'policy_version' = 'prelaunch-access-v1'
            AND (consent_proof ->> 'access_consent')::BOOLEAN = TRUE
            AND (consent_proof ->> 'marketing_consent')::BOOLEAN = FALSE
            AND consent_proof ->> 'collection_point' =
                'prelaunch_access_form'
        )
);

CREATE TABLE f34_prelaunch_email_outbox (
    id TEXT PRIMARY KEY,
    request_id TEXT NOT NULL UNIQUE
        REFERENCES f34_prelaunch_access_requests(id) ON DELETE CASCADE,
    event_name TEXT NOT NULL
        CHECK (event_name = 'f14.prelaunch_access.v1'),
    channel TEXT NOT NULL
        CHECK (channel = 'transactional'),
    template_id TEXT NOT NULL
        CHECK (template_id = 'prelaunch_access'),
    idempotency_key TEXT NOT NULL UNIQUE,
    command JSONB NOT NULL,
    occurred_at TIMESTAMPTZ NOT NULL,
    published_at TIMESTAMPTZ,
    CONSTRAINT f34_prelaunch_command_separation
        CHECK (
            command ->> 'event' = 'f14.prelaunch_access.v1'
            AND command ->> 'channel' = 'transactional'
            AND command ->> 'template_id' = 'prelaunch_access'
            AND NOT (command ? 'marketing_consent')
        )
);

CREATE TABLE f34_prelaunch_rate_limits (
    key_hash CHAR(64) NOT NULL,
    window_started_at TIMESTAMPTZ NOT NULL,
    request_count INTEGER NOT NULL
        CHECK (request_count > 0),
    PRIMARY KEY (key_hash, window_started_at)
);

CREATE INDEX f34_prelaunch_outbox_pending_idx
    ON f34_prelaunch_email_outbox (occurred_at)
    WHERE published_at IS NULL;

CREATE INDEX f34_prelaunch_rate_limits_window_idx
    ON f34_prelaunch_rate_limits (window_started_at);
