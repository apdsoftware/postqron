# D05 — Retention e obiettivi operativi

- **Stato:** adottata per l'MVP
- **Data:** 2026-07-24
- **Ambito:** dati applicativi, cancellazione, export, affidabilità e ripristino
- **Owner della policy:** Privacy Owner e Engineering Owner
- **Riesame ordinario:** almeno annuale e prima di ogni nuovo paese, provider o categoria di dati

## Contesto e principi

Postqron tratta dati di account, contenuti social, media e credenziali OAuth e deve
rendere verificabili sia la cancellazione sia la capacità di recupero del servizio.
Questa decisione fissa valori misurabili per l'MVP. Non sostituisce la revisione
legale richiesta prima del go-live.

Si applicano questi principi:

1. conservare ogni dato identificabile solo per uno scopo dichiarato e per il
   periodo minimo necessario;
2. distinguere cancellazione logica, eliminazione fisica e anonimizzazione
   irreversibile;
3. revocare l'accesso e interrompere le elaborazioni prima di eliminare i dati;
4. non prolungare la retention con copie di log, cache, export o backup;
5. misurare SLO, RPO e RTO dal punto di vista dell'utente e provare
   periodicamente il ripristino.

Tutte le durate sono giorni di calendario, salvo dove è indicato diversamente.
Il termine di retention decorre dall'evento riportato nella tabella. Per
**anonimizzazione** si intende una trasformazione non reversibile e non
ricollegabile a una persona con mezzi ragionevolmente disponibili; la sola
pseudonimizzazione non equivale a cancellazione.

## Policy di retention

| Categoria | Periodo e decorrenza | Azione alla scadenza |
| --- | --- | --- |
| Account, profilo, identità federate e membership | Per la durata dell'account. Dopo 24 mesi senza login o attività viene avviata la chiusura per inattività, con preavviso a 30 e 7 giorni. Una richiesta confermata di cancellazione apre il grace period definito sotto. | Eliminazione dei dati identificativi e delle identità collegate; anonimizzazione dei riferimenti che devono restare per integrità del workspace. |
| Workspace | Per la durata del workspace. La cancellazione richiede conferma dell'Owner e segue il grace period. La cancellazione del solo account di un Member non elimina il workspace. | Eliminazione di configurazione e dati del workspace; se viene eliminato solo un Member, rimozione della membership e anonimizzazione della sua attribuzione nei contenuti condivisi. |
| Contenuti, pianificazioni ed esiti di pubblicazione | Finché esistono nel workspace. Un contenuto eliminato dall'utente resta recuperabile per 7 giorni; poi viene eliminato entro 24 ore. La cancellazione del workspace segue il grace period. | Eliminazione di testo, destinazioni e metadati personali. Possono restare solo conteggi aggregati anonimi e, per gli eventi di sicurezza, i record coperti dalla retention audit. La rimozione locale non cancella automaticamente contenuti già pubblicati sui social. |
| Media | Upload mai collegati a un contenuto: 24 ore. Media scollegati da tutti i contenuti: 7 giorni. Media collegati: stessa retention del contenuto. Cache CDN: massimo 24 ore dalla cancellazione dell'oggetto sorgente. | Eliminazione dall'object storage, delle anteprime e delle copie di trasformazione; invalidazione delle cache. |
| Token OAuth social | Fino a disconnessione, revoca del provider o avvio della cancellazione. Alla prima di queste condizioni la revoca è tentata e il ciphertext locale viene eliminato entro 15 minuti. | Revoca presso il provider quando supportata ed eliminazione di access token, refresh token e chiavi associate. Un successivo annullamento della cancellazione richiede una nuova connessione del social. |
| Sessioni e artefatti OAuth/OIDC | Sessioni: fino a logout, scadenza configurata o avvio della cancellazione; revoca entro 5 minuti. `state`, nonce e PKCE verifier: massimo 15 minuti. | Invalidazione server-side e cancellazione. Questi valori non autorizzano a registrare token o segreti nei log. |
| Audit log | 12 mesi dalla registrazione. Il payload deve contenere identificativi interni e il minimo dato personale necessario, mai token, contenuti o segreti. | Eliminazione oppure aggregazione anonima. Un legal hold approvato può sospendere la scadenza per i soli record interessati. |
| Log operativi | 30 giorni dalla registrazione. Tracce diagnostiche con accesso ristretto: massimo 90 giorni se collegate a un incidente aperto. Metriche aggregate senza dati personali: 13 mesi. | Eliminazione; le metriche possono restare solo se anonime. |
| Backup | Backup e WAL sono cifrati, isolati dalla produzione e conservati al massimo 35 giorni dalla creazione. Non sono usati per analytics o consultazione ordinaria. | Scadenza automatica e cancellazione dal sistema di backup. Le cancellazioni vengono riapplicate dopo ogni restore come descritto sotto. |
| Pacchetti export | 7 giorni dalla creazione del pacchetto, anche se non scaricato. Il link di download scade dopo 24 ore. | Eliminazione del pacchetto e invalidazione di tutti i link. Il log minimale della richiesta segue la retention audit. |
| Dati aggregati anonimi | Nessun limite di retention, solo se è stata verificata l'anonimizzazione irreversibile e sono vietati arricchimento e re-identificazione. | Riesame annuale del rischio di re-identificazione; se la soglia non è più soddisfatta, il dataset torna soggetto alla categoria di origine. |

Le registrazioni fiscali o contabili obbligatorie non possono essere definite
finché non sono scelti paesi di lancio e provider di pagamento. Prima di trattare
pagamenti, Legal/Privacy deve aggiungere alla relativa policy le categorie, la
base giuridica e la durata imposta per giurisdizione. Questa eccezione non
autorizza a conservare token social, contenuti o media.

## Cancellazione, grace period e anonimizzazione

### Avvio e revoca

Una cancellazione volontaria richiede autenticazione recente e conferma
esplicita. Al tempo `T0` della conferma il sistema:

- rende account o workspace non operativo e impedisce nuove pianificazioni;
- revoca tutte le sessioni entro 5 minuti;
- tenta la revoca dei token social e ne elimina le copie locali entro 15 minuti;
- marca come annullati i job futuri e li rimuove dalla coda entro 5 minuti;
- richiede ai worker in esecuzione di fermarsi prima della chiamata al provider.

Se un provider ha già accettato una pubblicazione, Postqron non può garantirne il
richiamo: registra l'esito, non effettua retry e informa l'utente. I job devono
verificare lo stato di cancellazione subito prima di ogni effetto esterno; la
rimozione dalla coda da sola non è considerata sufficiente.

### Grace period e completamento

Il grace period è di **28 giorni** da `T0`. Durante questo periodo i dati sono
congelati e non vengono usati salvo che per annullare la richiesta, adempiere a
un obbligo di legge o gestire un incidente di sicurezza. L'utente può rinunciare
al grace period e richiedere la finalizzazione immediata.

L'utente può annullare la cancellazione entro il grace period con autenticazione
recente. Le sessioni restano revocate, i social devono essere riconnessi e i job
annullati non vengono ripristinati automaticamente.

Alla scadenza del grace period, o subito in caso di rinuncia:

- dati nel database primario, cache e indici sono eliminati o anonimizzati entro
  48 ore;
- media, anteprime e trasformazioni sono eliminati entro 7 giorni;
- viene conservata per 45 giorni solo una tombstone con identificatore interno
  opaco e timestamp, necessaria a riapplicare la cancellazione dopo un restore;
- le copie nei backup non vengono modificate: diventano irrecuperabili alla
  scadenza naturale, al massimo 35 giorni dopo la finalizzazione;
- dopo un restore, le tombstone vengono applicate prima di riaprire il servizio e
  i dati interessati sono nuovamente eliminati entro 24 ore.

La procedura deve produrre un evento audit di avvio, annullamento o
completamento, senza conservare il contenuto cancellato. Un errore di
finalizzazione apre un incidente e un alert immediato; non estende
silenziosamente le scadenze.

## Export dei dati

### Contenuto e formato

Un export personale contiene, per quanto riferibile al richiedente:

- profilo, identità/provider collegati senza credenziali, membership e
  preferenze;
- versioni, date e prove dei consensi;
- metadati dei canali social senza access token o refresh token;
- contenuti, pianificazioni, fuso IANA, destinazioni ed esiti che l'utente è
  autorizzato a consultare;
- media originali ancora conservati;
- eventi audit visibili all'utente e dati di piano o sottoscrizione disponibili.

Un export completo del workspace può essere richiesto solo da un Owner. I dati
personali degli altri membri sono esclusi o minimizzati se non indispensabili
alla comprensione dei contenuti. Non vengono mai esportati segreti, token,
credenziali, dati di altri tenant, contenuti già cancellati, copie di backup,
regole antifrode o dettagli che compromettano la sicurezza o i diritti altrui.

Il pacchetto usa un archivio ZIP con:

- dati strutturati in JSON UTF-8 e, per le tabelle principali, CSV;
- media nel formato originale;
- `README` con descrizione dei campi e schema versionato;
- manifest con istante UTC di generazione, fusi IANA, numero di record e checksum
  SHA-256 di ogni file.

### Generazione, accesso e scadenza

Il richiedente si autentica nuovamente all'avvio. Il target operativo è rendere
pronto il 95% degli export entro 24 ore e il 100% entro 7 giorni; resta fermo il
termine legale applicabile, che non può essere superato dal target tecnico.

Il pacchetto è cifrato a riposo e trasferito solo via TLS. Il download richiede
una sessione autenticata e un URL firmato con scadenza di 24 ore. Il pacchetto
viene eliminato dopo 7 giorni; fino a tale momento l'utente può ottenere un nuovo
link autenticandosi di nuovo. Creazione, download, rinnovo del link e
cancellazione sono registrati in audit senza salvare il contenuto del pacchetto.

## Obiettivi operativi

### SLI e SLO

Gli SLO sono misurati in produzione su finestre mensili, per tenant reali e
richieste sintetiche controllate. La dashboard deve mostrare valore corrente,
numeratore, denominatore, esclusioni e burn rate. Le manutenzioni programmate
contano come indisponibilità.

| Capacità | Indicatore e obiettivo |
| --- | --- |
| Disponibilità API e area autenticata | **99,9% mensile** di richieste valide non restituite come `5xx` e completate entro 2 secondi. `4xx` causati dal client non entrano nel denominatore. |
| Letture API comuni | **p95 ≤ 300 ms** lato server per profilo, piano, lista contenuti e calendario su dataset entro i limiti del piano. |
| Scritture API comuni | **p95 ≤ 500 ms** lato server per salvataggio bozza, programmazione, riprogrammazione e annullamento, esclusi upload e chiamate a provider esterni. |
| Presa in carico delle pubblicazioni | **99,5% mensile** dei job validi preso in carico dal worker entro 60 secondi dall'istante pianificato; **p95 ≤ 30 secondi**. |
| Persistenza dell'esito | **99,9% mensile** degli esiti ricevuti da un provider persistito entro 60 secondi, senza duplicare una pubblicazione. |
| Export | **95% entro 24 ore** e **100% entro 7 giorni** dalla richiesta verificata. |

Il tempo e la disponibilità delle API social sono misurati separatamente e non
sono imputati all'SLO API di Postqron. Restano invece imputabili errori degli
adapter, retry errati, perdita di job o gestione non corretta dei rate limit.
Upload, export e chiamate sincrone a provider hanno metriche p95 dedicate e non
possono essere nascostamente incluse o escluse dalle API comuni.

Quando il budget di errore mensile è consumato al 50%, Engineering apre una
revisione; al 100% sospende rilasci non essenziali finché il burn rate torna
sotto soglia o viene approvata un'eccezione.

### RPO e RTO

RPO misura la massima perdita di dati accettabile rispetto all'ultimo punto
coerente; RTO misura il tempo dall'avvio dichiarato dell'incidente al ripristino
verificato della capacità.

| Componente | RPO | RTO | Strategia minima |
| --- | ---: | ---: | --- |
| PostgreSQL, audit e stato durevole dei job | 15 minuti | 4 ore | Backup giornaliero più archiviazione continua dei WAL/PITR, cifrati e in un dominio di guasto separato. |
| Media e oggetti utente | 24 ore | 8 ore | Versionamento o snapshot giornaliero cifrato e inventario con checksum. |
| Configurazione applicativa e infrastruttura | 24 ore | 4 ore | Codice e IaC versionati; copia protetta delle configurazioni necessarie al bootstrap. I segreti restano nel secret manager, non nel repository. |
| Cache Redis e code derivate | Nessuna persistenza richiesta | 1 ora | Ricostruzione dal database autorevole. Nessun job può esistere soltanto in Redis. |
| Servizio end-to-end minimo | Come il componente autorevole coinvolto | 8 ore | API, database e almeno un worker funzionanti, con validazione di integrità prima della riapertura. |

## Backup e prove di ripristino

- I job di backup e la scadenza a 35 giorni sono controllati ogni giorno. Un
  backup mancante o non verificato entro 24 ore genera un alert.
- Ogni backup è cifrato, sottoposto a checksum e accessibile solo ai ruoli di
  recovery; le credenziali di produzione non sono sufficienti a cancellarlo.
- **Mensilmente** viene ripristinato l'ultimo backup di database in un ambiente
  isolato. Si verificano schema/migrazioni, integrità referenziale, conteggi,
  checksum di un campione di oggetti e applicazione delle tombstone.
- **Trimestralmente** si esegue una prova end-to-end partendo solo da backup,
  repository e secret manager. La prova misura RPO e RTO, avvia API e worker,
  esegue un job idempotente simulato e conferma che nessun effetto esterno reale
  sia possibile.
- **Annualmente**, e dopo ogni incidente severo o modifica sostanziale
  dell'architettura, si svolge un tabletop del piano di disaster recovery.
- Il report di ogni prova conserva per 13 mesi data, responsabili, backup usato,
  tempi, controlli, deviazioni e azioni correttive. Un test fallito apre un
  ticket con owner e scadenza; non vale come prova riuscita.

Il ripristino è concluso soltanto dopo i controlli di integrità, la
riapplicazione delle cancellazioni, la verifica dell'idempotenza dei job e
l'approvazione dell'Incident Commander.

## Eccezioni e approvatori

Nessuna eccezione può ampliare la raccolta dei dati oltre quanto dichiarato
all'utente o consentire la conservazione di token in chiaro.

| Tipo di modifica o eccezione | Approvazioni obbligatorie |
| --- | --- |
| Retention, export, cancellazione o anonimizzazione | Privacy Owner e Legal/DPO; Engineering Owner se richiede una modifica tecnica. |
| Legal hold su record specifici | Legal/DPO e Security Owner. Il Product Owner viene informato. |
| SLO o soglie p95 | Engineering Owner e Product Owner. |
| RPO, RTO, backup o frequenza dei test | Engineering Owner e Security Owner; Product Owner se riduce un obiettivo. |
| Eccezione operativa temporanea che usa budget di errore | Incident Commander ed Engineering Owner. |

Ogni eccezione deve avere ticket auditabile con motivazione e base giuridica,
categorie e tenant interessati, controlli compensativi, owner, data di inizio,
scadenza e piano di eliminazione. La durata massima è **90 giorni**, senza
rinnovo automatico. Un legal hold dura quanto richiesto dall'obbligo o
contenzioso, viene riesaminato almeno ogni 90 giorni e blocca solo i dati
strettamente necessari.

In emergenza, Incident Commander e Security Owner possono autorizzare
un'eccezione per massimo 7 giorni; Privacy Owner o Engineering Owner, secondo
l'ambito, deve ratificarla entro 2 giorni lavorativi oppure l'eccezione viene
revocata.

## Verifica e tracciabilità

Prima del go-live devono esistere controlli automatici per le scadenze,
dashboard SLO, alert backup, runbook di cancellazione/export e report dei restore.
Ogni riesame della policy deve confrontare configurazione e comportamento
effettivo con i valori di questo documento.

Questa decisione copre i criteri D5:

- retention di account, contenuti, media, token, audit, backup ed export;
- grace period, revoca, annullamento job e anonimizzazione;
- contenuto, protezione e scadenza degli export;
- SLO, p95, RPO, RTO e prove di ripristino;
- processo di eccezione e ruoli approvatori.

## Fonti

- [Regolamento (UE) 2016/679 (GDPR), in particolare artt. 5, 17, 20 e 32](https://eur-lex.europa.eu/eli/reg/2016/679/oj)
- [EDPB, guida ai diritti degli interessati](https://www.edpb.europa.eu/sme/be-compliant/respect-individuals-rights_en)
- [NIST SP 800-34 Rev. 1, Contingency Planning Guide for Federal Information Systems](https://csrc.nist.gov/pubs/sp/800/34/r1/upd1/final)
