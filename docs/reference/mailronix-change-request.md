# Richiesta di modifica a Mailronix — categoria del messaggio su `POST /email/send`

> **CHIUSA — risposta ricevuta il 2026-08-18: no su entrambi i punti.**
>
> Il documento resta per intero perché la risposta si capisce solo leggendo cosa
> avevamo chiesto. **Non riaprirlo senza aver letto prima la §0**: il campo che
> chiediamo qui non avrebbe prodotto la protezione per cui lo chiedevamo, e la
> ragione sta un livello sotto l'API di Mailronix.

## 0. L'esito, e perché la domanda era mal posta

**Mailronix consegna via AWS SES, con la suppression list a livello di account
AWS** — `BOUNCE` e `COMPLAINT` fra le ragioni, verificato da loro il 2026-07-22.
Quella lista sta **sotto** la loro API: vale per tutti i clienti e per tutte le
categorie, presenti e future.

Ne discende il punto che ribalta questa richiesta: **anche con il campo
`category` e con una suppression granulare, un nostro `transaction_receipt` verso
un indirizzo che ha reclamato sarebbe stato fermato comunque**, un livello più in
basso. Avremmo avuto un campo, un enum in documentazione e nessuna protezione in
più — precisamente il difetto che la §3.3 chiedeva loro di evitarci.

Escludere il transazionale da quella lista è tecnicamente possibile (un
*configuration set* SES dedicato) e hanno deciso di non farlo. La ragione che
conta non è la loro policy sulla reputazione condivisa: **non funzionerebbe
comunque.** Chi ha premuto «segnala spam» ha addestrato il proprio provider di
posta, e Gmail, Outlook e Apple scarteranno il messaggio successivo qualunque cosa
faccia SES. Avremmo ricevuto un `202` e un evento di consegna verso un utente che
non ha ricevuto niente: R20.1 un livello più in là.

**La parte che ci serviva davvero era però già soddisfatta.** Nella loro
piattaforma revoca e soppressione sono meccanismi distinti, e la revoca del
consenso **non è consultata da `POST /email/send`**: chi si disiscrive dalle
comunicazioni di prodotto continua a ricevere le transazionali, oggi, senza
modifiche. La promessa della nostra privacy policy §2.7 e §2.8 è già mantenuta.

Il confine reale non è «marketing contro transazionale». È **«revoca del
consenso»** — nostra, granulare, già separata — contro **«reclamo o bounce»**,
che è assoluto e in parte fuori dal controllo di chiunque mandi email.

### Cosa ne discende per noi

1. **Il marketing non passa da Mailronix**, ed è anche il loro consiglio
   esplicito. `marketing.Courier` resta senza chiamante non «finché Mailronix non
   cambia», ma perché quella non è la sua strada.
2. **Un reclamo costa l'indirizzo per tutte le categorie**, in silenzio e senza
   che possiamo recuperarlo. E può arrivare da un **altro cliente** della
   piattaforma sullo stesso indirizzo, senza che nulla ce lo segnali.
3. **Gli avvisi davvero critici — la sicurezza dell'account — hanno bisogno di un
   secondo canale**, perché l'email può venire meno per un singolo destinatario
   in modo silenzioso e definitivo. È il loro consiglio ed è la ragione per cui
   R57 (notifiche in prodotto, issue #470) smette di essere un miglioramento
   dell'esperienza e diventa un requisito di sicurezza.
4. La riga «Mailronix — Transactional email delivery» nella tabella dei fornitori
   della privacy policy **resta vera**, e resterà vera finché il marketing non
   passa di lì.

### Cosa resta aperto dalla loro parte

Gli **header personalizzati e RFC 8058** (§4 qui sotto) sono già in lavorazione
come attività di deliverability, separatamente. Ci hanno segnalato che header
arbitrari forniti dal chiamante sono una superficie di injection nota, quindi
passeranno da una valutazione di sicurezza dedicata.

---

**Da:** PostQron (apdsoftware/postqron)
**Data:** 2026-08-18
**Stato:** proposta, nessuna implementazione avviata da questa parte
**Blocca:** l'attivazione delle email di marketing di PostQron (issue #476)

---

## 1. Cosa chiediamo, in una riga

Aggiungere a `POST /email/send` un campo **facoltativo** `category`, con **default
`transaction_receipt`** — cioè il comportamento attuale — che permetta di dichiarare che
un messaggio è una comunicazione di prodotto e non una transazionale.

Nessuna richiesta esistente cambia comportamento. Chi non manda il campo continua a
ottenere esattamente ciò che ottiene oggi.

---

## 2. Perché serve, e perché non possiamo aggirarlo

Oggi il documento dell'API dice, testualmente:

> La categoria è sempre `transaction_receipt`, determinata esclusivamente lato server —
> non esiste alcun modo di selezionarla dalla richiesta.

PostQron manda due famiglie di email che la legge tratta in modo diverso e che i
provider di posta trattano in modo diverso:

| Famiglia | Esempi | Base giuridica | Disiscrizione |
|---|---|---|---|
| **Transazionale** | benvenuto, job fallito, cambio di piano, evento di sicurezza | esecuzione del contratto | **nessuna**, per definizione |
| **Marketing** | novità di prodotto, comunicazioni commerciali | consenso, revocabile | obbligatoria |

Con la categoria imposta lato server, **le due famiglie sono indistinguibili per
Mailronix**. La conseguenza non è formale:

**Un reclamo per spam su una comunicazione di prodotto ricade sulla stessa categoria, sullo
stesso tenant e sullo stesso dominio con cui mandiamo gli avvisi di sicurezza.** Se quel
reclamo porta a una soppressione, l'utente smette di ricevere anche le email che il
servizio *deve* mandargli — la conferma dell'indirizzo, l'avviso che un job è rotto, la
notifica di un accesso sospetto.

E non ce ne accorgeremmo. La stessa API dichiara:

> `202` — Accodata per l'invio. Identica sia che il destinatario venga effettivamente
> recapitato sia che sia silenziosamente scartato per suppression list (ADR-0017).

Quindi: perdiamo il canale di sicurezza, in silenzio, per colpa di una promozione.

### Le alternative che abbiamo scartato, e perché

Non chiediamo questa modifica per comodità. Abbiamo valutato tre aggiramenti e nessuno
regge:

1. **Dominio mittente separato** (es. `news.postqron.com`). Il campo `from` è nostro,
   quindi è fattibile senza toccare Mailronix. Risolve la reputazione **verso l'esterno**
   — Gmail e Outlook valutano il dominio — ma **non** la suppression list interna a
   Mailronix, che resta per tenant. Il rischio descritto sopra rimane quasi intatto.
2. **Chiave API separata.** Separa la contabilità, non la reputazione né la soppressione,
   per lo stesso motivo.
3. **Campagne dalla console.** Il marketing non passerebbe dal nostro backend, ma la
   disiscrizione deve restare nostra (è legata al consenso che registriamo noi): i due
   sistemi dovrebbero restare allineati a mano, ed è il punto in cui divergerebbero.

---

## 3. La modifica, in dettaglio

### 3.1 Schema

Aggiungere la proprietà a **`SendRequestDirect`** e a **`SendRequestTemplate`**:

```yaml
category:
  type: string
  enum: [transaction_receipt, product_update]
  default: transaction_receipt
  description: >
    Natura del messaggio. Determina come i reclami e le soppressioni si
    ripercuotono sul tenant: un reclamo su `product_update` non deve poter
    sopprimere il recapito dei messaggi `transaction_receipt` allo stesso
    destinatario.
    Omettere il campo equivale a `transaction_receipt`, che è il
    comportamento precedente all'introduzione di questo campo.
```

**Il campo non va aggiunto a `required`.** È la condizione che chiedi tu, ed è anche la
nostra: `SendRequestDirect` ha oggi `required: [from, to, subject]` e deve continuare ad
avercelo.

### 3.2 Comportamento

- **Campo assente** → `transaction_receipt`. Identico a oggi, byte per byte nella
  risposta.
- **Campo presente e valido** → la categoria dichiarata.
- **Campo presente e sconosciuto** (es. `"promo"`) → `400`, con lo stesso schema
  `ErrorResponse` già in uso. **Non ricadere sul default**: un valore che il chiamante
  credeva di aver dichiarato e che viene silenziosamente convertito in transazionale è
  esattamente il difetto che questa modifica esiste per evitare.

### 3.3 La parte che conta davvero

**Le soppressioni devono essere per categoria, non per destinatario.** Aggiungere il campo
senza questo non risolve niente: sarebbe un'etichetta che non produce effetti.

Concretamente: un destinatario che si lamenta di un `product_update`, o che risulta
soppresso per quella categoria, **deve continuare a ricevere i `transaction_receipt`**.

Se questa parte è troppo onerosa da fare subito, **diccelo**: preferiamo saperlo e tenere
il canale di marketing spento, piuttosto che avere un campo che ci fa credere di essere
protetti.

### 3.4 Cosa **non** chiediamo

- Nessuna modifica alla risposta `202`, che resta volutamente identica nei due casi
  (ADR-0017).
- Nessun nuovo endpoint, nessun cambio di autenticazione, nessuna modifica alle testate
  del messaggio.
- Nessuna categoria oltre alle due: se un giorno ne serviranno altre, l'`enum` cresce.

---

## 4. Una cosa che ci semplificherebbe la vita, ma è secondaria

Se fosse possibile passare **testate personalizzate** (`List-Unsubscribe` e
`List-Unsubscribe-Post`, RFC 8058), potremmo offrire la disiscrizione con **un clic solo**
dal pulsante nativo di Gmail e Outlook.

Oggi `SendRequestDirect` è `from`/`to`/`subject`/`html_body`/`text_body` e non ha modo di
farlo, quindi la nostra disiscrizione apre una pagina in cui si conferma. **Funziona ed è
conforme** — anzi, la pagina di conferma evita che i controlli automatici dei server di
posta disiscrivano persone che non hanno mai cliccato — quindi non è un blocco.

**È una richiesta separata e a priorità più bassa della §3.** Non mescolarla: se arriva
solo la categoria, per noi va bene.

---

## 5. Come verificheremo che sia arrivata

Dal nostro lato la giuntura è già pronta e **deliberatamente scollegata**:
`marketing.Courier` esiste, ha un `Sender` proprio, e non è agganciato a `cmd/api`. Il
perché è scritto accanto al codice.

Quando la modifica sarà disponibile:

1. aggiorniamo `docs/reference/mailronix-openapi.json` con lo schema nuovo;
2. il corriere di marketing dichiara `category: product_update`;
3. **verifichiamo che la separazione sia reale**, non solo dichiarata: un destinatario
   soppresso per `product_update` deve ricevere lo stesso un `transaction_receipt`. Se
   questa prova non passa, il canale resta spento;
4. aggiorniamo la riga «Mailronix — Transactional email delivery» nella tabella dei
   fornitori della nostra privacy policy, che oggi è vera proprio perché il marketing non
   passa di lì.

---

## 6. Urgenza

**Nessuna.** Le email di marketing non servono al lancio di PostQron, e il costo di
aspettare è zero. Questa richiesta esiste perché il canale sia pronto quando servirà, non
perché serva adesso.

Ciò che **non** faremo nel frattempo è mandare marketing dal canale transazionale.
