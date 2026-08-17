# Riferimenti di design

## `hexagon/` — mirror del template ThemeForest Hexagon

Copia locale del demo `http://demo.tempload.com/hexagon/blue-index.html`, scaricata il
2026-08-17: 8 pagine della variante **blue** più tutti gli asset (CSS, JS, immagini,
font Font Awesome).

**A cosa serve.** È il riferimento da riprodurre **fedelmente** per il sito pubblico
(SPEC §4.1, issue #401). Sta nel repository e non su un URL esterno perché il demo è
lento e può sparire, e perché ogni worktree deve avere lo stesso riferimento byte per
byte.

**Non è codice di produzione.** Nessun file qui dentro va importato così com'è nelle app
Nuxt: il template è HTML statico con jQuery, Bootstrap e plugin datati. Va portato a
componenti Vue 3 mantenendo identici layout, spaziature, tipografia, colori e
animazioni.

### Contenuto

| | |
|---|---|
| `blue-index.html` | home — **la pagina di riferimento principale** |
| `blue-features.html`, `blue-features-single.html` | funzionalità |
| `blue-about.html`, `blue-faq.html`, `blue-contact.html` | chi siamo, FAQ, contatti |
| `blue-blog.html`, `blue-blog-single.html` | blog |
| `assets/css/blue.css` | il foglio di stile del tema — **la fonte di verità visiva** |
| `assets/js/custom.js` | animazioni e comportamenti da replicare |

### Come consultarlo

```bash
cd design/hexagon && python3 -m http.server 8080
# poi apri http://localhost:8080/blue-index.html
```

Il font **Quicksand** arriva da Google Fonts e non è nel mirror: va dichiarato nelle app
Nuxt, servito localmente per evitare una dipendenza esterna a runtime.

### Cosa non è stato copiato

Solo la variante `blue`. Il template originale ha altre varianti di colore
(`green-index.html`, `index.html`) non scaricate: il progetto usa la blu.

### Licenza

Il template è un prodotto commerciale ThemeForest. Questa copia è materiale di
riferimento interno per un progetto che ne detiene la licenza; non va ridistribuita.
