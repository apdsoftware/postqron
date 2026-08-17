-- 0011 — Consegne del webhook GitHub (R11).
--
-- La 0004 ha creato `repositories`, che descrive *quali* repository seguiamo e
-- com'è andata l'ultima riconciliazione. Non basta per R11: `last_synced_commit`
-- dice a che punto è arrivato il sync, non quali consegne abbiamo già ricevuto.
-- Sono cose diverse, e la differenza si vede proprio nei casi che questa tabella
-- deve coprire:
--
--   * GitHub **ripete** la stessa consegna — automaticamente su errore, e a mano
--     dal registro dell'App. La ripetizione porta lo stesso `X-GitHub-Delivery`,
--     quindi l'identificativo della consegna è la chiave dell'idempotenza;
--   * una consegna arriva anche per un repository che nessuno ha collegato (la
--     App è installata su un account intero), e per eventi che non sono `push`.
--     Nessuna di queste ha un `repositories.id` a cui appoggiarsi.
--
-- Per la stessa ragione **non c'è una chiave esterna verso `repositories`**: una
-- consegna per un repository sconosciuto verrebbe rifiutata dal database, il
-- servizio risponderebbe 500 e GitHub la ripeterebbe all'infinito. Il legame è
-- per `repository_external_id`, che è l'identificativo numerico lato provider —
-- lo stesso su cui la 0004 ha già creato `repositories_external_id_idx`.

-- Stato di lavorazione di una consegna. Dominio chiuso, quindi un tipo
-- enumerato come nella 0001: uno stato scritto male dev'essere rifiutato dal
-- database, non conservato.
--
--   received   presa in carico da una richiesta, lavorazione in corso
--   processed  consegnata al consumatore degli eventi push (#422) senza errori
--   ignored    verificata ma senza niente da fare: evento non `push`, oppure
--              nessun consumatore configurato
--   failed     la lavorazione è fallita; è l'unico stato da cui una ripetizione
--              della stessa consegna viene rilavorata invece che scartata
CREATE TYPE github_delivery_status AS ENUM ('received', 'processed', 'ignored', 'failed');

CREATE TABLE github_webhook_deliveries (
    -- `X-GitHub-Delivery`: identificativo della consegna, uguale per la consegna
    -- originale e per ogni sua ripetizione. È la chiave primaria e non una
    -- colonna qualsiasi con un indice unico, perché è *l'unica* identità che
    -- questa riga ha: l'idempotenza di R11 è un conflitto su questa chiave.
    --
    -- Il tipo è `text` e non `uuid` benché oggi GitHub mandi un UUID: il valore
    -- arriva dall'esterno, e un formato diverso deve poter essere registrato e
    -- deduplicato invece di far fallire l'inserimento.
    delivery_id text PRIMARY KEY
        CHECK (delivery_id <> '' AND char_length(delivery_id) <= 100),

    -- `X-GitHub-Event`. Registriamo anche gli eventi che non ci riguardano: la
    -- deduplicazione dev'essere uniforme, e il registro serve a capire cosa è
    -- arrivato davvero quando una sincronizzazione non parte.
    event text NOT NULL CHECK (event <> '' AND char_length(event) <= 50),

    status github_delivery_status NOT NULL DEFAULT 'received',

    -- Installazione della GitHub App che ha generato l'evento: è quella che
    -- consente di ottenere il token con cui #422 leggerà `cron.yaml`. Non è un
    -- segreto. Nullo sugli eventi che non ce l'hanno (`ping` di una App appena
    -- creata).
    installation_id bigint CHECK (installation_id > 0),

    -- Identità del repository lato provider, come in `repositories`: sopravvive
    -- a rinomine e trasferimenti, mentre `owner/name` no. Il nome completo resta
    -- accanto perché è ciò che rende leggibile il registro.
    repository_external_id bigint CHECK (repository_external_id > 0),
    repository_full_name text
        CHECK (repository_full_name <> '' AND char_length(repository_full_name) <= 201),

    -- Riferimento spinto e commit di testa. `ref` è completo
    -- (`refs/heads/main`): quale ramo conti lo decide la riconciliazione (#423)
    -- confrontandolo con `repositories.default_branch`, non questa tabella.
    ref text CHECK (ref <> '' AND char_length(ref) <= 255),

    -- Il commit dopo la push. La cancellazione di un ramo lo porta a quaranta
    -- zeri, che è un valore legale per questo vincolo ed è giusto conservare.
    head_commit text CHECK (head_commit ~ '^[0-9a-f]{7,40}$'),

    -- Quante volte questa consegna è stata presa in carico. Cresce solo quando
    -- una ripetizione rilavora un fallimento: un valore alto è il segnale che
    -- qualcosa non va, ed è l'unica traccia che resta dei tentativi andati male.
    attempts integer NOT NULL DEFAULT 1 CHECK (attempts > 0),

    received_at timestamptz NOT NULL DEFAULT now(),
    processed_at timestamptz,

    -- Motivo del fallimento, per chi indaga. **Non contiene il payload**: il
    -- corpo di una consegna arriva da fuori e non va conservato qui.
    error_message text,

    CONSTRAINT github_webhook_deliveries_error_check
        CHECK (status IS DISTINCT FROM 'failed' OR error_message IS NOT NULL)
);

COMMENT ON TABLE github_webhook_deliveries IS 'Consegne del webhook GitHub già ricevute: è il registro su cui poggia l''idempotenza di R11.';
COMMENT ON COLUMN github_webhook_deliveries.delivery_id IS 'Valore di `X-GitHub-Delivery`; identico fra consegna originale e ripetizioni.';
COMMENT ON COLUMN github_webhook_deliveries.error_message IS 'Motivo del fallimento; mai il contenuto della consegna.';

-- «Le ultime consegne di questo repository»: è la lettura della diagnostica di
-- #422/#423 quando un sync non parte. Parziale perché gli eventi senza
-- repository (`ping`) non la servono e non devono farla crescere.
CREATE INDEX github_webhook_deliveries_repository_idx
    ON github_webhook_deliveries (repository_external_id, received_at DESC)
    WHERE repository_external_id IS NOT NULL;

-- Serve alla pulizia periodica, l'unica a scandire la tabella per data.
CREATE INDEX github_webhook_deliveries_received_at_idx
    ON github_webhook_deliveries (received_at);

-- ------------------------------------------------------------ manutenzione

-- Rimuove le consegne più vecchie di `grace`.
--
-- La retention non è una preferenza: **è il periodo in cui la deduplicazione
-- funziona**. Cancellata la riga, la stessa consegna ripetuta risulta nuova e
-- viene rilavorata. `grace` va quindi tenuto sopra la finestra entro cui GitHub
-- consente di ripetere una consegna dal registro dell'App — trenta giorni le
-- stanno comodamente sopra.
--
-- È una funzione e non un DELETE sparso nel codice per la stessa ragione della
-- 0009: la condizione va scritta una volta sola.
CREATE FUNCTION github_webhook_deliveries_purge(grace interval DEFAULT interval '30 days')
    RETURNS bigint
    LANGUAGE plpgsql
    SET search_path = pg_catalog, public
AS $$
DECLARE
    removed bigint;
BEGIN
    IF grace < interval '0' THEN
        RAISE EXCEPTION 'grace non può essere negativo: %', grace;
    END IF;

    DELETE FROM github_webhook_deliveries WHERE received_at < now() - grace;

    GET DIAGNOSTICS removed = ROW_COUNT;
    RETURN removed;
END
$$;

COMMENT ON FUNCTION github_webhook_deliveries_purge(interval) IS 'Elimina le consegne più vecchie di `grace`; oltre quella soglia la deduplicazione di R11 non vale più.';
