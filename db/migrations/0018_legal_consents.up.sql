-- 0018 — Tracce del consenso ai documenti legali (R46).
--
-- R46 chiede di registrare «versione, data e lingua in cui il consenso è stato
-- prestato, per ogni documento legale». Le tre cose insieme, e per una ragione
-- che si vede solo il giorno in cui serve: se un utente contesta una clausola,
-- la domanda è **cosa aveva davanti quando ha accettato**, e la risposta
-- dev'essere ricostruibile senza interpretazioni.
--
-- # Perché una riga per documento e non una per accettazione
--
-- Perché i quattro documenti **non sono alla stessa versione**, e non lo saranno
-- mai più. Oggi (legal/README.md): i Termini sono alla 1.2.0, la privacy policy
-- alla 1.1.0, la acceptable use policy e la cookie policy alla 1.0.0. Una riga
-- con una versione sola per l'insieme sarebbe già falsa adesso, e lo resterebbe
-- a ogni riedizione di uno solo dei quattro — che è la forma normale, perché un
-- documento si modifica quando è quel documento a dover cambiare.
--
-- # Perché la lingua
--
-- Perché il consenso vale su ciò che l'utente ha effettivamente letto (SPEC
-- §8-bis). Chi ha accettato leggendo l'italiano non ha accettato l'inglese,
-- anche quando i due testi vogliono dire la stessa cosa: la traduzione è una
-- versione del testo, non una decorazione dell'interfaccia. La colonna registra
-- la lingua del **testo mostrato**, che può non essere quella dell'interfaccia:
-- finché una traduzione non c'è (#447), l'utente vede l'inglese e qui va scritto
-- `en`.
--
-- # Che cosa questa tabella non è
--
-- **Non è il consenso al marketing** (#476). Quello ha una base giuridica
-- diversa — il consenso dell'Art. 6(1)(a), non l'esecuzione del contratto — è
-- revocabile in qualunque momento, e la privacy policy §2.8 promette che non
-- viene «mai chiesto insieme all'accettazione dei termini o alla creazione
-- dell'account». Tenerli in una tabella sola renderebbe impossibile revocare
-- l'uno senza toccare l'altro, cioè romperebbe proprio la distinzione che quella
-- frase garantisce. Il consenso al marketing avrà la sua tabella, con la sua
-- revoca.
--
-- # Che cosa ne è alla cancellazione dell'account
--
-- Se ne va con l'account, in cascata come tutto il resto. È una decisione, non
-- un'omissione, e va scritta per esteso perché la scelta opposta era difendibile.
--
-- La privacy policy §5 dice due cose che qui si incontrano: «Account and profile
-- | While the account exists» e «Records we must keep for tax or legal reasons
-- survive deletion, **and only those**». Un consenso ai termini non è un
-- documento fiscale né un obbligo di conservazione: è la prova di un rapporto
-- con una persona, e quando quella persona esercita la cancellazione, tenerne
-- traccia significherebbe conservare un dato personale — chi ha accettato cosa e
-- quando — di qualcuno a cui abbiamo dichiarato di aver rimosso tutto.
--
-- L'alternativa — conservarlo in forma anonima, come si fa con le righe di audit
-- di un admin — non regge alla prova dello scopo: una prova di consenso che non
-- dice **di chi** è non prova niente. Non c'è una via di mezzo utile fra
-- «cancellare» e «tenere un dato personale».
--
-- Se un giorno la conservazione servisse davvero — per esempio per il termine di
-- prescrizione di un contratto — la strada è cambiare prima la privacy policy,
-- non aggiungere qui un'eccezione che il documento non nomina. Il test
-- TestDopoLaPurgaNonSopravviveNienteDellUtente (internal/accountpg) tiene la
-- porta chiusa: cerca ogni marcatore dell'utente in ogni colonna di ogni tabella
-- dello schema, e diventerebbe rosso.

CREATE TABLE legal_consents (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,

    -- Il documento, con lo stesso nome che ha il file in `legal/` e il campo
    -- `document` del suo frontmatter. Un nome solo per il file, il registro Go e
    -- questa colonna significa che non esiste una tabella di conversione da
    -- sbagliare.
    --
    -- È un `text` con un CHECK e non un tipo enumerato come quelli della 0001,
    -- per la ragione che la 0001 stessa dichiara: aggiungere un valore a un enum
    -- non si può fare dentro una transazione, e l'insieme dei documenti legali è
    -- destinato a crescere — un quinto documento è una migrazione ordinaria, non
    -- un'operazione da programmare.
    document text NOT NULL
        CONSTRAINT legal_consents_document_check
        CHECK (document IN ('terms-of-service', 'acceptable-use-policy',
                            'privacy-policy', 'cookie-policy')),

    -- La versione accettata, nella forma del frontmatter. Il vincolo è sulla
    -- forma e non su un elenco di versioni ammesse: le versioni sono infinite e
    -- crescono nel tempo, e un elenco nel database sarebbe una terza copia da
    -- tenere allineata a `legal/` e al registro Go.
    version text NOT NULL
        CONSTRAINT legal_consents_version_format_check
        CHECK (version ~ '^[0-9]+\.[0-9]+\.[0-9]+$'),

    -- La lingua del testo mostrato. Stesso dominio di `users.language` (0015) e
    -- della stessa provenienza: le cinque lingue di SPEC §8-bis.
    language text NOT NULL
        CONSTRAINT legal_consents_language_check
        CHECK (language IN ('en', 'it', 'es', 'de', 'fr')),

    -- L'impronta SHA-256 del testo accettato, per intero e frontmatter
    -- compreso.
    --
    -- R46 non la chiede, e c'è lo stesso: versione e lingua identificano il
    -- testo solo finché ci si fida del repository, mentre l'impronta rende la
    -- prova verificabile anche da fuori — «questo utente ha accettato *questo*
    -- testo» invece di «ha accettato ciò che chiamavamo 1.2.0». Costa 64 byte per
    -- riga, e la riga si scrive una volta sola nella vita di un account.
    document_checksum text NOT NULL
        CONSTRAINT legal_consents_checksum_format_check
        CHECK (document_checksum ~ '^[0-9a-f]{64}$'),

    -- In quale momento il consenso è nato. I due casi hanno una forma giuridica
    -- diversa: alla registrazione l'accettazione è implicita nell'atto — i
    -- Termini si aprono con «By creating an account you accept them» — mentre
    -- una riaccettazione è un atto separato, compiuto da chi ha già un account
    -- davanti a un testo cambiato.
    source text NOT NULL
        CONSTRAINT legal_consents_source_check
        CHECK (source IN ('registration', 'reacceptance')),

    -- L'istante in cui l'utente si è vincolato. È l'unico dato della riga che
    -- non viene dal documento.
    --
    -- **Non esiste un `updated_at`, e non è una dimenticanza**: una prova non si
    -- aggiorna. Il trigger qui sotto rende la cosa vera invece di raccomandarla.
    accepted_at timestamptz NOT NULL DEFAULT now(),

    -- Una versione di un documento si accetta una volta sola.
    --
    -- Il vincolo fa due lavori. Il primo è impedire che un doppio invio del form
    -- moltiplichi la prova. Il secondo, più importante, è che rende
    -- l'accettazione **idempotente senza spostare la data**: chi riaccetta la
    -- stessa versione non scrive niente (`ON CONFLICT DO NOTHING`) e la data
    -- resta quella del primo consenso, che è l'istante in cui l'utente si è
    -- vincolato davvero.
    --
    -- La coppia (documento, versione) e non il solo documento: le versioni
    -- accettate in passato restano, e sono la prova di cosa vincolava allora.
    CONSTRAINT legal_consents_user_document_version_key
        UNIQUE (user_id, document, version)
);

COMMENT ON TABLE legal_consents IS
    'Prova del consenso ai documenti legali (R46): quale versione, in che lingua, quando. Append-only, e cancellata con l''account.';
COMMENT ON COLUMN legal_consents.language IS
    'Lingua del testo mostrato, non dell''interfaccia: il consenso vale su ciò che l''utente ha letto (SPEC §8-bis).';
COMMENT ON COLUMN legal_consents.document_checksum IS
    'Impronta SHA-256 del testo accettato: rende la prova verificabile senza fidarsi del repository.';

-- ------------------------------------------------------------- append-only

-- Una prova che si può riscrivere non prova niente.
--
-- L'audit log della 0008 è «append-only per contratto»: nessuna colonna
-- `updated_at` e nessun codice che aggiorni. Qui il contratto non basta, e la
-- differenza sta in chi è la controparte. Una riga di audit sbagliata è un
-- problema fra noi e noi; un consenso modificabile è un problema fra noi e la
-- persona che ha accettato — e chiunque possa scrivere sul database potrebbe
-- retrodatare un'accettazione, o cambiarne la versione, senza lasciare traccia.
--
-- # Perché il DELETE è ammesso solo se l'utente non c'è più
--
-- Perché la cancellazione dell'account (R45) deve poter funzionare: la purga
-- esegue `DELETE FROM users`, e i consensi cadono in cascata. Quella è l'unica
-- cancellazione prevista, e il trigger la riconosce dal fatto che **la riga
-- `users` non esiste più**: dentro la stessa transazione, quando la cascata
-- arriva qui, il padre è già stato cancellato.
--
-- Il risultato è la regola che serve, scritta dove nessuno la può aggirare: un
-- consenso si cancella insieme alla persona a cui appartiene, e in nessun altro
-- caso.
CREATE FUNCTION legal_consents_append_only() RETURNS trigger
    LANGUAGE plpgsql
    SET search_path = pg_catalog, public
AS $$
BEGIN
    IF TG_OP = 'UPDATE' THEN
        RAISE EXCEPTION
            'legal_consents è append-only: un consenso non si modifica (R46). Se il documento è cambiato, si registra un consenso nuovo sulla versione nuova.'
            USING ERRCODE = 'restrict_violation';
    END IF;

    IF EXISTS (SELECT 1 FROM users WHERE id = OLD.user_id) THEN
        RAISE EXCEPTION
            'legal_consents: un consenso si cancella solo insieme all''account a cui appartiene (R45), e l''utente % esiste ancora.',
            OLD.user_id
            USING ERRCODE = 'restrict_violation';
    END IF;

    RETURN OLD;
END
$$;

COMMENT ON FUNCTION legal_consents_append_only() IS
    'Vieta UPDATE e DELETE su legal_consents: un consenso è una prova. L''unica cancellazione ammessa è la cascata da users (R45).';

CREATE TRIGGER legal_consents_append_only
    BEFORE UPDATE OR DELETE ON legal_consents
    FOR EACH ROW EXECUTE FUNCTION legal_consents_append_only();
