-- Rollback di 0016.
--
-- Le righe revocate vengono **cancellate** prima di rimettere l'unicità totale.
-- Non è una scelta comoda ma l'unica possibile: lo schema a cui si torna non ha
-- un posto dove metterle — una riga revocata ha il materiale cifrato vuoto, che
-- i due `CHECK` della 0007 vietano, e occuperebbe lo slot `(user_id, provider)`
-- di una chiave viva. Ciò che si perde è una traccia senza contenuto: la chiave
-- era già stata cancellata dalla revoca.

DELETE FROM ai_credentials WHERE revoked_at IS NOT NULL;

ALTER TABLE ai_credentials
    ADD COLUMN last_four text
        CONSTRAINT ai_credentials_last_four_check CHECK (last_four ~ '^[A-Za-z0-9]{4}$');

DROP INDEX IF EXISTS ai_credentials_user_id_idx;
DROP INDEX IF EXISTS ai_credentials_live_provider_key;

ALTER TABLE ai_credentials
    ADD CONSTRAINT ai_credentials_user_provider_key UNIQUE (user_id, provider);

ALTER TABLE ai_credentials
    DROP CONSTRAINT ai_credentials_revoked_is_empty_check,
    ADD CONSTRAINT ai_credentials_ciphertext_check CHECK (octet_length(ciphertext) > 0),
    ADD CONSTRAINT ai_credentials_nonce_check CHECK (octet_length(nonce) > 0),
    DROP COLUMN revoked_at;

COMMENT ON TABLE ai_credentials IS
    'Chiavi AI degli utenti (BYOK, R18). Cifrate a riposo, mai loggate, mai restituite in chiaro dall''API.';
