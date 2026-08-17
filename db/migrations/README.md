# db/migrations

Migrazioni PostgreSQL di PostQron: lo schema del database vive qui, versionato
insieme al codice che lo usa.

## Regole

- **Numerazione progressiva e immutabile.** Ogni migrazione è una coppia di file
  `NNNN_descrizione.up.sql` / `NNNN_descrizione.down.sql`, con `NNNN` a quattro
  cifre e crescente.
- **Mai modificare una migrazione dopo il merge.** Uno schema già applicato in
  staging o in produzione si corregge con una nuova migrazione, non riscrivendo
  la precedente (AGENTS.md §7).
- **Sempre reversibili.** Il file `.down.sql` deve riportare lo schema allo stato
  esattamente precedente. Una migrazione senza `down` non è accettabile.
- **Solo DDL e dati di riferimento.** Nessun dato personale, nessun segreto,
  nessuna credenziale nei file di migrazione.
- **Idempotenza dell'applicazione.** Il tool di migrazione tiene traccia delle
  versioni già applicate: le migrazioni non vengono rieseguite.

## Applicazione

```bash
make db-up     # PostgreSQL locale
make migrate   # applica le migrazioni pendenti
```

## Stato

La cartella è vuota: lo schema iniziale (`users`, `api_keys`, `plans`,
`subscriptions`, `jobs`, `job_executions`, `repositories`, `ai_credentials`,
`audit_log`, `notifications`) e il tool `services/api/cmd/migrate` arrivano con
la issue dedicata dello schema iniziale — vedi
[docs/BACKLOG.md](../../docs/BACKLOG.md) E0.
