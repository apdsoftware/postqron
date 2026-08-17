# Marchio Postqron

Marchio proprio di Postqron (SPEC §4.0, R34). Sostituisce il ridisegno del
marchio del template ThemeForest Hexagon, che era in uso in `SiteLogo.vue` e che
non era nostro: ce l'ha chiunque abbia comprato lo stesso tema.

Il nome si scrive **`Postqron`** — P maiuscola, resto minuscolo. Mai `PostQron`,
mai `POSTQRON`, mai `postQron`.

> **Stato: proposta.** In `lab/candidati.html` ci sono sei direzioni, una per
> famiglia di identità. Qui è cablata quella consigliata — il gallo. La scelta
> definitiva è del proprietario del prodotto: si cambia `SCELTO` in
> [`tools/marchio.py`](tools/marchio.py) e si rilancia `esporta.py`, e kit,
> favicon, icona, card e sito si rifanno da soli.

---

## 1. L'idea

La ricognizione sui venti servizi del vicinato sta in
[`PANORAMA.md`](PANORAMA.md). In due righe: nella fascia cron l'orologio è il
default e un passo più in là lo è la spirale astratta, mentre **il carattere non
ce l'ha nessuno**. Fly.io dimostra che in questo mercato una figura riconoscibile
si fa ricordare; nella fascia cron di affetto non ce n'è un grammo.

Il simbolo è **la testa di un gallo, di profilo**.

Non è una trovata: è l'unico animale che il prodotto ha già. È l'essere vivente
che canta a ora fissa — la definizione di un cronjob prima che i computer
esistessero — e in questa categoria non lo ha nessuno. Fra tutte le direzioni
possibili, è la sola che un concorrente non potrebbe copiare senza che si veda
che ha copiato.

Il gallo non parla della configurazione come codice: parla di puntualità e lo fa
con una faccia. È una scelta di registro prima che di forma.

## 2. Costruzione

Il simbolo vive su una **griglia di 32 unità** per lato, e la sua sagoma è **un
tracciato solo** più la controforma dell'occhio.

Il contorno si costruisce camminando lungo il cerchio della testa — centro
(14,6 · 19,2), raggio 7,9 — e sostituendo tre archi con altrettanti elementi:

| Elemento | Dove | Come |
|---|---|---|
| Cresta | 234° → 312° | tre gobbe da 4,3 unità di freccia |
| Becco | 342° → 22° | due rette fino a una punta a 5,4 unità dalla testa |
| Bargiglio | 36° → 76° | una gobba da 3,0 unità |
| Occhio | (17,4 · 16,6) | controforma circolare di raggio 2,3 |

Disegnare l'unione di cerchi a mano avrebbe voluto dire calcolarne le
intersezioni; percorrerla no. La gobba si dichiara per **freccia** e non per
raggio, perché la freccia è la misura che interessa a chi disegna — e perché
oltre la metà della corda serve l'arco maggiore: i primi tentativi di cresta
erano tre gobbe da mezzo millimetro proprio per quello.

Sotto le 4 unità di griglia un dettaglio vale meno di 2 px a 16 px di resa, ed è
il motivo per cui qui non ci sono piume, narici né bargigli doppi.

Il **logotipo** è disegnato, non composto: sono tracciati, non testo. Vengono da
Quicksand (SIL OFL) al peso 600 con crenatura −15/1000 di em. Un logotipo
composto a runtime dipende dal caricamento del font e, nel frattempo, mostra il
nome nel carattere di sistema.

Il **lockup** è il simbolo a sinistra e il logotipo a destra, allineati
sull'estensione verticale reale del disegno — dall'altezza delle maiuscole al
fondo della discendente — e non sull'altezza nominale dei riquadri.

## 3. Varianti

| Variante | Simbolo | Lettere | Quando |
|---|---|---|---|
| **Primaria** | gradiente ciano→viola | `--pq-logo-ink` (#1e3056) | fondi chiari, che è il caso normale |
| **Invertita** | bianco pieno | bianco pieno | fondi pieni, immagini, fondi scuri |
| **Monocromatica** | un colore solo | lo stesso colore | stampa, incisione, sponsor, un solo inchiostro |

Nel sito si scelgono con la prop `variant` di `<SiteLogo>`: `primaria`,
`invertita`, `mono`. In stampa il componente passa da solo al nero: molti
browser scartano le vernici non piatte, e il gradiente sparirebbe.

## 4. Spazio di rispetto

Attorno al marchio resta libero **almeno mezza testa** — cioè metà dell'altezza
del simbolo, su tutti e quattro i lati.

```
        ┌───────────────────────┐
        │      ↕ ½ testa        │
        │   ┌───────────────┐   │
   ½ →  │   │ 🐓 Postqron   │   │  ← ½
        │   └───────────────┘   │
        │      ↕ ½ testa        │
        └───────────────────────┘
```

Nessun testo, nessuna cornice e nessun bordo dell'immagine entrano in
quest'area.

## 5. Dimensioni minime

| Uso | Minimo |
|---|---|
| Marchio completo, a schermo | **120 px** di larghezza (≈ 20 px di altezza) |
| Marchio completo, a stampa | **32 mm** di larghezza |
| Simbolo solo, a schermo | **20 px** |
| Simbolo solo, a stampa | **6 mm** |

**Sotto i 20 px il simbolo perde l'occhio e diventa una macchia**: sotto quella
soglia si usa l'**icona applicazione** — campo pieno, simbolo in negativo — che
alla stessa misura conserva la sagoma perché il contrasto lo dà lo sfondo.
È il limite reale di questa direzione, non un dettaglio di prudenza.

## 6. Usi vietati

- **Non ricomporre il logotipo** scrivendo «Postqron» in Quicksand: ha crenatura
  propria.
- **Non ruotare, inclinare, specchiare, deformare.** Il gallo guarda a destra:
  girarlo verso sinistra lo fa sembrare un altro animale.
- **Non ridisegnare il gradiente**: due sole fermate, ciano `#0fb4e5` →
  viola `#743fe5`, sull'asse basso-sinistra → alto-destra.
- **Non mettere la variante primaria su fondi pieni o su fotografie**: lì si usa
  l'invertita.
- **Non aggiungere ombre, contorni, bagliori, occhi che ammiccano, cappelli di
  Natale.** Se il gallo va animato o vestito, si disegna un'illustrazione a
  parte: il marchio resta il marchio.
- **Non contornare il simbolo pieno**: un tratto da un'unità sulle 32 della
  griglia è mezzo pixel su tutto il perimetro a 16 px, e lo ingrassa.
- **Non racchiuderlo in un riquadro** che non sia l'icona applicazione.
- **Non tradurre il nome** né aggiungergli un sottotitolo dentro il lockup.

## 7. Testo alternativo

Il marchio è un'immagine di testo: chi non lo vede deve ricevere **il nome, non
la descrizione del disegno**. Nessun «logo con un gallo».

- Il marchio **dentro un link già etichettato** — quello dell'header, che dice
  «Postqron, torna alla home» — è **decorativo**: `aria-hidden="true"`.
  Etichettarlo di nuovo lo fa annunciare due volte.
- Il marchio **da solo**, come nel footer, porta il nome del prodotto:
  `<SiteLogo :label="content.company.name" />`.
- Il testo alternativo è **`Postqron`**, e basta: il lettore di schermo annuncia
  già che si tratta di un'immagine.
- La card social dichiara `og:image:alt` uguale a `Postqron`.

## 8. Sistema visivo

Palette, tipografia, scala tipografica, spaziature e raggi stanno in
[`apps/web/assets/css/tokens.css`](../../apps/web/assets/css/tokens.css), che è
la loro sola fonte (R35). Le due fermate del gradiente sono valori del marchio
prima che del sito e vivono in entrambi i posti — `--pq-accent-start` e
`--pq-accent-end` da una parte, `design/marchio/tools/marchio.py` dall'altra.

## 9. I file

```
design/marchio/
├── PANORAMA.md  la ricognizione sul vicinato, e dove c'è spazio
├── svg/         kit vettoriale — è ciò che si consegna a chi chiede «il logo»
├── png/         icona applicazione e card social, rasterizzate
├── tools/       la costruzione: da qui escono sia il kit sia gli asset del sito
└── lab/         pagine di prova (generate)
```

| File | Cos'è |
|---|---|
| `svg/postqron-marchio*.svg` | marchio completo, nelle tre varianti |
| `svg/postqron-simbolo*.svg` | solo simbolo, nelle tre varianti |
| `svg/postqron-logotipo.svg` | sole lettere — per chi ha già il simbolo accanto |
| `svg/postqron-icona.svg` | icona applicazione, campo pieno |
| `svg/postqron-card-social.svg` | card 1200×630 |
| `png/postqron-icona-{256,512}.png` | icona applicazione rasterizzata |
| `png/postqron-card-social.png` | card social rasterizzata |

Nel sito finiscono `apps/web/public/favicon.svg`, `favicon.ico`,
`apple-touch-icon.png`, `social-card.png`, e `apps/web/utils/marchio.ts` — che è
**generato**, non si modifica a mano.

### Rigenerare

```sh
cd design/marchio/tools
python3 esporta.py            # SVG del kit, favicon, modulo TypeScript
python3 esporta.py --servi    # come sopra, poi apri l'indirizzo che stampa
                              # per rifare i PNG e il .ico
python3 candidati.py          # la pagina delle sei direzioni
```

Serve `fonttools` e `pillow`. I PNG li disegna un browser e non un convertitore
da riga di comando: è lo stesso motore che renderà davvero questi SVG, quindi
raster e vettore non possono divergere.

## 10. Cosa è stato scartato

**Il giro «config as code»** — tre direzioni finite, in `lab/proposte.html`: la
q disegnata come blocco di configurazione, la rotaia con le righe rientrate, il
gradino. Cadute per il concetto, non per l'esecuzione: la configurazione come
codice non è la storia che il prodotto vuole raccontare.

**Le altre cinque famiglie di questo giro**, in `lab/candidati.html`, ognuna con
il suo costo scritto accanto: il timbro postale (un anello è la forma che regge
peggio i 16 px, e assomiglia a un quadrante), il francobollo (a 16 px la
dentellatura sparisce e resta un quadrato), l'onda quadra (regge benissimo le
misure piccole ma dice «monitoraggio» più che «cron»), la P sola (competente e
dimenticabile), il solo logotipo (rinuncia all'avatar, all'adesivo, all'icona).

**Le letture involontarie**, tutte scoperte a schermo e nessuna nel codice: la Q
su ramo git leggeva come una lente d'ingrandimento, le graffe si sfaldavano a
16 px, il chevron in cerchio era l'icona di ricarica, l'asterisco dei cinque
campi di cron un **aeroplano**, il monogramma `Pq` rotazionale una **N**, la
coda della q girata a destra una **padella**, e il francobollo a tacche
semicircolari un **sole**.
