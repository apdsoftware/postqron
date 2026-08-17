-- 0002 — Utenti e chiavi API (R9, R14).

CREATE TABLE users (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),

    -- L'indirizzo è conservato come l'utente lo ha scritto; l'unicità è
    -- verificata sulla forma minuscola (indice più sotto), perché la parte a
    -- destra della chiocciola è insensibile alle maiuscole e i provider reali
    -- trattano così anche la parte a sinistra.
    email text NOT NULL
        CONSTRAINT users_email_format_check
        CHECK (email ~ '^[^@[:space:]]+@[^@[:space:]]+\.[^@[:space:]]+$'),
    email_verified_at timestamptz,

    -- Hash della password, mai la password. NULL è ammesso perché un account
    -- creato da un futuro provider OAuth non ne ha una.
    password_hash text CHECK (password_hash <> ''),

    full_name text,
    role user_role NOT NULL DEFAULT 'user',

    -- Fuso di presentazione dell'utente: le date in dashboard ed email si
    -- rendono qui. Non ha effetto sullo scheduling, che usa il fuso del job (R2).
    timezone text NOT NULL DEFAULT 'UTC' CHECK (timezone <> ''),

    last_login_at timestamptz,
    suspended_at timestamptz,

    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),

    -- Cancellazione logica: le esecuzioni e l'audit log di un account chiuso
    -- restano consultabili finché la retention lo consente.
    deleted_at timestamptz
);

COMMENT ON TABLE users IS 'Account applicativi (R14).';
COMMENT ON COLUMN users.password_hash IS 'Hash della password; il valore in chiaro non esiste a riposo (SPEC §5).';

-- L'unicità vale fra gli account vivi: un indirizzo liberato da una
-- cancellazione torna disponibile per una nuova registrazione.
CREATE UNIQUE INDEX users_email_key ON users (lower(email)) WHERE deleted_at IS NULL;

CREATE TRIGGER users_set_updated_at
    BEFORE UPDATE ON users
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- ---------------------------------------------------------------- api_keys

CREATE TABLE api_keys (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,

    name text NOT NULL CHECK (name <> '' AND char_length(name) <= 100),

    -- Prefisso mostrabile della chiave (per esempio `pq_live_a1b2`): serve solo
    -- a farla riconoscere in elenco. Non è un segreto e non basta a ricostruirla.
    prefix text NOT NULL CHECK (char_length(prefix) BETWEEN 4 AND 32),

    -- Hash della chiave. Il valore in chiaro è mostrato una sola volta alla
    -- creazione e non viene più conservato da nessuna parte (R9).
    key_hash text NOT NULL CHECK (char_length(key_hash) >= 32),

    -- Scope in forma `risorsa:azione` (per esempio `jobs:write`). Un array
    -- vuoto è una chiave senza permessi: legittima, ma inutile finché non le si
    -- assegna qualcosa.
    scopes text[] NOT NULL DEFAULT '{}'::text[] CHECK (has_unique_elements(scopes)),

    last_used_at timestamptz,
    expires_at timestamptz,
    revoked_at timestamptz,

    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

COMMENT ON TABLE api_keys IS 'Chiavi API per l''autenticazione delle richieste (R9).';
COMMENT ON COLUMN api_keys.key_hash IS 'Hash della chiave: il valore in chiaro è mostrato una sola volta e mai conservato (R9).';

-- L'autenticazione cerca per hash: è la lettura più frequente dell'API, e
-- l'unicità dell'indice è anche la garanzia che due chiavi non collidano.
CREATE UNIQUE INDEX api_keys_key_hash_key ON api_keys (key_hash);

-- Elenco delle chiavi vive di un utente: l'indice parziale ignora le revocate,
-- che restano in tabella come traccia storica ma non servono più a nessuna query.
CREATE INDEX api_keys_active_by_user_idx ON api_keys (user_id) WHERE revoked_at IS NULL;

CREATE TRIGGER api_keys_set_updated_at
    BEFORE UPDATE ON api_keys
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();
