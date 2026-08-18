-- 0016 — La chiave AI si revoca, e revocarla cancella il materiale (R18, R45).
--
-- La 0007 ha disegnato `ai_credentials` come un posto dove la chiave entra
-- cifrata, e quella parte regge. Quello che le manca è tutto ciò che viene
-- dopo la scrittura: **non c'è modo di revocare una chiave**. R45 dice che la
-- cancellazione dell'account «revoca le chiavi», e senza una data di revoca
-- l'unico modo di farlo è la `DELETE` — che cancella anche la traccia di ciò
-- che esisteva quando un'analisi dei log è fallita.
--
-- Questa migrazione porta su `ai_credentials` le tre scelte che la 0012 ha già
-- fatto per `workspace_secrets`, perché sono le stesse tre domande:
--
--   1. la revoca è una data, non una `DELETE`;
--   2. revocare **svuota** il testo cifrato, e un vincolo impedisce di
--      separare le due cose;
--   3. l'unicità vale **fra i soli vivi**, altrimenti la revoca brucerebbe per
--      sempre lo slot del provider.
--
-- # Perché `last_four` va via
--
-- La 0007 la motivava così: «sono le ultime quattro cifre, che servono a farla
-- riconoscere in dashboard e non bastano a ricostruirla». La seconda metà è
-- vera; è la prima che non regge, e la 0012 lo aveva già notato per i segreti
-- del workspace.
--
-- R18 dice che le chiavi sono **cifrate a riposo**. `last_four` è quattro
-- caratteri della chiave dell'utente conservati in chiaro in ogni backup, in
-- ogni replica e in ogni dump, protetti da niente: la frase e la colonna non
-- possono stare nella stessa tabella. Non è un rischio crittografico — sono
-- ventiquattro bit su una chiave che ne ha più di duecento — ma è la sola
-- colonna derivata dal testo in chiaro, e finché esiste non si può scrivere un
-- test che dica «nessuna colonna può contenere il chiaro» senza escluderne una.
--
-- Ciò che perdiamo è meno di quanto sembri. `ai_credentials` ha **una chiave
-- per provider**: alla domanda «quale chiave è questa?» risponde già il
-- provider, e alla domanda «è quella nuova che ho appena ruotato nella console
-- del fornitore?» risponde `updated_at`. Un frammento della chiave servirebbe a
-- distinguere fra chiavi diverse dello stesso provider, che qui non possono
-- coesistere.
--
-- La colonna è vuota su ogni ambiente: nessun codice l'ha mai scritta.

-- ------------------------------------------------------------------ la revoca

ALTER TABLE ai_credentials
    ADD COLUMN revoked_at timestamptz;

COMMENT ON COLUMN ai_credentials.revoked_at IS
    'Data di revoca. La riga resta come traccia, il materiale cifrato viene svuotato, il provider torna libero.';

-- I due vincoli della 0007 dicevano «il testo cifrato non è mai vuoto», che era
-- giusto finché la riga non poteva essere revocata. Ora la riga revocata *deve*
-- averlo vuoto, e il vincolo che li sostituisce lega le due cose in modo che non
-- possano separarsi: non si data la revoca lasciando in tabella il materiale, e
-- non si svuota il materiale lasciando la riga utilizzabile.
--
-- È la stessa promessa di `workspace_secrets_revoked_is_empty_check`, e per la
-- stessa ragione: qui il testo cifrato *è* la chiave, e tenerlo dopo la revoca
-- significherebbe che «revocata» dipende dal fatto che nessuno la decifri più.
ALTER TABLE ai_credentials
    DROP CONSTRAINT ai_credentials_ciphertext_check,
    DROP CONSTRAINT ai_credentials_nonce_check,
    ADD CONSTRAINT ai_credentials_revoked_is_empty_check
        CHECK ((revoked_at IS NULL) = (octet_length(ciphertext) > 0)
               AND (revoked_at IS NULL) = (octet_length(nonce) > 0));

-- --------------------------------------------------------- unicità fra i vivi

-- L'unicità della 0007 era **totale**: `UNIQUE (user_id, provider)` su tutte le
-- righe. Con la revoca diventa una trappola — la prima chiave Anthropic
-- revocata occuperebbe per sempre lo slot, e l'utente non potrebbe più
-- inserirne una nuova senza che qualcuno cancelli a mano la riga dal database.
--
-- L'indice parziale dice la stessa cosa sulle sole righe vive, che è ciò che il
-- vincolo voleva dire: una chiave per provider **in uso**. Le righe revocate si
-- accumulano come storico e non partecipano.
ALTER TABLE ai_credentials
    DROP CONSTRAINT ai_credentials_user_provider_key;

-- Query calda 1 — «la chiave viva di questo utente per questo provider»: è la
-- lettura che precede ogni chiamata al fornitore per l'analisi dei log (R30).
CREATE UNIQUE INDEX ai_credentials_live_provider_key
    ON ai_credentials (user_id, provider)
    WHERE revoked_at IS NULL;

-- Query calda 2 — l'elenco in dashboard, dalla più recente, revocate comprese.
-- L'indice serve perché il vincolo unico appena tolto era anche l'unico indice
-- con `user_id` in testa: senza, l'elenco diventerebbe una scansione della
-- tabella.
CREATE INDEX ai_credentials_user_id_idx
    ON ai_credentials (user_id, created_at DESC);

-- ------------------------------------------------------- via il frammento

ALTER TABLE ai_credentials
    DROP COLUMN last_four;

COMMENT ON TABLE ai_credentials IS
    'Chiavi AI degli utenti (BYOK, R18). Cifrate a riposo, mai loggate, mai restituite in chiaro dall''API — nemmeno un frammento.';
