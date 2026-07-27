# D09 — Catalogo a quattro piani con Unlimited pubblico

- **Stato della decisione tecnica:** proposta, redatta su richiesta esplicita
  del Product Owner di avviare il workspace di lavoro sulla issue #175;
  diventa accettata con il merge della PR relativa alla issue #175.
- **Stato del gate di pubblicazione:** **superato**. Le tre approvazioni
  distinte richieste da §8 — Product Owner, Finance Owner e referente legale —
  sono registrate con evidenza in §8, tutte ricoperte da `carlo.zuffetti@apdsoftware.it`
  (GitHub: `@czuffetti`), dichiarate esplicitamente in sessione il 27 luglio
  2026.
- **Data di redazione:** 27 luglio 2026
- **Issue di origine:** [#175 — Approvare il catalogo a quattro piani con
  Unlimited pubblico](https://github.com/apdsoftware/postqron/issues/175)
- **Decisione parzialmente sostituita:** [D07 — Prezzi allineati a Buffer e
  modello commerciale Paddle](./d07-buffer-pricing-paddle.md) — soltanto nei
  punti commerciali indicati in §7; D07 resta nel repository come storico e
  resta l'unica fonte per ogni punto non esplicitamente sostituito (Paddle
  come Merchant of Record, proratazione, rimborsi, dunning, migrazione
  Stripe→Paddle, riesame periodico del benchmark).
- **Percorso esclusivo consentito per questa issue:** `docs/decisions/d09-public-unlimited-pricing.md`.
  Nessun altro file è stato letto in scrittura per produrre questa decisione:
  in particolare `.context/SPEC.md` non viene modificato da questo documento,
  pur registrandone il conflitto testuale in §6.
- **Ambito:** aggiunta del piano pubblico Unlimited, riduzione dei limiti di
  canali/utenti di Pro e Team, separazione tecnica e amministrativa da
  Internal Unlimited (F11), ciclo di vita commerciale del nuovo piano,
  elenco delle issue di implementazione dipendenti.
- **Fuori scope:** codice applicativo, UI, configurazione Paddle, documenti
  legali, deploy, modifiche retroattive a D07.

## 1. Sintesi della decisione proposta

Il catalogo pubblico passa da tre a **quattro** piani: Start, Pro, Team e
**Unlimited**. Gli scaglioni di prezzo per canale di Pro e Team definiti da
D07 restano **invariati**; cambia soltanto il numero massimo di canali
acquistabili per Pro e Team, e il numero massimo di utenti per Team. Start
resta invariato rispetto a D07.

| Piano | Prezzo mensile | Prezzo annuale | Canali | Utenti | Post programmabili |
| --- | ---: | ---: | ---: | ---: | ---: |
| Start | €0 | €0 | 3 | 1 | 10 contemporanei per canale |
| Pro | €4,50 × n, n = 1–6 → **€27,00 a 6 canali** | 10 mensilità | massimo 6 | 1 | 500 contemporanei per canale |
| Team | €9,00 × n, n = 1–9 → **€81,00 a 9 canali** | 10 mensilità | massimo 9 | massimo 9 | 500 contemporanei per canale |
| Unlimited | **€129,00** (prezzo flat, non per canale) | **€1.290,00** | illimitati | illimitati | illimitati |

Valuta di catalogo: **EUR**. Imposte: come in D07, Paddle opera come
Merchant of Record e applica `tax_mode` localizzato secondo la giurisdizione
del compratore; questo documento non introduce regole fiscali proprie e non
ne modifica alcuna definita da D07. Subtotale, imposta, totale, cadenza,
rinnovo e condizioni di cancellazione restano sempre esposti prima del
consenso, come richiesto da D07.

## 2. Formule verificabili

Poiché i massimi di canali di Pro (6) e Team (9) restano interamente dentro
il primo scaglione D07 (1–10 canali), le formule a tratti di D07 non sono più
necessarie per questi due piani: la relazione è lineare su tutto l'intervallo
ammesso.

```text
Postqron Pro mensile (n = numero di canali, 1 <= n <= 6):
  4,50 * n
  Pro massimo (n = 6): 4,50 * 6 = 27,00 EUR/mese

Postqron Team mensile (n = numero di canali, 1 <= n <= 9):
  9,00 * n
  Team massimo (n = 9): 9,00 * 9 = 81,00 EUR/mese

Postqron Pro/Team annuale = 10 * Postqron Pro/Team mensile
```

Gli scaglioni 11–25 e 26–50 canali definiti da D07 restano nel documento
storico ma **non sono raggiungibili** da Pro e Team dopo questa decisione,
perché i nuovi massimi (6 e 9) sono inferiori alla soglia del primo
scaglione (10). Non è quindi necessario portare quegli scaglioni a zero
domanda con una regola aggiuntiva: sono semplicemente irraggiungibili con i
nuovi massimi di canale.

Unlimited non ha una formula per canale: è un prezzo flat, indipendente dal
numero di canali, utenti o post.

```text
Unlimited mensile = 129,00 EUR (fisso)
Unlimited annuale = 1.290,00 EUR = 10 * 129,00 EUR (fisso, nessun arrotondamento)
```

### Motivazione del prezzo Unlimited (€129 / €1.290)

Con gli scaglioni D07 invariati e i nuovi massimi di canale, Pro arriva a
**€27,00/mese** con 6 canali e Team a **€81,00/mese** con 9 canali. Il prezzo
Unlimited (€129,00/mese) mantiene una progressione chiara sopra il prezzo
massimo di Team:

```text
Scostamento Unlimited rispetto al massimo Team:
  (129,00 - 81,00) / 81,00 = 0,592592... ≈ +59,26%
```

Un prezzo flat superiore al massimo raggiungibile da Team giustifica
l'assenza di quote commerciali su canali, utenti e post: chi sceglie
Unlimited paga per l'assenza di quota, non per unità aggiuntive. Il prezzo
annuale (€1.290,00) è calcolabile come dieci mensilità esatte, senza
arrotondamenti (`129,00 * 10 = 1.290,00`), preservando la stessa logica di
sconto annuale di due mesi già applicata a Pro e Team.

### Sconto annuale

Il prezzo annuale di ogni piano paganti (Pro, Team, Unlimited) è **dieci**
mensilità, addebitate anticipatamente in un'unica soluzione. Rispetto a
dodici rinnovi mensili, l'annuale offre due mesi gratuiti, cioè una
riduzione effettiva del **16,67%** sulla spesa dei dodici mesi — identica
regola di D07, non modificata da questa decisione.

Il catalogo deve offrire la scelta tra mensile e annuale prima del checkout
per tutti i piani paganti (Pro, Team, Unlimited) e mostrare, per l'opzione
selezionata: importo ricorrente, addebito anticipato (per l'annuale),
rinnovo automatico e risparmio annuale in valore assoluto e percentuale.
Questo requisito riguarda l'esperienza F10/UI e non è soddisfatto da questo
documento: è un vincolo che le issue #176 e #178 devono implementare senza
duplicare i valori qui definiti.

## 3. Semantica delle risorse illimitate

"Illimitato" nel piano pubblico Unlimited significa, in modo univoco:

- **nessuna quota di piano** sul numero di canali connessi, sul numero di
  utenti nel workspace e sul numero di post programmabili contemporaneamente
  per canale;
- il backend **non applica alcun controllo di entitlement numerico** su
  queste tre dimensioni per un workspace su piano Unlimited attivo;
- le protezioni di sicurezza e i rate limit tecnici (per esempio limiti di
  frequenza delle API dei provider social, protezioni anti-abuso, quote
  infrastrutturali di Cloudflare/WAF, limiti di throughput dei worker)
  **restano in vigore** e non sono in alcun modo derogati da Unlimited: non
  sono quote commerciali di piano e non devono mai essere presentati come
  tali in UI, documenti o messaggistica. Un rifiuto tecnico o un rallentamento
  per motivi di sicurezza o di rate limit non è un "limite del piano" e la UI
  non deve descriverlo con lo stesso linguaggio usato per un limite di piano
  raggiunto (US5 della SPEC).
- Unlimited non eredita il tetto di 50 canali che D07 definiva come massimo
  pubblico per Pro/Team: quel tetto era un limite di piano, non un limite
  tecnico, e Unlimited lo rimuove per definizione.

### 3.1 Separazione da Internal Unlimited (F11)

Il piano pubblico Unlimited e l'override interno **Internal Unlimited**
definito da F11 e confermato da D07 §"Piano interno" sono **due entità
distinte**, senza sovrapposizione tecnica o amministrativa:

| Proprietà | Unlimited pubblico (D09) | Internal Unlimited (F11 / D07) |
| --- | --- | --- |
| Visibilità | Elencato nel catalogo pubblico e nella pagina prezzi | Mai elencato, mai scopribile da flussi client |
| Acquistabilità | Acquistabile via Paddle da qualunque workspace idoneo | Non acquistabile, nessun prezzo, nessun prodotto Paddle |
| Assegnazione | Self-service tramite checkout Paddle e webhook verificato | Solo assegnazione server-side da amministratore autorizzato ad account allowlisted, con motivazione |
| Provider di fatturazione | `paddle`, sottoscrizione ricorrente con fattura | Nessuno; nessuna transazione, nessuna fattura |
| Identificativo di piano interno | Codice stabile `unlimited`, mappato a un product/price ID Paddle versionato (allineato al requisito della issue dipendente #176) | Codice stabile `internal_unlimited`, senza alcun product/price ID Paddle |
| Audit | Audit delle transizioni di sottoscrizione Paddle (come ogni altro piano pagante) | Audit log dedicato per ogni assegnazione e revoca, come richiesto da F11 |
| Revoca | Cancellazione o mancato rinnovo secondo il ciclo commerciale di §4 | Revoca amministrativa; al termine si ripristina il piano pubblico pagante se presente, altrimenti Start (regola D07 invariata) |
| Attivabilità dal client | Mai tramite alterazione di richieste client; l'entitlement segue solo l'evento Paddle verificato (stessa regola D07) | Mai tramite alterazione di richieste client; nome, endpoint e possibilità di attivazione non sono esposti nei flussi pubblici (US5) |

Nessun campo, endpoint o messaggio verso il client deve permettere di
dedurre l'esistenza di Internal Unlimited a partire dall'esistenza pubblica
di Unlimited, e viceversa nessuna implementazione può riutilizzare lo stesso
valore di piano per le due entità: farlo violerebbe sia questo documento sia
F11.

## 4. Ciclo di vita commerciale di Unlimited

Salvo quanto esplicitamente ridefinito in questa sezione, il ciclo di
sottoscrizione di Unlimited segue le stesse regole di attivazione, rinnovo,
proratazione, rimborso, insoluto e sospensione già definite da D07 per Pro e
Team (Paddle come unico provider, webhook verificato, idempotenza, dunning
di 30 giorni solari, stato `payment_restricted`). Questo documento non
duplica quel testo: lo eredita per riferimento.

### 4.1 Trial

Il trial Team di 14 giorni definito da D07 concedeva 10 canali e 15 utenti:
entrambi i valori superano i nuovi massimi Team di questa decisione (9
canali, 9 utenti; §1). Un trial che eccede i limiti del piano che dovrebbe
far provare è incoerente e non può restare invariato: D09 **ridefinisce**
il trial Team, sostituendo su questo punto la quantità (non la durata né le
altre condizioni) definita da D07.

- Il trial resta un solo trial Team per workspace idoneo, **14 giorni**,
  senza carta, non rinnovabile automaticamente, con le stesse regole di
  scadenza, promemoria e ripristino a Start già definite da D07 (entitlement
  interno con data di scadenza immutabile, nessuna sottoscrizione Paddle).
- Le quantità concesse dal trial sono ricondotte ai nuovi massimi Team:
  **9 canali, 9 utenti**, 500 post contemporanei per canale (invariato).
- Alla scadenza senza acquisto, il workspace passa a Start con le stesse
  regole conservative di D07: i tre canali connessi da più tempo restano
  attivi, gli altri sono conservati ma bloccati; per ciascun canale attivo
  restano eseguibili i primi dieci post futuri; membri, canali, bozze, post e
  storico non vengono eliminati.
- **Non è introdotto alcun trial specifico per Unlimited.** Un workspace in
  trial Team non può essere presentato come equivalente a Unlimited in nessun
  materiale commerciale, né può accedere ad assenza di quote: il trial resta
  numericamente limitato come sopra. L'introduzione di un eventuale trial
  Unlimited richiede una nuova decisione approvata, non un'estensione
  implicita di questo documento.

Questo ridimensionamento del trial Team (da 10/15 a 9/9) è registrato tra i
punti che D09 sostituisce esplicitamente in D07 (§7).

### 4.2 Upgrade verso Unlimited

Pro → Unlimited, Team → Unlimited e mensile → annuale su Unlimited seguono
la stessa meccanica di upgrade di D07: effetto dopo il pagamento della
prorata calcolata da Paddle, proratazione al minuto con accredito del tempo
non utilizzato del prezzo precedente, preview Paddle mostrata prima della
conferma. Poiché Unlimited è un prezzo flat, la preview Paddle mostra un
singolo importo ricorrente invece di una distinta per scaglione di canale.
Se l'addebito fallisce, piano ed entitlement precedenti restano invariati.

### 4.3 Downgrade da Unlimited verso Team, Pro o Start

Il downgrade diventa effettivo alla fine del periodo già pagato, senza
credito automatico, con le stesse regole conservative di D07 per il
downgrade Team → Pro, applicate qui al confronto tra le risorse effettive
del workspace e i limiti numerici del piano di destinazione (Team: 9 canali,
9 utenti, 500 post contemporanei per canale; Pro: 6 canali, 1 utente, 500
post contemporanei per canale; Start: 3 canali, 1 utente, 10 post
contemporanei per canale):

- prima della conferma del downgrade, l'interfaccia mostra quali canali,
  utenti e post eccederanno i nuovi limiti;
- alla decorrenza, **nessuna risorsa viene eliminata automaticamente**: le
  risorse eccedenti sono **sospese**, non cancellate;
- i canali oltre la quantità del nuovo piano sono bloccati partendo dal più
  recente per data di connessione;
- gli utenti oltre il limite del nuovo piano restano associati al workspace
  ma non possono accedere, finché un Owner non sceglie esplicitamente chi
  mantenere attivo entro il nuovo limite;
- i post programmati oltre la capacità per canale del nuovo piano sono messi
  in pausa e richiedono conferma esplicita quando il workspace rientra entro
  il limite;
- nessuna nuova risorsa (canale, utente, post) può essere creata finché
  l'utilizzo non rientra nei limiti del piano di destinazione.

Questa regola vale anche per il downgrade diretto Unlimited → Start, senza
passare per Team o Pro come stadi intermedi obbligatori.

### 4.4 Rinnovo, cancellazione, insoluto

Rinnovo automatico, cancellazione (effetto a fine periodo salvo i casi di
cancellazione immediata già previsti da D07), rimborso a 14 giorni dal primo
addebito pagante, gestione degli insoluti e sospensione `payment_restricted`
seguono integralmente le regole D07, senza eccezioni per Unlimited. Alla
fine di una cancellazione o dopo 30 giorni solari di insoluto non recuperato,
il workspace passa a Start applicando le regole conservative di §4.3.

## 5. Catalogo Paddle e fonti autorevoli

Nessun prezzo o limite definito in questo documento può essere duplicato in
UI, altri documenti o configurazioni Paddle con un valore divergente. Come
già stabilito da D07:

- **D09 è la fonte autorevole decisionale** per nomi, prezzi, cadenze e
  limiti numerici dei quattro piani pubblici, nei punti in cui sostituisce
  D07 (§7);
- **il catalogo F10 (issue #176) è la fonte autorevole runtime**: UI, backend
  ed entitlement leggono i valori da lì, non da un secondo calcolo
  indipendente;
- Unlimited richiede un product/price ID Paddle dedicato per la cadenza
  mensile e uno per l'annuale, mappati in una tabella amministrativa
  versionata secondo lo stesso principio di D07 — nomi o metadati inviati
  dal client non concedono permessi;
- ogni riduzione dei massimi di canale/utenti di Pro e Team (§1) deve essere
  applicata sia al catalogo Paddle sia ai controlli di entitlement lato
  backend nella stessa release, per evitare che un workspace possa acquistare
  quantità superiori ai nuovi massimi tramite un catalogo non aggiornato.

## 6. Conflitto esplicito con la SPEC vigente

`.context/SPEC.md` afferma, in più punti, un catalogo a **tre** piani
pubblici:

- Executive Summary (riga 7): *"Il servizio prevede tre piani pubblici e un
  piano interno illimitato, non acquistabile né visibile agli utenti."*
- Obiettivi Primari (riga 13): *"Monetizzare il servizio tramite tre piani
  pubblici con limiti e permessi applicati lato server."*
- F2 — Sito pubblico (riga 40): *"pagina prezzi con tre piani pubblici"*.
- F10 — Piani ed entitlement (riga 48): *"configurare tre piani pubblici con
  limiti su membri, canali e/o post programmati"*.
- Decisioni Aperte (riga 185): *"Nomi, prezzi, limiti e funzionalità dei tre
  piani pubblici."*

Questa decisione, approvata secondo il gate di §8, **introduce un
conflitto testuale diretto** con tutte le occorrenze sopra elencate: il
catalogo pubblico passa da tre a quattro piani. Il percorso esclusivo
consentito per la issue #175 è limitato a questo file: **D09 non modifica
`.context/SPEC.md`**. L'aggiornamento della SPEC per riflettere quattro piani
pubblici resta un'azione successiva, fuori dall'ambito di questa issue, e non
deve essere eseguito come effetto collaterale di questo documento.

Fino a quell'aggiornamento, in caso di lettura della SPEC da parte di un
nuovo workspace o agente, il presente documento — una volta mersato —
prevale sulle cinque occorrenze sopra elencate per ogni punto commerciale in
conflitto, secondo la stessa gerarchia documentale già adottata da D07 verso
D03.

## 7. Cosa sostituisce di D07 e cosa resta valido

D09 sostituisce D07 **esclusivamente** nei seguenti punti commerciali in
conflitto:

- il numero di piani pubblici, da tre a quattro (aggiunta di Unlimited);
- il numero massimo di canali acquistabili da Pro, da 50 a 6;
- il numero massimo di canali acquistabili da Team, da 50 a 9;
- il numero massimo di utenti di Team, da 15 a 9;
- l'introduzione di un prezzo e un ciclo di vita dedicati per Unlimited
  (§1–§4), assenti da D07;
- l'estensione esplicita delle regole di downgrade conservativo di D07 al
  caso Unlimited → Team/Pro/Start (§4.3);
- le quantità del trial Team, da 10 canali/15 utenti a **9 canali/9 utenti**
  (§4.1), per restare coerenti con il nuovo massimo Team; durata (14 giorni),
  assenza di carta e regole di scadenza/ripristino del trial non sono
  modificate.

D09 **non sostituisce** e lascia integralmente validi in D07:

- gli scaglioni di prezzo per canale di Pro e Team (D07 §"Prezzi Pro" e
  §"Prezzi Team"), invariati in valore, solo raggiungibili entro un intervallo
  più corto;
- Start, invariato;
- Paddle come Merchant of Record esclusivo e l'intera meccanica di
  attivazione, proratazione, cancellazione, rimborso, insoluto e sospensione;
- l'esistenza del trial Team (14 giorni, un solo trial per workspace idoneo,
  senza carta) — solo le sue quantità di canali/utenti sono sostituite, non
  la sua esistenza né la sua durata;
- il piano interno Internal Unlimited (F11), la sua semantica di
  assegnazione, audit e revoca;
- la migrazione da Stripe a Paddle;
- il benchmark Buffer e il relativo processo di riesame trimestrale;
- le approvazioni storiche registrate da D07 per la issue #83, che restano
  valide per il contenuto non sostituito.

Questa decisione **non introduce alcuna modifica retroattiva a D07**: il
testo storico di D07 non viene alterato, solo dichiarato non più autorevole
nei punti elencati sopra, a partire dal merge di questa decisione.

## 8. Approvazioni richieste prima del merge

Il merge della PR relativa alla issue #175 richiede tre approvazioni
distinte, anche se ricoperte dalla stessa persona (stesso principio già
applicato da D07):

| Capacità | Approvatore | Stato | Evidenza |
| --- | --- | --- | --- |
| Product Owner | `carlo.zuffetti@apdsoftware.it` (GitHub: `@czuffetti`) | **Approvato** | Dichiarazione esplicita in sessione di lavoro sulla issue #175, 27 luglio 2026: approvazione del contenuto commerciale completo di questo documento (matrice, prezzo Unlimited, nuovi massimi Pro/Team, semantica illimitato, ciclo di vita, trial ridimensionato) nella capacità di Product Owner |
| Finance Owner | `carlo.zuffetti@apdsoftware.it` (GitHub: `@czuffetti`) | **Approvato** | Stessa dichiarazione esplicita in sessione, 27 luglio 2026, nella capacità di Finance Owner |
| Referente legale | `carlo.zuffetti@apdsoftware.it` (GitHub: `@czuffetti`) | **Approvato** | Stessa dichiarazione esplicita in sessione, 27 luglio 2026, nella capacità di referente legale |

Le tre capacità sono ricoperte dalla stessa persona, come già avvenuto per
D07: restano comunque tre approvazioni distinte, ciascuna dichiarata
esplicitamente per la propria capacità e non dedotta per estensione dalle
altre. L'evidenza registrata è la dichiarazione esplicita in questa sessione
di lavoro sulla issue #175, con lo stesso valore probatorio che D07 attribuisce
a review/merge della PR da parte dello stesso account; la PR aperta da questa
sessione riporta comunque il riferimento a questa approvazione nella propria
descrizione, in modo che l'evidenza sia anche verificabile sulla cronologia
GitHub.

## 9. Issue di implementazione dipendenti e divieto di rilascio parziale

La catena di implementazione, registrata nel commento della issue #175,
resta interamente **in attesa** e non deve essere avviata senza una nuova
conferma esplicita del Product Owner distinta da quella che ha originato
questo documento:

| Issue | Contenuto | Vincolo d'ordine |
| --- | --- | --- |
| [#176](https://github.com/apdsoftware/postqron/issues/176) | Catalogo ed entitlement F10 | Non può iniziare prima del merge di questa decisione (#175) |
| [#177](https://github.com/apdsoftware/postqron/issues/177) | Provisioning catalogo Paddle | Non prima del merge di #176 |
| [#178](https://github.com/apdsoftware/postqron/issues/178) | UI sito/prelaunch/billing e scelta mensile/annuale | Non prima del merge di #176 |
| [#179](https://github.com/apdsoftware/postqron/issues/179) | Aggiornamento Termini F25 con Claude Code / Claude Sonnet 5 Medium | Dipende dalla decisione #175 e dai valori definitivi di #176 |

Ordine minimo: **#175 → #176**; dopo #176 possono procedere in parallelo
#177 e #178. **Nessun rilascio in produzione è ammesso finché #176, #177,
#178 e #179 non sono integrati e verificati insieme**: un rilascio parziale
in cui, per esempio, il catalogo Paddle (#177) esponesse prezzi o massimi di
canale diversi da quelli di questo documento, o in cui la UI (#178) mostrasse
Unlimited prima che l'entitlement (#176) applichi davvero l'assenza di quote
lato backend, costituirebbe una violazione di questa decisione anche se ogni
singola issue fosse individualmente conforme al proprio ambito. La
responsabilità di verificare la coerenza congiunta di #176–#179 prima di
qualunque rilascio spetta a chi coordina quel rilascio, non a questo
documento.

## 10. Conseguenze e checklist

Conseguenze, a valle del superamento del gate di §8:

- Il catalogo pubblico passa da tre a quattro piani.
- I massimi di canale di Pro e Team si riducono rispettivamente a 6 e 9; il
  massimo di utenti di Team si riduce a 9.
- Unlimited diventa acquistabile e visibile, distinto da Internal Unlimited.
- `.context/SPEC.md` risulta in conflitto testuale su cinque punti (§6) fino
  a un aggiornamento successivo, fuori ambito di questa issue.
- La catena #176–#179 resta comunque bloccata finché non è aperta una nuova
  conferma esplicita del Product Owner distinta da quella registrata in §8
  per questa decisione (§9): l'approvazione di D09 sblocca solo #175, non
  avvia autonomamente #176–#179.

Checklist documentale (criteri di accettazione della issue #175):

- [x] Matrice completa dei quattro piani, prezzo mensile e annuale.
- [x] Formule verificabili per Pro, Team e Unlimited.
- [x] Valuta e riferimento al trattamento imposte/Merchant of Record di D07.
- [x] Motivazione del prezzo Unlimited (€129 / €1.290) con calcolo esplicito.
- [x] Semantica univoca delle risorse illimitate e separazione tecnica e
      amministrativa da Internal Unlimited (F11).
- [x] Upgrade, downgrade, trial, rinnovo, cancellazione e trattamento delle
      risorse eccedenti definiti senza ambiguità.
- [x] Elenco delle issue di implementazione dipendenti e divieto esplicito di
      rilascio parziale con fonti divergenti.
- [x] Conflitto con la frase "tre piani pubblici" della SPEC esplicitato,
      senza modificare `.context/SPEC.md`.
- [x] Approvazione Product Owner, Finance Owner e referente legale
      registrata prima del merge (§8).
- [x] PR verso `main` con `Closes #175`.
