-- 0008 — Audit log e notifiche (SPEC §5, R21, R29).

-- L'audit log è append-only per contratto: nessuna colonna `updated_at`,
-- nessun trigger di aggiornamento. Una riga che si può correggere non è una
-- traccia, e gli eventi che finiscono qui — impersonificazione, cambio piano,
-- revoca di chiavi — sono esattamente quelli su cui una correzione a
-- posteriori non deve essere possibile.
CREATE TABLE audit_log (
    -- Identità sequenziale, non uuid: l'ordine di inserimento è informazione,
    -- e su una traccia di sicurezza un buco nella sequenza è visibile.
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,

    occurred_at timestamptz NOT NULL DEFAULT now(),

    -- ON DELETE SET NULL, non CASCADE: la traccia deve sopravvivere alla
    -- cancellazione dell'account coinvolto, altrimenti chiudere un account
    -- basterebbe a cancellarne la storia.
    actor_user_id uuid REFERENCES users (id) ON DELETE SET NULL,

    -- Valorizzato quando l'azione è compiuta da un admin che sta impersonando
    -- qualcuno (SPEC §4.3): `actor_user_id` è l'admin, questo è l'utente di cui
    -- ha assunto l'identità.
    impersonated_user_id uuid REFERENCES users (id) ON DELETE SET NULL,

    -- Utente su cui l'azione ha effetto, quando è diverso dall'attore.
    target_user_id uuid REFERENCES users (id) ON DELETE SET NULL,

    -- Azione in forma `dominio.verbo`, per esempio `user.impersonated`,
    -- `plan.changed`, `api_key.revoked`. La forma è vincolata perché un audit
    -- log con nomi liberi diventa illeggibile alla prima query di ricerca.
    action text NOT NULL
        CONSTRAINT audit_log_action_format_check
        CHECK (action ~ '^[a-z][a-z0-9_]*\.[a-z][a-z0-9_]*$'),

    entity_type text CHECK (entity_type <> ''),
    entity_id text CHECK (entity_id <> ''),

    ip_address inet,
    user_agent text,

    -- Contesto dell'evento. Non deve contenere segreti né dati personali oltre
    -- il necessario (SPEC §5).
    metadata jsonb NOT NULL DEFAULT '{}'::jsonb
        CONSTRAINT audit_log_metadata_object_check CHECK (jsonb_typeof(metadata) = 'object'),

    -- Impersonare sé stessi non è un evento sensato: se compare, è un bug.
    CONSTRAINT audit_log_impersonation_check
        CHECK (impersonated_user_id IS NULL OR impersonated_user_id IS DISTINCT FROM actor_user_id)
);

COMMENT ON TABLE audit_log IS 'Traccia append-only degli eventi sensibili (SPEC §5). Non si aggiorna e non si corregge.';

-- Vista amministrativa: gli eventi recenti, in ordine inverso.
CREATE INDEX audit_log_occurred_at_idx ON audit_log (occurred_at DESC);

-- «Cosa ha fatto questo admin» e «cosa è successo a questo utente»: sono le due
-- domande che si pongono a un audit log, e sono indici parziali perché le righe
-- di sistema non hanno né attore né bersaglio.
CREATE INDEX audit_log_actor_idx
    ON audit_log (actor_user_id, occurred_at DESC)
    WHERE actor_user_id IS NOT NULL;

CREATE INDEX audit_log_target_idx
    ON audit_log (target_user_id, occurred_at DESC)
    WHERE target_user_id IS NOT NULL;

CREATE INDEX audit_log_action_idx ON audit_log (action, occurred_at DESC);

-- ---------------------------------------------------------------- notifications

CREATE TABLE notifications (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,

    event notification_event NOT NULL,
    channel alert_channel NOT NULL,
    status notification_status NOT NULL DEFAULT 'pending',

    job_id uuid REFERENCES jobs (id) ON DELETE CASCADE,

    -- Riferimento all'esecuzione che ha generato l'avviso, per colonne e senza
    -- foreign key: `job_executions` è partizionata e le sue partizioni vengono
    -- eliminate dalla retention (0006). Una foreign key renderebbe quel DROP
    -- impossibile finché esiste una notifica che la riferisce — cioè renderebbe
    -- la retention ostaggio della tabella delle notifiche.
    environment environment,
    execution_scheduled_for timestamptz,
    execution_attempt smallint CHECK (execution_attempt >= 1),

    -- Chiave di deduplicazione: impedisce che un job che fallisce ogni secondo
    -- generi un avviso al secondo. Chi la compone decide la finestra — per
    -- esempio job, ambiente e ora.
    dedupe_key text CHECK (dedupe_key <> ''),

    payload jsonb NOT NULL DEFAULT '{}'::jsonb
        CONSTRAINT notifications_payload_object_check CHECK (jsonb_typeof(payload) = 'object'),

    attempts smallint NOT NULL DEFAULT 0 CHECK (attempts >= 0),

    -- Momento a partire dal quale la notifica può partire: `now()` per un invio
    -- immediato, più avanti per un rinvio dopo un errore di recapito.
    scheduled_at timestamptz NOT NULL DEFAULT now(),
    sent_at timestamptz,
    error text,

    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT notifications_sent_check
        CHECK (status <> 'sent' OR sent_at IS NOT NULL),
    CONSTRAINT notifications_execution_ref_check
        CHECK ((execution_scheduled_for IS NULL) = (execution_attempt IS NULL)),
    CONSTRAINT notifications_execution_job_check
        CHECK (execution_scheduled_for IS NULL OR job_id IS NOT NULL)
);

COMMENT ON TABLE notifications IS 'Coda delle notifiche transazionali e degli alert (R21, R29).';
COMMENT ON COLUMN notifications.dedupe_key IS 'Chiave di deduplicazione: evita la tempesta di avvisi da un job che fallisce in continuazione.';

CREATE UNIQUE INDEX notifications_dedupe_key_idx
    ON notifications (dedupe_key)
    WHERE dedupe_key IS NOT NULL;

-- Coda di invio: solo le notifiche ancora da recapitare, in ordine di
-- scadenza. L'indice parziale non cresce con lo storico dei recapiti riusciti.
CREATE INDEX notifications_queue_idx
    ON notifications (scheduled_at)
    WHERE status = 'pending';

CREATE INDEX notifications_user_idx ON notifications (user_id, created_at DESC);

CREATE INDEX notifications_job_idx
    ON notifications (job_id, created_at DESC)
    WHERE job_id IS NOT NULL;

CREATE TRIGGER notifications_set_updated_at
    BEFORE UPDATE ON notifications
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();
