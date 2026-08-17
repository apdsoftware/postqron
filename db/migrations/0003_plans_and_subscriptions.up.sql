-- 0003 — Piani e sottoscrizioni (R15, R16).
--
-- I limiti di SPEC §8 vivono qui, non nel codice: sono la stessa matrice che il
-- backend applica a ogni scrittura (R15), e averla come dati significa poterla
-- correggere senza un deploy.
--
-- I **prezzi non stanno in questa tabella**. Paddle è il Merchant of Record e la
-- fonte di verità di importi, valute e IVA (SPEC §2, R16): duplicarli qui
-- creerebbe una seconda copia libera di sfasarsi dalla prima, esattamente il
-- problema che AGENTS.md §7 descrive per il DSN. Restano i soli identificativi
-- di prezzo Paddle, che sono riferimenti e non valori.

CREATE TABLE plans (
    -- Il codice è la chiave: è stabile, leggibile nelle query e nei log, e
    -- compare nei confronti di entitlement molto più spesso di quanto le righe
    -- di questa tabella cambino.
    code text PRIMARY KEY CHECK (code ~ '^[a-z][a-z0-9_]*$'),
    name text NOT NULL CHECK (name <> ''),

    -- Ordine di presentazione nella pagina prezzi e nella dashboard.
    sort_order smallint NOT NULL,

    -- Un piano non pubblico resta assegnabile a mano dall'area admin ma non
    -- compare nel listino: serve per accordi fuori listino e per ritirare un
    -- piano senza rompere le sottoscrizioni che lo usano ancora.
    is_public boolean NOT NULL DEFAULT true,

    -- NULL significa «nessun limite rigido». `fair_use_jobs` è la soglia
    -- dichiarata per i piani venduti come illimitati: distinguerla da un tetto
    -- vero evita che un limite commerciale morbido diventi per errore un
    -- rifiuto secco lato backend.
    max_jobs integer CHECK (max_jobs > 0),
    fair_use_jobs integer CHECK (fair_use_jobs > 0),

    -- Risoluzione minima concessa dal piano, in secondi (R22). È il valore
    -- contro cui si verifica `jobs.every_seconds` al sync di `cron.yaml`: un
    -- `every: 1s` su piano Free viene rifiutato, non degradato (SPEC §9).
    min_interval_seconds integer NOT NULL CHECK (min_interval_seconds >= 1),

    -- Giorni di conservazione dei log di esecuzione (R6, SPEC §8: 3/15/30/90).
    -- Guida sia la cancellazione delle righe sia il taglio delle partizioni di
    -- job_executions — vedi 0006.
    log_retention_days integer NOT NULL CHECK (log_retention_days >= 1),

    max_repositories integer CHECK (max_repositories > 0),
    max_members integer CHECK (max_members > 0),

    environments_enabled boolean NOT NULL DEFAULT false,
    ai_debugging_enabled boolean NOT NULL DEFAULT false,
    webhook_alerts_enabled boolean NOT NULL DEFAULT false,
    rbac_enabled boolean NOT NULL DEFAULT false,
    metrics_enabled boolean NOT NULL DEFAULT false,
    log_export_enabled boolean NOT NULL DEFAULT false,
    multi_workspace_enabled boolean NOT NULL DEFAULT false,
    dedicated_egress_ip boolean NOT NULL DEFAULT false,

    -- Identificativi di prezzo Paddle, non importi. Restano NULL finché le
    -- credenziali non sono disponibili (SPEC §7, Q3).
    paddle_price_id_monthly text CHECK (paddle_price_id_monthly <> ''),
    paddle_price_id_yearly text CHECK (paddle_price_id_yearly <> ''),

    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),

    -- Un tetto rigido più basso della soglia di fair use sarebbe una
    -- contraddizione: il primo scatterebbe sempre per primo.
    CONSTRAINT plans_fair_use_below_max_check
        CHECK (max_jobs IS NULL OR fair_use_jobs IS NULL OR fair_use_jobs <= max_jobs)
);

COMMENT ON TABLE plans IS 'Matrice dei limiti di piano applicata lato backend (R15, SPEC §8). I prezzi stanno in Paddle.';
COMMENT ON COLUMN plans.max_jobs IS 'Tetto rigido di job attivi; NULL = nessun limite rigido.';
COMMENT ON COLUMN plans.log_retention_days IS 'Retention dei log di esecuzione in giorni (R6).';

CREATE UNIQUE INDEX plans_sort_order_key ON plans (sort_order);

CREATE TRIGGER plans_set_updated_at
    BEFORE UPDATE ON plans
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- Dati di riferimento: i quattro piani approvati (SPEC §7 Q2, §8).
INSERT INTO plans (
    code, name, sort_order,
    max_jobs, fair_use_jobs, min_interval_seconds, log_retention_days,
    max_repositories, max_members,
    environments_enabled, ai_debugging_enabled, webhook_alerts_enabled,
    rbac_enabled, metrics_enabled, log_export_enabled,
    multi_workspace_enabled, dedicated_egress_ip
) VALUES
    ('free',   'Free',   1,   20, NULL,   60,  3,    1,    1, false, false, false, false, false, false, false, false),
    ('pro',    'Pro',    2,  200, NULL,   10, 15, NULL,    1, true,  true,  true,  false, false, false, false, false),
    ('team',   'Team',   3, NULL, 1000,    1, 30, NULL,    5, true,  true,  true,  true,  true,  true,  false, false),
    ('agency', 'Agency', 4, NULL, NULL,    1, 90, NULL, NULL, true,  true,  true,  true,  true,  true,  true,  true);

-- ---------------------------------------------------------- subscriptions

CREATE TABLE subscriptions (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,

    -- ON UPDATE CASCADE perché il codice del piano è la chiave: se un giorno
    -- va rinominato, le sottoscrizioni lo seguono. ON DELETE RESTRICT perché
    -- un piano con sottoscrizioni vive non si cancella, si rende non pubblico.
    plan_code text NOT NULL REFERENCES plans (code) ON UPDATE CASCADE ON DELETE RESTRICT,

    status subscription_status NOT NULL DEFAULT 'active',
    billing_period billing_period,

    -- NULL sul piano Free, che non passa da Paddle.
    paddle_subscription_id text CHECK (paddle_subscription_id <> ''),
    paddle_customer_id text CHECK (paddle_customer_id <> ''),

    current_period_start timestamptz,
    current_period_end timestamptz,
    trial_ends_at timestamptz,
    cancel_at timestamptz,
    canceled_at timestamptz,

    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT subscriptions_period_order_check
        CHECK (current_period_start IS NULL
            OR current_period_end IS NULL
            OR current_period_end > current_period_start),
    CONSTRAINT subscriptions_canceled_check
        CHECK (status <> 'canceled' OR canceled_at IS NOT NULL)
);

COMMENT ON TABLE subscriptions IS
    'Sottoscrizione dell''utente: è la fonte di verità del piano (R16). Anche il piano Free ha la sua riga, senza identificativi Paddle.';

CREATE UNIQUE INDEX subscriptions_paddle_subscription_id_key
    ON subscriptions (paddle_subscription_id)
    WHERE paddle_subscription_id IS NOT NULL;

-- Un solo abbonamento non terminato per utente. Le righe `canceled` restano
-- come storico e non partecipano al vincolo: senza questo indice parziale un
-- upgrade seguito da un webhook duplicato lascerebbe l'utente con due piani
-- attivi e un entitlement deciso dall'ordine di lettura.
CREATE UNIQUE INDEX subscriptions_one_live_per_user_idx
    ON subscriptions (user_id)
    WHERE status <> 'canceled';

CREATE INDEX subscriptions_plan_code_idx ON subscriptions (plan_code);

CREATE TRIGGER subscriptions_set_updated_at
    BEFORE UPDATE ON subscriptions
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();
