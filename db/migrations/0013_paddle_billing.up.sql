-- 0013 — Fatturazione Paddle: consegne del webhook, intenti di checkout,
-- sospensione dei job al cambio di piano (R16, R58, R63).
--
-- La 0003 aveva già `plans` e `subscriptions`, cioè *dove* vive il piano. Qui
-- arriva ciò che lo fa cambiare da solo — i webhook di Paddle — e ciò che il
-- cambio provoca sui job dell'utente. Sono tre cose distinte e il confine fra
-- loro conta:
--
--   * `paddle_webhook_events` è il registro dell'idempotenza. Paddle **ripete**
--     le consegne, e le ripetizioni arrivano anche fuori ordine: senza un
--     registro, due copie dello stesso evento producono due upgrade;
--   * `paddle_checkout_intents` conserva la conferma di uso professionale che
--     R63 pretende al checkout. Una conferma raccolta e non registrata non è
--     una conferma: è una casella disegnata;
--   * le due colonne nuove di `jobs` distinguono la pausa decisa dall'utente
--     dalla sospensione decisa da un cambio di piano (R58).

-- ------------------------------------------------------- stato delle consegne

-- Dominio chiuso come nella 0011: uno stato scritto male dev'essere rifiutato
-- dal database, non conservato.
--
--   received   presa in carico da una richiesta, lavorazione in corso
--   processed  applicata agli entitlement
--   ignored    verificata ma senza niente da fare: evento non sottoscritto,
--              oppure più vecchio di ciò che è già stato applicato
--   failed     la lavorazione è fallita; è l'unico stato da cui una ripetizione
--              dello stesso evento viene rilavorata invece che scartata
CREATE TYPE paddle_event_status AS ENUM ('received', 'processed', 'ignored', 'failed');

CREATE TABLE paddle_webhook_events (
    -- `event_id` del payload (`evt_...`): identico fra la consegna originale e
    -- ogni sua ripetizione, ed è quindi *l'unica* identità che questa riga ha.
    -- Chiave primaria e non colonna con indice unico, per la stessa ragione
    -- della 0011: l'idempotenza è un conflitto su questa chiave.
    event_id text PRIMARY KEY
        CHECK (event_id <> '' AND char_length(event_id) <= 100),

    -- `event_type`: `subscription.updated`, `transaction.completed`, ...
    -- Registriamo anche i tipi che non trattiamo: la deduplicazione dev'essere
    -- uniforme, e il registro serve a capire cosa è arrivato davvero quando un
    -- entitlement non cambia.
    event_type text NOT NULL CHECK (event_type <> '' AND char_length(event_type) <= 100),

    status paddle_event_status NOT NULL DEFAULT 'received',

    -- `occurred_at` del payload: il momento in cui il fatto è avvenuto **da
    -- Paddle**, non quello in cui è arrivato da noi. È la differenza su cui
    -- poggia l'ordinamento: `received_at` mette in fila le consegne nell'ordine
    -- in cui la rete ce le ha portate, che è precisamente l'ordine di cui non ci
    -- si può fidare.
    occurred_at timestamptz NOT NULL,

    -- Identità Paddle a cui l'evento si riferisce, per chi indaga. Nulle sugli
    -- eventi che non ne hanno.
    paddle_subscription_id text CHECK (paddle_subscription_id <> ''),
    paddle_customer_id text CHECK (paddle_customer_id <> ''),

    -- Quante volte questa consegna è stata presa in carico. Cresce solo quando
    -- una ripetizione rilavora un fallimento: un valore alto è il segnale che
    -- qualcosa non va.
    attempts integer NOT NULL DEFAULT 1 CHECK (attempts > 0),

    received_at timestamptz NOT NULL DEFAULT now(),
    processed_at timestamptz,

    -- Motivo del fallimento, per chi indaga. **Non contiene il payload**: il
    -- corpo di una consegna arriva da fuori, porta dati di fatturazione, e non
    -- va conservato qui.
    error_message text,

    CONSTRAINT paddle_webhook_events_error_check
        CHECK (status IS DISTINCT FROM 'failed' OR error_message IS NOT NULL)
);

COMMENT ON TABLE paddle_webhook_events IS 'Eventi Paddle già ricevuti: è il registro su cui poggia l''idempotenza di R16.';
COMMENT ON COLUMN paddle_webhook_events.occurred_at IS 'Istante del fatto secondo Paddle; è l''ordine di cui fidarsi, non quello di arrivo.';
COMMENT ON COLUMN paddle_webhook_events.error_message IS 'Motivo del fallimento; mai il contenuto della consegna.';

-- «Gli ultimi eventi di questa sottoscrizione»: è la lettura della diagnostica
-- quando un piano non cambia. Parziale perché gli eventi senza sottoscrizione
-- non la servono e non devono farla crescere.
CREATE INDEX paddle_webhook_events_subscription_idx
    ON paddle_webhook_events (paddle_subscription_id, occurred_at DESC)
    WHERE paddle_subscription_id IS NOT NULL;

-- Serve alla pulizia periodica, l'unica a scandire la tabella per data.
CREATE INDEX paddle_webhook_events_received_at_idx ON paddle_webhook_events (received_at);

-- Rimuove gli eventi più vecchi di `grace`.
--
-- Come nella 0011, la retention **è il periodo in cui la deduplicazione
-- funziona**: cancellata la riga, lo stesso evento ripetuto risulta nuovo e
-- viene rilavorato. Paddle ritenta una consegna per circa tre giorni e conserva
-- gli eventi per un mese; novanta giorni stanno sopra entrambi con margine, e la
-- tabella resta piccola perché ogni riga è una manciata di byte.
CREATE FUNCTION paddle_webhook_events_purge(grace interval DEFAULT interval '90 days')
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

    DELETE FROM paddle_webhook_events WHERE received_at < now() - grace;

    GET DIAGNOSTICS removed = ROW_COUNT;
    RETURN removed;
END
$$;

COMMENT ON FUNCTION paddle_webhook_events_purge(interval) IS 'Elimina gli eventi più vecchi di `grace`; oltre quella soglia la deduplicazione di R16 non vale più.';

-- ------------------------------------------------- ordinamento degli eventi

-- Filigrana dell'ultimo evento applicato a questa sottoscrizione.
--
-- La deduplicazione per `event_id` copre la **seconda copia dello stesso
-- evento**; non copre il caso peggiore, che è un evento *diverso* e più vecchio
-- che arriva dopo uno più recente. Paddle ritenta con backoff: un
-- `subscription.updated` fallito e ripetuto dieci minuti dopo può atterrare
-- dopo la cancellazione che lo seguiva, e riportare in vita un piano a pagamento
-- che l'utente non ha più.
--
-- Il confronto va fatto **nella stessa istruzione che aggiorna**, non prima:
-- letto e poi confrontato in Go, due consegne concorrenti passerebbero entrambe.
ALTER TABLE subscriptions
    ADD COLUMN paddle_event_occurred_at timestamptz,
    -- Prezzo Paddle attualmente in forza. È un riferimento, non un importo (vedi
    -- l'intestazione della 0003): serve a sapere *quale* riga di listino la
    -- sottoscrizione sta pagando quando il piano da solo non basta — Pro mensile
    -- e Pro annuale sono lo stesso piano con due prezzi (R62).
    ADD COLUMN paddle_price_id text CHECK (paddle_price_id <> '');

COMMENT ON COLUMN subscriptions.paddle_event_occurred_at IS
    'Istante dell''ultimo evento Paddle applicato: un evento più vecchio di questo non retrocede la riga (R16).';

-- ------------------------------------------------------- intenti di checkout

-- La conferma di uso professionale, registrata (R63).
--
-- R63 dice tre cose sul checkout: che chiede **conferma esplicita** di agire
-- nell'esercizio di un'attività, che raccoglie la partita IVA **dove esiste**, e
-- che il vincolo sta all'acquisto e non alla registrazione. Le prime due hanno
-- bisogno di un posto in cui restare — una conferma che non lascia traccia non è
-- opponibile a nessuno — e questa tabella è quel posto.
--
-- Non è una sottoscrizione e non diventa tale: è ciò che l'utente ha dichiarato
-- *prima* di aprire il checkout. Il collegamento con l'acquisto, se avviene, lo
-- fa l'evento di Paddle, che porta il nostro identificativo in `custom_data`.
CREATE TABLE paddle_checkout_intents (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,

    plan_code text NOT NULL REFERENCES plans (code) ON UPDATE CASCADE ON DELETE RESTRICT,
    billing_period billing_period NOT NULL,

    -- Il prezzo Paddle che il checkout aprirà. Riferimento, non importo: gli
    -- importi sono di Paddle (R61) e non ne teniamo copia.
    paddle_price_id text NOT NULL CHECK (paddle_price_id <> ''),

    -- La conferma di R63. `NOT NULL` con `CHECK (business_use)` e non un
    -- booleano libero: una riga di questa tabella esiste **solo** se la conferma
    -- c'è stata. Un intento senza conferma non è un intento da conservare, è una
    -- richiesta che va rifiutata prima di arrivare qui.
    business_use boolean NOT NULL CHECK (business_use),

    -- Partita IVA, **dove esiste**. R63 è esplicito sul fatto che non vada resa
    -- obbligatoria: diversi regimi minimi europei ne sono privi, e pretenderla
    -- escluderebbe acquirenti legittimi. NULL è quindi un valore normale, non un
    -- dato mancante.
    vat_number text CHECK (vat_number <> '' AND char_length(vat_number) <= 64),

    created_at timestamptz NOT NULL DEFAULT now()
);

COMMENT ON TABLE paddle_checkout_intents IS 'Conferma di uso professionale e dati di fatturazione raccolti prima del checkout (R63).';
COMMENT ON COLUMN paddle_checkout_intents.vat_number IS 'Partita IVA dove esiste; NULL è normale (R63), non un dato mancante.';

-- «L'ultimo intento di questo utente»: è la lettura con cui si ricostruisce cosa
-- ha dichiarato chi ha comprato.
CREATE INDEX paddle_checkout_intents_user_idx
    ON paddle_checkout_intents (user_id, created_at DESC);

-- ------------------------------------------------------ sospensione dei job

-- Perché un job è stato sospeso da noi (R58).
--
-- Il valore non descrive la *causa a monte* — downgrade, mancato pagamento,
-- scadenza — ma **il vincolo che il job viola**, perché è quello a decidere cosa
-- l'utente deve fare per riaccenderlo, ed è l'unica cosa che l'interfaccia ha
-- bisogno di dirgli:
--
--   plan_job_limit  i job attivi superavano il tetto del piano di destinazione.
--                   Rimedio: riaccenderne quanti il piano ne consente. La scelta
--                   è dell'utente, e R58 spiega per esteso perché non è nostra.
--   plan_resolution la schedulazione è più fitta di quanto il piano consenta.
--                   Rimedio: **cambiare la schedulazione**. Non c'è scelta da
--                   fare e riaccenderne un altro non libera posto: è un vincolo
--                   indipendente dal numero.
CREATE TYPE job_suspension_reason AS ENUM ('plan_job_limit', 'plan_resolution');

ALTER TABLE jobs
    ADD COLUMN suspended_at timestamptz,
    ADD COLUMN suspended_reason job_suspension_reason,

    -- I due valori vivono e muoiono insieme: una sospensione senza motivo non
    -- saprebbe cosa dire all'utente, e un motivo senza sospensione descrive
    -- qualcosa che non è successo.
    ADD CONSTRAINT jobs_suspension_check
        CHECK ((suspended_at IS NULL) = (suspended_reason IS NULL));

COMMENT ON COLUMN jobs.suspended_at IS
    'Quando un cambio di piano ha spento il job (R58). Distinto da `enabled = false`, che è la pausa decisa dall''utente.';
COMMENT ON COLUMN jobs.suspended_reason IS
    'Quale vincolo di piano il job viola, cioè cosa serve per riaccenderlo (R58).';

-- «Quali job ho sospeso a questo utente, e perché»: è la lettura della schermata
-- che gli chiede di scegliere quali riaccendere. Parziale perché i job non
-- sospesi — la grande maggioranza — non la servono.
CREATE INDEX jobs_suspended_idx
    ON jobs (user_id, suspended_at DESC)
    WHERE suspended_at IS NOT NULL;
