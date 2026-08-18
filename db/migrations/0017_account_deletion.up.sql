-- 0017 — Cancellazione dell'account: la finestra di ripensamento (R45).
--
-- R45 chiede la cancellazione «con conferma e periodo di sicurezza configurabile
-- prima della rimozione definitiva», e la privacy policy (legal/en/privacy-policy.md
-- §5) dice all'utente esattamente cosa succede:
--
--   «When you delete your account we stop execution and revoke keys immediately,
--    then remove the data after a grace period of 30 days, during which you can
--    change your mind.»
--
-- Sono due istanti distinti, e questa migrazione serve a tenerli distinti: il
-- momento in cui l'utente chiede, e il momento a partire dal quale la purga può
-- eseguire. Fra i due l'account **esiste ancora**, con i suoi dati, e la
-- promessa «you can change your mind» è vera perché c'è qualcosa a cui tornare.
--
-- # Perché non si riusa `deleted_at`
--
-- La colonna della 0002 c'è, e a prima vista direbbe la stessa cosa. Non la
-- diciamo con lei per una ragione precisa, che sta nell'indice della 0002:
--
--     CREATE UNIQUE INDEX users_email_key ON users (lower(email))
--         WHERE deleted_at IS NULL;
--
-- cioè **valorizzare `deleted_at` libera immediatamente l'indirizzo email**. È
-- corretto per una cancellazione definitiva, ed è esattamente sbagliato per una
-- finestra di ripensamento: per trenta giorni invitiamo l'utente a tornare
-- indietro, e in quei trenta giorni un estraneo potrebbe registrarsi con il suo
-- indirizzo. Al ritorno non ci sarebbe modo di accontentarlo — due account vivi
-- con lo stesso indirizzo violano l'indice — e avremmo scritto in un documento
-- legale una possibilità che il database rende impossibile.
--
-- Con una colonna propria l'indirizzo resta occupato per tutta la grazia e torna
-- disponibile alla purga, quando la riga `users` sparisce davvero. `deleted_at`
-- resta ciò che la 0002 aveva previsto e questa funzionalità non la tocca.
--
-- # Perché la scadenza è sulla riga e non nella configurazione
--
-- Il periodo è configurabile (R45), quindi cambia. Se la purga calcolasse
-- «richiesta + grazia corrente», abbassare la configurazione da trenta a sette
-- giorni cancellerebbe **domani** account che avevano ricevuto la promessa dei
-- trenta giorni, e alzarla la allungherebbe a chi non l'aveva chiesto. La
-- scadenza è quindi una proprietà della richiesta: si calcola una volta, quando
-- l'utente chiede, con il periodo in vigore in quel momento, e non si muove più.

ALTER TABLE users
    ADD COLUMN deletion_requested_at timestamptz,
    ADD COLUMN purge_after timestamptz,

    -- I due valori vivono e muoiono insieme, come `suspended_at` e
    -- `suspended_reason` sui job (0013): una richiesta senza scadenza non
    -- saprebbe quando eseguire, e una scadenza senza richiesta descrive qualcosa
    -- che non è successo.
    ADD CONSTRAINT users_deletion_request_check
        CHECK ((deletion_requested_at IS NULL) = (purge_after IS NULL)),

    -- Una grazia non negativa. `>=` e non `>`: una configurazione a zero è
    -- legittima — è ciò che serve a un ambiente di prova per verificare la purga
    -- senza aspettare — e la promessa dei trenta giorni la fa la configurazione
    -- d'esercizio, non lo schema.
    ADD CONSTRAINT users_purge_after_order_check
        CHECK (purge_after IS NULL OR purge_after >= deletion_requested_at);

COMMENT ON COLUMN users.deletion_requested_at IS
    'Quando l''utente ha chiesto la cancellazione (R45). L''account esiste ancora: è l''inizio della finestra di ripensamento.';
COMMENT ON COLUMN users.purge_after IS
    'Da quando la purga può rimuovere i dati. Calcolata alla richiesta e immutabile: la grazia promessa non cambia se cambia la configurazione.';

-- La query della passata di purga: «quali account hanno superato la scadenza».
-- Parziale perché gli account vivi — la totalità, meno una manciata — non la
-- servono e non devono farla crescere. È la stessa forma di `jobs_suspended_idx`
-- (0013) e per la stessa ragione.
CREATE INDEX users_pending_purge_idx
    ON users (purge_after)
    WHERE deletion_requested_at IS NOT NULL;

-- ------------------------------------------------- perché i job si sono fermati

-- R45 dice che la cancellazione «interrompe le esecuzioni». Fermarle è un UPDATE
-- su `jobs`, e la 0013 ha già il posto dove scrivere *perché* un job è spento da
-- noi invece che dall'utente — ma il suo dominio conosce solo i due vincoli di
-- piano.
--
-- Il valore serve a due cose, e la seconda è quella che conta:
--
--   1. l'interfaccia deve poter dire «fermato dalla richiesta di cancellazione»
--      e non «in pausa», che sarebbe una pausa che l'utente non ricorda di aver
--      messo;
--   2. **l'annullamento della richiesta deve riaccendere esattamente i job che
--      la richiesta aveva spento.** Senza un'etichetta, l'unico modo sarebbe
--      riaccenderli tutti — compresi quelli che l'utente aveva messo in pausa da
--      sé, mesi prima — cioè far ripartire chiamate verso bersagli che nessuno
--      voleva far ripartire.
--
-- Come per i due valori della 0013, il motivo non descrive la causa a monte ma
-- lo stato in cui il job si trova: la causa è nella riga `users`.
ALTER TYPE job_suspension_reason ADD VALUE 'account_deletion';

COMMENT ON COLUMN jobs.suspended_reason IS
    'Quale vincolo di piano il job viola, cioè cosa serve per riaccenderlo (R58); oppure `account_deletion`, se a fermarlo è stata la richiesta di cancellazione (R45).';
