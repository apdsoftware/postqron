-- 0004 — Repository collegati per il sync di `cron.yaml` (R11–R13).

CREATE TABLE repositories (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,

    provider repository_provider NOT NULL DEFAULT 'github',

    -- Identificativo dell'installazione della GitHub App sull'account
    -- dell'utente: è quello che consente di ottenere un token per leggere il
    -- file. Non è un segreto.
    installation_id bigint CHECK (installation_id > 0),

    -- Identificativo numerico del repository lato provider: sopravvive a
    -- rinomine e trasferimenti, mentre `owner`/`name` no.
    external_id bigint CHECK (external_id > 0),

    owner text NOT NULL CHECK (owner <> '' AND char_length(owner) <= 100),
    name text NOT NULL CHECK (name <> '' AND char_length(name) <= 100),

    default_branch text NOT NULL DEFAULT 'main' CHECK (default_branch <> ''),

    -- Percorso del file dentro il repository, relativo alla radice. Lo slash
    -- iniziale è rifiutato: `cron.yaml` e `/cron.yaml` indicherebbero lo stesso
    -- file con due chiavi diverse.
    config_path text NOT NULL DEFAULT 'cron.yaml'
        CHECK (config_path <> '' AND config_path NOT LIKE '/%'),

    enabled boolean NOT NULL DEFAULT true,

    -- Esito dell'ultima riconciliazione (R13). Un errore di parsing lascia
    -- intatti i job esistenti e si limita a comparire qui, con il commit su cui
    -- si è fermato: lo stato non viene mai corrotto da un file non valido.
    last_synced_at timestamptz,
    last_synced_commit text CHECK (last_synced_commit ~ '^[0-9a-f]{7,40}$'),
    last_sync_status sync_status,
    last_sync_error text,

    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT repositories_sync_error_check
        CHECK (last_sync_status IS DISTINCT FROM 'failed' OR last_sync_error IS NOT NULL)
);

COMMENT ON TABLE repositories IS 'Repository che contengono un `cron.yaml` sincronizzato (R11–R13).';
COMMENT ON COLUMN repositories.last_sync_error IS 'Errore di parsing o di sync, riportato all''utente; lo stato dei job resta quello precedente (R13).';

-- Su GitHub owner e nome sono insensibili alle maiuscole: `Acme/Api` e
-- `acme/api` sono lo stesso repository e non devono poter essere collegati due
-- volte dallo stesso utente. Utenti diversi possono collegare lo stesso
-- repository pubblico, quindi l'unicità è per utente.
CREATE UNIQUE INDEX repositories_identity_key
    ON repositories (user_id, provider, lower(owner), lower(name));

CREATE INDEX repositories_user_id_idx ON repositories (user_id);

-- Il webhook arriva con l'identificativo del repository lato provider e deve
-- risalire a tutte le installazioni che lo seguono.
CREATE INDEX repositories_external_id_idx
    ON repositories (provider, external_id)
    WHERE external_id IS NOT NULL;

CREATE TRIGGER repositories_set_updated_at
    BEFORE UPDATE ON repositories
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();
