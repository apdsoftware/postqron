-- Annulla la 0009.
--
-- Le funzioni prima delle tabelle e il tipo per ultimo: un DROP TYPE fallirebbe
-- finché una colonna lo usa. Gli indici e i vincoli cadono con le tabelle.

DROP FUNCTION IF EXISTS user_tokens_purge_expired(interval);
DROP FUNCTION IF EXISTS sessions_purge_expired(interval);

DROP TABLE IF EXISTS user_tokens;
DROP TABLE IF EXISTS sessions;

DROP TYPE IF EXISTS user_token_purpose;
