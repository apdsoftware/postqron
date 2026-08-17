-- 0005 — Job schedulati (R1, R22, R23).
--
-- Un job si esprime in **una** delle due modalità di SPEC §9:
--
--   * `schedule` — espressione cron a cinque campi, granularità minima 1 minuto;
--   * `every_seconds` — intervallo, che è ciò che rende possibile la risoluzione
--     sub-minuto dei piani Pro (10s) e Team (1s), fuori portata per il cron (R22).
--
-- Sono mutuamente esclusive e una dev'esserci: il vincolo è nel database, non
-- solo nel parser di `cron.yaml`. Un job creato da API o da dashboard passa da
-- qui senza toccare quel parser, e uno schedulatore che trovasse entrambe le
-- modalità impostate non avrebbe un comportamento corretto da scegliere.

CREATE TABLE jobs (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,

    -- NULL per i job creati da UI o API. Valorizzato per quelli che nascono da
    -- un `cron.yaml`: la riconciliazione lavora sull'insieme dei job di quel
    -- repository e non deve vedere gli altri (R13).
    repository_id uuid REFERENCES repositories (id) ON DELETE CASCADE,

    -- Identità stabile del job (SPEC §9): è la chiave su cui la
    -- riconciliazione decide se creare, aggiornare o disattivare. Rinominare un
    -- job equivale a cancellarlo e crearne un altro.
    name text NOT NULL
        CONSTRAINT jobs_name_format_check
        CHECK (name ~ '^[a-zA-Z0-9]([a-zA-Z0-9._-]*[a-zA-Z0-9])?$' AND char_length(name) <= 100),
    description text,

    -- ------------------------------------------------------------ schedulazione

    -- Espressione cron a cinque campi. Il vincolo controlla la forma, non la
    -- semantica dei singoli campi: quella è del parser (issue 5), che è l'unico
    -- posto in cui ha senso viva.
    schedule text
        CONSTRAINT jobs_schedule_shape_check
        CHECK (schedule IS NULL OR schedule ~ '^\S+(\s+\S+){4}$'),

    -- Intervallo in secondi. Il minimo assoluto è 1 secondo (piano Team); il
    -- minimo effettivo dipende dal piano dell'utente ed è verificato al
    -- momento della scrittura contro `plans.min_interval_seconds`, che è
    -- l'unico confronto che questo database non può fare da solo.
    every_seconds integer CHECK (every_seconds >= 1),

    -- Fuso orario del job, esplicito (R1). Conta per la modalità cron, dove
    -- decide anche il comportamento ai cambi di ora legale (R2); un intervallo
    -- è indifferente al fuso.
    timezone text NOT NULL DEFAULT 'UTC' CHECK (timezone <> ''),

    -- Ambienti in cui il job vive (R23). L'array è la forma di `cron.yaml`
    -- (`environments: [staging, production]`) e serve una relazione che ha al
    -- massimo due elementi da un dominio chiuso: una tabella di collegamento
    -- costerebbe un join su ogni lettura del dispatch senza aggiungere nulla.
    -- Ogni ambiente produce la propria esecuzione per ciascuna occorrenza.
    environments environment[] NOT NULL DEFAULT '{production}'::environment[]
        CONSTRAINT jobs_environments_check
        CHECK (cardinality(environments) > 0 AND has_unique_elements(environments)),

    -- ------------------------------------------------------------------ target

    -- Solo HTTP/HTTPS: Postqron non esegue comandi né container (SPEC §10).
    url text NOT NULL CONSTRAINT jobs_url_scheme_check CHECK (url ~* '^https?://'),
    method http_method NOT NULL DEFAULT 'POST',

    -- Header come oggetto JSON. I riferimenti `${VAR}` ai segreti del workspace
    -- restano non risolti a riposo e vengono espansi al momento
    -- dell'esecuzione (SPEC §9): qui non deve finire alcun valore in chiaro.
    headers jsonb NOT NULL DEFAULT '{}'::jsonb
        CONSTRAINT jobs_headers_object_check CHECK (jsonb_typeof(headers) = 'object'),
    body text,

    timeout_seconds integer NOT NULL DEFAULT 30 CHECK (timeout_seconds BETWEEN 1 AND 300),

    -- --------------------------------------------------------------- retry (R5)

    max_retries smallint NOT NULL DEFAULT 3 CHECK (max_retries BETWEEN 0 AND 10),
    retry_backoff retry_backoff NOT NULL DEFAULT 'exponential',

    -- Canali su cui avvisare in caso di fallimento (`alerts.on_failure` in
    -- `cron.yaml`). Un array vuoto significa nessun avviso, ed è una scelta
    -- legittima per un job rumoroso.
    alert_on_failure alert_channel[] NOT NULL DEFAULT '{email}'::alert_channel[]
        CONSTRAINT jobs_alert_channels_check CHECK (has_unique_elements(alert_on_failure)),

    -- ------------------------------------------------------------------- stato

    enabled boolean NOT NULL DEFAULT true,

    -- Distinto da `enabled`: `enabled = false` è una pausa decisa dall'utente,
    -- `archived_at` è la disattivazione di un job sparito dal `cron.yaml` (R13).
    -- Confonderle significherebbe che un sync resuscita un job che l'utente
    -- aveva messo in pausa a mano, o che una pausa manuale sopravvive alla
    -- rimozione dal file.
    archived_at timestamptz,

    -- Istante della prossima occorrenza, calcolato dallo scheduler (R2). È il
    -- perno della query calda del dispatch: vedi l'indice più sotto.
    next_run_at timestamptz,

    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),

    -- Il vincolo di SPEC §9: una modalità e una sola. `<>` fra due booleani è
    -- lo XOR, e con `IS NULL` a produrli non ci sono NULL a propagarsi.
    CONSTRAINT jobs_schedule_xor_every_check
        CHECK ((schedule IS NULL) <> (every_seconds IS NULL))
);

COMMENT ON TABLE jobs IS 'Job schedulati (R1). `schedule` ed `every_seconds` sono mutuamente esclusivi (SPEC §9, R22).';
COMMENT ON COLUMN jobs.every_seconds IS 'Intervallo in secondi: è la modalità che copre la risoluzione sub-minuto (R22).';
COMMENT ON COLUMN jobs.next_run_at IS 'Prossima occorrenza calcolata dallo scheduler; NULL se il job non ne ha una.';
COMMENT ON COLUMN jobs.headers IS 'Header della richiesta. I segreti restano riferimenti ${VAR}, risolti a runtime (SPEC §9).';

-- Query calda 1 — «quali occorrenze sono dovute adesso»:
--
--   SELECT id, ... FROM jobs
--    WHERE enabled AND archived_at IS NULL AND next_run_at <= now()
--    ORDER BY next_run_at
--    FOR UPDATE SKIP LOCKED
--    LIMIT $1;
--
-- L'indice è parziale su tre fronti: i job in pausa, quelli archiviati e quelli
-- senza prossima occorrenza non compaiono. È la differenza fra un indice che
-- cresce con il catalogo dei job e uno che cresce solo con i job che il
-- dispatch deve davvero guardare — e a 1 secondo di risoluzione questa query
-- gira in continuazione.
CREATE INDEX jobs_due_idx
    ON jobs (next_run_at)
    WHERE enabled AND archived_at IS NULL AND next_run_at IS NOT NULL;

-- Identità del job (SPEC §9). Due indici parziali invece di uno solo perché la
-- chiave cambia con l'origine: dentro un repository il nome è unico nel file,
-- mentre per i job creati a mano l'ambito è l'utente. Un unico vincolo su
-- (user_id, name) impedirebbe a due repository dello stesso utente di avere
-- entrambi un job `healthcheck`, che è normale e legittimo.
CREATE UNIQUE INDEX jobs_repository_name_key
    ON jobs (repository_id, name)
    WHERE repository_id IS NOT NULL;

CREATE UNIQUE INDEX jobs_user_name_key
    ON jobs (user_id, name)
    WHERE repository_id IS NULL;

-- Elenco dei job di un utente in dashboard, e conteggio per le quote di piano.
CREATE INDEX jobs_user_id_idx ON jobs (user_id, created_at DESC);

-- Riconciliazione: tutti i job di un repository, in un colpo solo (R13).
CREATE INDEX jobs_repository_id_idx ON jobs (repository_id) WHERE repository_id IS NOT NULL;

-- Filtro per ambiente (R23): `WHERE environments @> ARRAY['staging']::environment[]`.
CREATE INDEX jobs_environments_idx ON jobs USING gin (environments);

CREATE TRIGGER jobs_set_updated_at
    BEFORE UPDATE ON jobs
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();
