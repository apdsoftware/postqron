-- Annulla la 0011.
--
-- La funzione prima della tabella e il tipo per ultimo: un DROP TYPE fallirebbe
-- finché una colonna lo usa. Gli indici e i vincoli cadono con la tabella.

DROP FUNCTION IF EXISTS github_webhook_deliveries_purge(interval);

DROP TABLE IF EXISTS github_webhook_deliveries;

DROP TYPE IF EXISTS github_delivery_status;
