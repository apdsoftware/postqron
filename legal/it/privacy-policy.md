---
document: privacy-policy
version: 1.1.0
effective_date: 2026-08-18
language: it
status: approved
---

# Informativa sulla privacy

Questa informativa spiega quali dati personali Postqron tratta, perché, e che cosa puoi
farci. È scritta per essere letta, non per essere sopportata.

## 1. Chi è il titolare

Il titolare del trattamento dei tuoi dati personali è
Apdsoftware di Carlo Zuffetti, Via C. Colombo 15, 24047 Treviglio (BG), Italy — VAT 03835250162, REA BG 431224.

Puoi scriverci a
privacy@postqron.com.

Non abbiamo nominato un Responsabile della protezione dei dati: il nostro trattamento non
integra le condizioni dell'art. 37 GDPR — non siamo un'autorità pubblica, la nostra
attività principale non consiste nel monitoraggio sistematico su larga scala, e non
trattiamo categorie particolari di dati su larga scala. Le richieste in materia di
privacy vanno all'indirizzo qui sopra e le gestiamo direttamente noi.

## 2. Che cosa trattiamo, e perché

### 2.1 Account e autenticazione

Indirizzo email, password (conservata solo come hash Argon2id — la password in sé non la
deteniamo mai), lingua preferita, sessioni e loro scadenza, e i token usati per verificare
il tuo indirizzo o reimpostare la password.

**Perché:** per fornire il servizio che hai richiesto. **Base giuridica:** esecuzione di
un contratto (art. 6(1)(b) GDPR).

### 2.2 Job ed esecuzioni

Le pianificazioni che definisci, gli indirizzi di destinazione, i metodi HTTP, le
intestazioni e i corpi che configuri, e per ogni esecuzione: l'ora di inizio e di fine, la
durata, l'esito, lo stato HTTP, un estratto troncato della risposta e il numero del
tentativo.

Due cose vanno dette chiaramente. Primo, **decidi tu che cosa finisce in un job**: se
metti dati personali in un URL, in un'intestazione o in un corpo, li tratteremo perché ce
li hai messi tu. Secondo, **gli estratti delle risposte vengono conservati**, quindi se il
sistema che chiami restituisce dati personali, quei dati arrivano nei nostri log.

**Perché:** per far funzionare il servizio e per farti vedere che cosa è successo. **Base
giuridica:** esecuzione di un contratto.

**Conservazione:** i log di esecuzione sono conservati per il periodo previsto dal tuo
piano — 3, 15, 30 o 90 giorni — e poi cancellati.

### 2.3 Sincronizzazione dei repository

Se colleghi un repository GitHub, trattiamo l'identificativo del repository, gli eventi
che GitHub ci invia quando fai push e il contenuto del file `cron.yaml`. Richiediamo
accesso in sola lettura ai contenuti e ai metadati del repository, e a nient'altro.

**Base giuridica:** esecuzione di un contratto.

### 2.4 Segreti e credenziali

I segreti del workspace, le chiavi API e le chiavi dei fornitori di AI sono cifrati a
riposo, non vengono mai restituiti in chiaro dopo il salvataggio e non vengono mai scritti
nei log.

### 2.5 Fatturazione

I pagamenti sono gestiti da Paddle in qualità di Merchant of Record (§4). Riceviamo lo
stato dell'abbonamento, il piano e gli identificativi necessari a riconciliarlo. **Non
vediamo mai la tua carta di pagamento.**

**Base giuridica:** esecuzione di un contratto e obbligo legale per le scritture fiscali.

### 2.6 Sicurezza e audit

Registrazioni degli eventi sensibili: accessi, cambi di piano, revoca di chiavi,
impersonificazione da parte di un amministratore. I log tecnici sono strutturati in modo
da escludere segreti e dati personali non necessari.

**Base giuridica:** legittimo interesse a far funzionare un servizio sicuro (art.
6(1)(f)), e obbligo legale dove applicabile.

### 2.7 Email transazionali

Ti inviamo le email che ti servono per usare il servizio: benvenuto, avvisi di job
falliti, cambi di piano, eventi di sicurezza. Non sono marketing e non puoi disiscriverti
senza chiudere l'account, perché sono il modo in cui il servizio ti dice le cose.

### 2.8 Email di marketing

Se acconsenti, ti inviamo email sul prodotto: nuove funzionalità, cambiamenti che vale la
pena conoscere, di tanto in tanto qualcosa che abbiamo scritto.

**Sono separate dalle email qui sopra sotto ogni profilo.** La base giuridica è il tuo
**consenso** (art. 6(1)(a)), richiesto per conto proprio e mai unito all'accettazione dei
termini o alla creazione di un account. Rifiutare non ti costa nulla: il servizio funziona
allo stesso modo.

Ogni messaggio di marketing porta con sé un link di disiscrizione che funziona con un clic
e senza dover accedere. La disiscrizione ferma solo le email di marketing — continui a
ricevere le email transazionali che il servizio deve mandarti, perché quelle non sono
marketing.

Conserviamo traccia di quando hai acconsentito e di quando hai revocato, ed è così che
possiamo dimostrare di aver avuto il diritto di scriverti.

## 3. Funzioni di AI: un trasferimento che conviene capire

Se attivi il debug assistito dall'AI, fornisci **la tua** chiave API di un fornitore di AI
(OpenAI, Anthropic o un altro). Quando usi la funzione, il contenuto del log di esecuzione
che stai analizzando viene inviato a quel fornitore con la tua chiave e alle sue
condizioni.

Questo significa che i tuoi dati lasciano la nostra infrastruttura e raggiungono un terzo
**che hai scelto tu**, in forza di un contratto **fra te e lui**. Noi non ne siamo parte,
non controlliamo che cosa ne fa del contenuto, e valgono le sue regole di conservazione,
non le nostre.

La funzione è disattivata finché non la attivi, e ogni analisi è un'azione deliberata. Ti
chiediamo il consenso esplicito prima del primo trasferimento.

**Base giuridica:** consenso (art. 6(1)(a)), che puoi revocare in qualsiasi momento
rimuovendo la tua chiave. La revoca non incide sui trasferimenti già effettuati.

## 4. Chi altro tratta i tuoi dati

Ci avvaliamo di questi fornitori. Ciascuno tratta i dati su nostra istruzione, in forza di
un accordo sul trattamento dei dati.

| Fornitore | Ruolo | Dove |
|---|---|---|
| Hetzner | Server e database | Germania |
| Cloudflare | DNS, TLS, CDN, hosting statico, protezione perimetrale | Rete edge globale |
| Paddle | Merchant of Record: pagamenti, fatturazione, imposte | Regno Unito |
| Mailronix | Recapito delle email transazionali | Unione europea — gestito da Apdsoftware, la stessa entità che gestisce Postqron |
| GitHub | Sincronizzazione dei repository, solo se ne colleghi uno | Stati Uniti |

Teniamo aggiornato questo elenco. Se aggiungiamo o cambiamo un fornitore in modo che ti
riguardi, aggiorniamo questa informativa e, quando il cambiamento è sostanziale, te lo
diciamo prima che abbia effetto.

**Trasferimenti fuori dallo SEE.** Alcuni fornitori trattano dati fuori dallo Spazio
economico europeo. Dove accade, ci basiamo sulle garanzie dell'art. 46 GDPR, in primo
luogo le Clausole contrattuali tipo della Commissione europea, insieme alle misure
tecniche del fornitore stesso.

## 5. Per quanto tempo conserviamo le cose

| Dato | Conservato |
|---|---|
| Account e profilo | Finché l'account esiste |
| Log di esecuzione | 3, 15, 30 o 90 giorni, secondo il piano |
| Registrazioni di audit | 24 mesi |
| Documenti contabili e fiscali | Per il tempo richiesto dalla legge, di norma 10 anni |
| Backup | 30 giorni |

Quando cancelli il tuo account interrompiamo subito l'esecuzione e revochiamo le chiavi,
poi rimuoviamo i dati dopo un periodo di ripensamento di
30 giorni,
durante il quale puoi cambiare idea. I dati già scritti nei backup spariscono man mano che
quei backup ruotano. Sopravvivono alla cancellazione le sole registrazioni che dobbiamo
conservare per ragioni fiscali o di legge.

Una cosa sopravvive alla cancellazione senza più riguardare te. Dove un amministratore ha
agito sul tuo account, il nostro log di sicurezza conserva traccia di ciò che ha fatto
**lui**, con ogni riferimento a te rimosso. Ciò che resta dice che un'azione è avvenuta e
chi l'ha compiuta; non dice più nei confronti di chi. La conserviamo perché altrimenti
chiudere un account cancellerebbe la prova dell'accesso di qualcun altro. Non è una
registrazione che teniamo per ragioni fiscali o di legge — è una registrazione di
sicurezza sulle azioni di un'altra persona.

## 6. I tuoi diritti

Puoi chiederci di darti una copia dei tuoi dati, di correggerli, di cancellarli, di
limitarne od opporti al trattamento, o di fornirli in un formato portabile. Puoi revocare
il consenso dove il trattamento si basa sul consenso.

L'esportazione e la cancellazione sono disponibili nell'applicazione senza chiedercelo.
Per tutto il resto, scrivici e risponderemo entro un mese.

Se ritieni che stiamo trattando i tuoi dati in modo scorretto, puoi proporre reclamo
all'autorità di controllo del tuo Paese. In Italia è il *Garante per la protezione dei
dati personali*.

## 7. Sicurezza

Cifriamo i segreti a riposo, calcoliamo l'hash delle password con Argon2id, teniamo i log
privi di credenziali, verifichiamo la firma dei webhook in ingresso, limitiamo la
frequenza dei tentativi di autenticazione e registriamo gli eventi sensibili in un log di
audit.

Dovremmo dirti anche che cosa non abbiamo: Postqron gira su un unico server, scelto
deliberatamente perché lo scheduler e il database stiano l'uno accanto all'altro. Quella
scelta baratta resilienza per latenza. Facciamo backup e ne abbiamo verificato il
ripristino, ma un guasto di quella macchina interrompe il servizio.

## 8. Decisioni automatizzate

Non prendiamo decisioni con effetti giuridici o similmente significativi nei tuoi
confronti con mezzi automatizzati, e non ti profiliamo.

## 9. Minori

Postqron non è destinato a persone di età inferiore a
16 anni.
Non raccogliamo consapevolmente i loro dati.

## 10. Modifiche

Possiamo aggiornare questa informativa. La versione e la data di entrata in vigore sono in
testa. Quando una modifica è sostanziale te lo diciamo prima che abbia effetto e, dove la
legge lo richiede, ti chiediamo di nuovo il consenso.

---

**Contatto:** privacy@postqron.com
**Gestito da:** Apdsoftware di Carlo Zuffetti, Via C. Colombo 15, 24047 Treviglio (BG), Italy — VAT 03835250162, REA BG 431224
