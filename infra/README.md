# infra

Provisioning e delivery di PostQron (SPEC §2, E10 del backlog).

## Topologia

```
              Cloudflare (DNS, TLS, CDN, protezione edge)
                     │
     ┌───────────────┴────────────────┐
     │                                │
Cloudflare Pages                 Hetzner VPS
 (statico)                        (dinamico)
 ├─ postqron.com      →  apps/web        ├─ services/api  (API REST + motore cron)
 └─ app.postqron.com  →  apps/dashboard  └─ PostgreSQL     (stessa macchina, latenza zero)
```

Il backend Go sulla VPS è l'**unica origin dinamica**: i frontend sono artefatti
statici prodotti da `nuxi generate` e serviti da Pages. Nessun server Nitro gira
in produzione.

## Cosa appartiene a questa cartella

- Provisioning della VPS Hetzner: hardening, utenti, firewall, servizi systemd
  dell'API e del motore cron.
- Installazione e configurazione di PostgreSQL sulla stessa macchina, con
  backup periodici e ripristino verificato.
- Configurazione Cloudflare: zona DNS, TLS, regole edge, progetti Pages e
  variabili di build dei due frontend.
- Log strutturati, metriche e alerting operativo.

## Cosa non appartiene a questa cartella

- **Segreti di qualunque tipo.** Token, chiavi e password stanno nel gestore di
  segreti dell'ambiente, mai nel repository. Qui si versiona solo la forma della
  configurazione — vedi [`.env.example`](../.env.example).
- Workflow di CI: la pipeline è locale (`make ci`), non esistono GitHub Actions
  e non devono essere introdotte (AGENTS.md §2).

## Variabili di build dei frontend

I valori `NUXT_PUBLIC_*` vengono **incorporati nel bundle al momento della
build**: su hosting statico non esiste un processo che possa leggerli a runtime.
Vanno quindi impostati nelle variabili d'ambiente del progetto Cloudflare Pages,
non a deploy avvenuto.

| Progetto Pages | Comando di build | Directory di output |
|---|---|---|
| `postqron-web` | `pnpm --filter @postqron/web run generate` | `apps/web/.output/public` |
| `postqron-dashboard` | `pnpm --filter @postqron/dashboard run generate` | `apps/dashboard/.output/public` |

## Stato

La cartella contiene solo questo README: il provisioning effettivo arriva con le
issue di E10, bloccate da **Q3** (credenziali Hetzner e Cloudflare non
disponibili) — vedi [docs/SPEC.md §7](../docs/SPEC.md).
