# Marchio Postqron

Marchio proprio di Postqron (SPEC §4.0, R34). Sostituisce il ridisegno del
marchio del template ThemeForest Hexagon, che era in uso in `SiteLogo.vue` e che
non era nostro: ce l'ha chiunque abbia comprato lo stesso tema.

Il nome si scrive **`Postqron`** — P maiuscola, resto minuscolo. Mai `PostQron`,
mai `POSTQRON`, mai `postQron`.

> **Stato: proposta.** Il concetto — il gallo — è confermato dal proprietario.
> In `lab/candidati.html` c'è la **gamma di riduzione**: sei gradi dal disegno
> al segno. Qui è cablato quello consigliato, il gallo geometrico. La scelta del
> grado è del proprietario: si cambia `SCELTO` in
> [`tools/marchio.py`](tools/marchio.py) e si rilancia `esporta.py`, e kit,
> favicon, icona, card e sito si rifanno da soli.

---

## 1. L'idea

La ricognizione sui venti servizi del vicinato sta in
[`PANORAMA.md`](PANORAMA.md). In due righe: nella fascia cron l'orologio è il
default e un passo più in là lo è la spirale astratta, mentre **il carattere non
ce l'ha nessuno**. Fly.io dimostra che in questo mercato una figura
riconoscibile si fa ricordare; nella fascia cron di affetto non ce n'è un grammo.

Il simbolo è **la testa di un gallo, di profilo**. Non è una trovata: è l'unico
animale che il prodotto ha già — l'essere vivente che canta a ora fissa, cioè la
definizione di un cronjob prima che i computer esistessero — e in questa
categoria non lo ha nessuno.

### Il grado di figurazione

Il primo disegno del gallo aveva due difetti dichiarati: sotto i 20 px l'occhio
si chiudeva, e il registro era troppo simpatico per un prodotto che chiede una
partita IVA. **Erano lo stesso difetto.** Un animale *ritratto* ha bisogno di
occhi, becchi e bargigli, e sono proprio quei dettagli a collassare alle misure
piccole e a spostare il tono verso il giocattolo.

La risposta non è togliere il gallo: è **costruirlo invece di disegnarlo**. Il
gallo cablato qui è fatto con l'alfabeto di forme del logotipo — cerchi perfetti
e rette — perché Quicksand è una geometrica e il marchio deve condividerne la
costruzione, non solo starle accanto.

Ne segue che l'occhio non è più un dettaglio ma un cerchio pieno di 5,2 unità,
cioè 2,6 px alla misura della favicon: esiste anche là. E che il rischio
«polleria» sparisce, perché una costruzione geometrica non è un'insegna di
alimentari.

## 2. Costruzione

Il simbolo vive su una **griglia di 32 unità** per lato, e non contiene una sola
curva disegnata a mano: sono tutti cerchi e rette.

| Elemento | Geometria |
|---|---|
| Testa | cerchio, centro (14 · 19), raggio 8 |
| Occhio | controforma circolare, centro (17,4 · 16,4), raggio 2,6 |
| Cresta | tre cerchi di raggio 3, centri (9,4 · 10,6), (14 · 8,6), (18,4 · 10,4) |
| Becco | triangolo (19,6 · 15,4) → (29 · 19,4) → (19,6 · 23,4) |

I tre cerchi della cresta hanno raggio uguale e centri ad altezze diverse: sono
le altezze a fare la cresta, perché tre lobi allineati farebbero una nuvola e
tre punte uguali una corona.

Nessun dettaglio scende sotto le 4 unità di griglia, che a 16 px di resa valgono
2 px: sotto quella soglia il rendering subpixel sfoca via qualunque cosa.

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
| Marchio completo, a schermo | **110 px** di larghezza (≈ 18 px di altezza) |
| Marchio completo, a stampa | **29 mm** di larghezza |
| Simbolo solo, a schermo | **16 px** |
| Simbolo solo, a stampa | **5 mm** |

A 16 px la testa resta una testa: l'occhio è un cerchio da 2,6 px e la cresta
tre lobi da 3 px. È il grado di figurazione a renderlo possibile, ed è il motivo
per cui la scala si ferma qui e non più in basso.

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
gradino. Cadute per il concetto: la configurazione come codice non è la storia
che il prodotto vuole raccontare.

**Le altre cinque famiglie di identità**, nella stessa pagina: timbro postale,
francobollo, onda quadra, monogramma P, solo logotipo. Il gallo è stato l'unico
accettabile.

**Gli altri cinque gradi della gamma**, in `lab/candidati.html`, ognuno con il
suo costo scritto accanto:

- **Testa** — il gallo ritratto a mano: è il disegno respinto, tenuto come primo
  gradino per misurare gli altri.
- **Profilo** — il gallo intero, registro da banderuola, ma a 16 px coda, zampe
  e collo si impastano.
- **Tratto** — una linea continua sola: elegante dove c'è spazio, si chiude dove
  non ce n'è.
- **Cresta e becco** — la testa sottintesa: bolla a 16 px, ma il becco senza
  testa rischia di leggersi come una freccia «play».
- **Cresta** — le sole tre punte: regge qualunque misura, e da sola può leggersi
  come una corona.

**Le letture involontarie**, tutte scoperte a schermo e nessuna nel codice: la Q
su ramo git leggeva come una lente d'ingrandimento, le graffe si sfaldavano a
16 px, il chevron in cerchio era l'icona di ricarica, l'asterisco dei cinque
campi di cron un **aeroplano**, il monogramma `Pq` rotazionale una **N**, la
coda della q girata a destra una **padella**, il francobollo a tacche
semicircolari un **sole**, e la cresta a gobbe contigue una **nuvola** — che in
un vicinato di infrastruttura è la peggiore di tutte. La cresta si è raddrizzata
scavando le valli fra le punte e curvandone la base: è quello, e non il numero
dei lobi, a distinguere una cresta da una nuvola.
