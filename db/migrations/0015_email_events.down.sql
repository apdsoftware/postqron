-- Annulla la 0015.
--
-- Nessuna delle due colonne è riferita da altro: si tolgono nell'ordine inverso
-- a quello in cui sono state aggiunte. Tornando indietro si perdono le lingue
-- scelte dagli utenti e il legame fra una notifica e il suo messaggio nella
-- console Mailronix; nessuno dei due dato è ricostruibile, ed è il prezzo
-- dichiarato di questa migrazione all'indietro.

ALTER TABLE notifications DROP COLUMN IF EXISTS email_log_id;

ALTER TABLE users DROP COLUMN IF EXISTS language;
