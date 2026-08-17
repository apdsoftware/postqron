-- 0012 — Segreti del workspace (R42, R43).
--
-- Sono i valori contro cui `cron.yaml` risolve i suoi `${VAR}` (SPEC §9): un
-- token di autenticazione che finisce in una testata, una firma che finisce nel
-- corpo. Nel database entra solo il testo cifrato — la stessa forma della 0007,
-- e per la stessa ragione: chi legge un backup, una replica o un dump non deve
-- trovarci dentro le credenziali dei clienti.
--
-- # Perché `user_id` e non `workspace_id`
--
-- Il workspace di SPEC §9 oggi coincide con l'account: i workspace multipli sono
-- R25, appartengono al piano Agency e non hanno ancora una tabella. La colonna
-- porta quindi l'utente, come `jobs`, `repositories` e `api_keys`, e il nome
-- della tabella conserva il vocabolario della specifica perché è quello con cui
-- la funzionalità viene chiamata ovunque — nel file YAML, nella dashboard e
-- nelle rotte.
--
-- Quando R25 arriverà, questa tabella prenderà un `workspace_id` accanto a
-- `user_id` e l'unicità si sposterà là. È una migrazione additiva proprio perché
-- la chiave di unicità è in un indice e non nella chiave primaria.

CREATE TABLE workspace_secrets (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,

    -- Il nome è ciò che `${VAR}` scrive nel `cron.yaml`, e il vincolo è la
    -- stessa forma che il parser accetta: maiuscole, cifre e underscore, prima
    -- lettera alfabetica. Una sola forma ammessa significa che `${digest}` e
    -- `${DIGEST}` non possono essere due segreti diversi — e che il riferimento
    -- non risolvibile è un errore di battitura visibile, non un secondo segreto
    -- creato per sbaglio.
    name text NOT NULL
        CONSTRAINT workspace_secrets_name_format_check
        CHECK (name ~ '^[A-Z][A-Z0-9_]{0,63}$'),

    -- Nota dell'utente («token di lettura dell'API fatturazione»). Non è il
    -- valore e non ne è un pezzo: è l'unico testo di questa riga che l'API
    -- restituisce insieme al nome.
    description text CHECK (description IS NULL OR char_length(description) <= 200),

    -- Valore cifrato e nonce della cifratura, separati perché il nonce non è
    -- segreto e va cambiato a ogni scrittura. Non esiste nessuna colonna con il
    -- valore in chiaro, nemmeno parziale: a differenza delle chiavi API non c'è
    -- un prefisso da mostrare, perché il segreto è dell'utente e riconoscerlo è
    -- compito del nome che gli ha dato.
    --
    -- Vuoti se e solo se la riga è revocata: vedi il vincolo in fondo.
    ciphertext bytea NOT NULL,
    nonce bytea NOT NULL,

    -- Versione della chiave di cifratura usata (ENCRYPTION_KEY). Serve alla
    -- rotazione: durante una rotazione convivono righe cifrate con chiavi
    -- diverse, e senza questo numero non si saprebbe con quale decifrare quale.
    --
    -- La colonna c'è dal primo giorno anche se la rotazione non è ancora un
    -- comando, perché aggiungerla dopo significherebbe indovinare la versione
    -- delle righe già scritte. Vedi internal/secretbox.
    key_version smallint NOT NULL DEFAULT 1 CHECK (key_version >= 1),

    -- Ultima volta che il segreto è stato risolto da un'esecuzione. È la
    -- risposta alla domanda «posso revocarlo?», che senza questa colonna si
    -- risponde solo revocandolo e aspettando le segnalazioni.
    last_used_at timestamptz,

    -- La revoca è una data e non una DELETE: un segreto revocato resta come
    -- traccia di ciò che esisteva quando un'esecuzione è fallita, e il
    -- riferimento `${VAR}` rimasto nel `cron.yaml` va spiegato con «revocato il
    -- giorno tale», non con «non è mai esistito».
    revoked_at timestamptz,

    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),

    -- **Revocare significa che il valore smette di esistere.** Il vincolo lega
    -- le due cose in modo che non possano separarsi: finché la riga è viva il
    -- testo cifrato c'è, e dal momento in cui `revoked_at` è valorizzata dev'essere
    -- vuoto. Non si può revocare senza cancellare il valore, e non si può
    -- cancellare il valore lasciando la riga utilizzabile.
    --
    -- È una promessa più forte di quella delle chiavi API, dove la riga revocata
    -- conserva la propria impronta. Lì l'impronta non è la chiave; qui il testo
    -- cifrato *è* il segreto, e tenerlo in giro dopo la revoca significherebbe
    -- che «revocato» dipende dal fatto che nessuno lo decifri più.
    CONSTRAINT workspace_secrets_revoked_is_empty_check
        CHECK ((revoked_at IS NULL) = (octet_length(ciphertext) > 0)
               AND (revoked_at IS NULL) = (octet_length(nonce) > 0))
);

COMMENT ON TABLE workspace_secrets IS
    'Segreti del workspace (R42). Cifrati a riposo, mai loggati, mai restituiti in chiaro dall''API.';
COMMENT ON COLUMN workspace_secrets.name IS
    'Nome con cui `cron.yaml` lo riferisce come ${NOME} (SPEC §9).';
COMMENT ON COLUMN workspace_secrets.ciphertext IS
    'Valore cifrato. Il valore in chiaro non esiste a riposo (R42).';
COMMENT ON COLUMN workspace_secrets.key_version IS
    'Versione di ENCRYPTION_KEY con cui la riga è cifrata, per la rotazione.';
COMMENT ON COLUMN workspace_secrets.revoked_at IS
    'Data di revoca. La riga resta come traccia, il valore cifrato viene svuotato, il nome torna disponibile.';

-- Unicità del nome **fra i soli segreti vivi**. Parziale e non totale perché il
-- nome è un identificatore che l'utente riusa: revocato `DIGEST_TOKEN` deve
-- poterne creare un altro con lo stesso nome, altrimenti la revoca brucerebbe
-- per sempre il riferimento scritto nel `cron.yaml` e costringerebbe a un push
-- solo per rinominare una variabile.
CREATE UNIQUE INDEX workspace_secrets_live_name_key
    ON workspace_secrets (user_id, name)
    WHERE revoked_at IS NULL;

-- Query calda 1 — la risoluzione all'esecuzione (R43): «i segreti vivi di
-- questo utente con questi nomi». Gira a ogni occorrenza, che con la
-- risoluzione al secondo significa in continuazione, e legge quasi sempre una
-- riga sola. È servita dall'indice unico qui sopra, che ha `user_id` come
-- prefisso e non contiene le righe revocate.

-- Query calda 2 — l'elenco in dashboard, dalla più recente. Comprende le righe
-- revocate, che restano visibili come traccia.
CREATE INDEX workspace_secrets_user_id_idx
    ON workspace_secrets (user_id, created_at DESC);

CREATE TRIGGER workspace_secrets_set_updated_at
    BEFORE UPDATE ON workspace_secrets
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();
