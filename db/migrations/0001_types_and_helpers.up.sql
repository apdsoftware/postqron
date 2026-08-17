-- 0001 — Tipi enumerati e funzioni di supporto.
--
-- I domini chiusi del prodotto sono tipi enumerati, non stringhe libere: un
-- valore fuori dominio deve essere rifiutato dal database, non solo dal codice
-- che in quel momento lo scrive. Le migrazioni successive li usano; questa non
-- crea tabelle.

-- Ambienti di esecuzione (SPEC §8, R23). Il piano Free ha un ambiente solo e
-- usa `production`; dal piano Pro in su un job può appartenere a entrambi, con
-- routing e alert separati.
CREATE TYPE environment AS ENUM ('staging', 'production');

-- Ruolo applicativo dell'utente. `admin` accede alla dashboard amministrativa
-- e può impersonare (SPEC §4.3), sempre con traccia in audit_log.
CREATE TYPE user_role AS ENUM ('user', 'admin');

-- Metodi HTTP ammessi come target di un job. PostQron esegue esclusivamente
-- chiamate HTTP: nessun comando, nessun container (SPEC §10).
CREATE TYPE http_method AS ENUM ('GET', 'POST', 'PUT', 'PATCH', 'DELETE', 'HEAD', 'OPTIONS');

-- Politica di attesa fra un tentativo e il successivo (R5). `exponential` è il
-- default della spec; le altre due esistono perché aggiungere un valore a un
-- enum non è reversibile dentro una transazione, quindi il dominio si dichiara
-- per intero adesso.
CREATE TYPE retry_backoff AS ENUM ('exponential', 'linear', 'fixed');

-- Stato di un singolo tentativo di esecuzione (R6).
-- `skipped` copre l'occorrenza non eseguita per decisione del motore — job
-- disabilitato al momento del dispatch, quota di piano esaurita, esecuzione
-- precedente ancora in corso.
CREATE TYPE execution_status AS ENUM (
    'pending',
    'running',
    'succeeded',
    'failed',
    'timed_out',
    'skipped'
);

-- Origine del tentativo: la schedulazione, un trigger manuale da API o
-- dashboard (R8), oppure il retry di un tentativo fallito (R5).
CREATE TYPE execution_trigger AS ENUM ('schedule', 'manual', 'retry');

-- Stato della sottoscrizione, allineato al ciclo di vita di Paddle (R16).
CREATE TYPE subscription_status AS ENUM (
    'trialing',
    'active',
    'past_due',
    'paused',
    'canceled'
);

CREATE TYPE billing_period AS ENUM ('monthly', 'yearly');

-- Canali di notifica (R21, R29). `webhook` è il canale generico per un
-- endpoint dell'utente; Slack e Discord sono distinti perché il payload e la
-- firma cambiano.
CREATE TYPE alert_channel AS ENUM ('email', 'slack', 'discord', 'webhook');

-- Eventi che generano una notifica (R21).
CREATE TYPE notification_event AS ENUM (
    'welcome',
    'job_failed',
    'job_recovered',
    'plan_changed',
    'security'
);

CREATE TYPE notification_status AS ENUM ('pending', 'sent', 'failed', 'skipped');

-- Provider di hosting del repository che contiene `cron.yaml` (R11–R13). Oggi
-- solo GitHub.
CREATE TYPE repository_provider AS ENUM ('github');

-- Esito dell'ultima riconciliazione di un repository (R13).
CREATE TYPE sync_status AS ENUM ('pending', 'succeeded', 'failed');

-- Provider AI per il debugging BYOK (R18, R30). La chiave resta dell'utente,
-- cifrata a riposo.
CREATE TYPE ai_provider AS ENUM ('anthropic', 'openai', 'google');

-- has_unique_elements verifica che un array non contenga duplicati.
--
-- Serve nei CHECK sulle colonne array (ambienti di un job, canali di alert,
-- scope di una API key): un `environments: [production, production]` è un
-- errore di configurazione, e va fermato qui invece di produrre due esecuzioni
-- identiche per la stessa occorrenza.
--
-- Un CHECK non può contenere una subquery, ma può chiamare una funzione: è il
-- motivo per cui il controllo vive qui e non inline.
CREATE FUNCTION has_unique_elements(elements anyarray) RETURNS boolean
    LANGUAGE sql
    IMMUTABLE
    PARALLEL SAFE
    STRICT
    SET search_path = pg_catalog
AS $$
    SELECT cardinality(elements) = (SELECT count(DISTINCT e) FROM unnest(elements) AS e)
$$;

-- set_updated_at mantiene `updated_at` allineato senza dipendere dal chiamante.
-- Un aggiornamento fatto a mano in psql deve lasciare la stessa traccia di uno
-- fatto dall'API.
CREATE FUNCTION set_updated_at() RETURNS trigger
    LANGUAGE plpgsql
    SET search_path = pg_catalog
AS $$
BEGIN
    NEW.updated_at := now();
    RETURN NEW;
END
$$;
