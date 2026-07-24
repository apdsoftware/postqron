# D01 — Naming, tono e credito APDSoftware

- **Stato:** proposta per approvazione; diventa accettata con il merge della PR relativa alla issue #1
- **Data:** 24 luglio 2026
- **Ambito:** identità verbale e credito dello sviluppatore
- **Approvatore:** [@czuffetti](https://github.com/czuffetti), autore e responsabile della issue #1; l'approvazione è registrata tramite review e merge della PR

## Decisione

Il nome del prodotto è confermato come **Postqron**.

La grafia canonica usa la `P` maiuscola e il resto minuscolo. Il nome resta invariato
in ogni lingua, anche all'inizio di una frase. La forma tutta minuscola `postqron` è
riservata agli identificatori tecnici che non ammettono maiuscole, come repository,
domini o package.

Postqron è mantenuto perché:

- è già il nome coerente tra SPEC e repository;
- richiama il dominio dei post social e la pianificazione ricorrente senza descrivere
  il prodotto in modo generico;
- è breve e abbastanza distintivo da sostenere un'identità propria.

Questa decisione è di prodotto, non una verifica legale di disponibilità del marchio
o dei domini. Prima del lancio pubblico il titolare deve completare le verifiche
legali e commerciali necessarie.

### Descrizione breve

> Programma e gestisci i tuoi contenuti social da un unico spazio.

### Promessa di prodotto

> Postqron rende chiaro cosa verrà pubblicato, dove e quando.

La descrizione breve è preferita nei contesti introduttivi. La promessa può essere
usata nei messaggi di posizionamento, purché non venga presentata come garanzia di
pubblicazione da parte dei social network.

## Tono di voce

La voce di Postqron è:

- **chiara:** usa frasi brevi, verbi concreti e indica subito l'azione disponibile;
- **competente:** spiega vincoli ed esiti con precisione, senza tecnicismi non
  necessari;
- **calma:** segnala problemi senza allarmismo o colpevolizzazione;
- **utile:** dice cosa è successo, cosa comporta e come procedere;
- **trasparente:** non nasconde limiti del piano, attese, errori o dipendenze dai
  provider social.

Ci si rivolge all'utente con il **tu**. I testi dell'interfaccia usano la forma attiva
e un tono professionale ma naturale. Titoli, pulsanti e messaggi adottano il
sentence case; maiuscole e punti esclamativi non vengono usati per creare urgenza.

### Modello per i messaggi

- **Conferma:** risultato e, se utile, prossimo passo.
  Esempio: `Post programmato per il 28 luglio alle 10:30.`
- **Errore recuperabile:** problema, effetto e azione.
  Esempio: `Non siamo riusciti a pubblicare su LinkedIn. Ricollega il canale e riprova.`
- **Validazione:** campo o regola da correggere.
  Esempio: `Scegli una data futura per programmare il post.`
- **Azione irreversibile:** conseguenza esplicita e conferma non ambigua.
  Esempio: `Eliminando il post perderai testo e media. Questa azione non può essere annullata.`

Non si usa l'umorismo nei messaggi di errore, sicurezza, pagamento, privacy o
cancellazione.

## Messaggi e lessico

| Concetto | Termine da usare | Indicazione |
| --- | --- | --- |
| Contenuto destinato ai social | **post** | Usare “contenuto” solo come categoria generale o quando include più formati. |
| Profilo o pagina social collegata | **canale** | Specificare il provider quando evita ambiguità: “canale LinkedIn”. |
| Preparazione non ancora pianificata | **bozza** | Non suggerisce che il contenuto verrà pubblicato. |
| Pubblicazione futura | **programmare** | Mostrare sempre data, ora e fuso orario quando rilevanti. |
| Invio al social network | **pubblicare** | Non usare come sinonimo di “programmare”. |
| Ambiente condiviso di lavoro | **spazio di lavoro** | “Workspace” resta ammesso solo in documentazione tecnica. |
| Utente che gestisce lo spazio | **proprietario** | “Owner” resta ammesso solo in ruoli o contratti tecnici. |
| Utente invitato | **membro** | Evitare “collaboratore” se il ruolo effettivo è Member. |
| Problema di autorizzazione social | **da riconnettere** | Accompagnare con un'azione di riconnessione. |

Gli stati canonici visibili all'utente sono: **Bozza**, **Programmato**,
**In pubblicazione**, **Pubblicato**, **Fallito** e **Annullato**.

Le call to action descrivono l'azione: `Crea un post`, `Salva bozza`,
`Programma post`, `Riprogramma`, `Annulla programmazione` e `Riprova`.
Etichette vaghe come `Continua`, `OK` o `Conferma` sono ammesse solo quando il
contesto rende inequivocabile il risultato.

## Credito APDSoftware

La dicitura canonica è:

> Sviluppato da [APDSoftware](https://apdsoftware.it)

Il nome si scrive sempre **APDSoftware**, senza spazi e con `APD` e `S` maiuscole.
Il credito deve comparire nel footer del sito pubblico. L'intera dicitura deve
restare leggibile; almeno `APDSoftware` deve essere il testo accessibile del link.

L'URL canonico è `https://apdsoftware.it`. Il 24 luglio 2026:

- il sito ha risposto tramite HTTPS con stato HTTP `200` e URL effettivo
  `https://apdsoftware.it/`;
- il sito ufficiale usa `APDSoftware` nei contenuti e nel copyright;
- il profilo GitHub dell'organizzazione `apdsoftware`, proprietaria del repository
  Postqron, dichiara `https://apdsoftware.it` come sito.

## Regole d'uso

- Conservare la grafia canonica di Postqron e APDSoftware in UI, sito, email,
  documentazione e metadati rivolti al pubblico.
- Preferire messaggi specifici e verificabili a slogan assoluti.
- Separare sempre programmazione ed esito effettivo: un post programmato non è
  ancora pubblicato.
- Citare il provider o il canale quando un errore riguarda una sola destinazione.
- Esporre limiti, costi, fuso orario e conseguenze prima della conferma dell'azione.
- Adattare la frase alla lingua, ma non tradurre i nomi Postqron e APDSoftware.
- Mantenere il credito APDSoftware distinto da copyright, note legali e proprietà
  dei contenuti dell'utente.

## Casi vietati

- Alterare il nome in `PostQron`, `PostQRON`, `Post Cron`, `Post-cron`,
  `APD Software` o `Apdsoftware` nei contenuti pubblici.
- Usare superlativi o garanzie non dimostrabili, come “il migliore”, “senza errori”,
  “pubblicazione garantita” o “sempre online”.
- Attribuire un errore all'utente con formule come “hai sbagliato” o usare messaggi
  generici senza rimedio, come “Errore imprevisto”, quando è disponibile una causa
  utile.
- Creare urgenza artificiale con tutte maiuscole, più punti esclamativi, countdown
  ingannevoli o scarsità non reale.
- Presentare Postqron come affiliato, approvato o certificato da un social network
  senza autorizzazione documentata.
- Usare il credito APDSoftware come endorsement dei contenuti pubblicati dagli
  utenti o nascondere il link con testo generico come `qui`.
- Affermare o suggerire funzionalità non disponibili, incluse automazioni o capacità
  di intelligenza artificiale non ancora implementate.

## Fonti

- `.context/SPEC.md`, in particolare Executive Summary, F1, F2, F9, requisiti di
  accessibilità e decisioni aperte; letta il 24 luglio 2026.
- [Issue #1 — D1: Finalizzare naming, tono e credito APDSoftware](https://github.com/apdsoftware/postqron/issues/1);
  letta il 24 luglio 2026.
- [Sito ufficiale APDSoftware](https://apdsoftware.it); verificato il 24 luglio 2026.
- [Organizzazione GitHub apdsoftware](https://github.com/apdsoftware); metadati e
  proprietà del repository verificati il 24 luglio 2026.

## Conseguenze

Le future implementazioni di brand, sito, applicazione ed email devono riutilizzare
questa nomenclatura e queste regole. Logo, palette, tipografia, asset, dominio del
prodotto e registrazione del marchio restano fuori dall'ambito di questa decisione.
