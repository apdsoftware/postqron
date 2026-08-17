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

Prerequisiti: Go ≥ 1.26, Node ≥ 22, pnpm ≥ 11, Docker (solo per il database).

```bash
cp .env.example .env   # configurazione locale (ignorata da git)
make setup     # installa dipendenze e prepara l'ambiente
make db-up     # PostgreSQL locale via Docker
make migrate   # applica le migrazioni
make dev       # avvia API Go + frontend in parallelo
make ci        # pipeline completa: lint + test + build
```

`make dev` avvia tre processi:

| Processo | Porta | Comando |
|---|---|---|
| API Go (`services/api`) | 8080 | `pnpm run dev:api` |
| Sito pubblico (`apps/web`) | 3000 | `pnpm run dev:web` |
| Dashboard (`apps/dashboard`) | 3001 | `pnpm run dev:dashboard` |

### Build statica dei frontend

`make build` esegue `nuxi generate` su entrambe le app e produce output
interamente statico in `apps/*/.output/public`, senza server Nitro. Il sito
pubblico è pre-renderizzato (SSR solo in build, per la SEO), la dashboard è una
SPA. Il backend Go è l'unica origin dinamica — vedi
[docs/SPEC.md §2](docs/SPEC.md) e [infra/README.md](infra/README.md).

## Database locale

PostgreSQL gira in Docker tramite [`docker-compose.yml`](docker-compose.yml).
Serve Docker con il plugin Compose v2.

```bash
cp .env.example .env   # una volta sola: senza .env il container non parte
make db-up             # avvia PostgreSQL in background
make db-down           # ferma il container (i dati restano)
```

`make db-up` è idempotente: se il container è già attivo non fa nulla.

### Versione

L'immagine è **`postgres:17.11-bookworm`**, pinnata alla patch release.
PostgreSQL 17 è la major che gira sulla VPS Hetzner in produzione: è quella
pacchettizzata di default in Debian 13 (trixie), è disponibile via repository
PGDG ed è supportata da upstream fino a novembre 2029. L'immagine è quella
Debian e non la Alpine, per avere in locale le stesse collation glibc della
produzione — collation diverse cambiano l'ordinamento degli indici e rendono i
test locali non rappresentativi. Il database lavora in UTC; le timezone dei job
sono gestite esplicitamente dal motore cron (SPEC R2).

### Configurazione

Le credenziali arrivano da `.env`, che **non** è versionato; il modello è
[`.env.example`](.env.example). Nessuna credenziale reale entra nel repository.

| Variabile | Default | Descrizione |
|---|---|---|
| `POSTGRES_HOST` | `127.0.0.1` | indirizzo di pubblicazione del container |
| `POSTGRES_PORT` | `5432` | porta sull'host |
| `POSTGRES_DB` | `postqron` | nome del database |
| `POSTGRES_USER` | `postqron` | utente proprietario |
| `POSTGRES_PASSWORD` | — | obbligatoria: senza, il container non parte |
| `POSTGRES_SSLMODE` | `disable` | in locale la connessione è in chiaro sul loopback |

Utente, password e nome del database vengono applicati **solo al primo avvio**,
quando `initdb` crea il volume. Per applicare valori nuovi serve ripartire da
zero.

#### Una sola fonte di verità per la connessione

Queste variabili sono l'**unica** descrizione della connessione. Compose le usa
per creare e pubblicare il container; API e tool di migrazione compongono da esse
il proprio DSN. Cambiare porta significa toccare un solo valore.

Nel repository **non esiste un `DATABASE_URL` già formato**, ed è deliberato: un
URL che ripete host, porta e credenziali è una seconda copia libera di divergere
dalla prima. Basta spostare il container su un'altra porta e dimenticarsi di
aggiornare l'URL perché le migrazioni finiscano su un PostgreSQL diverso — quello
di un altro progetto ancora in ascolto sulla porta vecchia, per esempio — senza
un errore che lo segnali, se le credenziali per caso combaciano.

Interpolare l'URL non risolve: Compose espande `${...}` quando legge il `.env`,
un client Go che carica lo stesso file in genere no. Lo stesso file
significherebbe due cose diverse a seconda di chi lo legge.

Se ti serve un URL per uno strumento esterno, componilo al momento invece di
salvarlo:

```bash
set -a && . ./.env && set +a
export DSN="postgres://$POSTGRES_USER:$POSTGRES_PASSWORD@$POSTGRES_HOST:$POSTGRES_PORT/$POSTGRES_DB?sslmode=$POSTGRES_SSLMODE"
```

### Stato e dati

```bash
docker compose ps                # stato e healthcheck
docker compose logs -f postgres  # log del container

# shell psql: utente e database vengono dall'ambiente del container,
# così restano allineati al .env anche se li hai cambiati
docker compose exec postgres sh -c 'psql -U "$POSTGRES_USER" -d "$POSTGRES_DB"'
```

I dati vivono nel volume `postqron-postgres-data` e sopravvivono a `make
db-down`. Per azzerare il database — **operazione distruttiva** —
`docker compose down -v`.

## CI

La CI gira **esclusivamente in locale** (`make ci`, hook `pre-push`). Non esistono
workflow GitHub Actions e non devono essere introdotti — vedi
[AGENTS.md](AGENTS.md).

## Documentazione

- [docs/SPEC.md](docs/SPEC.md) — specifica funzionale, fonte di verità
- [docs/BACKLOG.md](docs/BACKLOG.md) — decomposizione in issue e dipendenze
- [AGENTS.md](AGENTS.md) — regole operative per gli agenti Paseo
