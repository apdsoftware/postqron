# PostQron

SaaS developer-first per gestione, sincronizzazione e monitoraggio di cronjob.

- **Dominio:** postqron.com
- **Sviluppato da:** APDSoftware

## Struttura

```
apps/web/          Sito pubblico — Nuxt 3 statico (template Hexagon)
apps/dashboard/    Dashboard cliente + admin — Nuxt 3 statico (template Flowbite)
services/api/      Backend Go: API REST + motore cron
db/migrations/     Migrazioni PostgreSQL versionate
emails/templates/  Template HTML transazionali (compilati da Go, recapitati da Mailronix)
infra/             Provisioning Hetzner + Cloudflare
docs/              SPEC.md (contratto funzionale) e BACKLOG.md (issue)
```

## Stack

| Livello | Tecnologia |
|---|---|
| Server / DB | Hetzner VPS + PostgreSQL (stessa VPS, latenza zero) |
| Network / hosting statico | Cloudflare DNS + Pages |
| Backend | Go |
| Frontend | Vue 3 + Nuxt 3 (`nuxi generate`, output statico) |
| Billing | Paddle (Merchant of Record) |
| Email | Mailronix (solo motore di recapito; l'HTML è compilato da noi) |

## Sviluppo

```bash
make setup     # installa dipendenze e prepara l'ambiente
make db-up     # PostgreSQL locale via Docker
make migrate   # applica le migrazioni
make dev       # avvia API Go + frontend in parallelo
make ci        # pipeline completa: lint + test + build
```

## CI

La CI gira **esclusivamente in locale** (`make ci`, hook `pre-push`). Non esistono
workflow GitHub Actions e non devono essere introdotti — vedi
[AGENTS.md](AGENTS.md).

## Documentazione

- [docs/SPEC.md](docs/SPEC.md) — specifica funzionale, fonte di verità
- [docs/BACKLOG.md](docs/BACKLOG.md) — decomposizione in issue e dipendenze
- [AGENTS.md](AGENTS.md) — regole operative per gli agenti Paseo
