-- 0014 — Comportamento sulle esecuzioni sovrapposte (R41).
--
-- Con la risoluzione al secondo (R22) un'occorrenza che scatta mentre la
-- precedente è ancora in corso non è un caso limite: un `every: 1s` che impiega
-- un secondo e mezzo si sovrappone a sé stesso per sempre. R41 chiede che il
-- comportamento sia **esplicito e configurabile per job**, con un predefinito
-- dichiarato, ed è questa colonna a dichiararlo.
--
-- I tre valori non sono intercambiabili, e la scelta giusta dipende da cosa fa
-- il job:
--
--   * `skip`  — l'occorrenza in eccesso non viene eseguita e si chiude come
--               `skipped`, che è lo stato che la 0001 riserva già a «esecuzione
--               precedente ancora in corso»;
--   * `queue` — l'occorrenza aspetta il proprio turno: le esecuzioni dello
--               stesso job restano serializzate, nessuna viene persa;
--   * `allow` — le esecuzioni si sovrappongono, entro il tetto tecnico del
--               servizio.
--
-- **Il predefinito è `skip`**, e la ragione è che è l'unico che non fa danni su
-- un job di cui non si sa niente. `allow` chiama due volte insieme un bersaglio
-- che potrebbe emettere una fattura per chiamata; `queue` accumula un arretrato
-- illimitato quando il job è stabilmente più lento del proprio intervallo, cioè
-- trasforma un job scritto male nel problema di tutti gli altri. `skip` perde
-- delle occorrenze — e lo dice, riga per riga, nel registro delle esecuzioni —
-- ma non produce né effetti doppi né code che crescono da sole. Chi ha bisogno
-- degli altri due comportamenti li chiede, e chiedendoli dichiara di conoscerne
-- il prezzo.
--
-- Il valore predefinito della colonna vale anche per le righe esistenti: i job
-- già creati passano a `skip`, che è un cambiamento di comportamento voluto —
-- fino a oggi si sovrapponevano tutti, senza che nessuno l'avesse scelto.

CREATE TYPE overlap_policy AS ENUM ('skip', 'queue', 'allow');

COMMENT ON TYPE overlap_policy IS
    'Comportamento quando un''occorrenza scatta mentre la precedente è ancora in corso (R41).';

ALTER TABLE jobs
    ADD COLUMN overlap_policy overlap_policy NOT NULL DEFAULT 'skip';

COMMENT ON COLUMN jobs.overlap_policy IS
    'R41: saltare, accodare o consentire la sovrapposizione. Il predefinito `skip` è quello che non fa danni.';
