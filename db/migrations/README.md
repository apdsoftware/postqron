# db/migrations

Migrazioni PostgreSQL di PostQron: lo schema del database vive qui, versionato
insieme al codice che lo usa.

## Regole

- **Numerazione progressiva e immutabile.** Ogni migrazione è una coppia di file
  `NNNN_descrizione.up.sql` / `NNNN_descrizione.down.sql`, con `NNNN` a quattro
  cifre e crescente.
- **Mai modificare una migrazione dopo il merge.** Uno schema già applicato in
  staging o in produzione si corregge con una nuova migrazione, non riscrivendo
  la precedente (AGENTS.md §7). Il tool registra il checksum di ciò che ha
  applicato e rifiuta di proseguire se un file è cambiato sotto i piedi.
- **Sempre reversibili.** Il file `.down.sql` deve riportare lo schema allo stato
  esattamente precedente. Una migrazione senza `down` non è accettabile.
- **Solo DDL e dati di riferimento.** Nessun dato personale, nessun segreto,
  nessuna credenziale nei file di migrazione.
- **Idempotenza dell'applicazione.** Il tool di migrazione tiene traccia delle
  versioni già applicate: le migrazioni non vengono rieseguite.

## Applicazione

```bash
make db-up                    # PostgreSQL locale
make migrate                  # applica le migrazioni pendenti
```

Da `services/api`, il tool accetta anche:

```bash
go run ./cmd/migrate status   # elenco con stato
go run ./cmd/migrate up 1     # applica solo la prima pendente
go run ./cmd/migrate down     # annulla l'ultima applicata
go run ./cmd/migrate down 3   # annulla le ultime tre
go run ./cmd/migrate version  # versione dello schema
```

La connessione arriva dalle `POSTGRES_*` (AGENTS.md §7): il DSN è composto da
`internal/config`, non riscritto qui. `POSTQRON_MIGRATIONS_DIR` o `-dir`
sostituiscono la ricerca automatica di questa directory.

## Migrazioni

| # | Contenuto |
|---|---|
| 0001 | Tipi enumerati e funzioni di supporto |
| 0002 | `users`, `api_keys` |
| 0003 | `plans` (con i quattro piani di SPEC §8), `subscriptions` |
| 0004 | `repositories` |
| 0005 | `jobs` |
| 0006 | `job_executions` partizionata e funzioni di manutenzione |
| 0007 | `ai_credentials` |
| 0008 | `audit_log`, `notifications` |

## Scelte di schema

Le motivazioni per esteso stanno nei commenti dei file. In sintesi:

**Due modalità di schedulazione, mutuamente esclusive.** `jobs.schedule`
(espressione cron, granularità 1 minuto) e `jobs.every_seconds` (intervallo, che
copre la risoluzione sub-minuto di R22) sono legate da un vincolo XOR: un job che
le dichiara entrambe, o nessuna, è rifiutato dal database. Il vincolo non sta
solo nel parser di `cron.yaml` perché un job può nascere anche da API o da
dashboard, senza passare da lì.

**`job_executions` è progettata sul volume peggiore.** Un job a 1 secondo produce
86.400 righe al giorno per ambiente. La tabella non ha chiave surrogata: la
chiave primaria è la quaterna naturale `(job_id, scheduled_for, environment,
attempt)`, che fa tre lavori con un indice solo — identifica la riga, è il lock
di idempotenza di R4 (il motore inserisce prima di dispatchare, e un duplicato
trova il conflitto), e il suo prefisso `(job_id, scheduled_for)` serve la query
calda dei log per job ordinati per data. Un `id uuid` avrebbe aggiunto un secondo
indice, con inserimenti sparsi, sull'unica tabella dove il costo si paga
decine di migliaia di volte al giorno.

**Retention per partizioni.** `job_executions` è partizionata per giorno su
`scheduled_for`. La retention lunga si applica eliminando partizioni intere
(`job_executions_drop_partitions_before`), che è istantaneo e non lascia bloat;
le retention più corte dei piani inferiori si applicano cancellando righe dentro
le partizioni vive, ed è sostenibile perché quei piani sono fermi a 1 minuto di
risoluzione. `job_executions_ensure_partitions` prepara la finestra futura e va
eseguita dalla manutenzione periodica: **senza una partizione disponibile
l'inserimento fallisce**, deliberatamente, invece di finire in una partizione di
default dove nessuno lo troverebbe.

Per la stessa ragione **nessuna tabella dichiara una foreign key verso
`job_executions`**: un `DROP` di partizione fallirebbe finché esiste una riga che
la riferisce, e la retention diventerebbe inapplicabile. `notifications` riferisce
l'esecuzione per colonne.

**Query calde e indici.** Le due query che contano hanno ciascuna il proprio
indice: `jobs_due_idx` è parziale su `(next_run_at)` e contiene solo i job
abilitati, non archiviati e con una prossima occorrenza — il dispatch la
interroga in continuazione, e l'indice cresce con quei job soltanto, non con il
catalogo. Per i log, l'indice è la chiave primaria di `job_executions`.

**Ambienti.** `jobs.environments` è un array di `environment` (R23): la relazione
ha al massimo due elementi da un dominio chiuso, e una tabella di collegamento
costerebbe un join su ogni lettura del dispatch senza aggiungere nulla. Ogni
ambiente produce la propria esecuzione per ciascuna occorrenza.

**Piani.** `plans` contiene la matrice di limiti di SPEC §8 come dati, non come
costanti nel codice: è la stessa matrice che il backend applica a ogni scrittura
(R15). **I prezzi non ci sono**: Paddle è la fonte di verità di importi, valute e
IVA, e duplicarli qui sarebbe la stessa seconda copia che AGENTS.md §7 vieta per
il DSN. Restano i soli identificativi di prezzo Paddle.
