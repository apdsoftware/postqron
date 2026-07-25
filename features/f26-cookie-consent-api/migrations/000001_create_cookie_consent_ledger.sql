CREATE TABLE f26_cookie_subjects (
    subject_key text PRIMARY KEY,
    subject_kind text NOT NULL CHECK (
        subject_kind IN ('pseudonymous_browser', 'authenticated_account')
    ),
    external_subject_id text NOT NULL,
    created_at timestamptz NOT NULL,
    UNIQUE (subject_kind, external_subject_id),
    CHECK (length(external_subject_id) BETWEEN 8 AND 200)
);

CREATE TABLE f26_cookie_preferences (
    subject_key text PRIMARY KEY REFERENCES f26_cookie_subjects(subject_key) ON DELETE CASCADE,
    necessary boolean NOT NULL DEFAULT true CHECK (necessary),
    preferences boolean NOT NULL DEFAULT false,
    analytics boolean NOT NULL DEFAULT false,
    marketing boolean NOT NULL DEFAULT false,
    policy_version text NOT NULL CHECK (
        policy_version ~ '^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$'
    ),
    policy_digest_sha256 char(64) NOT NULL CHECK (
        policy_digest_sha256 ~ '^[a-f0-9]{64}$'
    ),
    source text NOT NULL CHECK (
        source IN ('banner', 'preferences_center', 'account')
    ),
    selected_at timestamptz NOT NULL,
    expires_at timestamptz NOT NULL,
    revision bigint NOT NULL CHECK (revision > 0),
    CHECK (expires_at > selected_at),
    CHECK (expires_at <= selected_at + interval '6 months')
);

CREATE TABLE f26_cookie_consent_events (
    event_id char(64) PRIMARY KEY CHECK (event_id ~ '^[a-f0-9]{64}$'),
    subject_key text NOT NULL,
    category text NOT NULL CHECK (
        category IN ('preferences', 'analytics', 'marketing')
    ),
    action text NOT NULL CHECK (
        action IN ('granted', 'rejected', 'withdrawn')
    ),
    enabled boolean NOT NULL,
    policy_version text NOT NULL CHECK (
        policy_version ~ '^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$'
    ),
    policy_digest_sha256 char(64) NOT NULL CHECK (
        policy_digest_sha256 ~ '^[a-f0-9]{64}$'
    ),
    occurred_at timestamptz NOT NULL,
    source text NOT NULL CHECK (
        source IN ('banner', 'preferences_center', 'account')
    ),
    idempotency_key text NOT NULL,
    preference_revision bigint NOT NULL CHECK (preference_revision > 0),
    retention_until timestamptz NOT NULL,
    CHECK (retention_until > occurred_at),
    CHECK (
        (action = 'granted' AND enabled)
        OR (action IN ('rejected', 'withdrawn') AND NOT enabled)
    ),
    UNIQUE (subject_key, idempotency_key, category)
);

CREATE INDEX f26_cookie_consent_events_subject_time_idx
ON f26_cookie_consent_events (subject_key, occurred_at DESC);

CREATE INDEX f26_cookie_consent_events_retention_idx
ON f26_cookie_consent_events (retention_until);

CREATE TABLE f26_cookie_idempotency (
    subject_key text NOT NULL REFERENCES f26_cookie_subjects(subject_key) ON DELETE CASCADE,
    idempotency_key text NOT NULL,
    request_fingerprint char(64) NOT NULL CHECK (
        request_fingerprint ~ '^[a-f0-9]{64}$'
    ),
    response jsonb NOT NULL CHECK (jsonb_typeof(response) = 'object'),
    created_at timestamptz NOT NULL,
    PRIMARY KEY (subject_key, idempotency_key)
);

CREATE FUNCTION f26_guard_cookie_evidence()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF TG_OP = 'UPDATE' THEN
        RAISE EXCEPTION 'f26_cookie_consent_events is append-only';
    END IF;
    IF OLD.retention_until > now() THEN
        RAISE EXCEPTION 'cookie evidence retention has not elapsed';
    END IF;
    RETURN OLD;
END;
$$;

CREATE TRIGGER f26_cookie_evidence_retention_guard
BEFORE UPDATE OR DELETE ON f26_cookie_consent_events
FOR EACH ROW EXECUTE FUNCTION f26_guard_cookie_evidence();
