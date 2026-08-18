-- Rollback di 0019.
--
-- Si perde la traccia del consenso al marketing, e va detto per intero: non si
-- perde una preferenza, si perde **la prova di aver avuto il diritto di
-- scrivere**. Lo schema a cui si torna non ha un posto dove conservarla, e la
-- privacy policy §2.8 promette di tenerla.
--
-- La direzione in cui il danno cade è comunque quella giusta: senza tabella
-- nessuno ha consenso, quindi dopo questo rollback non parte alcuna email di
-- marketing. Il codice che le manda legge lo stato da qui, e uno stato assente
-- è un rifiuto — mai un permesso.
--
-- Chi lo esegue su un database con dati dovrebbe esportare la tabella prima,
-- perché è l'unico posto in cui quei consensi esistono.

DROP VIEW IF EXISTS marketing_consent_state;

DROP TABLE IF EXISTS marketing_consents;

DROP TYPE IF EXISTS marketing_consent_source;
DROP TYPE IF EXISTS marketing_consent_decision;
