-- Rollback di 0018.
--
-- Le prove del consenso si perdono, e vale la pena dire cosa significa: tornando
-- allo schema precedente non resta nessuna traccia di quale versione dei
-- documenti ogni utente abbia accettato, né in che lingua. Non è un dato
-- ricostruibile da altre colonne — è per questo che la tabella esiste (R46).
--
-- Il trigger va tolto per primo: vieta il DELETE finché l'utente esiste, e
-- `DROP TABLE` non lo eseguirebbe comunque, ma lasciare in giro una funzione che
-- nomina una tabella che non c'è più sarebbe un residuo che il prossimo `up`
-- troverebbe di traverso.
DROP TRIGGER IF EXISTS legal_consents_append_only ON legal_consents;

DROP TABLE IF EXISTS legal_consents;

DROP FUNCTION IF EXISTS legal_consents_append_only();
