-- Rollback di 0003. La cancellazione di `plans` porta via anche i dati di
-- riferimento inseriti dalla migrazione.

DROP TABLE IF EXISTS subscriptions;
DROP TABLE IF EXISTS plans;
