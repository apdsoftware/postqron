-- 0015 — Le due cose che mancavano all'invio delle email (R21, R33, R20.1).
--
-- La coda delle notifiche esiste dalla 0008 e i suoi campi bastavano a dire
-- *cosa* comunicare. Agganciarci gli eventi di dominio (R21) ha mostrato due
-- buchi, uno per estremo del percorso: da chi parte non si sa **in che lingua**
-- scrivere, e a chi arriva non si sa **cosa annotare** della risposta.

-- ---------------------------------------------------------------- users.language

-- R33 dice che la lingua del profilo «determina la lingua delle email
-- transazionali (R19–R21)». Senza questa colonna quella frase non è
-- implementabile: l'unica lingua disponibile sarebbe quella del browser di chi
-- ha causato l'evento, e un alert di job fallito lo causa il motore, che di
-- browser non ne ha.
--
-- La colonna è il minimo che serve al recapito. Il selettore nel profilo, la
-- rotta che lo scrive e la validazione lato API restano lavoro della issue del
-- profilo utente: qui si aggiunge il posto in cui quel valore andrà a finire,
-- con il predefinito che la spec prescrive.
--
-- Il vincolo elenca le cinque lingue invece di rimandare a una tabella perché
-- l'elenco è nella spec e cambia con essa (SPEC §8-bis): una lingua in più è una
-- migrazione, ed è giusto che lo sia. L'inglese è il predefinito perché è la
-- lingua sorgente dei contenuti, non un ripiego arbitrario.
ALTER TABLE users
    ADD COLUMN language text NOT NULL DEFAULT 'en'
        CONSTRAINT users_language_check
        CHECK (language IN ('en', 'it', 'es', 'de', 'fr'));

COMMENT ON COLUMN users.language IS
    'Lingua predefinita dell''utente (R33): decide la lingua delle email transazionali (R19–R21).';

-- ------------------------------------------------------ notifications.email_log_id

-- R20.1: Mailronix risponde `202` in modo identico sia che l'email parta sia che
-- il destinatario sia in suppression list. Di quella risposta si registra
-- `email_log_id` **e nulla di più**, ed è esattamente ciò che questa colonna
-- conserva: l'unico appiglio per ritrovare il messaggio nella console Mailronix
-- quando un cliente dice di non aver ricevuto niente.
--
-- Non è una prova di recapito, e il nome della colonna è scelto per non
-- suggerirlo: non si chiama `delivered_at` né `delivery_status`. La colonna
-- `sent_at` che le sta accanto, dalla 0008, dice quando **noi** abbiamo
-- consegnato il messaggio a Mailronix — non quando l'utente l'ha ricevuto.
ALTER TABLE notifications
    ADD COLUMN email_log_id text
        CONSTRAINT notifications_email_log_id_check CHECK (email_log_id <> '');

COMMENT ON COLUMN notifications.email_log_id IS
    'Identificativo Mailronix del messaggio accodato. Non è una prova di recapito: R20.1 non ne offre nessuna.';
