# db/migrations

Migrazioni PostgreSQL di Postqron: lo schema del database vive qui, versionato
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
| 0009 | `sessions`, `user_tokens` e le loro funzioni di pulizia |
| 0010 | `jobs_unscheduled_idx`: i job in attesa della prima occorrenza |
| 0011 | `github_webhook_deliveries` e la sua pulizia periodica |
| 0012 | `workspace_secrets`: i segreti contro cui `cron.yaml` risolve `${VAR}` |
| 0013 | `paddle_webhook_events`, `paddle_checkout_intents` e la sospensione dei job al cambio di piano |
| 0014 | `jobs.overlap_policy`: cosa fare quando un'occorrenza scavalca la precedente (R41) |
| 0015 | `users.language` e l'esito di consegna su `notifications` (R21, R33) |
| 0016 | `ai_credentials`: revoca che svuota il materiale, unicità fra le sole chiavi vive, via `last_four` (R18) |
| 0017 | `users.deletion_requested_at` e `users.purge_after`: la finestra di ripensamento prima della purga (R45) |
| 0018 | `legal_consents`: quale versione di quale documento, in che lingua e quando — append-only (R46) |

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
calda dei log per job ordinati per data.

L'alternativa con `id uuid` avrebbe comunque avuto bisogno dell'indice naturale
— serve a R4 e ai log — quindi il suo costo è esattamente un secondo btree, con
inserimenti in posizione casuale. Misurato su 500.000 righe distribuite su 200
job, PostgreSQL 17 in Docker (tre ripetizioni, ordine invertito per escludere
l'effetto della cache):

| | inserimento di 500k righe | heap | indici | totale |
|---|---|---|---|---|
| chiave naturale | **0,73–0,79 s** | 33 MB | 42 MB | **75 MB** |
| `id uuid` + indice naturale | 1,74–2,00 s | 40 MB | 61 MB | 102 MB |

Circa 2,4 volte il tempo e il 36% di spazio in più, su una tabella che è l'unica
del sistema a crescere di decine di migliaia di righe al giorno per job.

Il rovescio di una chiave ordinata è la contesa sulla pagina di destra
dell'indice. Non si manifesta: la colonna di testa è `job_id`, un uuid casuale,
quindi job diversi scrivono su rami diversi del btree e solo le occorrenze di uno
stesso job sono monotone. Il benchmark `BenchmarkOccurrenceInsert` misura i due
casi separatamente e non trova differenza — a ~69 µs per inserimento il costo
dominante è il flush del WAL, non la posizione nell'indice.

**Riconoscere il conflitto di idempotenza.** Il codice SQLSTATE è `23505`. Il
*nome* del vincolo violato è però quello della partizione
(`job_executions_20260817_pkey`), non `job_executions_pkey`: confrontarlo con una
costante funziona in un test su tabella semplice e fallisce in produzione. Vedi
`TestLosingWorkerGetsARecognisableConflict`.

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

A queste si è aggiunta con la 0010 una terza query calda, `jobs_unscheduled_idx`:
lo scheduler cerca a ogni passata i job che una prossima occorrenza non ce
l'hanno ancora, perché `next_run_at` — anche il primo valore — è «calcolato dallo
scheduler» e un job appena creato nasce con la colonna a NULL. L'indice è
parziale sulle stesse condizioni della query, quindi a regime è vuoto: contiene
solo i job che stanno aspettando. Che il pianificatore usi davvero tutti e tre
gli indici è verificato con un EXPLAIN su tabelle popolate, in
`internal/scheduler/plan_test.go` — un indice che esiste e un indice che viene
usato sono cose diverse.

**La retention delle consegne GitHub è la finestra dell'idempotenza.**
`github_webhook_deliveries` (0011) esiste perché GitHub ripete le consegne — da
solo su errore, e a mano dal registro dell'App — e la ripetizione porta lo stesso
`X-GitHub-Delivery`. La chiave primaria è quell'identificativo: l'idempotenza di
R11 è un conflitto su di essa, risolto da un `INSERT ... ON CONFLICT` in una sola
istruzione, perché due copie della stessa consegna possono arrivare insieme e due
istruzioni separate le lascerebbero passare entrambe. Il conflitto aggiorna la
riga **solo se lo stato è `failed`**: è il caso per cui GitHub ripete, e scartarlo
come duplicato perderebbe l'evento per sempre.

Ne segue che `github_webhook_deliveries_purge` non regola solo l'occupazione:
cancellata la riga, la stessa consegna ripetuta risulta nuova. La retention va
tenuta sopra la finestra entro cui GitHub consente di ripetere una consegna.

Nessuna chiave esterna verso `repositories`: il webhook arriva anche per
repository che nessuno ha collegato, e una chiave esterna li rifiuterebbe
facendo rispondere 500 — cioè chiedendo a GitHub di ripetere all'infinito una
consegna che non ci serve. Il legame è per `repository_external_id`, sull'indice
che la 0004 ha già.

**Gli eventi Paddle hanno bisogno di due difese, non di una.**
`paddle_webhook_events` (0013) è la stessa idea di `github_webhook_deliveries`,
con `event_id` al posto di `X-GitHub-Delivery`, e copre la **seconda copia dello
stesso evento**. Non copre il caso peggiore, che è un evento *diverso e più
vecchio* che arriva dopo uno più recente: Paddle ritenta con backoff, e un
`subscription.updated` ripetuto dieci minuti dopo può atterrare dopo la
cancellazione che lo seguiva. Con la sola deduplicazione, quell'evento è nuovo,
legittimo e firmato — e riporta in vita un piano a pagamento che l'utente non ha
più.

La seconda difesa è `subscriptions.paddle_event_occurred_at`, la filigrana
dell'ultimo evento applicato: l'aggiornamento porta `occurred_at >= filigrana`
nella **stessa** istruzione che scrive, perché letta prima e confrontata in Go
due consegne concorrenti passerebbero entrambe. L'istante è quello del payload e
non quello di arrivo: `received_at` mette le consegne nell'ordine in cui la rete
le ha portate, che è precisamente l'ordine di cui non ci si può fidare.

**La sospensione di un job non è la sua pausa.** `jobs.enabled = false` è una
scelta dell'utente; `jobs.suspended_at` (0013) è un cambio di piano che ha spento
il job (R58). Le due convivono — un job sospeso è anche spento, altrimenti il
motore continuerebbe a eseguirlo — e `suspended_reason` dice *quale* vincolo il
job viola, perché è quello a decidere il rimedio: riaccenderne di meno
(`plan_job_limit`) oppure cambiarne la schedulazione (`plan_resolution`). Nulla
viene cancellato in nessuno dei due casi, che è la promessa che R58 e i Termini
fanno per iscritto.

**Sessioni e token monouso conservati come impronta.** `sessions.token_hash` e
`user_tokens.token_hash` non contengono il valore che il client possiede ma il suo
HMAC-SHA256 sotto una chiave derivata da `SESSION_SECRET`: un dump del database non
contiene sessioni utilizzabili né link di recupero password validi. Non è Argon2 —
i token hanno 256 bit di entropia da CSPRNG, quindi non c'è nulla da indovinare e
un KDF lento andrebbe pagato a ogni richiesta autenticata invece di una volta per
login. La chiave serve a due cose che uno SHA-256 semplice non darebbe: ruotare
`SESSION_SECRET` invalida tutte le sessioni in un colpo senza toccare le tabelle, e
chi ottiene soltanto un backup non ha il materiale per verificare ipotesi sui
token.

**Un'unica tabella per i due tipi di token monouso.** `user_tokens` copre conferma
dell'indirizzo e recupero password, distinti da `purpose`: le colonne sono le
stesse e il ciclo di vita è lo stesso (nasce, scade, si consuma una volta). Il
consumo è un `UPDATE ... WHERE consumed_at IS NULL ... RETURNING`, non una SELECT
seguita da un UPDATE, perché due richieste concorrenti con lo stesso token devono
produrre un vincitore e un rifiuto — con due istruzioni separate un link di
recupero servirebbe due volte.

**Ambienti.** `jobs.environments` è un array di `environment` (R23): la relazione
ha al massimo due elementi da un dominio chiuso, e una tabella di collegamento
costerebbe un join su ogni lettura del dispatch senza aggiungere nulla. Ogni
ambiente produce la propria esecuzione per ciascuna occorrenza.

**Piani.** `plans` contiene la matrice di limiti di SPEC §8 come dati, non come
costanti nel codice: è la stessa matrice che il backend applica a ogni scrittura
(R15). **I prezzi non ci sono**: Paddle è la fonte di verità di importi, valute e
IVA, e duplicarli qui sarebbe la stessa seconda copia che AGENTS.md §7 vieta per
il DSN. Restano i soli identificativi di prezzo Paddle.
