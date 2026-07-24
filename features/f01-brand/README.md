# Postqron brand and design system

Questa slice contiene l'identità visiva e i componenti UI fondamentali di
Postqron. È autonoma e viene individuata tramite `feature.yaml`, senza registri
centrali.

## Contenuto

- `assets/`: marchio, varianti, favicon, icona applicazione e social card;
- `tokens/`: sorgente JSON conforme al formato Design Tokens e custom property CSS;
- `components/`: componenti Vue 3 e relativi stili;
- `examples/BrandShowcase.vue`: composizione responsive di riferimento;
- `docs/brand-guidelines.md`: regole di marchio, contenuto e accessibilità;
- `test/`: controlli automatici su asset, token, contrasto e componenti.

## Uso in Nuxt/Vue

Importare gli stili una sola volta nel punto di ingresso della superficie e i
componenti necessari vicino all'uso:

```ts
import 'features/f01-brand/components/components.css'
import { PqButton, PqField } from 'features/f01-brand/components'
```

```vue
<PqField
  v-model="title"
  label="Titolo del post"
  help="Descrivi in modo chiaro il contenuto."
  required
/>
<PqButton type="submit">Salva bozza</PqButton>
```

Per il tema impostare `data-pq-theme="light"`, `dark` o `system` sull'elemento
radice. Il tema light è il valore predefinito. I componenti non richiedono font
remoti: Inter è preferito quando disponibile e la pila di sistema è il fallback.

## Verifica locale

```sh
pnpm --dir features/f01-brand test
pnpm --dir features/f01-brand typecheck
```

I test verificano anche le coppie di colore documentate. Il controllo automatico
non sostituisce una prova manuale con tastiera, zoom al 200%, modalità ad alto
contrasto e screen reader sulla pagina che integra i componenti.
