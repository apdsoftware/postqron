# Marchio Postqron

Marchio proprio di Postqron (SPEC §4.0, R34). Sostituisce il ridisegno del
marchio del template ThemeForest Hexagon, che era in uso in `SiteLogo.vue` e che
non era nostro: ce l'ha chiunque abbia comprato lo stesso tema.

Il nome si scrive **`Postqron`** — P maiuscola, resto minuscolo. Mai `PostQron`,
mai `POSTQRON`, mai `postQron`.

---

## 1. L'idea

Postqron non esegue richieste a orario: fa vivere le schedulazioni **come codice
nel repository di chi le scrive**. Il `cron.yaml` viene riletto a ogni push, e
la revisione di un cronjob passa da una pull request. Il marchio doveva dire
questo, non «tempo»: di orologi il mercato è pieno.

Il simbolo è la **q di Postqron disegnata come un blocco di configurazione**.

- L'occhiello è un rettangolo a lati dritti e angoli raccordati — un campo di
  testo, non un quadrante.
- La controforma è quadrata, come un valore dentro un campo.
- L'asta scende sotto la riga: la `q` è l'unica lettera del nome che lo fa, ed è
  l'unico appiglio tipografico che il nome offre.

Il simbolo non sta **accanto** al logotipo: ne prende il posto al centro, dove la
`q` sta nel nome. Sono un oggetto solo, non due oggetti che ripetono la stessa
forma a mezzo centimetro di distanza — ed è il motivo per cui non esiste un
lockup «simbolo + logotipo» da comporre a mano, con le sue proporzioni da
sbagliare.

## 2. Costruzione

Il simbolo vive su una **griglia di 32 unità** per lato.

| Elemento | Misura |
|---|---|
| Occhiello, ingombro | 20 × 20 unità |
| Parete dell'occhiello | 4 unità |
| Controforma | 12 × 12 unità |
| Raccordo esterno / interno | 7,5 / 4,5 unità |
| Asta | 4 unità di larghezza, fino a 3 unità dal bordo inferiore |
| Inchiostro totale | 20 × 26 unità |

La parete di 4 unità non è arbitraria: innestata nel logotipo vale 108 unità di
em contro le 100 dell'asta delle lettere, cioè lo stesso peso a meno dell'8 %.
A 5 unità la `q` pesava visibilmente più del resto della parola.

Il raggio è il parametro che è costato più prove. Sotto le 6 unità il simbolo
innestato si legge come **il quadratino del glifo mancante**; sopra le 9 smette
di dire «blocco» e torna a dire «cerchio». A 7,5 i lati restano dritti e gli
angoli non sono più spigoli.

Il **logotipo** è disegnato, non composto: sono tracciati, non testo. Vengono da
Quicksand (SIL OFL) al peso 600 con crenatura −15/1000 di em, poi corretti. Un
logotipo composto a runtime dipende dal caricamento del font e, nel frattempo,
mostra il nome nel carattere di sistema.

## 3. Varianti

| Variante | Simbolo | Lettere | Quando |
|---|---|---|---|
| **Primaria** | gradiente ciano→viola | `--pq-logo-ink` (#1e3056) | fondi chiari, che è il caso normale |
| **Invertita** | bianco pieno | bianco pieno | fondi pieni, immagini, fondi scuri |
| **Monocromatica** | un colore solo | lo stesso colore | stampa, incisione, sponsor, un solo inchiostro |

Nel sito si scelgono con la prop `variant` di `<SiteLogo>`: `primaria`,
`invertita`, `mono`. In stampa il componente passa da solo al nero: molti
browser scartano le vernici non piatte, e il gradiente sparirebbe lasciando
«Post ron».

## 4. Spazio di rispetto

Attorno al marchio resta libero **almeno un occhiello** — l'altezza della `x`,
cioè l'ingombro del quadrato del simbolo. Sul lato dell'asta si misura dal fondo
della discendente, non dalla linea di base.

```
        ┌───────────────────────┐
        │        ↕ 1 occhiello  │
        │   ┌───────────────┐   │
   1 →  │   │   Postqron    │   │  ← 1
        │   └───────────────┘   │
        │        ↕ 1 occhiello  │
        └───────────────────────┘
```

Nessun testo, nessuna cornice e nessun bordo dell'immagine entrano in
quest'area.

## 5. Dimensioni minime

| Uso | Minimo |
|---|---|
| Marchio completo, a schermo | **96 px** di larghezza (≈ 21 px di altezza) |
| Marchio completo, a stampa | **26 mm** di larghezza |
| Simbolo solo, a schermo | **16 px** |
| Simbolo solo, a stampa | **5 mm** |

Sotto i 96 px il marchio completo non si legge più: si usa il **simbolo da
solo**, che è disegnato apposta per reggere i 16 px della favicon — la misura a
cui la maggior parte delle persone lo vedrà più spesso.

## 6. Usi vietati

- **Non ricomporre il logotipo** scrivendo «Postqron» in Quicksand: il logotipo
  ha crenatura propria e la `q` è il simbolo.
- **Non usare il simbolo accanto alla parola intera** («◻ Postqron»): la `q`
  comparirebbe due volte.
- **Non ruotare, inclinare, specchiare, deformare.** L'altezza e la larghezza si
  cambiano insieme.
- **Non ridisegnare il gradiente**: due sole fermate, ciano `#0fb4e5` →
  viola `#743fe5`, sull'asse basso-sinistra → alto-destra.
- **Non mettere la variante primaria su fondi pieni o su fotografie**: il viola
  del gradiente sparisce sullo scuro. Lì si usa l'invertita.
- **Non aggiungere ombre, contorni, bagliori, sfumature interne.**
- **Non racchiuderlo in un riquadro** che non sia l'icona applicazione.
- **Non cambiarne i colori** per adattarlo a una campagna o a un fondo colorato:
  esiste la monocromatica.
- **Non tradurre il nome** né aggiungergli un sottotitolo dentro il lockup.

## 7. Testo alternativo

Il marchio è un'immagine di testo: chi non lo vede deve ricevere **il nome, non
la descrizione del disegno**.

- Il marchio **dentro un link già etichettato** — quello dell'header, che dice
  «Postqron, torna alla home» — è **decorativo**: `aria-hidden="true"`.
  Etichettarlo di nuovo lo fa annunciare due volte.
- Il marchio **da solo**, come nel footer, porta il nome del prodotto:
  `<SiteLogo :label="content.company.name" />`.
- Il testo alternativo è **`Postqron`**, e basta. Non «logo Postqron»: il lettore
  di schermo annuncia già che si tratta di un'immagine.
- La card social dichiara `og:image:alt` uguale a `Postqron`, perché è ciò che
  l'immagine contiene.

## 8. Sistema visivo

Palette, tipografia, scala tipografica, spaziature e raggi stanno in
[`apps/web/assets/css/tokens.css`](../../apps/web/assets/css/tokens.css), che è
la loro sola fonte (R35). Le due fermate del gradiente sono valori del marchio
prima che del sito e vivono in entrambi i posti — `--pq-accent-start` e
`--pq-accent-end` da una parte, `design/marchio/tools/marchio.py` dall'altra.

## 9. I file

```
design/marchio/
├── svg/     kit vettoriale — è ciò che si consegna a chi chiede «il logo»
├── png/     icona applicazione e card social, rasterizzate
├── tools/   la costruzione: da qui escono sia il kit sia gli asset del sito
└── lab/     pagine di prova (generate)
```

| File | Cos'è |
|---|---|
| `svg/postqron-marchio.svg` | marchio completo, primaria |
| `svg/postqron-marchio-invertito.svg` | marchio completo, invertita |
| `svg/postqron-marchio-mono.svg` | marchio completo, monocromatica |
| `svg/postqron-simbolo*.svg` | solo simbolo, nelle tre varianti |
| `svg/postqron-logotipo.svg` | sole lettere, `q` compresa — per chi ha già il simbolo accanto |
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
python3 proposte.py           # la pagina delle tre direzioni
```

Serve `fonttools` e `pillow`. I PNG li disegna un browser e non un convertitore
da riga di comando: è lo stesso motore che renderà davvero questi SVG, quindi
raster e vettore non possono divergere.

## 10. Cosa è stato scartato

Due direzioni sono arrivate finite alla pagina di prova e non sono state scelte
— stanno in `lab/proposte.html`, con questa.

- **Indent** — una rotaia verticale e tre righe rientrate. Dice «configurazione
  come codice» nel modo più diretto di tutti e regge benissimo i 16 px. Legge
  però come l'icona «vista a elenco» di una barra strumenti: competente e
  dimenticabile, che è esattamente il difetto da cui si veniva.
- **Gradino** — l'indentazione in un tratto solo. Il più insolito dei tre, ma è
  alto e stretto, non sta bene accanto alla parola, e a mente fredda si legge
  come un tubo o una zeta.

Prima ancora, e documentati in [`../marchio-proposte/`](../marchio-proposte/),
erano caduti la Q su ramo git (leggeva come una lente d'ingrandimento), le
graffe (si sfaldano a 16 px) e il chevron in cerchio (l'icona di ricarica).
Questo giro ne ha aggiunti altri: l'asterisco dei cinque campi di cron legge come
un **aeroplano**, il monogramma `Pq` rotazionale come una **N**, la coda della q
che gira a destra rifà la **padella**.
