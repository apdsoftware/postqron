# D03 — Modello commerciale e billing

- **Stato:** accettata
- **Data:** 24 luglio 2026
- **Ambito:** MVP
- **Decisione collegata:** D3 della SPEC

## Decisione

Postqron viene commercializzato in euro con tre abbonamenti pubblici — Start,
Pro e Team — disponibili con fatturazione mensile o annuale anticipata. Tutti i
piani includono le funzioni core dell'MVP; il valore crescente deriva dalla
capacità del workspace e dalla collaborazione, non da funzioni necessarie alla
pubblicazione affidabile.

### Offerta pubblica

#### Start

- **Prezzo:** 9 EUR al mese oppure 90 EUR all'anno.
- **Limiti:** 1 membro, 5 canali social e 100 pubblicazioni programmate
  al mese.
- **Funzioni:** workspace individuale e tutte le funzioni core.

#### Pro

- **Prezzo:** 24 EUR al mese oppure 240 EUR all'anno.
- **Limiti:** 5 membri, 15 canali social e 500 pubblicazioni programmate
  al mese.
- **Funzioni:** tutto Start, workspace condiviso e gestione membri.

#### Team

- **Prezzo:** 49 EUR al mese oppure 490 EUR all'anno.
- **Limiti:** 15 membri, 50 canali social e 2.000 pubblicazioni programmate
  al mese.
- **Funzioni:** tutto Pro, con capacità superiore per team e portafogli
  di canali.

I prezzi annuali sono addebitati in un'unica soluzione e corrispondono a due
mensilità gratuite rispetto alla cadenza mensile. Non sono previsti piano
gratuito permanente, tariffazione a consumo, sovrapprezzi per eccedenza,
componenti aggiuntivi o costi di attivazione nell'MVP.

Le **funzioni core**, presenti in tutti e tre i piani, sono:

- connessione e revoca dei canali social supportati;
- composer, media, bozze e pubblicazione multi-canale;
- calendario editoriale, programmazione, modifica, duplicazione,
  riprogrammazione e annullamento;
- stati di pubblicazione, retry controllati e notifiche email di errore;
- visualizzazione dell'utilizzo e della capacità residua;
- gestione del profilo e funzioni privacy previste dalla SPEC.

Non fanno parte dell'offerta MVP approvazioni editoriali, analytics, libreria
media, coda intelligente, assistente ai contenuti, API pubblica o funzioni
mobile. Una loro futura introduzione richiede una nuova decisione commerciale;
non sono implicitamente incluse dai nomi dei piani.

### Definizione dei limiti

- Il limite **membri** comprende l'Owner. Un invito pendente riserva un posto;
  un invito scaduto o revocato lo libera.
- Un **canale** è una singola destinazione social connessa, per esempio una
  pagina o un profilo selezionato, non il provider nel suo complesso. Un canale
  disconnesso libera capacità.
- Una **pubblicazione programmata** è una destinazione che entra per la prima
  volta nello stato Programmato. Lo stesso contenuto destinato a tre canali
  consuma tre unità.
- Riprogrammare la medesima destinazione non consuma una nuova unità.
  Duplicarla, oppure aggiungere una nuova destinazione, sì.
- Una pubblicazione annullata o fallita resta conteggiata nella finestra in cui
  è stata programmata, per evitare aggiramenti del limite. Bozze e storico non
  consumano la quota.
- La quota delle pubblicazioni si azzera ogni mese alla data e ora UTC di
  attivazione dell'abbonamento, anche per la cadenza annuale. Per date non
  presenti in un mese si usa l'ultimo giorno del mese.
- Tutti i limiti sono applicati lato server. Al raggiungimento di un limite la
  nuova azione viene rifiutata con l'indicazione del limite raggiunto, senza
  addebiti automatici né perdita di dati.

## Prova gratuita

Ogni nuovo workspace può utilizzare una sola prova gratuita di **14 giorni**
con funzioni e limiti del piano Pro. Non è richiesta una carta per iniziare e
non viene creata automaticamente una sottoscrizione a pagamento.

Per continuare oltre la prova, l'Owner deve scegliere esplicitamente uno dei
tre piani e completare il pagamento. Vengono inviati promemoria sette giorni e
tre giorni prima della scadenza, più una notifica alla scadenza.

Se la prova termina senza un abbonamento:

- il workspace passa allo stato limitato `trial_expired`;
- dati, membri e connessioni non vengono cancellati;
- non si possono aggiungere membri o canali né creare nuove programmazioni;
- le pubblicazioni future vengono messe in pausa, non annullate;
- dopo l'attivazione, quelle con data ancora futura riprendono; quelle la cui
  data è trascorsa richiedono una riprogrammazione esplicita.

La prova è concessa una sola volta per workspace e cliente verificato. Il
supporto può autorizzare eccezioni solo tramite un'azione amministrativa
tracciata.

## Provider, valuta e imposte

Il provider scelto per l'MVP è **Stripe**:

- Stripe Checkout per l'acquisto;
- Stripe Billing per sottoscrizioni, fatture e cambi piano;
- Stripe Customer Portal per metodo di pagamento, fatture e disdetta;
- Stripe Tax con calcolo automatico delle imposte.

L'unica valuta di catalogo dell'MVP è **EUR**. I prezzi Stripe sono configurati
con comportamento fiscale inclusivo: l'importo pubblico è il totale ricorrente
del piano e include IVA o imposte equivalenti quando dovute. Checkout raccoglie
i dati fiscali necessari, mostra il trattamento applicato prima della conferma
e la fattura espone aliquota, esenzione o reverse charge. Non si effettua
conversione dinamica in altre valute.

Stripe Tax calcola l'imposta, ma Postqron resta responsabile di registrazioni,
versamenti, dichiarazioni e correttezza dei paesi in cui vende. Prima del
go-live un consulente fiscale deve confermare registrazioni, testo prezzi,
codice fiscale del prodotto e trattamento B2B/B2C; tale verifica non cambia la
scelta del provider o i prezzi base di questa decisione.

## Ciclo dell'abbonamento

### Acquisto e rinnovo

L'entitlement a pagamento viene attivato solo dopo conferma del primo
pagamento. Il rinnovo avviene automaticamente alla stessa cadenza scelta.
L'Owner può disdire in qualsiasi momento: per impostazione predefinita la
disdetta ha effetto alla fine del periodo già pagato e non elimina il workspace.

### Upgrade e cambio di cadenza

- L'upgrade a un piano superiore ha effetto immediato dopo il pagamento della
  differenza prorata per il tempo residuo.
- Stripe calcola la proratazione al secondo e genera subito la fattura. Il
  credito del piano precedente compensa il costo del nuovo.
- Se il pagamento dell'upgrade fallisce, il piano e i limiti precedenti restano
  attivi; non viene concesso temporaneamente il piano superiore.
- Il passaggio da mensile ad annuale è immediato, con credito prorato del
  periodo mensile inutilizzato e addebito del nuovo anno.
- Il passaggio da annuale a mensile è trattato come downgrade e avviene alla
  fine dell'anno già pagato.

### Downgrade

Il downgrade viene programmato per la fine del periodo già pagato, senza
rimborso né credito prorato. Fino a quel momento restano attivi piano e limiti
correnti.

Prima della decorrenza l'Owner vede quali limiti verrebbero superati e può
ridurre membri e canali. Alla decorrenza, eventuali risorse eccedenti vengono
conservate: nessun membro viene rimosso, nessun canale viene disconnesso e
nessun post viene cancellato. Le pubblicazioni già programmate continuano.
Sono bloccate soltanto nuove aggiunte nelle categorie eccedenti finché
l'utilizzo non torna entro il limite. Se la quota mensile dei post è già
superata, nuove programmazioni restano bloccate fino al suo azzeramento o a un
nuovo upgrade.

### Pagamenti insoluti

Un rinnovo fallito porta la sottoscrizione in `past_due` e avvia quattro
tentativi complessivi nei giorni 0, 3, 7 e 14, accompagnati da email all'Owner
con un collegamento sicuro per aggiornare il pagamento.

Durante i 14 giorni di tolleranza il servizio e le pubblicazioni già
programmate continuano normalmente. Se anche l'ultimo tentativo fallisce:

- Stripe marca la sottoscrizione `unpaid`, senza cancellarla automaticamente;
- il workspace entra nello stato `payment_restricted`;
- dati, membri, canali e post vengono conservati;
- sono bloccate nuove programmazioni, nuovi membri e nuovi canali;
- le pubblicazioni future vengono messe in pausa anziché cancellate;
- un pagamento riuscito ripristina il piano; le pubblicazioni ancora future
  riprendono, mentre quelle scadute richiedono conferma e riprogrammazione.

Non è configurata alcuna cancellazione automatica per insoluto. La cancellazione
dei dati segue esclusivamente una richiesta esplicita dell'utente e la policy
privacy/retention. Il supporto può chiudere manualmente un debito irrecuperabile,
ma l'azione deve essere tracciata e non comporta di per sé l'eliminazione del
workspace.

## Piano interno

Esiste un entitlement separato **Internal Unlimited**, con membri, canali e
pubblicazioni illimitati e tutte le funzioni MVP. Non è un prodotto Stripe e
non ha prezzo, trial, fattura o rinnovo.

Il piano interno:

- non compare in sito, pagina prezzi, Checkout, Customer Portal o payload
  accessibili ai client;
- non può essere acquistato, richiesto o autoassegnato;
- può essere assegnato solo lato server da un amministratore autorizzato a un
  account presente in allowlist;
- richiede autenticazione amministrativa forte e un motivo obbligatorio;
- registra in audit log assegnazione e revoca con attore, destinatario,
  timestamp e motivazione;
- non può essere derivato da campi inviati dal client o da metadati Stripe.

Alla revoca non viene applicato automaticamente un piano pubblico: il workspace
mantiene i dati e passa al piano pubblico già pagato, se presente, oppure allo
stato limitato finché l'Owner non ne acquista uno. Eventuali eccedenze seguono
le stesse regole conservative del downgrade.

## Conseguenze

- Billing ed entitlement hanno una matrice di piani deterministica e
  verificabile lato server.
- Non esiste un percorso commerciale che cancelli automaticamente risorse
  dell'utente.
- L'assenza di overage mantiene semplici fatture e aspettative nell'MVP.
- I prezzi inclusivi semplificano il messaggio pubblico, ma Postqron assorbe le
  differenze di aliquota tra giurisdizioni.
- Le funzioni post-MVP restano fuori dall'offerta finché non vengono decise e
  implementate esplicitamente.

## Fonti di implementazione

Le fonti seguenti supportano le capacità attribuite al provider; prezzi, limiti
e policy commerciali restano decisioni di Postqron.

- [Stripe — How subscriptions work](https://docs.stripe.com/billing/subscriptions/overview)
- [Stripe — Use trial periods on subscriptions](https://docs.stripe.com/billing/subscriptions/trials)
- [Stripe — Prorations](https://docs.stripe.com/billing/subscriptions/prorations)
- [Stripe — Subscription schedules](https://docs.stripe.com/billing/subscriptions/subscription-schedules)
- [Stripe — Revenue recovery and Smart Retries](https://docs.stripe.com/billing/revenue-recovery)
- [Stripe — Product tax codes and tax behavior](https://docs.stripe.com/tax/products-prices-tax-codes-tax-behavior)

Fonti consultate il 24 luglio 2026.

## Verifica dei criteri di accettazione

- [x] Nome, prezzo, valuta, cadenza e funzioni dei tre piani pubblici.
- [x] Limiti e regole di conteggio per membri, canali e pubblicazioni.
- [x] Prova gratuita, trattamento fiscale e provider.
- [x] Upgrade, downgrade, proratazione e insoluti senza cancellazioni
  automatiche.
- [x] Piano interno separato, illimitato, nascosto e amministrato lato server.
