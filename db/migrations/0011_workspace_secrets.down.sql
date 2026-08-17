-- Annulla la 0011.
--
-- Il trigger e gli indici cadono con la tabella; `set_updated_at` è della 0001 e
-- resta. Nessun tipo enumerato è stato introdotto, quindi non c'è niente da
-- rimuovere in 0001.

DROP TABLE IF EXISTS workspace_secrets;
