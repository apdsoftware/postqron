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

## Verifica

```sh
pnpm --dir features/f02-marketing-site test
pnpm --dir features/f02-marketing-site typecheck
pnpm --dir features/f02-marketing-site build
```
