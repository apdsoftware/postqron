-- 0010 — I job che aspettano la loro prima occorrenza (R2).
--
-- `jobs.next_run_at` è «calcolato dallo scheduler» (0005): anche il **primo**
-- valore, non solo gli avanzamenti. Un job appena creato da API, da dashboard o
-- da una riconciliazione di `cron.yaml` nasce quindi con la colonna a NULL, e
-- resta invisibile al dispatch finché il motore non gliela riempie — perché
-- `jobs_due_idx` esclude proprio i job senza prossima occorrenza.
--
-- Lo scheduler li cerca a ogni passata:
--
--   SELECT ... FROM jobs
--    WHERE enabled AND archived_at IS NULL AND next_run_at IS NULL
--    ORDER BY created_at
--    FOR UPDATE SKIP LOCKED
--    LIMIT $1;
--
-- Senza indice quella query è una scansione completa di `jobs` quattro volte al
-- secondo, per sempre — e il costo cresce con il catalogo dei job, cioè con il
-- numero di clienti, mentre il lavoro utile resta zero.
--
-- L'indice è parziale sulle stesse tre condizioni, quindi a regime **contiene
-- zero righe**: un job resta senza prossima occorrenza per la frazione di
-- secondo che passa fra la sua creazione e la passata successiva del motore.
-- È lo stesso ragionamento di `job_executions_in_flight_idx` (0006): un indice
-- che contiene solo il lavoro in attesa costa quanto il lavoro in attesa.
--
-- La chiave è `created_at` perché è l'ordine in cui i job vanno serviti: se una
-- riconciliazione ne crea più di quanti ne entrino in un lotto, i primi arrivati
-- partono per primi.

CREATE INDEX jobs_unscheduled_idx
    ON jobs (created_at)
    WHERE enabled AND archived_at IS NULL AND next_run_at IS NULL;

COMMENT ON INDEX jobs_unscheduled_idx IS
    'Job abilitati in attesa della prima occorrenza calcolata dallo scheduler (R2). Vuoto a regime.';
