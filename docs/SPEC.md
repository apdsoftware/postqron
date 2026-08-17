# PostQron — Specifica Funzionale

> Fonte di verità del progetto. Ogni issue deve riferirsi a un requisito di questo
> documento. Modifiche alla spec richiedono approvazione umana.

Versione: 1.0 · Data: 2026-08-17

---

## 1. Panoramica

**PostQron** è un SaaS developer-first per la gestione, la sincronizzazione e il
monitoraggio di cronjob. L'utente definisce job schedulati (via UI o via file
`cron.yaml` nel proprio repository GitHub), PostQron li esegue in modo affidabile,
applica retry sugli errori, registra i log e notifica i fallimenti.

- **Dominio:** postqron.com
- **Sviluppatore:** APDSoftware

### Proposta di valore

- Scheduling affidabile senza gestire un server cron proprio.
- **Sync da GitHub:** i job vivono come codice nel repository dell'utente.
- Log e osservabilità delle esecuzioni con alert su fallimento.
- Interfaccia pensata per sviluppatori: API-first, chiavi API, config as code.

---

## 2. Infrastruttura

| Componente | Scelta | Note |
|---|---|---|
| Server | Hetzner VPS | host di API, motore cron e database |
| Database | PostgreSQL | **sulla stessa VPS**, per latenza zero |
| DNS / edge | Cloudflare | DNS, TLS, CDN, protezione edge |
| Hosting statico | Cloudflare Pages | distribuzione SPA/SSG dei frontend |
| Backend | Go | motore ad alta concorrenza per il dispatch |
| Frontend | Vue 3 + Nuxt 3 | build statica (`nuxi generate`) |
| Billing | Paddle | Merchant of Record: gestisce IVA e fatturazione |
| Email | Mailronix (mailronix.com) | solo recapito; l'HTML è compilato dal backend |

**Vincolo di distribuzione:** entrambi i frontend devono essere generabili
staticamente. Nessuna dipendenza da SSR a runtime, nessun server Nitro in
produzione: il backend Go è l'unica origin dinamica.

---

## 3. Requisiti funzionali

### 3.1 Motore cron (Core Engine, Go)

- **R1 — Definizione job.** Un job ha: nome, espressione cron, timezone esplicita,
  target HTTP (URL, metodo, header, body), timeout, politica di retry, stato
  abilitato/disabilitato.
- **R2 — Scheduling.** Il motore calcola le prossime esecuzioni e le dispatcha
  all'orario dovuto rispettando la timezone del job (inclusi cambi di ora legale).
- **R3 — Esecuzione concorrente.** Il dispatch è ad alta concorrenza tramite worker
  pool; un job lento non deve ritardare gli altri.
- **R4 — Idempotenza.** Ogni occorrenza schedulata è eseguita **una sola volta**,
  anche in caso di riavvio del processo o esecuzioni sovrapposte.
- **R5 — Retry.** Su errore (timeout, status ≥ 400, errore di rete) si applica un
  retry con backoff esponenziale, entro un numero massimo di tentativi configurabile
  per job.
- **R6 — Log esecuzioni.** Ogni tentativo registra su PostgreSQL: istante di inizio e
  fine, durata, esito, status HTTP, estratto della risposta troncato, numero di
  tentativo. Retention configurabile per piano.
- **R7 — Osservabilità.** Health check, metriche di coda e di latenza, alert su job
  falliti in modo persistente.

### 3.2 API

- **R8 — API REST** per CRUD dei job, consultazione delle esecuzioni e trigger
  manuale.
- **R9 — Autenticazione via API key** con scope e revoca; le chiavi sono mostrate in
  chiaro una sola volta e conservate come hash.
- **R10 — Rate limiting** e quote applicate **lato server** in base al piano.

### 3.3 Sync GitHub

- **R11 — Webhook GitHub.** Ricezione degli eventi push con **verifica della firma
  HMAC**; le richieste non firmate correttamente vengono rifiutate.
- **R12 — Parsing `cron.yaml`.** Il file nel repository dell'utente descrive i job;
  al push i task vengono aggiornati automaticamente.
- **R13 — Sync idempotente.** Il sync riconcilia lo stato desiderato con quello
  corrente: crea, aggiorna e disattiva i job rimossi, senza duplicati. Gli errori di
  parsing vengono riportati all'utente senza corrompere lo stato esistente.

### 3.4 Account, piani e billing

- **R14 — Autenticazione utente** con registrazione, login, logout, sessioni e
  recupero password.
- **R15 — Piani ed entitlement.** Limiti (numero di job, frequenza minima, retention
  dei log) applicati **lato backend**, mai solo in UI.
- **R16 — Paddle.** Checkout, upgrade e downgrade; i webhook Paddle aggiornano gli
  entitlement. La sottoscrizione è la fonte di verità del piano.
- **R17 — MRR.** Le statistiche di ricavo in area admin provengono dall'API Paddle.

### 3.5 AI — BYOK

- **R18 — Bring Your Own Key.** L'utente inserisce la propria chiave API del
  provider AI. Le chiavi sono **cifrate a riposo**, mai loggate e mai restituite in
  chiaro dall'API.

### 3.6 Email transazionali

- **R19 — Template in repository.** I template HTML risiedono in `emails/templates/`
  e sono versionati con il codice.
- **R20 — Compilazione lato backend.** Go compila dinamicamente l'HTML e lo invia
  come **payload completo** all'API di Mailronix, che funge **esclusivamente** da
  motore di recapito. Nessuna logica di template risiede su Mailronix.
- **R21 — Eventi coperti:** onboarding/benvenuto, alert di job fallito, notifiche di
  variazione piano, eventi di sicurezza.

---

## 4. Interfacce

### 4.1 Sito pubblico — postqron.com

- Template ThemeForest **Hexagon** (riferimento demo: `blue-index.html`), adattato
  fedelmente all'architettura a componenti Vue 3 / Nuxt 3.
- Pagine: home, funzionalità, prezzi, FAQ, contatti.
- **Pagine legali obbligatorie:** Privacy Policy, Termini e Condizioni, Cookie
  Policy — con la stessa identità grafica del template.
- Banner cookie con rifiuto semplice quanto l'accettazione e blocco preventivo dei
  cookie non essenziali.

### 4.2 Dashboard cliente

Template [`themesberg/flowbite-admin-dashboard`](https://github.com/themesberg/flowbite-admin-dashboard).

- Gestione cronjob: creazione, modifica, abilitazione, esecuzione manuale.
- **Log in tempo reale** delle esecuzioni.
- Integrazione Paddle per upgrade e downgrade.
- Inserimento API key AI (BYOK).
- Gestione delle proprie chiavi API PostQron.

### 4.3 Dashboard amministratore

Stesso template Flowbite.

- Gestione completa lato cliente (utenti, job, esecuzioni).
- **Impersonificazione utente ("login as")** — ogni impersonificazione è registrata
  in audit log.
- Gestione dei piani.
- Statistiche MRR via API Paddle.
- Monitoraggio del carico della VPS.

---

## 5. Sicurezza

- Segreti gestiti centralmente, mai committati.
- Chiavi AI e credenziali utente cifrate a riposo.
- Log strutturati privi di segreti e di dati personali non necessari.
- Audit log su eventi sensibili: impersonificazione, cambio piano, revoca chiavi.
- Verifica della firma su tutti i webhook in ingresso (GitHub, Paddle).
- Backup del database e rate limiting sulle API pubbliche.

---

## 6. Vincoli di processo

- **CI esclusivamente locale.** Nessun workflow GitHub Actions. Vedi [AGENTS.md](../AGENTS.md).
- **Un worktree git isolato per issue**, cancellato dopo il merge.
- Le migrazioni del database sono versionate e applicate in ordine.

---

## 7. Punti aperti (richiedono decisione umana)

Questi punti bloccano le issue collegate e non possono essere risolti da un agente.

| # | Argomento | Domanda |
|---|---|---|
| Q1 | Template Hexagon | I file del template acquistato su ThemeForest non sono nel repository. Dove reperirli? |
| Q2 | Piani e prezzi | Quanti piani, con quali limiti (job, frequenza minima, retention) e a che prezzo? |
| Q3 | Credenziali | Account e chiavi API di Paddle, Mailronix, Hetzner, Cloudflare e GitHub App. |
| Q4 | Schema `cron.yaml` | Formato esatto del file di configurazione utente. |
| Q5 | Target dei job | Solo webhook HTTP, o anche esecuzione di comandi/container? |
