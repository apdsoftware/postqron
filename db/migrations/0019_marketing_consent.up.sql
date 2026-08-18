-- 0019 — Consenso alle email di marketing, e la sua revoca (Privacy Policy §2.8).
--
-- La privacy policy dice, di quelle email:
--
--   «The legal basis is your **consent** (Art. 6(1)(a)), asked for on its own
--    and never bundled with accepting the terms or creating an account. […] We
--    keep a record of when you consented and when you withdrew, which is how we
--    can show that we had the right to write to you.»
--
-- L'ultima frase è ciò che questa tabella conserva, e va letta per intero: non
-- serve sapere **se** l'utente ha acconsentito, serve poter mostrare **quando**
-- lo ha fatto e quando ha ritirato. Sono due domande diverse, e la seconda non
-- si risponde con una colonna booleana.
--
-- # Perché una traccia e non due colonne su `users`
--
-- La lettura ovvia è `users.marketing_consent boolean` più due timestamp:
-- `granted_at` e `withdrawn_at`. Regge finché l'utente cambia idea una volta
-- sola. Alla seconda — acconsente, ritira, riacconsente — `granted_at` perde il
-- primo consenso, e con lui la prova di aver avuto il diritto di scrivere nel
-- periodo in mezzo. È esattamente il periodo su cui un reclamo verte.
--
-- Qui ogni riga è **una decisione**, e non si aggiorna mai. Lo stato attuale è
-- l'ultima riga; la prova è tutta la colonna. Vale la stessa regola di
-- `audit_log` nella 0008: una traccia che si può correggere non è una traccia.
--
-- # Perché non è `audit_log`
--
-- `audit_log` esiste ed è append-only, ma la privacy policy §5 gli assegna una
-- conservazione di 24 mesi. La prova del consenso deve durare quanto il
-- trattamento che giustifica: finché scriviamo a un indirizzo, il consenso che
-- ce ne dà il diritto deve essere esibibile. Metterla in una tabella con una
-- scadenza più corta significherebbe perdere la prova prima del fatto che deve
-- provare.
--
-- # Perché non è il consenso ai documenti legali
--
-- R46 chiede «versione, data e lingua in cui il consenso è stato prestato, per
-- ogni documento legale» ed è un'altra cosa, in lavorazione sulla issue #461.
-- Le due non si mescolano, e la ragione sta in §2.8: il consenso al marketing è
-- chiesto «on its own and never bundled with accepting the terms». Una tabella
-- sola per entrambi renderebbe naturale scriverli insieme in una transazione —
-- cioè renderebbe facile, a chi arriva dopo, proprio la cosa che il documento
-- promette di non fare.

-- La decisione presa. Due valori, un tipo enumerato per la stessa ragione della
-- 0001: il dominio è chiuso, e un `granted` scritto male dev'essere rifiutato
-- dal database e non registrato come una terza possibilità.
CREATE TYPE marketing_consent_decision AS ENUM ('granted', 'withdrawn');

-- Da dove è arrivata la decisione. Non è telemetria: è la parte della prova che
-- dice **come** il consenso è stato raccolto, ed è ciò che distingue un
-- consenso valido da uno estorto dentro un altro modulo.
--
--   profile          — la dashboard, con una sessione, da un comando che non
--                      fa nient'altro (§2.8: «asked for on its own»);
--   unsubscribe_link — il link nel piè di pagina di un'email di marketing, che
--                      per §2.8 funziona senza accedere. Può quindi produrre
--                      solo `withdrawn`: un consenso non si presta senza aver
--                      dimostrato di essere l'intestatario dell'indirizzo.
CREATE TYPE marketing_consent_source AS ENUM ('profile', 'unsubscribe_link');

CREATE TABLE marketing_consents (
    -- Identità sequenziale come in `audit_log`, e per lo stesso motivo:
    -- l'ordine di inserimento è informazione. Serve anche a rompere la parità
    -- fra due righe con lo stesso `occurred_at`, che l'orologio del database
    -- può produrre.
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,

    -- ON DELETE CASCADE, al contrario di `audit_log`: questa traccia riguarda
    -- **l'utente**, non l'operato di un terzo su di lui. Quando l'account
    -- sparisce non c'è più nessun indirizzo a cui scrivere, quindi non c'è più
    -- niente da provare — e conservare la prova di un diritto che non
    -- eserciteremo più sarebbe conservare un dato personale senza scopo.
    user_id uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,

    decision marketing_consent_decision NOT NULL,
    source marketing_consent_source NOT NULL,

    -- Quando. È la colonna che la privacy policy promette, ed è scritta da
    -- PostgreSQL e non dall'applicazione: la prova non deve dipendere
    -- dall'orologio del processo che l'ha raccolta.
    occurred_at timestamptz NOT NULL DEFAULT now(),

    -- Indirizzo da cui è arrivata la decisione. Facoltativo, e usato con
    -- parsimonia: è lo stesso dato che `sessions` e `audit_log` già
    -- conservano, e qui serve a distinguere una revoca chiesta dall'utente da
    -- una arrivata da chissà dove — che, con un link che funziona senza
    -- accedere, è una domanda che ci verrà posta.
    ip_address inet,

    -- Un consenso non si presta senza autenticazione. Il vincolo lo dice al
    -- database invece che lasciarlo alla disciplina del chiamante: chi
    -- aggiungesse domani un percorso che concede il consenso dal link di
    -- disiscrizione — «disiscriviti, oppure clicca qui per riscriverti» —
    -- troverebbe un rifiuto qui, dove non si può aggirare con un `if`.
    CONSTRAINT marketing_consents_grant_needs_session_check
        CHECK (decision = 'withdrawn' OR source <> 'unsubscribe_link')
);

COMMENT ON TABLE marketing_consents IS
    'Traccia append-only del consenso alle email di marketing e della sua revoca (Privacy Policy §2.8). Non si aggiorna e non si corregge.';
COMMENT ON COLUMN marketing_consents.decision IS
    'La decisione presa. Lo stato attuale è la riga più recente; l''assenza di righe è assenza di consenso.';
COMMENT ON COLUMN marketing_consents.source IS
    'Come la decisione è stata raccolta: è la parte della prova che dimostra che il consenso non è stato chiesto in blocco.';

-- «Qual è l'ultima decisione di questo utente»: è la domanda che precede ogni
-- invio di marketing, e la sola che il percorso caldo pone. L'ordine
-- decrescente su entrambe le colonne è quello della query, così la risposta è
-- la prima riga dell'indice.
CREATE INDEX marketing_consents_latest_idx
    ON marketing_consents (user_id, occurred_at DESC, id DESC);

-- ------------------------------------------------------------------ lo stato
--
-- La vista è ciò che rende la regola **una sola**: chiunque debba sapere se può
-- scrivere a qualcuno la interroga, invece di riscrivere il `DISTINCT ON` e
-- sbagliarlo. Un `ORDER BY occurred_at` senza `id` a rompere la parità
-- restituisce una riga qualsiasi fra due decisioni contemporanee — cioè,
-- proprio nel caso «ho ritirato e poi rimesso il consenso», la risposta
-- sbagliata la metà delle volte.
--
-- Non elenca chi non ha mai deciso, e non è una dimenticanza: **l'assenza di
-- una riga è assenza di consenso**. Il consenso dell'Art. 6(1)(a) è un atto
-- positivo, e una vista che ripiegasse su un valore predefinito per chi non
-- compare qui sarebbe il posto in cui quel predefinito potrebbe diventare
-- `true` per distrazione.
CREATE VIEW marketing_consent_state AS
SELECT DISTINCT ON (c.user_id)
       c.user_id,
       c.decision,
       c.occurred_at,
       c.source
  FROM marketing_consents c
 ORDER BY c.user_id, c.occurred_at DESC, c.id DESC;

COMMENT ON VIEW marketing_consent_state IS
    'Ultima decisione per utente. Chi non compare non ha consenso: l''assenza di una riga è assenza di consenso, non un valore predefinito.';
