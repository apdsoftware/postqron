# Marchio Postqron

Marchio proprio di Postqron (SPEC §4.0, R34). Sostituisce il ridisegno del
marchio del template ThemeForest Hexagon, che era in uso in `SiteLogo.vue` e che
non era nostro: ce l'ha chiunque abbia comprato lo stesso tema.

Il nome si scrive **`Postqron`** — P maiuscola, resto minuscolo. Mai `Postqron`,
mai `POSTQRON`, mai `postQron`.

> **Stato: proposta.** Concetto e grado sono decisi dal proprietario: il gallo,
> ridotto alla sola **cresta**. In `lab/candidati.html` c'è la gamma del disegno
> — sette varianti sugli assi peso, proporzioni, graduazione, terminali e
> simmetria. Qui è cablata quella consigliata. Cambiare variante è una riga:
> `SCELTO` in [`tools/marchio.py`](tools/marchio.py), poi `esporta.py`, e kit,
> favicon, icona, card e sito si rifanno da soli.

---

## 1. L'idea

La ricognizione sui venti servizi del vicinato sta in
[`PANORAMA.md`](PANORAMA.md). In due righe: nella fascia cron l'orologio è il
default e un passo più in là lo è la spirale astratta, mentre **il carattere non
ce l'ha nessuno**.

Il marchio è **la cresta di un gallo**. Il gallo è l'unico animale che il
prodotto ha già — l'essere vivente che canta a ora fissa, cioè la definizione di
un cronjob prima che i computer esistessero — e in questa categoria non lo ha
nessuno. La cresta è la sua parte riconoscibile: la sola che si legga a 16 px,
e la sola che stia in un header venduto a un'azienda senza far ridere nessuno.

Delle tre punte contano due cose, e sono entrambe frutto di errori visti a
schermo:

- **le valli.** Le prime tre gobbe si toccavano in alto e leggevano **nuvola**,
  che in un vicinato di infrastruttura è la lettura peggiore possibile. Non è il
  numero dei lobi a distinguere una cresta da una nuvola: sono gli incavi fra le
  punte e la base curva.
- **l'ordine.** Tre punte **in salita** non dicono «cresta», dicono **grafico di
  crescita** — e staccate diventano tre barre, cioè il marchio del template che
  questa issue esiste per sostituire. Le stesse tre altezze con **la più alta in
  mezzo** perdono il grafico e tengono il ritmo. È anche l'ordine che un pettine
  ha davvero.

## 2. Costruzione

Il simbolo vive su una **griglia di 32 unità** per lato ed è **un tracciato
solo**.

| Elemento | Misura |
|---|---|
| Centri delle punte | 9,6 · 16 · 22,4 |
| Altezze, dalla base | 11 · 18,5 · 14 |
| Larghezza alla base | 6,2 per punta |
| Raggio della cima | 1,8 |
| Fondo dell'incavo | 30 % della punta più bassa fra le due |
| Pancia della base | 2,6 unità sotto la linea di base |
| Inchiostro totale | 19 × 21 unità |

Ogni misura sta sopra le 4 unità di griglia, che a 16 px di resa valgono 2 px:
sotto quella soglia il rendering subpixel sfoca via qualunque cosa. È il motivo
per cui la cresta ha tre punte e non sette.

Il **gradiente non è decorazione**: essendo ancorato alla griglia
(`gradientUnits="userSpaceOnUse"`) e non al riquadro del singolo oggetto,
attraversa il disegno da sinistra a destra, e ogni punta lo incontra a una tappa
diversa. Il colore racconta la successione senza che nessuno gliela assegni.

Il **logotipo** è disegnato, non composto: sono tracciati, non testo. Vengono da
Quicksand (SIL OFL) al peso 600 con crenatura −15/1000 di em. Un logotipo
composto a runtime dipende dal caricamento del font e, nel frattempo, mostra il
nome nel carattere di sistema.

Il **lockup** è il simbolo a sinistra e il logotipo a destra, allineati
sull'estensione verticale reale del disegno — dall'altezza delle maiuscole al
fondo della discendente — e non sull'altezza nominale dei riquadri. **È la forma
in cui il marchio vive quasi sempre, ed è lì che va giudicato**: la cresta da
sola è un segno, e ha bisogno del nome accanto per dire quale.

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

Attorno al marchio resta libero **almeno mezza cresta** — cioè metà dell'altezza
del simbolo, su tutti e quattro i lati.

```
        ┌───────────────────────┐
        │     ↕ ½ cresta        │
        │   ┌───────────────┐   │
   ½ →  │   │ ⩕ Postqron    │   │  ← ½
        │   └───────────────┘   │
        │     ↕ ½ cresta        │
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

A 16 px la cresta resta una cresta: le punte valgono 3 px alla base e gli incavi
poco meno di 2. Non c'è nessun dettaglio che possa chiudersi, ed è il motivo per
cui questo grado di riduzione è stato scelto.

**Il simbolo da solo va usato con parsimonia**: fuori dal lockup è un segno, e
chi non ha mai visto il nome accanto non ha modo di sapere che è una cresta.

## 6. Usi vietati

- **Non ricomporre il logotipo** scrivendo «Postqron» in Quicksand: ha crenatura
  propria.
- **Non ruotare, inclinare, specchiare, deformare.** L'ordine delle punte —
  media, alta, bassa — è il disegno: riordinarle in salita lo trasforma in un
  grafico, specchiarle lo fa pendere dalla parte sbagliata.
- **Non aggiungere né togliere punte.** Tre è il numero che regge i 16 px.
- **Non ridisegnare il gradiente**: due sole fermate, ciano `#0fb4e5` →
  viola `#743fe5`, sull'asse basso-sinistra → alto-destra.
- **Non mettere la variante primaria su fondi pieni o su fotografie**: lì si usa
  l'invertita.
- **Non aggiungere ombre, contorni, bagliori.** Se serve un gallo intero — per
  un'illustrazione, un adesivo, una campagna — si disegna a parte: il marchio
  resta la cresta.
- **Non contornare il simbolo pieno**: un tratto da un'unità sulle 32 della
  griglia è mezzo pixel su tutto il perimetro a 16 px, e lo ingrassa.
- **Non racchiuderlo in un riquadro** che non sia l'icona applicazione.
- **Non tradurre il nome** né aggiungergli un sottotitolo dentro il lockup.

## 7. Testo alternativo

Il marchio è un'immagine di testo: chi non lo vede deve ricevere **il nome, non
la descrizione del disegno**. Nessun «logo con una cresta di gallo».

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

**Il giro «config as code»** — la q come blocco di configurazione, la rotaia con
le righe rientrate, il gradino. Caduto per il concetto: la configurazione come
codice non è la storia che il prodotto vuole raccontare.

**Le altre cinque famiglie di identità** — timbro postale, francobollo, onda
quadra, monogramma P, solo logotipo. Il gallo è stato l'unico accettabile.

**Gli altri cinque gradi di riduzione del gallo** — testa disegnata, profilo
intero da banderuola, tratto continuo, costruzione geometrica, cresta con becco.
Il proprietario ha indicato l'ultimo gradino: la cresta sola.

**Le altre sei varianti della cresta**, in `lab/candidati.html`, ognuna con il
suo costo scritto accanto: Graduata (punte in salita: legge «grafico di
crescita»), Acuta (punte a spillo: il registro più nervoso), Lame (punte
staccate: diventa il marchio Hexagon che stiamo sostituendo), Contorno (elegante
in grande, si chiude a 16 px), Sghemba (energia in cambio di compostezza), Bassa
(la proporzione anatomica, tenuta come controllo dell'esperimento).

**Le letture involontarie**, tutte scoperte a schermo e nessuna nel codice: la Q
su ramo git leggeva come una lente d'ingrandimento, le graffe si sfaldavano a
16 px, il chevron in cerchio era l'icona di ricarica, l'asterisco dei cinque
campi di cron un **aeroplano**, il monogramma `Pq` rotazionale una **N**, la
coda della q girata a destra una **padella**, il francobollo a tacche
semicircolari un **sole**, la cresta a gobbe contigue una **nuvola** e la cresta
in salita un **grafico di crescita**.
