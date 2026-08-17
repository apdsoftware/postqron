-- Annulla la 0013.
--
-- L'ordine è l'inverso della salita: prima ciò che dipende dai tipi enumerati,
-- poi i tipi. Le colonne aggiunte a `jobs` e `subscriptions` si tolgono con i
-- vincoli che le accompagnano — `DROP COLUMN` porta via da solo il CHECK che le
-- nomina — mentre gli indici parziali cadono con le colonne su cui poggiano.
--
-- **La discesa perde dati**, ed è inevitabile dirlo: `suspended_at` è la traccia
-- del perché un job è spento. Tornata indietro la migrazione, quei job restano
-- `enabled = false` e diventano indistinguibili da una pausa decisa dall'utente.
-- Nessun job viene però cancellato, che è la promessa di R58.

ALTER TABLE jobs
    DROP COLUMN IF EXISTS suspended_reason,
    DROP COLUMN IF EXISTS suspended_at;

DROP TYPE IF EXISTS job_suspension_reason;

DROP TABLE IF EXISTS paddle_checkout_intents;

ALTER TABLE subscriptions
    DROP COLUMN IF EXISTS paddle_price_id,
    DROP COLUMN IF EXISTS paddle_event_occurred_at;

DROP FUNCTION IF EXISTS paddle_webhook_events_purge(interval);

DROP TABLE IF EXISTS paddle_webhook_events;

DROP TYPE IF EXISTS paddle_event_status;
