-- 0007 — Chiavi AI dell'utente, BYOK (R18, R30).
--
-- La chiave è dell'utente e resta illeggibile a chi guarda il database: qui
-- entra solo il testo cifrato. Nessuna colonna contiene il valore in chiaro,
-- nemmeno parzialmente — `last_four` sono le ultime quattro cifre, che servono
-- a farla riconoscere in dashboard e non bastano a ricostruirla.

CREATE TABLE ai_credentials (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,

    provider ai_provider NOT NULL,
    label text CHECK (label <> '' AND char_length(label) <= 100),

    -- Chiave cifrata e nonce della cifratura, separati perché il nonce non è
    -- segreto e va cambiato a ogni scrittura.
    ciphertext bytea NOT NULL CHECK (octet_length(ciphertext) > 0),
    nonce bytea NOT NULL CHECK (octet_length(nonce) > 0),

    -- Versione della chiave di cifratura usata. Serve alla rotazione: durante
    -- una rotazione convivono righe cifrate con chiavi diverse, e senza questo
    -- numero non si saprebbe con quale decifrare quale.
    key_version smallint NOT NULL DEFAULT 1 CHECK (key_version >= 1),

    -- Ultime quattro cifre, per il riconoscimento in dashboard.
    last_four text CHECK (last_four ~ '^[A-Za-z0-9]{4}$'),

    last_used_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),

    -- Una chiave per provider: un secondo inserimento è un aggiornamento, non
    -- una seconda credenziale da dover disambiguare al momento dell'uso.
    CONSTRAINT ai_credentials_user_provider_key UNIQUE (user_id, provider)
);

COMMENT ON TABLE ai_credentials IS
    'Chiavi AI degli utenti (BYOK, R18). Cifrate a riposo, mai loggate, mai restituite in chiaro dall''API.';
COMMENT ON COLUMN ai_credentials.ciphertext IS 'Chiave cifrata. Il valore in chiaro non esiste a riposo (R18).';
COMMENT ON COLUMN ai_credentials.key_version IS 'Versione della chiave di cifratura, per la rotazione.';

CREATE TRIGGER ai_credentials_set_updated_at
    BEFORE UPDATE ON ai_credentials
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();
