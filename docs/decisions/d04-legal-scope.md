# D04 — Mercati e requisiti legali

- **Stato della decisione tecnica:** accettata per l'MVP
- **Stato dell'approvazione legale:** obbligatoria prima del go-live
- **Data:** 24 luglio 2026
- **Ambito:** mercato iniziale, documenti legali, prove delle scelte, cookie,
  marketing, fornitori e trasferimenti di dati

> Questo documento traduce il perimetro legale in requisiti di prodotto e
> software. Non sostituisce un parere legale. Dove è indicata
> un'«approvazione legale», il requisito tecnico può essere implementato ma la
> relativa funzione non può essere pubblicata finché un consulente abilitato
> non ha approvato la decisione e il testo destinato agli utenti.

## Decisione sintetica

1. L'MVP è offerto a clienti B2C e B2B **in Italia soltanto**, in lingua
   italiana e con valuta EUR.
2. La registrazione e l'acquisto richiedono un paese di residenza o sede e un
   indirizzo di fatturazione italiani. Le pagine pubbliche possono essere
   consultate altrove, ma non devono promuovere né consentire l'attivazione del
   servizio fuori dall'Italia.
3. Il termine generico «Marketing Act» è interpretato in questa decisione come
   lo svedese **Marketing Act (Marknadsföringslagen), SFS 2008:486**. La Svezia
   non è un mercato dell'MVP, quindi la legge non costituisce il requisito
   locale di lancio. Nessuna legge nazionale chiamata “Marketing Act” deve
   essere trattata come una norma europea generica.
4. Per il marketing rivolto all'Italia valgono il quadro UE e italiano
   descritto sotto. L'email promozionale è disattivata per impostazione
   predefinita e richiede un consenso preventivo, specifico e revocabile. La
   deroga italiana del cosiddetto “soft spam” non è usata nell'MVP.
5. I documenti pubblicati sono artefatti immutabili e versionati. Accettazioni,
   consensi, rifiuti e revoche sono eventi append-only collegati alla versione
   e al digest esatti del documento o della UI mostrata.
6. I cookie sono divisi in `necessary`, `preferences`, `analytics` e
   `marketing`. Solo `necessary` è attivo prima di una scelta. Le altre tre
   categorie richiedono opt-in separato.
7. L'hosting e i fornitori sono scelti con preferenza SEE. Un fornitore che
   tratta dati personali non può entrare in produzione senza classificazione
   del ruolo, DPA quando richiesto, censimento dei sub-responsabili e verifica
   del meccanismo di trasferimento.

## 1. Mercati e normativa

### 1.1 Perimetro territoriale

| Territorio | Stato MVP | Conseguenza software |
| --- | --- | --- |
| Italia | Incluso | `IT` è l'unico valore ammesso dall'allowlist di attivazione e acquisto. |
| Altri paesi UE/SEE, inclusa la Svezia | Esclusi | Consultazione pubblica consentita; registrazione attiva, trial, acquisto e campagne locali bloccati. |
| Regno Unito, Svizzera e paesi extra SEE | Esclusi | Stesso blocco; nessuna deduzione di equivalenza dal fatto che un paese abbia una decisione di adeguatezza. |

Il paese contrattuale è una scelta esplicita dell'utente e viene verificato
rispetto ai dati di fatturazione. L'indirizzo IP può essere usato come segnale
antifrode, non come unica prova del paese e non per cambiare silenziosamente il
paese dichiarato.

L'allowlist dei mercati deve essere applicata dal backend, non solo
dall'interfaccia. L'aggiunta di un paese è una release legale separata che
richiede almeno: analisi B2C/B2B locale, privacy ed e-privacy, cookie,
comunicazioni commerciali, imposte e fatturazione, lingua obbligatoria,
meccanismi di recesso/reclamo, termini aggiornati e una nuova approvazione
legale registrata. Una feature flag da sola non costituisce approvazione.

### 1.2 Norme di riferimento per il lancio italiano

Il perimetro minimo da sottoporre al consulente comprende:

- Regolamento (UE) 2016/679 (**GDPR**): principi, informativa, basi giuridiche,
  diritti, privacy by design, sicurezza, rapporti titolare-responsabile e
  trasferimenti internazionali;
- direttiva 2002/58/CE (**ePrivacy**) come recepita in Italia, in particolare
  per accesso al terminale, cookie e comunicazioni elettroniche;
- d.lgs. 196/2003 (**Codice in materia di protezione dei dati personali**),
  inclusi gli artt. 122 e 130;
- d.lgs. 206/2005 (**Codice del consumo**), incluse pratiche commerciali,
  informazioni precontrattuali, contratti a distanza, recesso e servizi
  digitali per utenti consumatori;
- d.lgs. 70/2003 sul **commercio elettronico**, incluse informazioni permanenti
  sul prestatore, comunicazioni commerciali e conclusione del contratto;
- direttive 2005/29/CE sulle pratiche commerciali sleali, 2011/83/UE sui
  diritti dei consumatori, 93/13/CEE sulle clausole abusive e 2019/770 sui
  servizi digitali, come recepite e vigenti in Italia.

Il consulente deve verificare il testo consolidato e l'applicabilità effettiva
alla data del go-live. Il prodotto deve distinguere cliente consumatore e
cliente professionale perché informative, recesso e rimedi possono differire.

### 1.3 Risoluzione di “Marketing Act”

La fonte istituzionale che usa esattamente il titolo inglese “The Marketing
Act” identifica la legge svedese **SFS 2008:486**. La traduzione inglese
pubblicata dal Governo svedese è dichiarata non ufficiale e non necessariamente
aggiornata; un eventuale lancio in Svezia richiede quindi verifica del testo
autentico vigente e consulenza locale.

Per l'MVP:

- `SE` resta fuori dall'allowlist;
- non vengono acquistate campagne specifiche per la Svezia, usati domini
  svedesi o prodotti testi/prezzi in svedese;
- le regole italiane su pratiche commerciali e marketing non vengono
  etichettate nel codice come `marketing_act`;
- la conformità dell'interfaccia italiana a trasparenza, assenza di dark
  pattern e opt-in non è considerata automaticamente sufficiente per la
  Svezia o per altri paesi.

## 2. Documenti legali e pubblicazione

### 2.1 Set minimo

| Chiave stabile | Documento | Pubblico | Azione richiesta |
| --- | --- | --- | --- |
| `terms_it` | Termini e condizioni, incluse condizioni B2C/B2B | Sì | Accettazione contrattuale obbligatoria |
| `privacy_it` | Informativa privacy ai sensi degli artt. 13-14 GDPR | Sì | Presa visione registrabile, non “consenso privacy” |
| `cookies_it` | Cookie Policy con inventario aggiornato | Sì | Preferenze granulari per finalità |
| `dpa_it` | Data Processing Agreement per clienti per cui Postqron è responsabile | Sì o su link stabile | Accettazione del rappresentante autorizzato B2B |
| `subprocessors` | Registro dei sub-responsabili e dei trasferimenti | Sì | Consultazione; notifica delle modifiche secondo il DPA |

Termini, informativa e Cookie Policy devono essere raggiungibili senza login.
Il footer espone link permanenti ai documenti vigenti e a “Rivedi le tue scelte
sui cookie”. Durante la registrazione i link aprono la versione che l'utente
sta accettando, non un alias che può cambiare durante il flusso.

L'identità e i contatti del soggetto che stipula il contratto e del titolare
del trattamento non sono deducibili dal solo credito “Sviluppato da
APDSoftware”: devono essere approvati e inseriti nei documenti prima del
go-live.

### 2.2 Modello di versione

Ogni pubblicazione crea una nuova riga e un artefatto non modificabile con:

- `document_key`, `jurisdiction` (`IT`) e `locale` (`it-IT`);
- versione monotona `major.minor` assegnata dal processo legale;
- `published_at`, `effective_at` e, se ritirata, `superseded_at`, in UTC;
- digest `sha256` dei byte esatti resi all'utente;
- URL permanente della versione e URL alias della versione corrente;
- identificativo dell'approvazione legale, senza firme o dati personali nel
  repository;
- tipo di modifica (`material` o `non_material`) e regola di notifica/
  riaccettazione approvata.

Una correzione, anche tipografica, non sovrascrive mai l'artefatto pubblicato.
Rollback significa ripubblicare una versione approvata come nuova versione
corrente conservando tutta la cronologia.

### 2.3 Eventi di prova

Il registro append-only supporta almeno `accepted`, `acknowledged`, `granted`,
`rejected` e `withdrawn`. Ogni evento contiene:

- soggetto autenticato oppure identificatore pseudonimo del browser per i
  cookie;
- workspace, se applicabile;
- chiave, versione e digest del documento o della UI di consenso;
- finalità o categoria esatta, senza consensi cumulativi ambigui;
- esito, timestamp UTC, locale, paese contrattuale e superficie UI;
- ID di correlazione/idempotenza e versione del testo del controllo mostrato.

IP completo e user-agent non sono raccolti come prova per impostazione
predefinita. Un loro eventuale uso richiede necessità documentata,
approvazione legale, minimizzazione e retention specifica.

Gli eventi storici non vengono modificati alla revoca: si aggiunge un evento
`withdrawn` con effetto immediato. L'accesso alle prove è limitato e
auditabile. Retention e cancellazione delle prove devono essere definite dal
consulente in base a finalità e termini di tutela; non possono coincidere
automaticamente con la durata dell'account.

### 2.4 Quando richiedere una nuova azione

- **Termini:** una modifica materiale richiede una nuova accettazione prima di
  continuare a usare le funzioni autenticate; il consulente approva cosa sia
  materiale, preavviso ed eventuali diritti di recesso.
- **Privacy:** l'informativa descrive il trattamento e non è una base di
  consenso generale. Una nuova versione viene notificata e può avere presa
  visione, ma non viene presentata come “accettazione della privacy”.
- **Marketing:** una finalità o un canale nuovi richiedono un nuovo opt-in. Il
  silenzio, l'uso del servizio o l'accettazione dei Termini non valgono come
  consenso.
- **Cookie:** l'aggiunta di una finalità, categoria o terza parte non coperta
  dalla scelta precedente richiede una nuova scelta prima dell'attivazione.
- **DPA:** le modifiche seguono il meccanismo contrattuale approvato per i
  clienti B2B e non sono accettabili da un membro privo di potere.

## 3. Basi giuridiche e ruoli privacy

La seguente matrice è il default tecnico. Ogni riga deve essere confermata nel
registro dei trattamenti e nell'informativa dal consulente prima del go-live.

| Trattamento | Ruolo previsto di Postqron | Base prevista | Regola software |
| --- | --- | --- | --- |
| Account, autenticazione, workspace, programmazione e assistenza richiesta | Titolare per dati account; normalmente responsabile per contenuti trattati per conto del cliente | Art. 6(1)(b), esecuzione del contratto | Raccogliere solo dati necessari; separare dati account e contenuti cliente. |
| Fatturazione, contabilità e richieste delle autorità | Titolare | Art. 6(1)(c), obbligo legale | Retention per categoria e accesso ristretto; norma e durata approvate. |
| Sicurezza, prevenzione abusi, audit sensibili e difesa di diritti | Titolare | Art. 6(1)(f), interesse legittimo, ove applicabile | LIA documentata, minimizzazione, retention e opt-out quando richiesto. |
| Pubblicazione sui social scelta dal cliente | Responsabile o titolare a seconda del dato e dei termini del provider | Art. 6(1)(b) per i dati account; istruzioni/DPA per dati trattati per conto | Classificare ogni flusso e provider; nessuna qualificazione automatica. |
| Email strettamente transazionali e di sicurezza | Titolare | Art. 6(1)(b), 6(1)(c) o 6(1)(f) secondo evento | Template e code separati dal marketing; nessun contenuto promozionale accessorio. |
| Newsletter, offerte e campagne Postqron | Titolare | Art. 6(1)(a) e disciplina speciale dell'art. 130 | Opt-in separato, prova, unsubscribe in ogni messaggio e suppression list. |
| Cookie e tracciatori non necessari | Titolare, con ruoli delle terze parti da verificare | Consenso e disciplina ePrivacy/art. 122 | Blocco preventivo, scelta per categoria e revoca semplice. |
| Export, cancellazione, opposizione e altre richieste privacy | Titolare; responsabile quando eseguito su istruzione del cliente | Art. 6(1)(c), obbligo legale | Workflow autenticato, scadenze auditabili e verifica dell'identità proporzionata. |

Il consenso non viene usato se il servizio non può ragionevolmente essere
erogato senza quel trattamento contrattuale. Nessuna casella facoltativa è
preselezionata. I dati che rivelano categorie particolari ai sensi dell'art. 9
GDPR non sono richiesti; se compaiono nei contenuti caricati dal cliente,
devono essere coperti dalle sue istruzioni e dal DPA, senza riuso da parte di
Postqron.

Per il marketing elettronico italiano il backend usa soltanto destinatari con
consenso valido per finalità e canale. L'MVP non importa liste acquistate, non
deduce consenso da fonti pubbliche o social e non usa il legittimo interesse o
il “soft spam” per aggirare l'opt-in. La revoca aggiorna prima la suppression
list e poi i sistemi di invio, in modo idempotente.

## 4. Cookie, tracciatori e revoca

### 4.1 Categorie

| Codice | Esempi ammessi | Default | Consenso |
| --- | --- | --- | --- |
| `necessary` | Sessione, sicurezza, bilanciamento, scelta cookie, completamento del pagamento | Attivo | Non richiesto; documentare necessità e durata |
| `preferences` | Preferenze non indispensabili tra visite o dispositivi | Inattivo | Opt-in separato |
| `analytics` | Misurazione del pubblico e comportamento non strettamente necessario | Inattivo | Opt-in separato nell'MVP |
| `marketing` | Advertising, retargeting, profilazione e attribuzione cross-site | Inattivo | Opt-in separato |

Un cookie analitico non viene riclassificato come necessario. L'eventuale
esenzione prevista per analytics con garanzie specifiche può essere introdotta
solo dopo inventario e approvazione legale; fino ad allora resta opt-in.

L'inventario contiene per ogni cookie, SDK, pixel, local storage o tecnica
equivalente: nome, dominio/terza parte, finalità, categoria, dati, durata,
prima/terza parte, destinatari, paese di trattamento e link alla policy.
La scansione automatica aiuta il controllo ma non sostituisce la
classificazione umana.

### 4.2 Interfaccia e comportamento

- Al primo accesso nessun tag opzionale viene richiesto, precaricato o eseguito.
- Il primo livello offre `Accetta tutte`, `Rifiuta tutte` e `Personalizza` con
  pari facilità e senza caselle preattivate.
- La chiusura, lo scroll o la prosecuzione della navigazione non valgono come
  consenso. Il rifiuto non impedisce le funzioni acquistate, salvo spiegare
  una funzione realmente dipendente da una preferenza opzionale.
- La scelta è salvata per un massimo di **sei mesi**, salvo nuova richiesta
  giustificata, modifica sostanziale o cancellazione locale da parte
  dell'utente.
- Il footer e l'area account espongono sempre “Rivedi le tue scelte sui
  cookie”. Revocare richiede non più passaggi che prestare il consenso.
- La revoca impedisce immediatamente nuove letture/scritture e nuovi eventi,
  elimina i cookie opzionali accessibili a Postqron, invoca le API di revoca
  disponibili dei tag già caricati e registra `withdrawn`. La UI avverte
  chiaramente se cookie di terze parti devono essere rimossi dal relativo
  dominio/browser.
- La modifica di una sola categoria non altera le altre. Lo stato lato server
  e quello del browser convergono in modo idempotente sul timestamp più
  recente.

## 5. Fornitori, DPA, sub-responsabili e trasferimenti

### 5.1 Classificazione e DPA

Prima di attivare un fornitore, il proprietario del servizio compila un
registro con servizio, finalità, categorie di dati/interessati, istruzioni,
ruolo privacy, località di trattamento/supporto, retention, misure di
sicurezza, subfornitori e meccanismo di trasferimento.

- Se Postqron è titolare e il fornitore tratta dati per suo conto, è richiesto
  un accordo conforme all'art. 28 GDPR prima dell'accesso ai dati.
- Se Postqron tratta dati per conto di un cliente, il DPA cliente definisce
  oggetto, durata, istruzioni, sicurezza, assistenza sui diritti,
  cancellazione/restituzione, audit e autorizzazione dei sub-responsabili.
- Un social network, provider OAuth o di pagamento può essere titolare
  autonomo, contitolare o responsabile a seconda del flusso e del contratto:
  l'etichetta commerciale “integrazione” non decide il ruolo.
- Nessuna chiave di produzione o dato personale viene fornito prima che la
  classificazione e i contratti risultino approvati.

### 5.2 Registro pubblico dei sub-responsabili

Il registro pubblico mostra almeno nome legale, servizio/finalità, paese di
stabilimento e trattamento, categorie generali di dati, meccanismo di
trasferimento e data di ingresso. Conserva anche la cronologia dei fornitori
rimossi.

Il default contrattuale tecnico è autorizzazione generale con:

1. preavviso di almeno **30 giorni** per aggiunta o sostituzione;
2. notifica all'Owner e archivio della prova di consegna;
3. canale per obiezione motivata prima dell'attivazione;
4. sospensione dell'attivazione per il cliente che ha sollevato obiezione
   finché non viene risolta secondo il DPA.

Durata, rimedi e conseguenze dell'obiezione richiedono approvazione legale nel
DPA; il software non può interpretare il silenzio come rinuncia a diritti non
prevista dal contratto.

### 5.3 Trasferimenti fuori SEE

L'architettura preferisce storage, database, backup, log e supporto nel SEE.
“Regione UE” non basta: accesso remoto, assistenza, telemetria, disaster
recovery e subfornitori sono inclusi nella mappa dei trasferimenti.

Un trasferimento extra SEE è bloccato finché il registro non contiene:

1. destinazione, importatore, dati, finalità e necessità;
2. decisione di adeguatezza vigente **oppure** garanzia appropriata;
3. quando si usano SCC, modulo corretto delle SCC 2021/914, allegati
   completati, Transfer Impact Assessment e misure supplementari necessarie;
4. verifica dell'efficacia e data della prossima revisione;
5. informativa, DPA e registro sub-responsabili aggiornati.

Per gli Stati Uniti, l'EU-US Data Privacy Framework può essere usato soltanto
se la decisione è vigente e l'esatta entità destinataria risulta certificata
per i dati interessati; il nome di gruppo o una dichiarazione del vendor non
bastano. In assenza, si applica il percorso SCC/TIA oppure il trasferimento
resta disabilitato. Le deroghe dell'art. 49 non sono una base ordinaria per
fornitori ricorrenti.

Il sistema deve poter disattivare un flusso o un fornitore senza perdere le
prove contrattuali se adeguatezza, certificazione o garanzie cessano di essere
valide.

## 6. Separazione tra decisioni tecniche e approvazioni legali

### 6.1 Decisioni tecniche adottate

Engineering e prodotto possono implementare senza reinterpretare il diritto:

- allowlist server-side con il solo paese `IT`;
- ledger append-only di versioni ed eventi di prova;
- separazione tra accettazione dei Termini, presa visione della Privacy e
  consensi facoltativi;
- CMP con blocco preventivo e quattro categorie;
- marketing opt-in e suppression list; soft spam disattivato;
- infrastruttura SEE-first e gate di onboarding fornitori;
- registro pubblico versionato dei sub-responsabili;
- blocco di pubblicazione in assenza di una release legale completa.

### 6.2 Approvazioni riservate al consulente

Prima del go-live devono risultare approvati:

- entità contraente, titolare/i, contatti, eventuale DPO e autorità
  competente;
- applicabilità e testi vigenti delle norme, inclusa la conclusione sul
  Marketing Act per i mercati effettivamente target;
- testi e versioni di Termini, Privacy, Cookie Policy e DPA;
- ruoli, basi giuridiche, LIA, registro dei trattamenti, DPIA quando richiesta
  e informative dei singoli provider;
- inventario/categoria/durata di ogni cookie e tracciatore;
- retention per account, contenuti, token, log, fatture, richieste privacy,
  prove e backup;
- condizioni B2C/B2B, prezzi/imposte, recesso, rimedi, reclami e limiti di età;
- DPA dei fornitori, lista sub-responsabili, SCC/TIA, misure supplementari e
  decisioni di adeguatezza/certificazioni;
- modalità e preavviso di modifiche contrattuali e di aggiunta dei
  sub-responsabili.

La release legale è un record distinto dall'approvazione tecnica e contiene
gli ID delle approvazioni, le versioni/digest pubblicabili, il paese e la data
di efficacia. Il deploy rifiuta configurazioni senza record valido. Un
checkbox di un developer o l'avvenuta implementazione non sostituiscono la
firma legale.

## 7. Gate di go-live e verifica

Il mercato `IT` può essere abilitato soltanto quando tutti i controlli seguenti
sono verificabili:

- [ ] documenti approvati, versionati, pubblici e collegati ai digest del
      record di release;
- [ ] registro dei trattamenti, basi, ruoli e retention approvati;
- [ ] flussi B2C/B2B, recesso, fatturazione e contatti legali approvati;
- [ ] scansione e inventario manuale dei cookie coincidono; nessun tag
      opzionale parte prima dell'opt-in;
- [ ] rifiuto e revoca sono testati end-to-end e fermano marketing/tracciatori;
- [ ] email transazionali e marketing usano code, template e regole separate;
- [ ] ogni fornitore ha classificazione, DPA se necessario, località,
      sub-responsabili e trasferimento approvati;
- [ ] export, cancellazione e richieste privacy hanno workflow e audit;
- [ ] backend blocca attivazione/acquisto per ogni paese diverso da `IT`;
- [ ] un consulente ha registrato la release legale per `IT`.

## Fonti istituzionali

Fonti consultate il 24 luglio 2026; prima del go-live occorre ricontrollarne
versione e vigenza:

- [Regolamento (UE) 2016/679 — GDPR, EUR-Lex](https://eur-lex.europa.eu/eli/reg/2016/679/oj?locale=it)
- [Direttiva 2002/58/CE — ePrivacy, EUR-Lex](https://eur-lex.europa.eu/eli/dir/2002/58/oj)
- [Linee guida cookie e altri strumenti di tracciamento, Garante, 10 giugno 2021](https://www.garanteprivacy.it/home/docweb/-/docweb-display/docweb/9677876)
- [Linee guida su attività promozionale e spam, Garante](https://www.garanteprivacy.it/home/docweb/-/docweb-display/docweb/2542348)
- [Codice del consumo — d.lgs. 206/2005, Normattiva](https://www.normattiva.it/atto/caricaDettaglioAtto?atto.codiceRedazionale=005G0232&atto.dataPubblicazioneGazzetta=2005-10-08)
- [Commercio elettronico — d.lgs. 70/2003, Normattiva](https://www.normattiva.it/atto/caricaDettaglioAtto?atto.codiceRedazionale=003G0090&atto.dataPubblicazioneGazzetta=2003-04-14)
- [Unfair Commercial Practices Directive, Commissione europea](https://commission.europa.eu/law/law-topic/consumer-protection-law/unfair-commercial-practices-and-price-indication/unfair-commercial-practices-directive_en)
- [Consumer Rights Directive, Commissione europea](https://commission.europa.eu/law/law-topic/consumer-protection-law/consumer-contract-law/consumer-rights-directive_en)
- [Trasferimenti internazionali di dati, Commissione europea](https://commission.europa.eu/law/law-topic/data-protection/international-dimension-data-protection/rules-international-data-transfers_en)
- [Decisioni di adeguatezza, Commissione europea](https://commission.europa.eu/law/law-topic/data-protection/international-dimension-data-protection/adequacy-decisions_en)
- [Standard Contractual Clauses, Commissione europea](https://commission.europa.eu/law/law-topic/data-protection/international-dimension-data-protection/standard-contractual-clauses-scc_en)
- [The Marketing Act (Marknadsföringslagen), SFS 2008:486, Governo svedese](https://www.government.se/government-policy/consumer-affairs/the-marketing-act-marknadsforingslagen/)
