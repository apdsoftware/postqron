# F2 — Sito pubblico

Slice Nuxt SSR autonoma per il sito pubblico italiano di Postqron. Espone home,
funzionalità, prezzi, FAQ e documenti legali senza richiedere registri centrali.

## Integrazioni

- riusa token, logo e componenti della slice `brand`;
- legge il catalogo pubblico esclusivamente da
  `GET /api/v1/billing/plans` di F10, senza prezzi o piani di fallback;
- pubblica gli artefatti approvati e salva le preferenze cookie tramite i
  contratti F13;
- legge il credito APDSoftware da `NUXT_PUBLIC_APDSOFTWARE_URL`.

`POSTQRON_API_BASE` configura l'origine server-side dell'API.
`NUXT_PUBLIC_SITE_URL` e `NUXT_PUBLIC_APP_URL` configurano rispettivamente URL
canonici e destinazione delle call to action.

## Modello prezzi condiviso

`src/pricing-model.ts` è il modello condiviso di selezione quantità/cadenza/
piano usato dalla pagina prezzi e riusabile da F34 e F29 (issue #195): slider
discreto accessibile da 1 a `10+`, con soglie derivate dal catalogo runtime, compatibilità
Start → Pro → Team → Unlimited, preselezione del piano minimo compatibile,
conservazione della scelta esplicita superiore finché compatibile, totali,
prezzo per canale, termini annuali (10 mensilità per 12 mesi) e intent di
checkout validi. Il copy dei selettori nelle cinque lingue vive in
`PRICING_COPY` (`src/catalog.ts`). Nessun prezzo o limite è duplicato nel
client: la fonte runtime resta `GET /api/v1/billing/plans` (F10, `d09-v2`).

## Verifica

```sh
pnpm --dir features/f02-marketing-site test
pnpm --dir features/f02-marketing-site typecheck
pnpm --dir features/f02-marketing-site build
```
