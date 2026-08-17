-- Annulla la 0014.
--
-- La colonna va tolta prima del tipo: un tipo enumerato ancora usato da una
-- colonna non si elimina, e l'ordine inverso fallirebbe.

ALTER TABLE jobs DROP COLUMN IF EXISTS overlap_policy;

DROP TYPE IF EXISTS overlap_policy;
