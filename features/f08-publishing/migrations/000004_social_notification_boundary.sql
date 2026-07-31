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
   SET lease_token = 'legacy-ambiguous-' || id,
       locked_until = updated_at,
       provider_call_started_at = updated_at
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
