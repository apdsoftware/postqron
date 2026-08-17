-- 0009 — Sessioni utente e token monouso (R14).
--
-- La 0002 ha creato `users` con `password_hash` ed `email_verified_at`, ma non
-- il resto di R14: una sessione va conservata da qualche parte, e un link di
-- recupero password è un segreto con scadenza e con un solo uso ammesso.
-- Nessuna delle due cosa entra in `users` — sono relazioni uno-a-molti — quindi
-- servono due tabelle nuove.

-- Scopo di un token monouso. Due valori oggi, un tipo enumerato perché il
-- dominio è chiuso e un `purpose = 'passwordreset'` scritto male deve essere
-- rifiutato dal database (stessa logica della 0001).
CREATE TYPE user_token_purpose AS ENUM ('email_verification', 'password_reset');

-- ---------------------------------------------------------------- sessions

CREATE TABLE sessions (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,

    -- HMAC-SHA256 del token di sessione, in esadecimale. Il valore in chiaro
    -- vive solo nel cookie del browser: da questa colonna non si risale al
    -- token, quindi un dump del database non contiene sessioni utilizzabili.
    --
    -- Non è un hash di password e non usa Argon2: il token è generato da CSPRNG
    -- con 256 bit di entropia, quindi non c'è nulla da indovinare e un KDF
    -- lento andrebbe pagato a ogni richiesta autenticata, non una volta per
    -- login. La chiave dell'HMAC è derivata da SESSION_SECRET: ruotarla
    -- invalida tutte le sessioni in un colpo.
    token_hash text NOT NULL CONSTRAINT sessions_token_hash_format_check
        CHECK (token_hash ~ '^[0-9a-f]{64}$'),

    created_at timestamptz NOT NULL DEFAULT now(),

    -- Ultimo utilizzo: da qui si misura la scadenza per inattività, che è
    -- distinta dalla scadenza assoluta. Una sessione usata ogni giorno muore
    -- comunque a `expires_at`; una abbandonata muore prima.
    last_used_at timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz NOT NULL,

    -- Revoca esplicita: logout, cambio password, «chiudi le altre sessioni».
    -- La riga resta come traccia finché la pulizia periodica non la rimuove:
    -- l'elenco dei dispositivi in dashboard ha bisogno di sapere che una
    -- sessione è stata chiusa, non solo che non esiste più.
    revoked_at timestamptz,

    -- Contesto della sessione, mostrato nell'elenco «dove sono collegato».
    -- Sono gli stessi due campi che `audit_log` (0008) conserva per gli eventi
    -- sensibili: qui servono a far riconoscere all'utente una sessione che non
    -- è sua, che è l'unico modo perché possa revocarla.
    ip_address inet,
    user_agent text,

    CONSTRAINT sessions_expiry_check CHECK (expires_at > created_at)
);

COMMENT ON TABLE sessions IS 'Sessioni di login (R14). Il token in chiaro non è conservato.';
COMMENT ON COLUMN sessions.token_hash IS 'HMAC-SHA256 del token di sessione; la chiave deriva da SESSION_SECRET (SPEC §5).';

-- L'autenticazione di ogni richiesta cerca per hash: è la lettura più frequente
-- dell'API. L'unicità è anche la garanzia che due sessioni non collidano.
CREATE UNIQUE INDEX sessions_token_hash_key ON sessions (token_hash);

-- Elenco delle sessioni vive di un utente, dalla più recente. L'indice parziale
-- ignora le revocate, che restano in tabella ma non servono a questa query.
CREATE INDEX sessions_active_by_user_idx
    ON sessions (user_id, last_used_at DESC)
    WHERE revoked_at IS NULL;

-- Serve alla pulizia periodica, che è l'unica a scandire la tabella per data.
CREATE INDEX sessions_expires_at_idx ON sessions (expires_at);

-- Nessun `updated_at` e nessun trigger: `last_used_at` si aggiorna a ogni
-- richiesta autenticata, e un trigger plpgsql su quella scrittura sarebbe costo
-- puro su una delle query più calde del sistema.

-- ---------------------------------------------------------------- user_tokens

-- Una tabella sola per i due scopi, non due tabelle gemelle: le colonne sono le
-- stesse, il ciclo di vita è lo stesso (nasce, scade, si consuma una volta) e
-- l'unica differenza è cosa autorizza. Due tabelle identiche avrebbero
-- significato duplicare anche i vincoli e la pulizia periodica.
CREATE TABLE user_tokens (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,

    purpose user_token_purpose NOT NULL,

    -- Come per `sessions`: HMAC-SHA256 del valore inviato per email, che a
    -- riposo non esiste. Chi legge il database non può usare un token pendente
    -- per reimpostare la password di qualcuno.
    token_hash text NOT NULL CONSTRAINT user_tokens_token_hash_format_check
        CHECK (token_hash ~ '^[0-9a-f]{64}$'),

    created_at timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz NOT NULL,

    -- Un token si consuma una volta sola. Il consumo è un UPDATE condizionato
    -- (`WHERE consumed_at IS NULL`), quindi due richieste concorrenti con lo
    -- stesso token producono un vincitore e un rifiuto, non due reimpostazioni.
    consumed_at timestamptz,

    -- Indirizzo che ha chiesto il token. Serve a indagare un abuso del recupero
    -- password; non identifica il destinatario, che è già `user_id`.
    requested_ip inet,

    CONSTRAINT user_tokens_expiry_check CHECK (expires_at > created_at)
);

COMMENT ON TABLE user_tokens IS 'Token monouso per verifica email e recupero password (R14).';
COMMENT ON COLUMN user_tokens.token_hash IS 'HMAC-SHA256 del token inviato per email; il valore in chiaro non è conservato (SPEC §5).';

CREATE UNIQUE INDEX user_tokens_token_hash_key ON user_tokens (token_hash);

-- «I token pendenti di questo utente per questo scopo»: la richiesta di un
-- nuovo token invalida i precedenti, e il reset riuscito invalida tutto il
-- resto.
CREATE INDEX user_tokens_pending_idx
    ON user_tokens (user_id, purpose)
    WHERE consumed_at IS NULL;

CREATE INDEX user_tokens_expires_at_idx ON user_tokens (expires_at);

-- ------------------------------------------------------------ manutenzione

-- Le due tabelle crescono a ogni login e a ogni «ho dimenticato la password»,
-- e le righe morte non servono a nessuno oltre una finestra breve. La pulizia è
-- una funzione e non un `DELETE` sparso nel codice applicativo per la stessa
-- ragione della 0006: la manutenzione periodica deve poterla chiamare senza
-- riscrivere la condizione, che è il punto dove si sbaglia.

-- Rimuove le sessioni scadute o revocate da più di `grace`.
--
-- `grace` esiste perché l'elenco dei dispositivi in dashboard deve poter
-- mostrare «chiusa ieri»: cancellare al momento della revoca farebbe sparire
-- dalla vista anche la sessione che l'utente ha appena chiuso.
CREATE FUNCTION sessions_purge_expired(grace interval DEFAULT interval '30 days')
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

    DELETE FROM sessions
    WHERE (expires_at < now() - grace)
       OR (revoked_at IS NOT NULL AND revoked_at < now() - grace);

    GET DIAGNOSTICS removed = ROW_COUNT;
    RETURN removed;
END
$$;

COMMENT ON FUNCTION sessions_purge_expired(interval) IS 'Elimina le sessioni scadute o revocate da oltre `grace`; restituisce quante.';

-- Rimuove i token scaduti o già consumati da più di `grace`.
CREATE FUNCTION user_tokens_purge_expired(grace interval DEFAULT interval '7 days')
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

    DELETE FROM user_tokens
    WHERE (expires_at < now() - grace)
       OR (consumed_at IS NOT NULL AND consumed_at < now() - grace);

    GET DIAGNOSTICS removed = ROW_COUNT;
    RETURN removed;
END
$$;

COMMENT ON FUNCTION user_tokens_purge_expired(interval) IS 'Elimina i token scaduti o consumati da oltre `grace`; restituisce quanti.';
