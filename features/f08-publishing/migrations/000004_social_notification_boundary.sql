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
