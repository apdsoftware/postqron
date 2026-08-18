-- Rollback di 0017.
--
-- Le richieste di cancellazione pendenti si perdono, ed è inevitabile: lo schema
-- a cui si torna non ha un posto dove tenerle. Ciò che si perde è la **scadenza**
-- della grazia, non i dati dell'utente — che a questo punto ci sono ancora tutti,
-- perché la purga non è avvenuta. L'utente resta con l'account in piedi e i job
-- fermi: è la direzione sbagliata meno pericolosa delle due.

-- --------------------------------------------------- il motivo della sospensione
--
-- Un valore non si toglie da un tipo enumerato: il tipo si ricostruisce senza di
-- lui. Prima però vanno sciolte le sospensioni che lo usano, perché la
-- conversione le rifiuterebbe.
--
-- I job **restano spenti**. Riaccenderli qui significherebbe far ripartire
-- chiamate verso i bersagli di un utente che ha chiesto di andarsene, e un
-- rollback di schema non è il posto da cui prendere quella decisione.
-- `jobs_suspension_check` esige che le due colonne si azzerino insieme.
UPDATE jobs
   SET suspended_at = NULL, suspended_reason = NULL
 WHERE suspended_reason = 'account_deletion';

ALTER TYPE job_suspension_reason RENAME TO job_suspension_reason_old;

CREATE TYPE job_suspension_reason AS ENUM ('plan_job_limit', 'plan_resolution');

ALTER TABLE jobs
    ALTER COLUMN suspended_reason TYPE job_suspension_reason
    USING suspended_reason::text::job_suspension_reason;

DROP TYPE job_suspension_reason_old;

COMMENT ON COLUMN jobs.suspended_reason IS
    'Quale vincolo di piano il job viola, cioè cosa serve per riaccenderlo (R58).';

-- ------------------------------------------------------- la finestra di grazia

DROP INDEX IF EXISTS users_pending_purge_idx;

ALTER TABLE users
    DROP CONSTRAINT users_purge_after_order_check,
    DROP CONSTRAINT users_deletion_request_check,
    DROP COLUMN purge_after,
    DROP COLUMN deletion_requested_at;
