ALTER TABLE f08_publication_destinations
    ADD COLUMN draft_revision bigint NOT NULL DEFAULT 1
        CHECK (draft_revision > 0),
    ADD COLUMN mode text NOT NULL DEFAULT 'auto'
        CHECK (mode IN ('auto', 'notification')),
    ADD COLUMN capability_id text NOT NULL DEFAULT 'legacy-disabled'
        CHECK (length(btrim(capability_id)) > 0),
    ADD COLUMN capabilities jsonb NOT NULL DEFAULT
        '{"version":"legacy-disabled","mode":"auto","native_idempotency":false,"reconciliation":false,"multi_step":false,"remote_permalink":false,"notification_idempotency":false}'::jsonb
        CHECK (jsonb_typeof(capabilities) = 'object'),
    ADD COLUMN snapshot_hash text NOT NULL DEFAULT
        '0000000000000000000000000000000000000000000000000000000000000000'
        CHECK (snapshot_hash ~ '^[0-9a-f]{64}$'),
    ADD COLUMN permalink text,
    ADD COLUMN notification_id text,
    ADD COLUMN checkpoint jsonb NOT NULL DEFAULT '{}'::jsonb
        CHECK (jsonb_typeof(checkpoint) = 'object'),
    ADD COLUMN needs_reconciliation boolean NOT NULL DEFAULT false,
    ADD COLUMN error_ambiguous boolean NOT NULL DEFAULT false;

ALTER TABLE f08_publication_destinations
    ALTER COLUMN connection_id DROP NOT NULL,
    DROP CONSTRAINT IF EXISTS f08_publication_destinations_status_check,
    ADD CONSTRAINT f08_publication_destinations_status_check CHECK (
        status IN (
            'pending',
            'publishing',
            'retry_wait',
            'published',
            'notified',
            'dead_letter',
            'cancelled'
        )
    ),
    DROP CONSTRAINT IF EXISTS f08_publication_destinations_check1,
    ADD CONSTRAINT f08_publication_destinations_terminal_result_check CHECK (
        (
            status = 'published'
            AND mode = 'auto'
            AND remote_id IS NOT NULL
            AND notification_id IS NULL
            AND published_at IS NOT NULL
        )
        OR (
            status = 'notified'
            AND mode = 'notification'
            AND remote_id IS NULL
            AND notification_id IS NOT NULL
            AND published_at IS NOT NULL
        )
        OR (
            status NOT IN ('published', 'notified')
            AND remote_id IS NULL
            AND notification_id IS NULL
            AND published_at IS NULL
        )
    ),
    ADD CONSTRAINT f08_publication_destinations_connection_mode_check CHECK (
        (mode = 'auto' AND connection_id IS NOT NULL AND length(btrim(connection_id)) > 0)
        OR (mode = 'notification')
    );

ALTER TABLE f08_publication_attempts
    DROP CONSTRAINT IF EXISTS f08_publication_attempts_outcome_check,
    ADD CONSTRAINT f08_publication_attempts_outcome_check CHECK (
        outcome IN (
            'in_progress',
            'progress',
            'published',
            'notified',
            'retry',
            'dead_letter',
            'cancelled'
        )
    );

CREATE UNIQUE INDEX f08_destinations_notification_id_idx
    ON f08_publication_destinations (provider, notification_id)
    WHERE notification_id IS NOT NULL;

CREATE OR REPLACE FUNCTION f08_reject_snapshot_mutation()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF ROW(
        NEW.command_id,
        NEW.workspace_id,
        NEW.post_id,
        NEW.generation,
        NEW.draft_revision,
        NEW.channel_id,
        NEW.provider,
        NEW.connection_id,
        NEW.mode,
        NEW.capability_id,
        NEW.capabilities,
        NEW.payload,
        NEW.snapshot_hash,
        NEW.idempotency_key
    ) IS DISTINCT FROM ROW(
        OLD.command_id,
        OLD.workspace_id,
        OLD.post_id,
        OLD.generation,
        OLD.draft_revision,
        OLD.channel_id,
        OLD.provider,
        OLD.connection_id,
        OLD.mode,
        OLD.capability_id,
        OLD.capabilities,
        OLD.payload,
        OLD.snapshot_hash,
        OLD.idempotency_key
    ) THEN
        RAISE EXCEPTION 'F8 destination snapshot is immutable'
            USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER f08_destination_snapshot_immutable
BEFORE UPDATE ON f08_publication_destinations
FOR EACH ROW
EXECUTE FUNCTION f08_reject_snapshot_mutation();

COMMENT ON COLUMN f08_publication_destinations.capabilities IS
    'Immutable capability declaration resolved from the registered F8 adapter at enqueue time.';
COMMENT ON COLUMN f08_publication_destinations.checkpoint IS
    'Durable provider-neutral checkpoint; each adapter call performs at most one remote side effect.';
COMMENT ON COLUMN f08_publication_destinations.needs_reconciliation IS
    'True after an ambiguous outcome or lease-expired publishing claim; cleared only by a safe terminal/progress transition.';
