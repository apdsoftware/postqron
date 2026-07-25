CREATE TABLE f31_admin_records (
    account_id text PRIMARY KEY REFERENCES auth_accounts(id),
    email text NOT NULL,
    active boolean NOT NULL,
    updated_at timestamptz NOT NULL,
    CHECK (email = lower(btrim(email))),
    CHECK (email <> '')
);

CREATE UNIQUE INDEX f31_admin_records_active_email_idx
    ON f31_admin_records (email)
    WHERE active;

CREATE TABLE f31_admin_audit_events (
    id text PRIMARY KEY,
    code text NOT NULL,
    actor_id text NOT NULL,
    subject_id text NOT NULL,
    reason text NOT NULL,
    outcome text NOT NULL,
    correlation_id text NOT NULL,
    occurred_at timestamptz NOT NULL,
    CHECK (length(id) BETWEEN 8 AND 128),
    CHECK (length(code) BETWEEN 1 AND 128),
    CHECK (length(actor_id) BETWEEN 1 AND 128),
    CHECK (length(subject_id) BETWEEN 1 AND 128),
    CHECK (length(reason) BETWEEN 1 AND 500),
    CHECK (length(correlation_id) BETWEEN 8 AND 128)
);

CREATE INDEX f31_admin_audit_events_time_idx
    ON f31_admin_audit_events (occurred_at DESC);

CREATE TABLE f31_admin_idempotency (
    key text PRIMARY KEY,
    result_code text NOT NULL,
    correlation_id text NOT NULL,
    created_at timestamptz NOT NULL,
    CHECK (length(key) BETWEEN 8 AND 512),
    CHECK (length(result_code) BETWEEN 1 AND 128),
    CHECK (length(correlation_id) BETWEEN 8 AND 128)
);

CREATE FUNCTION f31_reject_admin_audit_mutation()
RETURNS trigger
LANGUAGE plpgsql
AS $function$
BEGIN
    RAISE EXCEPTION 'admin audit events are append-only'
        USING ERRCODE = '55000';
END
$function$;

CREATE TRIGGER f31_admin_audit_events_append_only
    BEFORE UPDATE OR DELETE ON f31_admin_audit_events
    FOR EACH ROW
    EXECUTE FUNCTION f31_reject_admin_audit_mutation();

ALTER TABLE f31_admin_audit_events
    ENABLE ALWAYS TRIGGER f31_admin_audit_events_append_only;

REVOKE UPDATE, DELETE, TRUNCATE ON f31_admin_audit_events FROM PUBLIC;
