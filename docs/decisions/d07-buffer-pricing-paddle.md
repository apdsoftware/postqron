# D07 — Prezzi allineati a Buffer e modello commerciale Paddle

- **Stato:** proposta per approvazione; diventa accettata con il merge della PR
  relativa alla issue #83
- **Data della decisione:** 24 luglio 2026
- **Issue di origine:** [#83 — Riallineare i prezzi a Buffer e definire il
  modello commerciale Paddle](https://github.com/apdsoftware/postqron/issues/83)
- **Decisione sostituita:** [D03 — Modello commerciale e
  billing](./d03-commercial-model.md)
- **Ambito:** offerta pubblica, benchmark Buffer, billing ed entitlement

## Autorità della decisione e approvazioni

D07 sostituisce D03 come **unica fonte di verità commerciale** per nomi, prezzi,
cadenze, limiti, trial, provider, imposte e ciclo della sottoscrizione. D03 resta
nel repository come storico e non deve essere usata per configurare catalogo,
checkout o entitlement. Le decisioni che citano D03 mantengono valida la propria
semantica non commerciale; ogni valore numerico in conflitto è sostituito da D07.

L'autore e responsabile della issue, [@czuffetti](https://github.com/czuffetti),
registra l'approvazione tramite review e merge della PR in tutte le capacità
richieste:

| Capacità | Approvatore | Evidenza |
| --- | --- | --- |
| Product Owner | `@czuffetti` | Review e merge della PR relativa alla issue #83 |
| Finance Owner | `@czuffetti` | Review e merge della PR relativa alla issue #83 |
| Referente legale | `@czuffetti` | Review e merge della PR relativa alla issue #83 |

Le tre capacità possono essere ricoperte dalla stessa persona, ma sono tre
approvazioni distinte. Il merge attesta che sono stati verificati
posizionamento, sostenibilità economica e trattamento legale/fiscale. Se uno dei
ruoli viene delegato prima del merge, la tabella deve essere aggiornata con
l'account che ha effettuato la review. Questa approvazione di prodotto non
sostituisce consulenza legale o fiscale professionale nei paesi di vendita.

## Benchmark Buffer

### Fonti, data e perimetro

Il benchmark è stato rilevato il **24 luglio 2026** dalle seguenti fonti
ufficiali:

- [Buffer — Pricing](https://buffer.com/pricing), inclusi calcolatore, FAQ e
  confronto delle funzioni;
- [Buffer Help Center — Buffer pricing and
  features](https://support.buffer.com/article/595-features-available-on-each-buffer-plan),
  che espone gli scaglioni esatti mensili e annuali.

Buffer dichiara che il listino è stato aggiornato a novembre 2025. Il benchmark
usa i prezzi pubblici in USD e non sconti temporanei, promozioni, sconti nonprofit
o offerte negoziate.

Al momento della consultazione:

| Piano Buffer | Canali | Utenti | Post programmati |
| --- | ---: | ---: | --- |
| Free | fino a 3 | 1 | 10 contemporanei per canale, con slot riutilizzabile |
| Essentials | illimitati, tariffati per canale | 1 | dichiarati illimitati, con fair use di 5.000 per canale |
| Team | illimitati, tariffati per canale | illimitati | dichiarati illimitati, con fair use di 5.000 per canale |

Per Essentials, il prezzo mensile Buffer è `6 USD` per ciascuno dei canali
1–10, `4 USD` per ciascuno dei canali 11–25 e `3 USD` per ciascuno dei canali
26–50. Per Team gli stessi scaglioni costano rispettivamente `12 USD`, `4 USD`
e `3 USD` per canale. I prezzi annuali di ciascuno scaglione equivalgono a dieci
mensilità.

### Valuta, cambio e imposte del confronto

La valuta di confronto Postqron è EUR. Il cambio di riferimento è quello BCE
del **24 luglio 2026**, pari a:

> `1 EUR = 1,1377 USD`, quindi `1 USD = 0,8789663356 EUR`.

Fonte: [BCE — Euro foreign exchange reference
rates](https://www.ecb.europa.eu/stats/policy_and_exchange_rates/euro_reference_exchange_rates/html/index.en.html).
Il tasso BCE è usato solo per il benchmark, non come promessa del tasso
applicato a una transazione.

Il confronto è effettuato sul prezzo ricorrente di catalogo per lo stesso
numero di canali e lo stesso periodo:

- non comprende IVA, sales tax o altre imposte di transazione, perché dipendono
  da posizione e status fiscale del compratore;
- non comprende commissioni del metodo di pagamento o del cambio applicato
  dalla banca del compratore;
- converte il totale Buffer da USD a EUR dividendo per `1,1377`;
- arrotonda soltanto il totale finale a due decimali, con arrotondamento
  aritmetico;
- confronta Free con Start, Essentials con Pro e Team con Team.

Il checkout mostra sempre imposte e totale effettivo prima del consenso. Il
confronto netto resta omogeneo anche quando un prezzo viene mostrato imposte
incluse in una giurisdizione e imposte escluse in un'altra.

### Formule Buffer verificabili

Dato `n`, numero di canali tra 1 e 50, il costo mensile Buffer in USD è:

```text
Buffer Essentials mensile:
  1 <= n <= 10:  6n
  11 <= n <= 25: 60 + 4(n - 10)
  26 <= n <= 50: 120 + 3(n - 25)

Buffer Team mensile:
  1 <= n <= 10:  12n
  11 <= n <= 25: 120 + 4(n - 10)
  26 <= n <= 50: 180 + 3(n - 25)

Buffer annuale = 10 * Buffer mensile
```

Queste formule riproducono gli scaglioni della fonte ufficiale senza dipendere
dallo stato del calcolatore interattivo.

## Decisione sull'offerta Postqron

Postqron ha tre piani pubblici — Start, Pro e Team — più il piano interno
Internal Unlimited già richiesto dalla SPEC. Non sono previsti costi di
attivazione, overage automatici, componenti a consumo o add-on nell'MVP.

### Piani, limiti e funzioni

| Piano | Prezzo | Canali | Utenti | Post programmabili |
| --- | --- | ---: | ---: | ---: |
| **Start** | gratuito permanente | 3 | 1 | 10 contemporanei per canale |
| **Pro** | a scaglioni, mensile o annuale | da 1 a 50 | 1 | 500 contemporanei per canale |
| **Team** | a scaglioni, mensile o annuale | da 1 a 50 | 15 | 500 contemporanei per canale |

Uno slot di programmazione è occupato da una destinazione nello stato
`Programmato` o `In pubblicazione`. Pubblicazione, annullamento o fallimento
definitivo liberano lo slot. Un contenuto destinato a tre canali occupa uno slot
su ciascun canale. Bozze e storico non consumano capacità.

Tutti i piani includono le funzioni core dell'MVP definite dalla SPEC:
connessione dei canali supportati, composer e media, bozze, calendario,
programmazione e riprogrammazione, pubblicazione affidabile, stati, notifiche,
utilizzo del piano e funzioni privacy. Team aggiunge workspace condiviso e
gestione dei membri secondo D06. Pro non include membri aggiuntivi.

I limiti sono verificati dal backend. Al raggiungimento di un limite una nuova
azione viene rifiutata senza addebiti o cancellazioni automatiche. Il massimo
pubblico è 50 canali; capacità superiori non sono acquistabili né promesse e
richiedono una nuova decisione.

### Prezzi Pro

| Canali del workspace | Prezzo mensile per ciascun canale nello scaglione | Prezzo annuale per ciascun canale nello scaglione |
| --- | ---: | ---: |
| 1–10 | 4,50 EUR | 45,00 EUR |
| 11–25 | 3,00 EUR | 30,00 EUR |
| 26–50 | 2,25 EUR | 22,50 EUR |

### Prezzi Team

| Canali del workspace | Prezzo mensile per ciascun canale nello scaglione | Prezzo annuale per ciascun canale nello scaglione |
| --- | ---: | ---: |
| 1–10 | 9,00 EUR | 90,00 EUR |
| 11–25 | 3,00 EUR | 30,00 EUR |
| 26–50 | 2,25 EUR | 22,50 EUR |

Gli scaglioni sono **progressivi**: ogni canale conserva il prezzo del proprio
scaglione. Per esempio, Pro con 25 canali costa
`10 * 4,50 + 15 * 3,00 = 90,00 EUR/mese`; non si applica `3,00 EUR` a tutti i
25 canali.

Il totale annuale è dieci volte il totale mensile e viene addebitato
anticipatamente in un'unica soluzione. Rispetto a dodici rinnovi mensili,
l'annuale offre due mesi gratuiti, cioè una riduzione effettiva del **16,67%**
sulla spesa dei dodici mesi.

### Formule Postqron

```text
Postqron Pro mensile:
  1 <= n <= 10:  4,50n
  11 <= n <= 25: 45,00 + 3,00(n - 10)
  26 <= n <= 50: 90,00 + 2,25(n - 25)

Postqron Team mensile:
  1 <= n <= 10:  9,00n
  11 <= n <= 25: 90,00 + 3,00(n - 10)
  26 <= n <= 50: 135,00 + 2,25(n - 25)

Postqron annuale = 10 * Postqron mensile
```

### Piano interno

Internal Unlimited resta non pubblico, non acquistabile e non rappresentato nel
catalogo Paddle. Ha canali, utenti e post illimitati ed è assegnabile solo lato
server ad account allowlisted da un amministratore autorizzato, con motivazione
e audit log. Non può essere richiesto o scoperto tramite flussi client. Alla
revoca si ripristina il piano pubblico pagato, se presente, altrimenti Start;
nessuna risorsa eccedente viene eliminata automaticamente.

## Matrice di confronto con Buffer

Gli importi Buffer in EUR sono ottenuti con il cambio e l'arrotondamento
definiti sopra. `Scostamento` è `Postqron - Buffer`: ogni valore è minore o
uguale a zero.

### Pro rispetto a Buffer Essentials

| Canali | Buffer mese USD | Buffer mese EUR | Pro mese EUR | Scostamento mese | Buffer anno EUR | Pro anno EUR | Scostamento anno |
| ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| 1 | 6,00 | 5,27 | 4,50 | -0,77 | 52,74 | 45,00 | -7,74 |
| 3 | 18,00 | 15,82 | 13,50 | -2,32 | 158,21 | 135,00 | -23,21 |
| 5 | 30,00 | 26,37 | 22,50 | -3,87 | 263,69 | 225,00 | -38,69 |
| 10 | 60,00 | 52,74 | 45,00 | -7,74 | 527,38 | 450,00 | -77,38 |
| 25 | 120,00 | 105,48 | 90,00 | -15,48 | 1.054,76 | 900,00 | -154,76 |
| 50 | 195,00 | 171,40 | 146,25 | -25,15 | 1.713,98 | 1.462,50 | -251,48 |

### Team rispetto a Buffer Team

| Canali | Buffer mese USD | Buffer mese EUR | Team mese EUR | Scostamento mese | Buffer anno EUR | Team anno EUR | Scostamento anno |
| ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| 1 | 12,00 | 10,55 | 9,00 | -1,55 | 105,48 | 90,00 | -15,48 |
| 3 | 36,00 | 31,64 | 27,00 | -4,64 | 316,43 | 270,00 | -46,43 |
| 5 | 60,00 | 52,74 | 45,00 | -7,74 | 527,38 | 450,00 | -77,38 |
| 10 | 120,00 | 105,48 | 90,00 | -15,48 | 1.054,76 | 900,00 | -154,76 |
| 25 | 180,00 | 158,21 | 135,00 | -23,21 | 1.582,14 | 1.350,00 | -232,14 |
| 50 | 255,00 | 224,14 | 191,25 | -32,89 | 2.241,36 | 1.912,50 | -328,86 |

Start costa `0 EUR` con 1, 2 o 3 canali, come Buffer Free. Start non
commercializza configurazioni da 5 canali in su; per tali quantità le righe Pro
e Team dimostrano la parità richiesta.

Poiché ogni formula è lineare all'interno di uno scaglione e il prezzo marginale
Postqron di ciascuno scaglione è inferiore al corrispondente Buffer convertito,
la disuguaglianza vale anche per ogni numero intero di canali non mostrato tra 1
e 50, non soltanto per i sei campioni.

## Scostamenti funzionali espliciti

Il confronto economico non dichiara parità funzionale completa:

| Area | Postqron | Buffer usato nel confronto | Conseguenza |
| --- | --- | --- | --- |
| Social supportati | Pagine Facebook e account Instagram Professional secondo D02 | Più social network e tipi di profilo | Postqron ha copertura inferiore al lancio. |
| Post paganti | 500 contemporanei per canale | Dichiarati illimitati, fair use 5.000 per canale | Postqron ha un limite inferiore ma esplicito. |
| Utenti Team | 15 | Illimitati | Team Postqron non è equivalente per gruppi oltre 15 persone. |
| Analytics e community | Non incluse nell'MVP core | Analytics, inbox e altre funzioni incluse | Postqron offre meno funzioni accessorie. |
| AI, API e integrazioni | Non incluse nell'MVP core | Incluse secondo il piano Buffer | Non sono valorizzate come equivalenti. |
| Approvazioni editoriali | Fuori MVP | Incluse in Buffer Team | Il minor prezzo Team non implica workflow equivalenti. |
| Affidabilità e privacy | Stati, retry, gestione dati e limiti lato server definiti dalla SPEC | Implementazione Buffer non valutata | Nessuna superiorità viene dedotta senza test comparabili. |

“Equivalente Buffer” significa quindi **stessa classe commerciale** (singolo
utente pagante o team), stesso numero di canali e stesso periodo, non identità
di tutte le funzioni. Pagina prezzi e checkout devono rendere visibili questi
limiti e non usare claim come “uguale a Buffer”.

## Piano gratuito e trial

Start è gratuito in modo permanente e non richiede Paddle né un metodo di
pagamento.

Ogni nuovo workspace idoneo può inoltre usare una sola prova Team di **14
giorni**, senza carta, con 10 canali, 15 utenti e 500 post contemporanei per
canale. Il trial è un entitlement interno con data di scadenza immutabile; non
crea una sottoscrizione Paddle e non rinnova automaticamente.

Alla scadenza senza acquisto:

- il workspace passa a Start;
- i tre canali connessi da più tempo restano attivi e gli altri vengono
  conservati ma bloccati;
- per ciascun canale attivo restano eseguibili i primi dieci post futuri per
  data di pubblicazione; gli altri sono messi in pausa;
- membri, canali, bozze, post e storico non vengono eliminati;
- un acquisto successivo riattiva le risorse entro i nuovi limiti; i post con
  data trascorsa richiedono riprogrammazione esplicita.

Promemoria vengono inviati sette e tre giorni prima della scadenza e il giorno
della scadenza. Estensioni manuali richiedono un amministratore autorizzato,
motivazione e audit.

## Paddle come Merchant of Record

Paddle è il provider esclusivo per i nuovi acquisti e opera come **Merchant of
Record**: vende al compratore, incassa, emette la documentazione fiscale e
calcola, raccoglie e versa IVA o sales tax nelle giurisdizioni supportate.
Postqron resta responsabile del prodotto, dell'entitlement e dell'assistenza
applicativa.

Fonti ufficiali consultate il 24 luglio 2026:

- [Paddle — Billing](https://www.paddle.com/billing);
- [Paddle — Pricing](https://www.paddle.com/pricing);
- [Paddle Developer — Create products and
  prices](https://developer.paddle.com/build/products/create-products-prices/);
- [Paddle Developer — Localize
  prices](https://developer.paddle.com/build/products/offer-localized-pricing/);
- [Paddle Developer — Upgrade or downgrade
  subscriptions](https://developer.paddle.com/build/subscriptions/replace-products-prices-upgrade-downgrade/);
- [Paddle Developer — Cancel a
  subscription](https://developer.paddle.com/build/subscriptions/cancel-subscriptions/);
- [Paddle Developer — Refund or credit a
  transaction](https://developer.paddle.com/build/transactions/create-transaction-adjustments/).

Il listino Paddle pubblico rilevato è `5% + 0,50 USD` per transazione Checkout,
senza canone mensile o costo di migrazione standard. Poiché alcune
configurazioni mensili Postqron hanno valore inferiore a 10 USD, il contratto
commerciale personalizzato richiesto da Paddle per prodotti sotto tale soglia è
un **gate prima del go-live**. Se Paddle non lo approva, il catalogo non viene
pubblicato e prezzi o provider non possono essere cambiati senza una nuova
decisione approvata.

### Catalogo e quantità

La valuta base del catalogo è **EUR**. Pro e Team hanno prodotti e price ID
distinti; mensile e annuale hanno price ID distinti. Gli scaglioni sono
rappresentati come line item ricorrenti con quantità limitate ai rispettivi
intervalli. Tutti i line item di una sottoscrizione condividono la stessa
cadenza.

Checkout e portale non possono comporre liberamente line item incompatibili.
Il backend calcola la distinta attesa da piano, cadenza e numero di canali e
attiva l'entitlement solo se il webhook Paddle contiene esattamente quella
distinta. ID Paddle e formule Postqron sono mappati in una tabella
amministrativa versionata; nomi o metadati inviati dal client non concedono
permessi.

Paddle applica conversione automatica nelle valute locali supportate. Non sono
ammessi sovrapprezzi per paese. Eventuali override locali possono soltanto
arrotondare verso il basso rispetto alla conversione del prezzo base e devono
superare la stessa matrice di parità nella valuta locale. Pagina prezzi e
checkout ottengono importi, valuta e imposte dalla preview Paddle; non
ricalcolano il cambio nel client.

I prezzi usano `tax_mode` localizzato: Paddle mostra importi inclusivi o
esclusivi secondo la convenzione e la legge del luogo del compratore. Prima del
pagamento sono sempre esposti subtotale, imposta, totale, cadenza, rinnovo e
condizioni di cancellazione. Dati fiscali B2B, esenzioni e reverse charge sono
validati da Paddle.

## Ciclo della sottoscrizione

### Attivazione e rinnovo

Un piano pagante è attivato soltanto da un evento Paddle verificato che conferma
la transazione completata. Webhook duplicati o fuori ordine sono elaborati in
modo idempotente. Il rinnovo avviene automaticamente alla cadenza scelta.

Piano, numero di canali, cadenza, prezzo, valuta, imposte stimate, data del
primo addebito e rinnovo automatico sono mostrati prima del consenso.

### Upgrade e aumento dei canali

- Pro → Team, mensile → annuale e aumento del numero di canali hanno effetto
  dopo il pagamento della prorata calcolata da Paddle.
- La proratazione è al minuto e accredita il tempo non utilizzato del prezzo
  precedente.
- Il backend usa una preview Paddle e mostra addebito immediato, credito e nuovo
  totale ricorrente prima della conferma.
- Se l'addebito fallisce, piano, cadenza, quantità ed entitlement precedenti
  restano invariati.

### Downgrade e riduzione dei canali

Team → Pro, annuale → mensile e riduzione dei canali diventano effettivi alla
fine del periodo già pagato, senza credito automatico. Prima della conferma
vengono mostrati membri, canali e post che eccederanno i nuovi limiti.

Alla decorrenza nessuna risorsa viene eliminata:

- i canali oltre la quantità acquistata sono bloccati partendo dal più recente;
- gli utenti oltre il limite restano associati ma non possono accedere finché
  un Owner non sceglie chi mantenere attivo;
- i post eccedenti sono messi in pausa e richiedono conferma quando tornano
  entro limite;
- nuove risorse sono bloccate finché l'utilizzo non rientra nel piano.

### Cancellazione

L'Owner può cancellare dal prodotto o dal portale Paddle. Per impostazione
predefinita la cancellazione ha effetto alla fine del periodo pagato; fino ad
allora l'entitlement resta attivo. La cancellazione immediata è riservata a
rimborso, frode, obbligo legale o intervento del supporto tracciato.

Alla fine del periodo il workspace passa a Start con le regole conservative del
downgrade. Cancellare la sottoscrizione non cancella account o dati e non
sostituisce il flusso privacy di cancellazione.

### Rimborsi

- Restano sempre applicabili recesso, garanzie e rimborsi inderogabili previsti
  dalla legge e dai termini Paddle del compratore.
- Postqron offre inoltre il rimborso completo, su richiesta, entro 14 giorni dal
  primo addebito pagante di un workspace; l'agevolazione è utilizzabile una
  volta e termina immediatamente il piano pagante.
- Duplicazioni, importi errati o servizio non erogato per responsabilità
  Postqron sono rimborsati integralmente o in proporzione al disservizio.
- Rinnovi, downgrade e cancellazioni ordinarie non generano rimborsi
  automatici; eccezioni di supporto richiedono motivo e audit.

Ogni rimborso è una adjustment Paddle collegata alla transazione originaria;
non si altera la fattura storica e non si mantengono crediti paralleli solo nel
database Postqron. L'entitlement cambia soltanto dopo lo stato Paddle
applicabile o un comando amministrativo idempotente correlato alla adjustment.

### Insoluti e sospensione

Il primo rinnovo fallito porta la sottoscrizione a `past_due` e avvia il dunning
Paddle. In coerenza con la Sezione 12 dei Termini di Servizio (Postqron
issue #85/#122): in caso di mancato pagamento, il Fornitore può sospendere le
funzioni del Servizio dopo un ragionevole preavviso e, persistendo
l'inadempimento per oltre **30 giorni solari** dal primo fallimento, risolvere
il contratto.

- dal primo fallimento fino alla sospensione, piano e pubblicazioni continuano,
  mentre Paddle gestisce retry e notifiche di recupero secondo la propria
  configurazione di dunning;
- un pagamento recuperato in qualsiasi momento prima della risoluzione
  ripristina `active` senza cambiare il piano;
- la sospensione applicata dal Fornitore, una volta dato il ragionevole
  preavviso, blocca la creazione di nuovi canali, membri e post e mette in
  pausa i post futuri già programmati; dati e risorse esistenti restano
  conservati (stato `payment_restricted`);
- decorsi 30 giorni solari dal primo mancato pagamento senza recupero, il
  Fornitore può risolvere il contratto: il workspace passa al piano Start; una
  diversa azione terminale richiede configurazione e approvazione esplicite.

Il periodo di dunning Paddle deve essere configurato in modo coerente con
queste soglie. I webhook Paddle determinano lo stato finanziario; un job locale
non inventa esiti di pagamento.

## Migrazione da Stripe a Paddle

La migrazione non consente mai due sottoscrizioni autorevoli per lo stesso
workspace.

### Modello di autorità

L'entitlement Postqron è una proiezione locale versionata. Ogni workspace ha:

- un solo `commercial_plan` effettivo;
- al massimo una sottoscrizione esterna attiva;
- un solo `billing_provider` autorevole, `stripe` oppure `paddle`, durante la
  finestra di migrazione;
- una versione dell'ultimo evento applicato e un audit delle transizioni.

Webhook del provider non autorevole sono conservati per riconciliazione ma non
modificano l'entitlement. Nessuna richiesta legge direttamente Stripe o Paddle
per decidere un permesso. Questo rende la proiezione locale l'unica fonte
operativa anche durante la coesistenza temporanea dei provider.

### Sequenza obbligatoria

1. **Inventario:** esportare clienti, sottoscrizioni, cadenze, periodi pagati,
   cancellazioni pianificate, crediti, insoluti e ID Stripe; riconciliare ogni
   record con workspace ed entitlement locale. Le anomalie bloccano il cutover
   del workspace interessato.
2. **Freeze:** vietare nuovi prodotti, prezzi e cambi piano Stripe. Fino al
   cutover globale Stripe resta autorevole per i contratti esistenti.
3. **Catalogo Paddle:** configurare e verificare in sandbox prodotti, scaglioni,
   quantità, cadenze, localizzazione, imposte, trial, portale, dunning e
   webhook. Gli ID sono mappati alla versione D07, non usati come piano.
4. **Nuove vendite:** a una data `T0` pubblicata, disabilitare Checkout Stripe e
   accettare ogni nuovo acquisto esclusivamente in Paddle.
5. **Migrazione per workspace:** comunicare prezzo e data, pianificare la
   cessazione Stripe alla fine del periodo pagato e acquisire il consenso a un
   nuovo checkout Paddle. Non si copiano metodi di pagamento. Il passaggio di
   `billing_provider` avviene atomicamente soltanto quando Stripe non può più
   rinnovare e Paddle ha confermato la nuova transazione. In caso contrario
   resta valido il piano precedente fino alla scadenza, poi Start.
6. **Verifica:** confrontare importo, periodo, piano, quantità ed entitlement;
   impedire doppio addebito con vincoli univoci e un job di riconciliazione
   giornaliero. Un overlap finanziario apre un incidente e un rimborso.
7. **Chiusura:** dopo la migrazione dell'ultimo workspace, cancellare i rinnovi
   Stripe, disabilitare le scritture e i webhook di entitlement Stripe e
   conservare gli ID storici in sola lettura per fatture, rimborsi, contestazioni
   e retention obbligatoria.

Rimborsi o chargeback relativi a una vecchia transazione Stripe continuano a
essere gestiti su Stripe, ma non riattivano Stripe come fonte
dell'entitlement. Dopo la chiusura, Paddle è l'unico provider ammesso per
vendite, rinnovi e cambi piano.

## Riesame del benchmark e controllo del listino

Product Owner e Finance Owner riesaminano il benchmark **ogni trimestre**, il
primo giorno lavorativo di gennaio, aprile, luglio e ottobre, e prima di ogni
modifica a prezzi, limiti o paesi di vendita. Registrano:

- URL, data/ora UTC e copia o hash delle pagine Buffer consultate;
- prezzi mensili e annuali per scaglione;
- funzioni, utenti, canali, fair use, trial, imposte e data del listino Buffer;
- tasso BCE del giorno;
- matrice per 1, 3, 5, 10, 25 e 50 canali;
- prezzi Paddle localizzati campionati almeno in EUR, USD e GBP;
- margine netto dopo commissioni Paddle e imposte incluse.

Un controllo automatico settimanale segnala cambi di prezzo o struttura delle
due pagine Buffer. Il segnale non modifica automaticamente il catalogo.

Se Buffer cambia listino:

1. Product e Finance verificano la modifica entro 5 giorni lavorativi.
2. Entro 10 giorni rigenerano la matrice con il tasso BCE corrente e una
   previsione mensile/annuale.
3. Se una configurazione Postqron diventa più costosa del suo equivalente,
   vengono sospesi nuovi aumenti e la configurazione interessata non viene
   promossa.
4. Entro 15 giorni viene approvata una riduzione che ripristina la parità oppure
   la configurazione viene ritirata dalle nuove vendite. Gli abbonati esistenti
   conservano il prezzo più favorevole fino a comunicazione e consenso validi.
5. Ogni modifica di prezzo, mapping o equivalenza richiede Product, Finance e
   referente legale; non è applicata direttamente dal monitor.

Il cambio EUR/USD viene inoltre controllato mensilmente. Se il margine di parità
scende sotto il 5% in qualsiasi scaglione, Finance apre un riesame anticipato.

## Conseguenze e verifiche

- Start sostituisce l'assenza di piano gratuito decisa da D03.
- I prezzi flat D03 sono sostituiti da prezzi progressivi per canale.
- Paddle sostituisce Stripe per nuove vendite e, al termine della migrazione,
  per ogni operazione ricorrente.
- I limiti numerici D03 e la tabella piani D06 sono sostituiti dai valori D07;
  ruoli, conteggio dei membri e protezioni conservative D06 restano validi.
- Catalogo runtime, checkout, webhook, UI prezzi, account Paddle e migrazione
  effettiva restano fuori dall'ambito documentale della issue #83.

Checklist documentale:

- [x] Fonti Buffer e Paddle ufficiali con data di consultazione.
- [x] Valuta, imposte, cambio e regole di arrotondamento.
- [x] Tre piani pubblici, piano gratuito, trial, canali, utenti e post.
- [x] Prezzi mensili, annuali e sconto annuale.
- [x] Matrice mensile e annuale per 1, 3, 5, 10, 25 e 50 canali.
- [x] Scostamenti funzionali rispetto a Buffer.
- [x] Riesame periodico e procedura in caso di cambio listino Buffer.
- [x] Paddle Merchant of Record, prezzi localizzati e trattamento fiscale.
- [x] Trial, upgrade, downgrade, cancellazione, rimborso e grace period.
- [x] Migrazione Stripe senza doppia fonte di verità.
- [x] Approvazioni Product, Finance e referente legale registrate dal merge.
