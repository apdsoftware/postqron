# PostQron — Backlog

Decomposizione della [specifica](SPEC.md) in issue eseguibili da un singolo agente in
un worktree isolato. Ogni riga indica il requisito coperto, le dipendenze e il
provider ammesso secondo le regole di [AGENTS.md](../AGENTS.md) §4.

Legenda provider: **C** = `claude` obbligatorio · **C/X** = `claude` o `codex`.

---

## E0 — Fondamenta (blocca tutto il resto)

| # | Issue | Req | Dip. | Prov. |
|---|---|---|---|---|
| 1 | Scaffold monorepo: `pnpm-workspace`, `go.work`, `tsconfig.base`, layout cartelle | — | — | C |
| 2 | CI locale: `Makefile` completo (lint, test, build, e2e) + hook `pre-push` | §6 | 1 | C |
| 3 | Docker Compose di sviluppo con PostgreSQL | — | 1 | C |
| 4 | Schema PostgreSQL iniziale + tool di migrazione versionata | R1, R6 | 1, 3 | C |

**Tabelle previste (issue 4):** `users`, `api_keys`, `plans`, `subscriptions`,
`jobs`, `job_executions`, `repositories`, `ai_credentials`, `audit_log`,
`notifications`.

---

## E1 — Motore cron (Go)

| # | Issue | Req | Dip. | Prov. |
|---|---|---|---|---|
| 5 | Parser e validatore di espressioni cron con timezone (incl. ora legale) | R1, R2 | 4 | C |
| 6 | Scheduler: calcolo delle occorrenze e accodamento del dispatch | R2, R4 | 5 | C |
| 7 | Worker pool concorrente per il dispatch, isolamento dei job lenti | R3 | 6 | C |
| 8 | Esecutore HTTP: metodo, header, body, timeout, redirect, TLS | R1 | 7 | C |
| 9 | Lock di idempotenza: una sola esecuzione per occorrenza, resistente a riavvii | R4 | 6 | C |
| 10 | Retry con backoff esponenziale e politiche per job | R5 | 8 | C |
| 11 | Persistenza delle esecuzioni + retention configurabile | R6 | 4, 8 | C |
| 12 | Health check, metriche e alert su fallimenti persistenti | R7 | 11 | C |

---

## E2 — API e autenticazione

| # | Issue | Req | Dip. | Prov. |
|---|---|---|---|---|
| 13 | API REST: CRUD job, lista esecuzioni, trigger manuale | R8 | 4 | C |
| 14 | Autenticazione utente: registrazione, login, sessioni, reset password | R14 | 4 | C |
| 15 | API key: generazione, hash a riposo, scope, revoca | R9 | 14 | C |
| 16 | Rate limiting e quote per piano applicate lato server | R10, R15 | 15 | C |
| 17 | Ruoli, permessi admin e impersonificazione con audit log | §4.3 | 14 | C |
| 18 | Streaming log esecuzioni in tempo reale (SSE) | §4.2 | 11, 13 | C |

---

## E3 — Sito pubblico (Nuxt 3 + Hexagon)

> ⚠️ Bloccato da **Q1** (file del template Hexagon non disponibili).

| # | Issue | Req | Dip. | Prov. |
|---|---|---|---|---|
| 19 | Setup Nuxt 3 statico + porting del design system Hexagon a componenti Vue | §4.1 | 1, Q1 | C/X |
| 20 | Home, funzionalità, FAQ, contatti | §4.1 | 19 | C/X |
| 21 | Pagina prezzi con i piani | §4.1 | 19, Q2 | C/X |
| 22 | Pagine legali: Privacy Policy, Termini e Condizioni, Cookie Policy | §4.1 | 19 | C/X |
| 23 | Banner cookie con rifiuto semplice e blocco preventivo dei non essenziali | §4.1 | 22 | C/X |

---

## E4 — Dashboard cliente (Flowbite)

| # | Issue | Req | Dip. | Prov. |
|---|---|---|---|---|
| 24 | Setup Nuxt 3 statico + integrazione template Flowbite, layout e navigazione | §4.2 | 1 | C/X |
| 25 | Auth guard, sessione lato client, pagine login/registrazione | R14 | 14, 24 | C/X |
| 26 | CRUD cronjob con validazione dell'espressione cron e anteprima esecuzioni | §4.2 | 13, 24 | C/X |
| 27 | Vista log in tempo reale con filtri e dettaglio del tentativo | §4.2 | 18, 24 | C/X |
| 28 | UI billing Paddle: upgrade, downgrade, stato sottoscrizione | R16 | 35, 24 | C/X |
| 29 | Impostazioni: chiavi API PostQron e chiave AI (BYOK) | R9, R18 | 15, 33 | C/X |

---

## E5 — Dashboard amministratore (Flowbite)

| # | Issue | Req | Dip. | Prov. |
|---|---|---|---|---|
| 30 | Shell admin, elenco utenti, dettaglio account, gestione job | §4.3 | 17, 24 | C/X |
| 31 | Impersonificazione utente ("login as") con banner e audit visibile | §4.3 | 17, 30 | C/X |
| 32 | Gestione piani e assegnazione manuale | R15 | 16, 30 | C/X |
| 33 | Statistiche MRR da API Paddle | R17 | 35, 30 | C/X |
| 34 | Monitoraggio del carico VPS (CPU, RAM, disco, code) | §4.3 | 12, 30 | C/X |

---

## E6 — Billing (Paddle)

| # | Issue | Req | Dip. | Prov. |
|---|---|---|---|---|
| 35 | Integrazione Paddle: checkout, webhook firmati, sincronizzazione entitlement | R16 | 4, Q2, Q3 | C |

---

## E7 — Email (Mailronix)

| # | Issue | Req | Dip. | Prov. |
|---|---|---|---|---|
| 36 | Template HTML responsive in `emails/templates/` + renderer Go | R19, R20 | 1 | C |
| 37 | Client Mailronix: invio del payload HTML completo, retry, gestione errori | R20 | 36, Q3 | C |
| 38 | Eventi email: benvenuto, job fallito, variazione piano, sicurezza | R21 | 37, 12 | C |

---

## E8 — Sync GitHub

| # | Issue | Req | Dip. | Prov. |
|---|---|---|---|---|
| 39 | Ricezione webhook GitHub con verifica della firma HMAC | R11 | 13 | C |
| 40 | Schema e parser di `cron.yaml` con errori diagnostici leggibili | R12 | 39, Q4 | C |
| 41 | Riconciliazione idempotente dei job dal repository | R13 | 40 | C |

---

## E9 — AI (BYOK)

| # | Issue | Req | Dip. | Prov. |
|---|---|---|---|---|
| 42 | Storage cifrato delle chiavi AI, mai in chiaro in API e log | R18 | 4, 15 | C |

---

## E10 — Infrastruttura e delivery

| # | Issue | Req | Dip. | Prov. |
|---|---|---|---|---|
| 43 | Provisioning VPS Hetzner + PostgreSQL sulla stessa macchina | §2 | Q3 | C |
| 44 | Cloudflare: DNS, TLS e deploy dei frontend su Pages | §2 | 43, Q3 | C |
| 45 | Backup del database, log strutturati, alerting operativo | §5 | 43 | C |

---

## Ordine di esecuzione

**Onda 1 (nessuna dipendenza, parallelizzabile):** 1 → poi 2, 3 in parallelo.

**Onda 2 (dopo 4):** il backend E1/E2 procede in parallelo (5, 13, 14, 36), mentre
il frontend 24 parte in parallelo su E4.

**Onda 3:** tutto il resto secondo la colonna dipendenze.

**Bloccate da decisione umana:** 19, 21, 35, 40, 43, 44 — vedi [SPEC.md §7](SPEC.md#7-punti-aperti-richiedono-decisione-umana).
