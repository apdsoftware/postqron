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
| `content/` | i testi e i dati, separati dal markup |
| `types/content.ts` | la forma di quei dati |

## Regole che valgono per le pagine successive

1. **Niente valori esadecimali nei componenti.** Il colore arriva da un token di
   `tokens.css`. Se manca, si aggiunge lì.
2. **I contenuti stanno in `content/`.** Un componente riceve dati tipizzati e
   non conosce i testi: è ciò che rende sostituibile la copia senza toccare il
   layout.
3. **Ogni rotta a cui punta un link deve esistere.** Il pre-rendering segue i
   link con `failOnError: true`: una voce di menu verso una pagina non ancora
   scritta fa fallire la build. Le nuove pagine aggiungono la propria voce in
   `content/navigation.ts` insieme al proprio file in `pages/`.
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
`http://localhost:3000/`. Le misure devono coincidere: alla larghezza di 1440px
il contenitore è largo 1320, le card delle funzionalità 306 e quelle di prezzi e
testimonianze 416.
