CREATE TABLE f14_email_deliveries (
    id text PRIMARY KEY,
    idempotency_key text NOT NULL,
    channel text NOT NULL,
    template_id text NOT NULL,
    template_version text NOT NULL,
    recipient_id text NOT NULL,
    recipient_email text NOT NULL,
    recipient_name text NOT NULL DEFAULT '',
    subject text NOT NULL,
    html_body text NOT NULL,
    text_body text NOT NULL,
    message_headers jsonb NOT NULL DEFAULT '{}'::jsonb,
    state text NOT NULL DEFAULT 'pending',
    attempt_count integer NOT NULL DEFAULT 0,
    max_attempts integer NOT NULL DEFAULT 5,
    next_attempt_at timestamptz NOT NULL,
    provider_message_id text,
    last_diagnostic_code text NOT NULL DEFAULT '',
    last_diagnostic_detail text NOT NULL DEFAULT '',
    accepted_at timestamptz,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    CONSTRAINT f14_email_channel_check
        CHECK (channel IN ('transactional', 'marketing')),
    CONSTRAINT f14_email_state_check
        CHECK (state IN (
            'pending',
            'sending',
            'retry',
            'accepted',
            'delivered',
            'bounced',
            'complained',
            'failed',
            'suppressed'
        )),
    CONSTRAINT f14_email_attempts_check
        CHECK (
            attempt_count >= 0
            AND max_attempts > 0
            AND attempt_count <= max_attempts
        ),
    CONSTRAINT f14_email_idempotency_unique
        UNIQUE (channel, idempotency_key),
    CONSTRAINT f14_email_provider_message_unique
        UNIQUE (provider_message_id)
);

CREATE INDEX f14_email_due_idx
    ON f14_email_deliveries (next_attempt_at, created_at)
    WHERE state IN ('pending', 'retry');

CREATE INDEX f14_email_recipient_idx
    ON f14_email_deliveries (recipient_id, created_at DESC);

CREATE TABLE f14_email_provider_events (
    provider_event_id text PRIMARY KEY,
    provider_message_id text NOT NULL,
    event_type text NOT NULL,
    recipient_id text NOT NULL,
    diagnostic_code text NOT NULL DEFAULT '',
    diagnostic_detail text NOT NULL DEFAULT '',
    occurred_at timestamptz NOT NULL,
    recorded_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT f14_email_event_type_check
        CHECK (event_type IN (
            'delivered',
            'deferred',
            'soft_bounce',
            'hard_bounce',
            'complaint'
        )),
    CONSTRAINT f14_email_event_delivery_fk
        FOREIGN KEY (provider_message_id)
        REFERENCES f14_email_deliveries (provider_message_id)
);

CREATE INDEX f14_email_provider_events_message_idx
    ON f14_email_provider_events (provider_message_id, occurred_at);

CREATE TABLE f14_email_suppressions (
    recipient_id text PRIMARY KEY,
    scope text NOT NULL,
    reason text NOT NULL,
    occurred_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT f14_email_suppression_scope_check
        CHECK (scope IN ('marketing', 'all'))
);

CREATE FUNCTION f14_claim_email_delivery(p_now timestamptz)
RETURNS SETOF f14_email_deliveries
LANGUAGE plpgsql
AS $$
DECLARE
    v_id text;
BEGIN
    SELECT delivery.id
      INTO v_id
      FROM f14_email_deliveries AS delivery
      LEFT JOIN f14_email_suppressions AS suppression
        ON suppression.recipient_id = delivery.recipient_id
     WHERE delivery.state IN ('pending', 'retry')
       AND delivery.next_attempt_at <= p_now
       AND delivery.attempt_count < delivery.max_attempts
       AND (
            suppression.recipient_id IS NULL
            OR (
                suppression.scope = 'marketing'
                AND delivery.channel = 'transactional'
            )
       )
     ORDER BY delivery.next_attempt_at, delivery.created_at
     FOR UPDATE OF delivery SKIP LOCKED
     LIMIT 1;

    IF v_id IS NULL THEN
        RETURN;
    END IF;

    RETURN QUERY
    UPDATE f14_email_deliveries
       SET state = 'sending',
           attempt_count = attempt_count + 1,
           updated_at = p_now
     WHERE id = v_id
     RETURNING *;
END;
$$;

CREATE FUNCTION f14_record_email_provider_event(
    p_provider_event_id text,
    p_provider_message_id text,
    p_event_type text,
    p_recipient_id text,
    p_diagnostic_code text,
    p_diagnostic_detail text,
    p_occurred_at timestamptz
)
RETURNS boolean
LANGUAGE plpgsql
AS $$
DECLARE
    v_delivery_id text;
    v_recipient_id text;
BEGIN
    SELECT id, recipient_id
      INTO v_delivery_id, v_recipient_id
      FROM f14_email_deliveries
     WHERE provider_message_id = p_provider_message_id
     FOR UPDATE;

    IF v_delivery_id IS NULL THEN
        RAISE EXCEPTION 'unknown provider message id';
    END IF;
    IF v_recipient_id <> p_recipient_id THEN
        RAISE EXCEPTION 'provider event recipient mismatch';
    END IF;

    INSERT INTO f14_email_provider_events (
        provider_event_id,
        provider_message_id,
        event_type,
        recipient_id,
        diagnostic_code,
        diagnostic_detail,
        occurred_at
    )
    VALUES (
        p_provider_event_id,
        p_provider_message_id,
        p_event_type,
        p_recipient_id,
        p_diagnostic_code,
        p_diagnostic_detail,
        p_occurred_at
    )
    ON CONFLICT (provider_event_id) DO NOTHING;

    IF NOT FOUND THEN
        RETURN false;
    END IF;

    UPDATE f14_email_deliveries
       SET state = CASE p_event_type
               WHEN 'delivered' THEN 'delivered'
               WHEN 'soft_bounce' THEN 'bounced'
               WHEN 'hard_bounce' THEN 'bounced'
               WHEN 'complaint' THEN 'complained'
               ELSE state
           END,
           last_diagnostic_code = p_diagnostic_code,
           last_diagnostic_detail = p_diagnostic_detail,
           updated_at = p_occurred_at
     WHERE id = v_delivery_id;

    IF p_event_type IN ('hard_bounce', 'complaint') THEN
        PERFORM f14_suppress_email_recipient(
            p_recipient_id,
            'all',
            p_event_type,
            p_occurred_at
        );
    END IF;

    RETURN true;
END;
$$;

CREATE FUNCTION f14_suppress_email_recipient(
    p_recipient_id text,
    p_scope text,
    p_reason text,
    p_occurred_at timestamptz
)
RETURNS void
LANGUAGE plpgsql
AS $$
BEGIN
    INSERT INTO f14_email_suppressions (
        recipient_id,
        scope,
        reason,
        occurred_at,
        updated_at
    )
    VALUES (
        p_recipient_id,
        p_scope,
        p_reason,
        p_occurred_at,
        p_occurred_at
    )
    ON CONFLICT (recipient_id) DO UPDATE
       SET scope = CASE
               WHEN f14_email_suppressions.scope = 'all' THEN 'all'
               ELSE EXCLUDED.scope
           END,
           reason = CASE
               WHEN f14_email_suppressions.scope = 'all' THEN f14_email_suppressions.reason
               ELSE EXCLUDED.reason
           END,
           occurred_at = LEAST(
               f14_email_suppressions.occurred_at,
               EXCLUDED.occurred_at
           ),
           updated_at = EXCLUDED.updated_at;

    UPDATE f14_email_deliveries
       SET state = 'suppressed',
           updated_at = p_occurred_at
     WHERE recipient_id = p_recipient_id
       AND state IN ('pending', 'retry')
       AND (
            p_scope = 'all'
            OR channel = 'marketing'
       );
END;
$$;
