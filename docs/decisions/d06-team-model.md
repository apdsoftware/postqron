# D06 — Modello team e ruoli per l'MVP

- **Stato:** accettata
- **Data:** 2026-07-24
- **Decisione collegata:** [D03 — Offerta commerciale e billing](https://github.com/apdsoftware/postqron/issues/3)
- **Issue di origine:** [#6 — Confermare modello team e ruoli](https://github.com/apdsoftware/postqron/issues/6)

## Contesto

La SPEC richiede un workspace personale creato al primo accesso e almeno i
ruoli Owner e Member. Indica inoltre che l'Owner gestisce membri, canali
social, piano e cancellazione del workspace, ma lascia aperta la necessità
effettiva dei team nell'MVP.

## Decisione

Il modello team è incluso nell'MVP.

Il workspace personale è un tenant collaborativo a tutti gli effetti: nasce
per un singolo utente, ma può accogliere altri utenti tramite invito. L'MVP
espone due soli ruoli fissi, `Owner` e `Member`; non prevede ruoli
personalizzati, permessi assegnati al singolo utente o un ruolo di
amministratore del workspace separato da `Owner`.

Un workspace può avere più Owner, ma deve avere sempre almeno un Owner finché
è attivo. Non esiste un “Owner principale”: tutti gli Owner hanno gli stessi
permessi.

Autorizzazioni e limiti sono verificati dal backend su ogni operazione. Il
client può nascondere o disabilitare controlli non disponibili, ma non è una
fonte di autorizzazione.

## Workspace personale e membership

- Al primo accesso completato, il sistema crea in modo idempotente un
  workspace personale e assegna all'utente una membership `Owner`.
- L'idempotenza è legata all'account: callback ripetuti, retry o richieste
  concorrenti non possono creare più workspace personali per lo stesso
  account.
- “Personale” descrive l'origine del workspace, non un vincolo permanente a
  un solo membro e non concede privilegi speciali al creatore.
- Un account può appartenere al proprio workspace e a più workspace ricevuti
  tramite invito. Nell'MVP non può creare manualmente workspace aggiuntivi.
- La membership è unica per la coppia account/workspace e contiene il ruolo e
  lo stato. I contenuti e i canali appartengono al workspace, non al membro che
  li ha creati o collegati.
- Trasferire il ruolo Owner, lasciare il workspace o rimuovere il creatore non
  crea automaticamente un nuovo workspace personale.

## Inviti

Solo un Owner può invitare nuovi membri.

1. L'Owner inserisce un indirizzo email e il backend verifica il limite membri
   definito da D03.
2. Il sistema crea un invito a ruolo `Member`, monouso, revocabile e valido
   per 7 giorni. Il token è casuale, memorizzato solo in forma non reversibile
   e sostituito a ogni reinvio.
3. Per accettare, il destinatario deve autenticarsi con lo stesso indirizzo
   email verificato dell'invito. L'accettazione non collega automaticamente
   account con identità ambigue o email non verificate.
4. L'accettazione crea una sola membership anche in presenza di retry. Se
   l'utente è già membro, l'operazione termina senza duplicati; un invito
   scaduto o revocato è rifiutato con un esito comprensibile.

Un Owner può revocare un invito pendente. Un nuovo invito allo stesso indirizzo
per lo stesso workspace sostituisce quello pendente invece di accumulare
prenotazioni di posti.

## Rimozione e uscita

- Un Owner può rimuovere un Member o un altro Owner, purché l'operazione non
  lasci il workspace senza Owner.
- Un Member può lasciare autonomamente il workspace.
- Un Owner può lasciare o retrocedersi solo se rimane almeno un altro Owner;
  altrimenti deve prima trasferire l'ownership oppure cancellare il workspace.
- Rimozione e uscita revocano immediatamente l'accesso futuro al workspace,
  senza cancellare l'account personale dell'utente né i contenuti del
  workspace.
- Revoca e controllo dell'ultimo Owner avvengono nella stessa transazione, con
  protezione dalle richieste concorrenti.

## Matrice Owner/Member

| Operazione | Owner | Member |
| --- | :---: | :---: |
| Visualizzare workspace, membri, canali, piano e utilizzo | Sì | Sì |
| Creare, modificare, programmare e annullare contenuti | Sì | Sì |
| Gestire contenuti creati da altri membri | Sì | Sì |
| Collegare, riconnettere o scollegare canali social | Sì | No |
| Invitare membri, reinviare o revocare inviti | Sì | No |
| Rimuovere membri e cambiare ruoli | Sì, con vincolo ultimo Owner | No |
| Modificare nome e impostazioni del workspace | Sì | No |
| Consultare e modificare piano o fatturazione | Sì | No |
| Trasferire l'ownership | Sì | No |
| Lasciare il workspace | Sì, se non è l'ultimo Owner | Sì |
| Richiedere la cancellazione del workspace | Sì | No |

La gestione del proprio profilo, dei propri provider di accesso e dei diritti
sui dati personali resta disponibile al singolo account e non dipende dal
ruolo nel workspace.

## Trasferimento dell'ownership e ultimo Owner

Un Owner può promuovere un Member a Owner. Il trasferimento completo esegue
atomicamente due cambi:

1. promuove a `Owner` un Member attivo del workspace;
2. retrocede a `Member` l'Owner che avvia il trasferimento.

La conferma del destinatario non è richiesta perché questi è già un membro
autenticato del workspace; entrambi ricevono una notifica dell'evento. Se il
destinatario non è più attivo al momento della scrittura, l'intera operazione
fallisce.

Promozione, trasferimento, retrocessione, rimozione e uscita serializzano il
controllo sulle membership Owner. Il backend rifiuta sempre uno stato finale
con zero Owner, anche quando due richieste concorrenti sarebbero valide se
valutate separatamente.

## Cancellazione del workspace

Qualunque Owner può richiedere la cancellazione dell'intero workspace, dopo
una nuova conferma dell'identità e una conferma esplicita dell'azione. La
richiesta riguarda tutti i membri e non equivale alla semplice uscita
dell'Owner.

Il workspace passa nello stato previsto dal flusso di cancellazione: non
accetta nuovi inviti, collegamenti o programmazioni e non esegue nuovi job di
pubblicazione. La durata del periodo di sicurezza, l'eventuale annullamento
della richiesta, la revoca dei token e l'eliminazione o anonimizzazione dei
dati sono definiti dalle decisioni di retention e dalla funzionalità F12.
Fino alla cancellazione definitiva rimane valido il vincolo di almeno un
Owner.

Richiesta, annullamento ed esecuzione della cancellazione sono eventi
registrati in audit log.

## Collegamento ai limiti D03

D03 è l'unica fonte dei valori numerici dei limiti per piano. D06 ne definisce
la semantica per i team:

| Piano D03 | Membri massimi per workspace |
| --- | ---: |
| Start | 1 |
| Pro | 5 |
| Team | 15 |
| Internal Unlimited | Illimitati |

- `member_limit` conta tutte le membership attive, inclusi gli Owner;
- ogni invito pendente non scaduto riserva un posto, così inviti concorrenti
  non possono superare il limite;
- inviti revocati o scaduti e membership rimosse non consumano posti;
- creazione e accettazione di un invito ricontrollano il limite lato backend
  nella stessa transazione che riserva o assegna il posto;
- promozioni, retrocessioni e trasferimenti non cambiano il consumo perché non
  creano membership;
- al downgrade non vengono rimossi membri automaticamente: le membership
  esistenti restano attive, ma nuovi inviti e accettazioni sono bloccati
  finché l'utilizzo non rientra nel limite;
- i limiti di canali e post programmati restano limiti del workspace e non
  vengono moltiplicati per il numero di membri;
- il piano interno illimitato usa la stessa autorizzazione Owner/Member, senza
  un ruolo privilegiato esposto nel prodotto.

In assenza di un entitlement valido, il backend non assume un limite
illimitato e restituisce un errore riprovabile senza perdere dati.

## Eventi di audit minimi

Devono essere tracciati almeno: invito creato, reinviato, revocato, scaduto e
accettato; membro rimosso o uscito; ruolo promosso o retrocesso; ownership
trasferita; cancellazione richiesta, annullata o eseguita. Ogni evento include
workspace, attore, soggetto, timestamp ed esito, evitando token e dati
personali non necessari.

## Fuori scope per l'MVP

- ruoli personalizzati, permessi granulari e gruppi;
- guest o accessi limitati a singoli contenuti o canali;
- inviti direttamente al ruolo Owner;
- creazione manuale di workspace aggiuntivi;
- directory aziendali, SSO organizzativo e provisioning SCIM;
- approvazioni editoriali, che restano nella funzionalità F17.

## Conseguenze

Il modello copre piccoli team senza introdurre un sistema RBAC configurabile.
Permettere più Owner evita che l'accesso amministrativo dipenda da un singolo
account; i vincoli transazionali su ultimo Owner e limiti impediscono stati
irrecuperabili o overbooking. Tutti i membri possono collaborare sui contenuti,
mentre canali, persone, piano e cancellazione rimangono operazioni Owner.

## Fonti e tracciabilità

- `.context/SPEC.md`: F4, F10, F11 e sezioni “Decisioni Aperte” e “Dipendenze
  tra Funzionalità”.
- Issue [#3 — D03](https://github.com/apdsoftware/postqron/issues/3): limiti
  membri, canali e post; downgrade senza cancellazioni automatiche; piano
  interno separato.
- Issue [#6 — D06](https://github.com/apdsoftware/postqron/issues/6): criteri
  di accettazione del modello team.
- Issue [#10 — F4](https://github.com/apdsoftware/postqron/issues/10):
  requisiti implementativi per workspace, membership, RBAC, ultimo Owner e
  concorrenza.
