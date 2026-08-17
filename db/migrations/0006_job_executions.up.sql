-- 0006 — Log delle esecuzioni (R4, R6).
--
-- È la tabella che cresce, e cresce in modo diverso da tutte le altre. Un job
-- del piano Team con `every: 1s` produce **86.400 righe al giorno da solo**, per
-- ogni ambiente in cui vive; il piano Free, a 1 minuto, ne produce 1.440. Lo
-- schema è progettato attorno a questo numero, non attorno al caso medio.
--
-- Tre conseguenze, tutte deliberate.
--
-- 1. **Nessuna chiave surrogata.** La chiave primaria è la quaterna naturale
--    (job_id, scheduled_for, environment, attempt). Un `id uuid` costerebbe un
--    secondo indice sull'unica tabella con questo volume di scritture, e con
--    UUID casuali ogni inserimento cadrebbe in una pagina diversa dell'indice:
--    esattamente il pattern peggiore a questo ritmo. La quaterna, invece, è
--    quasi ordinata per costruzione, quindi gli inserimenti restano in coda.
--    Un'esecuzione si indirizza per chiave naturale, che è anche l'unica cosa
--    che la identifica davvero.
--
-- 2. **La stessa chiave è il lock di idempotenza (R4).** Ogni occorrenza è
--    eseguita una sola volta perché il motore inserisce la riga *prima* di
--    dispatchare: un secondo processo, o lo stesso processo dopo un riavvio,
--    trova un conflitto sulla chiave primaria e si ferma. Non serve una tabella
--    di lock separata, e non c'è finestra fra «verifico» e «inserisco».
--
-- 3. **La stessa chiave serve la query calda dei log.** Il prefisso
--    (job_id, scheduled_for) è esattamente «log di un job ordinati per data»:
--    l'indice che garantisce l'idempotenza è anche quello che serve la lettura
--    più frequente della dashboard. Un solo indice per due lavori — su una
--    tabella che prende 86.400 inserimenti al giorno per job, ogni indice
--    risparmiato è un costo risparmiato 86.400 volte.
--
-- **Partizionamento.** La tabella è partizionata per intervallo su
-- `scheduled_for`, con una partizione al giorno. La retention (R6, SPEC §8:
-- 3/15/30/90 giorni) si applica così: le partizioni interamente più vecchie
-- della retention massima fra i piani attivi si eliminano con DROP TABLE, che è
-- istantaneo e non lascia bloat; le retention più corte dei piani inferiori si
-- applicano cancellando righe dentro le partizioni ancora vive. La divisione è
-- fortunata: i piani a retention lunga (30 e 90 giorni) sono quelli con la
-- risoluzione al secondo, cioè quelli il cui volume rende impraticabile la
-- cancellazione riga per riga; i piani a retention corta sono fermi a 1 minuto,
-- dove un DELETE periodico è del tutto sostenibile.
--
-- Il taglio giornaliero segue la retention più corta del listino (3 giorni) e
-- tiene ogni singola partizione, con i suoi indici, abbastanza piccola da
-- restare in cache nella finestra calda — che è dove vive quasi tutto il
-- traffico.

CREATE TABLE job_executions (
    job_id uuid NOT NULL REFERENCES jobs (id) ON DELETE CASCADE,

    -- Istante teorico dell'occorrenza, non quello in cui è partita davvero:
    -- è ciò che la rende identificabile prima di essere eseguita, ed è quindi
    -- ciò su cui l'idempotenza può appoggiarsi. `timestamptz` ha risoluzione al
    -- microsecondo: il secondo di R22 ci sta comodamente dentro.
    scheduled_for timestamptz NOT NULL,

    -- Un job in due ambienti produce due esecuzioni per occorrenza, con
    -- routing e alert separati (R23).
    environment environment NOT NULL,

    -- 1 è il primo tentativo; i successivi sono i retry di R5.
    attempt smallint NOT NULL DEFAULT 1 CHECK (attempt >= 1),

    status execution_status NOT NULL DEFAULT 'pending',
    triggered_by execution_trigger NOT NULL DEFAULT 'schedule',

    started_at timestamptz,
    finished_at timestamptz,

    -- Generata invece che scritta dall'applicazione: una durata calcolata a
    -- mano e una coppia di istanti sono due verità che possono divergere, e la
    -- prima è ricavabile dalla seconda.
    duration_ms integer GENERATED ALWAYS AS (
        (extract(epoch FROM (finished_at - started_at)) * 1000)::integer
    ) STORED,

    response_status smallint CHECK (response_status BETWEEN 100 AND 599),

    -- Estratto troncato della risposta (R6). Il limite è nello schema perché una
    -- risposta da qualche megabyte, moltiplicata per il volume di questa
    -- tabella, riempie il disco della VPS molto prima che se ne accorga
    -- qualcuno.
    response_excerpt text CHECK (char_length(response_excerpt) <= 8192),
    error text CHECK (char_length(error) <= 8192),

    created_at timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT job_executions_pkey
        PRIMARY KEY (job_id, scheduled_for, environment, attempt),

    CONSTRAINT job_executions_timeline_check
        CHECK (finished_at IS NULL OR (started_at IS NOT NULL AND finished_at >= started_at)),

    -- Uno stato terminale senza istanti è un log che non dice quando è successo.
    -- `skipped` è l'eccezione: l'occorrenza non è mai partita.
    CONSTRAINT job_executions_terminal_check
        CHECK (status IN ('pending', 'running', 'skipped')
            OR (started_at IS NOT NULL AND finished_at IS NOT NULL))
) PARTITION BY RANGE (scheduled_for);

COMMENT ON TABLE job_executions IS
    'Tentativi di esecuzione (R6). Partizionata per giorno su scheduled_for; la chiave primaria è anche il lock di idempotenza (R4).';
COMMENT ON COLUMN job_executions.scheduled_for IS 'Istante teorico dell''occorrenza e chiave di partizionamento.';
COMMENT ON COLUMN job_executions.response_excerpt IS 'Estratto troncato della risposta, al massimo 8 KiB (R6).';

-- Nessuna tabella dichiara una foreign key **verso** job_executions, ed è una
-- scelta, non una dimenticanza: un DROP di partizione fallirebbe finché una
-- riga referenziante esiste, e la retention diventerebbe impossibile da
-- applicare. `notifications` riferisce l'esecuzione per colonne (0008).

-- Recupero dopo un riavvio: le esecuzioni rimaste appese vanno riconciliate
-- prima di ripartire (R4). L'indice parziale contiene solo quelle in volo, quindi
-- resta minuscolo qualunque sia la dimensione della tabella.
CREATE INDEX job_executions_in_flight_idx
    ON job_executions (scheduled_for)
    WHERE status IN ('pending', 'running');

-- ---------------------------------------------------- gestione delle partizioni

-- job_executions_ensure_partition crea, se manca, la partizione di un giorno.
--
-- I confini sono calcolati in UTC esplicito e non dipendono dal fuso della
-- sessione: due manutenzioni lanciate da postazioni diverse devono produrre
-- gli stessi intervalli.
CREATE FUNCTION job_executions_ensure_partition(day date) RETURNS text
    LANGUAGE plpgsql
    SET search_path = public, pg_catalog
AS $$
DECLARE
    partition_name text := format('job_executions_%s', to_char(day, 'YYYYMMDD'));
BEGIN
    EXECUTE format(
        'CREATE TABLE IF NOT EXISTS %I PARTITION OF job_executions FOR VALUES FROM (%L) TO (%L)',
        partition_name,
        day::timestamp AT TIME ZONE 'UTC',
        (day + 1)::timestamp AT TIME ZONE 'UTC'
    );
    RETURN partition_name;
END
$$;

COMMENT ON FUNCTION job_executions_ensure_partition(date) IS
    'Crea la partizione giornaliera di job_executions, se manca. Idempotente.';

-- job_executions_ensure_partitions prepara la finestra di partizioni attorno a
-- oggi e restituisce quante ne ha create o confermate.
--
-- Va eseguita periodicamente dalla manutenzione: **senza una partizione
-- disponibile l'inserimento fallisce**. È deliberato che fallisca invece di
-- finire in una partizione di default: un log che sparisce in un contenitore
-- generico si scopre settimane dopo, un inserimento che si rifiuta si scopre
-- subito. Il margine predefinito di due settimane serve proprio a rendere il
-- problema visibile con largo anticipo.
CREATE FUNCTION job_executions_ensure_partitions(
    days_ahead integer DEFAULT 14,
    days_behind integer DEFAULT 1
) RETURNS integer
    LANGUAGE plpgsql
    SET search_path = public, pg_catalog
AS $$
DECLARE
    day date;
    created integer := 0;
BEGIN
    IF days_ahead < 0 OR days_behind < 0 THEN
        RAISE EXCEPTION 'days_ahead e days_behind non possono essere negativi';
    END IF;

    FOR day IN
        SELECT generate_series(
            (now() AT TIME ZONE 'UTC')::date - days_behind,
            (now() AT TIME ZONE 'UTC')::date + days_ahead,
            interval '1 day'
        )::date
    LOOP
        PERFORM job_executions_ensure_partition(day);
        created := created + 1;
    END LOOP;

    RETURN created;
END
$$;

COMMENT ON FUNCTION job_executions_ensure_partitions(integer, integer) IS
    'Prepara la finestra di partizioni giornaliere attorno a oggi. Da eseguire dalla manutenzione periodica.';

-- job_executions_drop_partitions_before applica la retention lunga (R6): elimina
-- le partizioni interamente più vecchie della data indicata.
--
-- Il chiamante passa `oggi - max(log_retention_days)` fra i piani con
-- sottoscrizioni vive. Le retention più corte non si applicano qui: una
-- partizione contiene le esecuzioni di tutti gli utenti, e va eliminata solo
-- quando è superflua per l'ultimo di loro.
CREATE FUNCTION job_executions_drop_partitions_before(cutoff date) RETURNS integer
    LANGUAGE plpgsql
    SET search_path = public, pg_catalog
AS $$
DECLARE
    partition record;
    dropped integer := 0;
BEGIN
    FOR partition IN
        SELECT c.relname
        FROM pg_class c
        JOIN pg_inherits i ON i.inhrelid = c.oid
        WHERE i.inhparent = 'job_executions'::regclass
          AND c.relname ~ '^job_executions_[0-9]{8}$'
          -- La partizione copre [giorno, giorno + 1): è interamente anteriore
          -- al taglio solo se il giorno stesso lo precede.
          AND to_date(right(c.relname, 8), 'YYYYMMDD') < cutoff
    LOOP
        EXECUTE format('DROP TABLE %I', partition.relname);
        dropped := dropped + 1;
    END LOOP;

    RETURN dropped;
END
$$;

COMMENT ON FUNCTION job_executions_drop_partitions_before(date) IS
    'Elimina le partizioni di job_executions interamente più vecchie del taglio (retention, R6).';

-- Finestra iniziale, così che il motore possa scrivere dal primo dispatch.
SELECT job_executions_ensure_partitions();
