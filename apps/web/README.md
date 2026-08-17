# `apps/web` — sito pubblico postqron.com

Nuxt 3 a output statico con il design system del template ThemeForest
**Hexagon** portato a componenti Vue 3. Il riferimento visivo è
[`design/hexagon/blue-index.html`](../../design/hexagon/) e non va modificato.

```bash
pnpm --filter @postqron/web run dev       # sviluppo su :3000
pnpm --filter @postqron/web run generate  # statico in .output/public
```

## Com'è organizzato

| | |
|---|---|
| `assets/css/tokens.css` | colori, gradienti, ombre, forme e tempi del tema |
| `assets/css/fonts.css` | Quicksand servito dal nostro dominio |
| `assets/css/base.css` | reset, tipografia del documento, stato del reveal |
| `assets/css/layout.css` | `.container` e griglia a 12 colonne |
| `components/ui/` | i mattoni del design system |
| `components/layout/` | header, footer, velo di caricamento |
| `components/home/` | l'unico blocco specifico della home (l'hero) |
| `content/<lingua>.ts` | i testi di una lingua, separati dal markup |
| `content/index.ts` | le cinque lingue indicizzate per codice |
| `types/content.ts` | la forma di quei dati |
| `utils/locale.ts` | lingue, rilevamento e percorsi prefissati |
| `composables/useSiteLocale.ts` | lingua e contenuti della pagina corrente |
| `composables/useLocalizedHead.ts` | `lang`, `canonical` e `hreflang` |

## Multilingua

Cinque lingue — EN, IT, ES, DE, FR — con **l'inglese come lingua sorgente**
(SPEC §8-bis): si scrive in `content/en.ts` e si traduce da lì.

Il sito è statico, quindi nessun server legge `Accept-Language`. Ne discende la
struttura:

- **la lingua sta nel percorso**: `pages/[locale]/` genera `/en/`, `/it/`,
  `/es/`, `/de/`, `/fr/`, tutte pre-renderizzate;
- **la radice `/` non ha contenuto proprio**: rileva la lingua nel browser e
  reindirizza. Se avesse una home, sarebbe una sesta variante da tradurre;
- **la precedenza** è: scelta esplicita nel selettore (in `localStorage`), poi
  `navigator.languages`, poi inglese;
- **ogni pagina dichiara** il proprio `canonical` e un `hreflang` verso tutte le
  lingue, sé compresa, più `x-default` sull'inglese.

### Prezzi

La valuta **non** segue la lingua: è l'euro e gli importi sono gli stessi in
tutte e cinque (R61). Quel che cambia sono due convenzioni di scrittura, e
stanno in `money` di ogni file di lingua:

| | `en` | `it` · `es` · `de` · `fr` |
|---|---|---|
| `currencyPosition` | `before` → `€9` | `after` → `9 €` |
| `taxNote` | `+ VAT` | `+ IVA` · `+ IVA` · `+ MwSt.` · `+ TVA` |

Gli importi di SPEC §8 sono **al netto dell'IVA** — Paddle la calcola sul paese
del cliente in quanto Merchant of Record — quindi `taxNote` accompagna ogni
cifra (R61-bis). Vale per la pagina prezzi e il riepilogo del checkout quanto
per le card: un prezzo senza indicazione dell'imposta è un difetto.

Il listino non prevede periodi di prova: il piano Free è l'ingresso (R59).

```bash
pnpm --filter @postqron/web run test    # rilevamento, percorsi, hreflang, parità fra lingue
pnpm exec playwright test --project=web # le cinque rotte servite come in produzione
```

## Regole che valgono per le pagine successive

1. **Niente valori esadecimali nei componenti.** Il colore arriva da un token di
   `tokens.css`. Se manca, si aggiunge lì.
2. **Nessuna stringa nei componenti.** Un componente riceve dati tipizzati e non
   conosce i testi: una frase scritta nel markup non è traducibile, e resterebbe
   in una lingua sola in tutte e cinque le versioni della pagina.
   `test/no-strings.test.ts` legge il markup e fallisce se ne rientra una.
   Le poche etichette di interfaccia — «Menu», «Chiudi», «Leggi» — stanno nella
   sezione `ui` di ogni lingua.
3. **Ogni rotta a cui punta un link deve esistere, in tutte le lingue.** Il
   pre-rendering segue i link con `failOnError: true`, cinque volte. I percorsi
   in `content/` si scrivono **senza** prefisso (`/#pricing`) ed è `href()` di
   `useSiteLocale()` a metterlo: un link scritto a mano porta fuori dalla lingua
   corrente o, peggio, in una lingua sola. Le nuove pagine aggiungono la propria
   voce nella sezione `nav` di **tutte e cinque** le lingue insieme al proprio
   file in `pages/[locale]/`.
4. **Nessuna dipendenza esterna a runtime.** Font, immagini e icone sono servite
   dal nostro dominio; un embed di terze parti si carica solo dopo un gesto
   esplicito (vedi `VideoDialog`).
5. **Niente output di server.** `pnpm run generate` deve produrre solo
   `.output/public`: nessuna `server/` API route, nessun `defineEventHandler`.

## Cosa è stato sostituito del template

Il tema originale è HTML statico con jQuery e sei plugin. Qui non ce n'è
nessuno:

| Template | Qui |
|---|---|
| jQuery + Bootstrap JS | Vue 3 con `<script setup>` |
| griglia di Bootstrap | `layout.css`, CSS Grid con le stesse soglie |
| scrollReveal.js | direttiva `v-reveal` su `IntersectionObserver` |
| jquery.counterUp | `StatCounter` con `requestAnimationFrame` |
| parallax.js | `background-attachment` in `GradientBand` |
| magnific-popup | `<dialog>` in `VideoDialog` |
| imgfix.min.js | `object-fit: cover` |
| Font Awesome (webfont) | `HexIcon`, tracciati SVG inline |
| esagoni a tre `div` | `clip-path` in `HexagonShape` e `HexagonAvatar` |
| Quicksand da Google Fonts | due `.woff2` in `assets/fonts/` |

## Confronto con il riferimento

```bash
cd design/hexagon && python3 -m http.server 8080   # riferimento
pnpm --filter @postqron/web run dev                # porting
```

Poi si affiancano `http://localhost:8080/blue-index.html` e
`http://localhost:3000/en/`. Le misure devono coincidere: alla larghezza di
1440px il contenitore è largo 1320, le card delle funzionalità 306 e quelle
delle testimonianze 416.

Le card dei prezzi fanno eccezione, ed è deliberato: SPEC §8 definisce **quattro**
piani pubblici e acquistabili, mentre la griglia del tema ne prevedeva tre da
416px. Mostrarne tre presenterebbe come completa un'offerta che non lo è, quindi
la riga passa a `col-lg-3` e le card a 306px — la stessa misura, già verificata
contro il tema, delle card delle funzionalità.
