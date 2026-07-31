ALTER TABLE f08_meta_notification_outbox
    DROP COLUMN payload,
    ADD COLUMN locale text NOT NULL DEFAULT 'en' CHECK (
        locale IN ('en', 'it', 'es', 'fr', 'de')
    ),
    ADD COLUMN template_id text NOT NULL DEFAULT 'facebook_group_manual_publish'
        CHECK (
            template_id IN (
                'facebook_group_manual_publish',
                'instagram_personal_manual_publish'
            )
        ),
    ADD COLUMN email_delivery_id text,
    ADD COLUMN permanent_failed_at timestamptz,
    ADD COLUMN retention_until timestamptz;

ALTER TABLE f14_email_deliveries
    ADD COLUMN source_workspace_id text,
    ADD COLUMN lease_token text,
    ADD COLUMN locked_until timestamptz,
    ADD COLUMN provider_call_started_at timestamptz,
    ADD COLUMN retention_until timestamptz;

UPDATE f14_email_deliveries AS delivery
   SET source_workspace_id = notification.workspace_id
  FROM f08_meta_notification_outbox AS notification
 WHERE notification.email_delivery_id = delivery.id
   AND delivery.source_workspace_id IS NULL;

DELETE FROM f14_email_provider_events
 WHERE provider_message_id IN (
     SELECT provider_message_id
       FROM f14_email_deliveries
      WHERE template_id IN (
          'facebook_group_manual_publish',
          'instagram_personal_manual_publish'
      )
        AND source_workspace_id IS NULL
        AND provider_message_id IS NOT NULL
 );

DELETE FROM f14_email_deliveries
 WHERE template_id IN (
     'facebook_group_manual_publish',
     'instagram_personal_manual_publish'
 )
   AND source_workspace_id IS NULL;

UPDATE f14_email_deliveries
   SET state = 'failed',
       last_diagnostic_code = 'legacy_ambiguous_delivery',
       last_diagnostic_detail = '',
       retention_until = updated_at + INTERVAL '12 months',
       lease_token = NULL,
       locked_until = NULL,
       provider_call_started_at = NULL
 WHERE state = 'sending';

UPDATE f14_email_deliveries
   SET retention_until = COALESCE(accepted_at, updated_at, created_at)
                         + INTERVAL '12 months'
 WHERE state IN (
     'accepted',
     'delivered',
     'bounced',
     'complained',
     'failed',
     'suppressed'
 );

ALTER TABLE f14_email_deliveries
    ADD CONSTRAINT f14_email_social_workspace_check CHECK (
        template_id NOT IN (
            'facebook_group_manual_publish',
            'instagram_personal_manual_publish'
        )
        OR (
            source_workspace_id IS NOT NULL
            AND length(btrim(source_workspace_id)) > 0
        )
    ),
    ADD CONSTRAINT f14_email_lease_check CHECK (
        (
            state = 'sending'
            AND lease_token IS NOT NULL
            AND locked_until IS NOT NULL
        )
        OR
        (
            state <> 'sending'
            AND lease_token IS NULL
            AND locked_until IS NULL
            AND provider_call_started_at IS NULL
        )
    );

CREATE INDEX f14_email_retention_idx
    ON f14_email_deliveries (retention_until, id)
    WHERE retention_until IS NOT NULL;

CREATE TABLE f08_meta_notification_tombstones (
    id text PRIMARY KEY CHECK (
        id ~ '^meta_notification_[0-9a-f]{32}$'
    ),
    provider text NOT NULL CHECK (
        provider IN ('facebook_groups', 'instagram_personal')
    ),
    payload_fingerprint text NOT NULL CHECK (
        payload_fingerprint ~ '^[0-9a-f]{64}$'
    ),
    outcome text NOT NULL CHECK (outcome = 'permanent_failure'),
    expires_at timestamptz NOT NULL
);

CREATE INDEX f08_meta_notification_tombstone_expiry_idx
    ON f08_meta_notification_tombstones (expires_at, id);

CREATE OR REPLACE FUNCTION f14_suppress_email_recipient(
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
               WHEN f14_email_suppressions.scope = 'all'
               THEN f14_email_suppressions.reason
               ELSE EXCLUDED.reason
           END,
           occurred_at = LEAST(
               f14_email_suppressions.occurred_at,
               EXCLUDED.occurred_at
           ),
           updated_at = EXCLUDED.updated_at;

    UPDATE f14_email_deliveries
       SET state = 'suppressed',
           updated_at = p_occurred_at,
           retention_until = p_occurred_at + INTERVAL '12 months',
           lease_token = NULL,
           locked_until = NULL,
           provider_call_started_at = NULL
     WHERE recipient_id = p_recipient_id
       AND state IN ('pending', 'retry')
       AND (
            p_scope = 'all'
            OR channel = 'marketing'
       );
END;
$$;

CREATE OR REPLACE FUNCTION f14_record_email_provider_event(
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
           updated_at = p_occurred_at,
           retention_until = CASE
               WHEN p_event_type IN (
                   'delivered',
                   'soft_bounce',
                   'hard_bounce',
                   'complaint'
               )
               THEN p_occurred_at + INTERVAL '12 months'
               ELSE retention_until
           END,
           lease_token = CASE
               WHEN p_event_type IN (
                   'delivered',
                   'soft_bounce',
                   'hard_bounce',
                   'complaint'
               )
               THEN NULL
               ELSE lease_token
           END,
           locked_until = CASE
               WHEN p_event_type IN (
                   'delivered',
                   'soft_bounce',
                   'hard_bounce',
                   'complaint'
               )
               THEN NULL
               ELSE locked_until
           END,
           provider_call_started_at = CASE
               WHEN p_event_type IN (
                   'delivered',
                   'soft_bounce',
                   'hard_bounce',
                   'complaint'
               )
               THEN NULL
               ELSE provider_call_started_at
           END
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

CREATE FUNCTION f14_claim_email_delivery_v2(
    p_now timestamptz,
    p_lease_token text,
    p_locked_until timestamptz
)
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
     WHERE (
            delivery.state IN ('pending', 'retry')
            OR (
                delivery.state = 'sending'
                AND delivery.locked_until <= p_now
                AND delivery.provider_call_started_at IS NULL
            )
       )
       AND delivery.next_attempt_at <= p_now
       AND (
            delivery.attempt_count < delivery.max_attempts
            OR (
                delivery.state = 'sending'
                AND delivery.provider_call_started_at IS NULL
            )
       )
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
           lease_token = p_lease_token,
           locked_until = p_locked_until,
           provider_call_started_at = NULL,
           updated_at = p_now
     WHERE id = v_id
     RETURNING *;
END;
$$;

DO $$
DECLARE
    v_constraint_name text;
    v_state_attnum smallint;
    v_delivered_attnum smallint;
BEGIN
    SELECT attnum
      INTO v_state_attnum
      FROM pg_attribute
     WHERE attrelid = 'f08_meta_notification_outbox'::regclass
       AND attname = 'state'
       AND NOT attisdropped;

    SELECT attnum
      INTO v_delivered_attnum
      FROM pg_attribute
     WHERE attrelid = 'f08_meta_notification_outbox'::regclass
       AND attname = 'delivered_at'
       AND NOT attisdropped;

    FOR v_constraint_name IN
        SELECT conname
          FROM pg_constraint
         WHERE conrelid = 'f08_meta_notification_outbox'::regclass
           AND contype = 'c'
           AND (
               conkey = ARRAY[v_state_attnum]::smallint[]
               OR v_delivered_attnum = ANY(conkey)
           )
    LOOP
        EXECUTE format(
            'ALTER TABLE f08_meta_notification_outbox DROP CONSTRAINT %I',
            v_constraint_name
        );
    END LOOP;
END;
$$;

UPDATE f08_meta_notification_outbox
   SET template_id = CASE provider
           WHEN 'facebook_groups' THEN 'facebook_group_manual_publish'
           ELSE 'instagram_personal_manual_publish'
       END,
       retention_until = CASE
           WHEN state = 'delivered'
           THEN COALESCE(delivered_at, created_at) + INTERVAL '12 months'
           ELSE NULL
       END;

ALTER TABLE f08_meta_notification_outbox
    ADD CONSTRAINT f08_meta_notification_state_check CHECK (
        state IN (
            'pending',
            'sending',
            'retry',
            'delivered',
            'permanent_failure'
        )
    ),
    ADD CONSTRAINT f08_meta_notification_terminal_check CHECK (
        (
            state = 'delivered'
            AND delivered_at IS NOT NULL
            AND permanent_failed_at IS NULL
            AND retention_until IS NOT NULL
        )
        OR
        (
            state = 'permanent_failure'
            AND delivered_at IS NULL
            AND permanent_failed_at IS NOT NULL
            AND retention_until IS NOT NULL
        )
        OR
        (
            state NOT IN ('delivered', 'permanent_failure')
            AND delivered_at IS NULL
            AND permanent_failed_at IS NULL
            AND retention_until IS NULL
        )
    );

ALTER TABLE f08_meta_notification_outbox
    ADD CONSTRAINT f08_meta_notification_template_provider_check CHECK (
        (
            provider = 'facebook_groups'
            AND template_id = 'facebook_group_manual_publish'
        )
        OR
        (
            provider = 'instagram_personal'
            AND template_id = 'instagram_personal_manual_publish'
        )
    ),
    ADD CONSTRAINT f08_meta_notification_email_delivery_fk
        FOREIGN KEY (email_delivery_id)
        REFERENCES f14_email_deliveries (id);

COMMENT ON TABLE f08_meta_notification_outbox IS
    'Minimized F8-to-F9/F14 social notification commands. Recipient, locale and template are resolved server-side; social content and credentials are never retained.';
COMMENT ON COLUMN f08_meta_notification_outbox.payload_fingerprint IS
    'One-way conflict detector for the F8 command; the social payload itself is not retained.';
COMMENT ON COLUMN f08_meta_notification_outbox.retention_until IS
    'Terminal minimized audit rows are eligible for D05 privacy cleanup after this instant.';
COMMENT ON COLUMN f14_email_deliveries.source_workspace_id IS
    'Server-resolved workspace scope for F8/F9 social delivery privacy erasure.';
COMMENT ON COLUMN f14_email_deliveries.provider_call_started_at IS
    'Persisted ambiguity boundary: an expired lease after this instant must never replay the provider call.';
COMMENT ON COLUMN f14_email_deliveries.retention_until IS
    'Instant after which delivery PII and linked provider events must be purged.';
COMMENT ON TABLE f08_meta_notification_tombstones IS
    'PII-free one-way idempotency evidence retained for twelve months after terminal F8 audit erasure.';
