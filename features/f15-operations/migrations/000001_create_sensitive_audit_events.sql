CREATE SCHEMA IF NOT EXISTS operations;

CREATE TABLE operations.sensitive_audit_events (
    event_id text PRIMARY KEY,
    occurred_at timestamptz NOT NULL,
    actor_type text NOT NULL,
    actor_id text NOT NULL,
    workspace_id text,
    action text NOT NULL,
    target_type text NOT NULL,
    target_id text NOT NULL,
    outcome text NOT NULL CHECK (outcome IN ('attempted', 'denied', 'failed', 'succeeded')),
    correlation_id text NOT NULL,
    source_ip_hash text,
    inserted_at timestamptz NOT NULL DEFAULT now(),
    CHECK (length(event_id) BETWEEN 1 AND 128),
    CHECK (length(actor_id) BETWEEN 1 AND 128),
    CHECK (workspace_id IS NULL OR length(workspace_id) BETWEEN 1 AND 128),
    CHECK (length(target_id) BETWEEN 1 AND 128),
    CHECK (length(correlation_id) BETWEEN 1 AND 128),
    CHECK (source_ip_hash IS NULL OR source_ip_hash ~ '^sha256:[0-9a-f]{64}$')
);

CREATE INDEX sensitive_audit_events_occurred_at_idx
    ON operations.sensitive_audit_events (occurred_at);

CREATE INDEX sensitive_audit_events_workspace_time_idx
    ON operations.sensitive_audit_events (workspace_id, occurred_at DESC)
    WHERE workspace_id IS NOT NULL;

CREATE FUNCTION operations.reject_sensitive_audit_mutation()
RETURNS trigger
LANGUAGE plpgsql
AS $function$
BEGIN
    IF TG_OP = 'DELETE'
       AND current_setting('postqron.audit_retention_purge', true) = 'enabled' THEN
        RETURN OLD;
    END IF;
    RAISE EXCEPTION 'sensitive audit events are append-only';
END;
$function$;

CREATE TRIGGER sensitive_audit_events_append_only
    BEFORE UPDATE OR DELETE ON operations.sensitive_audit_events
    FOR EACH ROW
    EXECUTE FUNCTION operations.reject_sensitive_audit_mutation();

ALTER TABLE operations.sensitive_audit_events
    ENABLE ALWAYS TRIGGER sensitive_audit_events_append_only;

CREATE FUNCTION operations.purge_expired_sensitive_audit_events(cutoff timestamptz)
RETURNS bigint
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, operations
AS $function$
DECLARE
    deleted_count bigint;
    previous_purge_setting text;
BEGIN
    IF cutoff > now() - INTERVAL '12 months' THEN
        RAISE EXCEPTION 'audit retention cutoff must be at least 12 months old';
    END IF;

    previous_purge_setting :=
        current_setting('postqron.audit_retention_purge', true);
    PERFORM set_config('postqron.audit_retention_purge', 'enabled', true);
    DELETE FROM operations.sensitive_audit_events
    WHERE occurred_at < cutoff;
    GET DIAGNOSTICS deleted_count = ROW_COUNT;
    PERFORM set_config(
        'postqron.audit_retention_purge',
        coalesce(previous_purge_setting, ''),
        true
    );
    RETURN deleted_count;
END;
$function$;

REVOKE UPDATE, DELETE ON operations.sensitive_audit_events FROM PUBLIC;
REVOKE ALL ON FUNCTION operations.purge_expired_sensitive_audit_events(timestamptz)
    FROM PUBLIC;
