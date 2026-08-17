-- Rollback di 0006. DROP TABLE sulla tabella partizionata porta via anche tutte
-- le sue partizioni, comprese quelle create dopo la migrazione dalla
-- manutenzione periodica.

DROP FUNCTION IF EXISTS job_executions_drop_partitions_before(date);
DROP FUNCTION IF EXISTS job_executions_ensure_partitions(integer, integer);
DROP FUNCTION IF EXISTS job_executions_ensure_partition(date);

DROP TABLE IF EXISTS job_executions;
