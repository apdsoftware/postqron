# Postqron

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

Prerequisiti: Go ≥ 1.26, Node ≥ 22, pnpm ≥ 11,
[gitleaks](https://github.com/gitleaks/gitleaks) (`brew install gitleaks`),
[govulncheck](https://pkg.go.dev/golang.org/x/vuln/cmd/govulncheck)
(`go install golang.org/x/vuln/cmd/govulncheck@latest`), Docker (solo per il
database).

La **versione** di Go non va scelta a mano: `go.work` e `services/api/go.mod`
fissano la toolchain con la direttiva `toolchain`, e il comando `go` scarica da
sé quella richiesta. Basta averne una ≥ 1.26 per far partire il meccanismo.

```bash
cp .env.example .env   # configurazione locale (ignorata da git)
make setup     # installa dipendenze, scarica il browser e2e, installa l'hook
make db-up     # PostgreSQL locale via Docker
make db-check  # smoke test dell'ambiente database
make migrate   # applica le migrazioni
make dev       # avvia API Go + frontend in parallelo
make ci        # pipeline completa: lint + typecheck + test + build + e2e
```

`make help` elenca tutti i target.

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

### Verifica dell'ambiente

```bash
make db-check   # smoke test: è il nostro container? porta, credenziali, UTC, UTF8, versione
```

`make db-check` è il primo comando da lanciare quando il backend si comporta in
modo inspiegabile. Controlla le assunzioni che il resto del progetto dà per
scontate: che sulla porta di `.env` risponda il container di Postqron e non altro,
che le credenziali autentichino davvero, che la timezone del server sia UTC (il
motore cron converte dai fusi dei job, R2), che la codifica sia UTF8 e che la
major version sia la stessa della produzione.

Lo stesso controllo di identità è un prerequisito di `make migrate`, e non è
formale. Se `POSTGRES_PORT` punta a una porta occupata da un **altro**
PostgreSQL, `make db-up` fallisce con «port is already allocated» — ma le
migrazioni, che quella porta la trovano aperta e parlante, si connetterebbero
comunque; con credenziali di default combacianti finirebbero sul database di un
altro progetto senza un errore. La guardia chiede a Docker quale `host:porta`
pubblica il nostro container e la confronta con `.env`: se non coincidono, quella
porta è di qualcun altro.

```
✗ il container di Postqron pubblica 127.0.0.1:5433, ma .env punta a 127.0.0.1:5432.
```

La soluzione è cambiare `POSTGRES_PORT` in `.env` — un valore solo, il resto lo
eredita — non riusare la porta che si trova libera di rispondere.

### Migrazioni

Lo schema vive in [`db/migrations/`](db/migrations/README.md) come coppie di
file `NNNN_descrizione.up.sql` / `.down.sql`, applicate in ordine dal tool
`services/api/cmd/migrate`.

```bash
make migrate                          # applica tutte le pendenti
go run ./cmd/migrate status           # elenco con stato (da services/api)
go run ./cmd/migrate down             # annulla l'ultima applicata
go run ./cmd/migrate down 3           # annulla le ultime tre
```

`make migrate` passa prima dalla guardia descritta sopra: applica le migrazioni
solo dopo aver verificato che su `POSTGRES_HOST:POSTGRES_PORT` risponda il
container di Postqron.

Il tool compone il DSN da `internal/config` (AGENTS.md §7) e legge le
`POSTGRES_*` dall'ambiente; quelle che mancano le prende dal `.env` più vicino,
risalendo le directory, così `go run ./cmd/migrate` funziona da qualunque punto
del monorepo.

Se una `POSTGRES_*` è impostata nell'ambiente **con un valore diverso** da quello
del `.env`, il comando si ferma. La ragione è la guardia qui sopra:
`scripts/db-env.sh` legge le variabili con `set -a; . ./.env`, dando la
precedenza al file, mentre il tool la darebbe all'ambiente. Un
`POSTGRES_PORT=15432 make migrate` farebbe quindi verificare alla guardia la
porta del `.env` e connettere il tool a un'altra: il controllo passerebbe su un
server e la migrazione ne toccherebbe un altro. Per puntare altrove si cambia il
valore in `.env` — che è l'unico posto in cui la connessione è descritta.

Ogni migrazione gira nella propria transazione, ed è registrata in
`schema_migrations` con il checksum dei due file. Modificare una migrazione già
applicata la fa rifiutare al passaggio successivo: uno schema in staging o in
produzione si corregge con una migrazione nuova, mai riscrivendo la vecchia. Un
lock consultivo serializza due migratori sullo stesso database.

## CI

La CI gira **esclusivamente in locale** (`make ci`). Non esistono workflow GitHub
Actions e non devono essere introdotti — vedi [AGENTS.md](AGENTS.md).

### Cosa esegue

```
preflight                      strumenti e layout del monorepo
├─ ci-go          services/api  go vet · gofmt · go build · go test -race
├─ ci-web         apps/web      eslint · nuxt typecheck · vitest · nuxt generate
├─ ci-dashboard   apps/dashboard  idem                                   in parallelo
├─ ci-root        radice        prettier · tsc (e2e) · gitleaks · test degli script
└─ ci-vuln        services/api  govulncheck
e2e                            Playwright sull'output statico delle due app
```

I cinque job girano in parallelo, uno per componente. La partizione non è per
fase (prima tutti i lint, poi tutti i test) perché dentro una singola app Nuxt le
fasi si contendono la directory `.nuxt`; fra componenti diversi non c'è nulla di
condiviso. Gli e2e restano a valle: servono le build di entrambe le app.

Ogni job scrive su un log suo e la pipeline stampa una riga per componente,
riversando l'output completo solo di quelli falliti — `make -j` da solo
mescolerebbe le righe, e GNU Make 3.81 (quello di macOS) non ha `--output-sync`.

Per lanciare un pezzo alla volta: `make lint`, `make test`, `make build`,
`make e2e`, oppure il singolo componente (`make ci-web`, `make test-go`,
`make lint-dashboard`).

### Quando un job fallisce

Ogni corsa lascia una directory datata sotto `.ci-logs/` (ignorata da git) con il
log integrale di **ogni** job — non solo dei falliti — e un `corsa.txt` che
riporta ora, ramo, revisione, stato dell'albero e componenti eseguiti in
parallelo. Il riepilogo finale stampa il percorso dei log dei job falliti.

```
.ci-logs/
├─ degradato-ultima-corsa.txt   solo se un controllo non ha potuto controllare
└─ 20260817-181205-45123/
   ├─ corsa.txt     contesto: ora, ramo, revisione, componenti in parallelo
   ├─ ci-go.log     output integrale del job
   ├─ ci-web.log
   └─ esito         ok | ko
```

Serve perché i log **sopravvivano alla riesecuzione**: un fallimento
intermittente si perdeva del tutto, dato che la corsa successiva è di solito
verde. È costato tre indagini fallite sullo stesso flake ([#489](https://github.com/apdsoftware/postqron/issues/489)) prima che
qualcuno lo caratterizzasse con ottocento esecuzioni.

La rotazione tiene **10 corse rosse e 3 verdi**, contate separatamente: davanti a
un rosso intermittente la prima cosa che si fa è rilanciare, e con un tetto unico
le riesecuzioni verdi spingerebbero fuori proprio il log da leggere. I tetti si
cambiano con `CI_LOG_KEEP_FAILED` e `CI_LOG_KEEP_PASSED`, la radice con
`CI_LOG_DIR`.

### Vulnerabilità note (`make vulncheck`)

`ci-vuln` esegue [govulncheck](https://pkg.go.dev/golang.org/x/vuln/cmd/govulncheck)
su `services/api`. Costa circa 2 secondi e gira in parallelo ai job più lunghi:
il tempo aggiunto alla corsa completa non è misurabile.

Due comportamenti da conoscere.

**Fallisce solo sul raggiungibile.** govulncheck distingue le vulnerabilità che
il nostro codice *chiama* da quelle che stanno in moduli che importiamo e non
tocchiamo. Solo le prime fermano la CI; le seconde compaiono nel rapporto con un
`⚠`. Oggi è il caso di `GO-2026-5932` su `golang.org/x/crypto`: riguarda il
pacchetto `openpgp`, che non importiamo, ed è dichiarata senza versione corretta.
Se bloccasse, la CI sarebbe rossa per sempre per qualcosa che non possiamo
sistemare — e il primo che deve spingere una correzione urgente commenterebbe la
riga nel `Makefile`.

**Senza rete non mente.** Il database delle vulnerabilità si scarica. Se non è
raggiungibile il job non fallisce e non passa: risulta `⚠ controllo incompleto`
nel riepilogo, la corsa resta verde e `make ci` ripete l'avviso in fondo, dopo
gli e2e. Il verde di quella corsa non include il controllo delle vulnerabilità, e
lo dice.

```
  ✓ ci-go          33s
  ⚠ ci-vuln        0s  → controllo incompleto: .ci-logs/…/ci-vuln.log
```

### La CI pretende i componenti

`make preflight` fallisce se un componente dichiarato nel manifest del
`Makefile` non c'è — e anche se sotto `apps/` ne compare uno non dichiarato, o se
un `package.json` non definisce uno degli script che la pipeline invoca. È una
scelta esplicita: la versione precedente saltava in silenzio ciò che non trovava,
il che a repository vuoto era corretto ma oggi significa che una cartella
rinominata produce una CI verde che non ha eseguito niente.

Quando il monorepo cresce, il manifest in cima al `Makefile` è l'unico posto da
aggiornare.

### Test end-to-end

Gli e2e servono `apps/*/.output/public` con un server di file statici
(`scripts/static-server.mjs`) che emula Cloudflare Pages, non con `nuxt preview`:
in produzione nessun Nitro gira, e testare contro un server che non esiste
proverebbe la cosa sbagliata. Verificano che il sito pubblico sia davvero
pre-renderizzato, che la dashboard si idrati lato client e che il fallback di
routing della SPA funzioni.

### Hook pre-push

`make hooks` (già incluso in `make setup`) imposta `core.hooksPath=.githooks`:
prima di ogni push git esegue `make ci` e rifiuta il push se fallisce. L'hook è
versionato in [`.githooks/`](.githooks/) — non in `.git/hooks/`, che oltre a non
essere nel repository non è nemmeno una directory quando si lavora da un worktree.

Per saltarlo consapevolmente: `git push --no-verify`.

## Osservabilità (R7)

Tre endpoint che rispondono a tre domande diverse. Tenerle separate è il punto:
un processo vivo che non riesce a scrivere sul database è malato, e un health
check che risponde `200` perché il server HTTP funziona è peggio di nessun health
check — fa credere che vada tutto bene mentre non parte più niente.

| Endpoint | Domanda | Accesso |
|---|---|---|
| `GET /healthz` | «questo processo è in piedi?» | pubblico |
| `GET /readyz` | «il motore sta funzionando adesso?» | stato pubblico, dettaglio autenticato |
| `GET /metrics` | tutte le grandezze del motore | autenticato, o assente |

**`/healthz` non tocca il database e risponde sempre `200`**, ed è quello che
deve fare: serve a chi riavvia un processo inchiodato, e una liveness che
fallisce per colpa del database lo farebbe riavviare in un ciclo che non risolve
niente.

**`/readyz` risponde `503`** quando il motore non può lavorare: il database non
accetta scritture, `job_executions` non ha una partizione in cui scrivere, o lo
scheduler non porta a termine una passata da mezzo minuto. Risponde `200` con
`status: degraded` quando funziona e qualcuno deve intervenire prima che smetta —
tipicamente il margine di partizioni sceso sotto i tre giorni. Le sonde girano su
un ticker e l'endpoint legge l'ultimo esito: interrogarlo non costa una query.

### Le metriche, e come si protegge l'accesso

`POSTQRON_METRICS_TOKEN` (`openssl rand -base64 32`) si presenta come
`Authorization: Bearer <token>`:

```bash
curl -fsS -H "Authorization: Bearer $POSTQRON_METRICS_TOKEN" \
  http://127.0.0.1:8080/metrics
```

**Senza la variabile la rotta non esiste** — `404`, non `403`. È la stessa regola
dei segreti dei webhook: un endpoint di metriche aperto racconta al mondo il
carico del servizio, il numero di clienti attivi e i tempi di risposta dei loro
bersagli.

La grandezza da guardare per prima è `postqron_dispatch_lag_seconds`, lo scarto
fra l'orario dovuto di un'occorrenza e la sua partenza: è la misura del prodotto
(R47), e accanto c'è `postqron_dispatch_tolerance_seconds`, che è l'impegno
dichiarato. Poi `postqron_partition_horizon_days`, che scende di uno al giorno e
preannuncia con due settimane di anticipo il momento in cui il motore smetterà di
poter scrivere.

Il formato è quello di esposizione testuale di Prometheus, scritto a mano: il
binario non prende dipendenze per produrlo, la registrazione è un'atomica per
occorrenza e la raccolta non tocca il database. Su una macchina sola (SPEC §2)
l'osservabilità non deve pesare più del servizio che osserva.

## Documentazione

- [docs/SPEC.md](docs/SPEC.md) — specifica funzionale, fonte di verità
- [docs/BACKLOG.md](docs/BACKLOG.md) — decomposizione in issue e dipendenze
- [AGENTS.md](AGENTS.md) — regole operative per gli agenti Paseo
