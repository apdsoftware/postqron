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
  L'integrazione usa `POST /email/send` in modalità a contenuto diretto, con
  autenticazione `Authorization: Bearer`; contratto completo in
  [`reference/mailronix-openapi.json`](reference/mailronix-openapi.json) e vincoli
  operativi in [`CREDENTIALS.md`](CREDENTIALS.md) §2.
- **R20.1 — Il recapito non è osservabile dalla risposta.** Mailronix risponde `202`
  in modo identico sia che l'email venga recapitata sia che il destinatario sia in
  suppression list. Nessuna logica di prodotto può dedurre il recapito dalla risposta
  all'invio: si registra `email_log_id` e nulla di più. Un alert di job fallito
  (R21) va quindi progettato senza assumere che l'email sia arrivata.
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

## 7. Decisioni prese

| # | Argomento | Decisione | Data |
|---|---|---|---|
| Q1 | Template Hexagon | Riproduzione **fedele** del demo `blue-index.html`. Mirror di riferimento in `design/hexagon/`. | 2026-08-17 |
| Q2 | Piani e prezzi | Quattro piani: Free, Pro, Team, Agency — vedi §8. | 2026-08-17 |
| Q3 | Credenziali | Procedura in [`docs/CREDENTIALS.md`](CREDENTIALS.md). **In attesa dei valori.** | 2026-08-17 |
| Q4 | Schema `cron.yaml` | Definito in §9. | 2026-08-17 |
| Q5 | Target dei job | **Solo HTTP/webhook.** Nessuna esecuzione di comandi o container — vedi §10. | 2026-08-17 |

---

## 8. Piani e limiti

Quattro livelli, modello freemium. I limiti sono applicati **lato backend** (R15): la UI
li mostra, non li applica.

| | **Free** | **Pro** | **Team** | **Agency** |
|---|---|---|---|---|
| Prezzo | $0 | $12/mese · $120/anno | $39/mese | da $99/mese |
| Target | side-project | freelance in produzione | startup e PMI | agenzie e scale-up |
| Cronjob | 20 | 200 | illimitati (fair use: 1.000 task) | illimitati |
| **Risoluzione minima** | **1 minuto** | **10 secondi** | **1 secondo** | 1 secondo |
| Retention log | 3 giorni | 15 giorni | 30 giorni + export CSV/JSON | 90 giorni + export |
| Repository `cron.yaml` | 1 | illimitati | illimitati | illimitati |
| Membri | 1 | 1 | 5 inclusi | 5+ |
| Ambienti | singolo | staging + production | staging + production | staging + production |
| AI debugging (BYOK) | — | ✓ | ✓ | ✓ |
| Alert | email | email + webhook (Slack/Discord) | avanzati per membro e ambiente | avanzati |
| RBAC | — | — | Admin / Developer / Viewer | ✓ |
| Metriche e grafici | — | — | ✓ | ✓ |
| Multi-workspace | — | — | — | ✓ isolati, fatturazione unificata |
| IP statico dedicato in uscita | — | — | — | ✓ |
| Supporto | — | — | — | prioritario |

### Requisiti che i piani introducono

- **R22 — Risoluzione sub-minuto.** Il motore deve schedulare fino a 1 secondo. Le
  espressioni cron hanno granularità minima di 1 minuto e **non sono sufficienti**:
  serve una modalità a intervallo affiancata a quella cron (§9).
- **R23 — Ambienti.** Un job appartiene a uno o più ambienti (`staging`,
  `production`) con routing e alert separati.
- **R24 — Team e RBAC.** Ruoli Admin, Developer e Viewer, con inviti e permessi
  applicati lato backend.
- **R25 — Multi-workspace.** Workspace isolati sotto un unico account padre, con
  fatturazione Paddle unificata e separazione netta dei dati fra workspace.
- **R26 — IP statico in uscita.** Le chiamate dei job dei piani Agency escono da un
  IP dedicato e stabile, dichiarabile nei firewall dei clienti.
- **R27 — Export dei log.** Esportazione delle esecuzioni in CSV e JSON.
- **R28 — Metriche.** Durata media, tasso di fallimento e andamento per job e per
  ambiente.
- **R29 — Alert su webhook.** Notifiche di fallimento verso webhook esterni
  (Slack, Discord), configurabili per membro e per ambiente.
- **R30 — AI debugging (BYOK).** Analisi dei log di errore tramite la chiave AI
  dell'utente. La chiave resta cifrata e non viene mai loggata (R18).

---

## 8-bis. Multilingua

Il prodotto è multilingua fin dall'inizio, non come aggiunta successiva.

- **Lingue supportate:** inglese, italiano, spagnolo, tedesco, francese.
- **Lingua predefinita: inglese.** L'inglese è la **lingua sorgente** dei contenuti:
  si scrive in inglese e si traduce, non il contrario.
- **R31 — Rilevamento dalla lingua del browser.** Al primo accesso la lingua si
  deduce dalle preferenze del browser; se nessuna corrisponde, si usa l'inglese.
- **R32 — Selettore di lingua** presente nell'interfaccia, su sito pubblico e
  applicazione. La scelta esplicita dell'utente prevale sempre sul rilevamento
  automatico e persiste fra le visite.
- **R33 — Lingua nelle impostazioni utente.** L'utente autenticato imposta la
  propria lingua predefinita nel profilo; vale su tutte le sue sessioni e
  **determina la lingua delle email transazionali** (R19–R21).

### Vincoli che il modello statico impone

Entrambi i frontend sono generati staticamente (SPEC §2): non esiste un server che
legga `Accept-Language` e risponda di conseguenza. Ne discende che:

- **Le rotte sono prefissate per lingua** — `/en/...`, `/it/...`, `/es/...`,
  `/de/...`, `/fr/...` — e ogni lingua viene pre-renderizzata. Il numero di rotte
  generate si moltiplica per cinque.
- **Il rilevamento avviene lato client**, con un reindirizzamento dalla radice alla
  lingua scelta. La radice `/` non deve mostrare contenuto proprio: serve solo a
  smistare, altrimenti diventa una sesta variante da mantenere.
- **SEO:** ogni pagina dichiara `hreflang` verso le altre lingue e un `canonical`
  proprio. Senza, le cinque versioni competono fra loro nei motori di ricerca.
- **Nessuna stringa nei componenti.** I testi vivono in file di traduzione; un
  componente che contiene una frase è un difetto, perché non è traducibile.

### Costo non tecnico

Le **pagine legali** (§4.1) in cinque lingue non sono una traduzione automatica:
Privacy Policy, Termini e Condizioni e Cookie Policy hanno valore legale in ogni
giurisdizione in cui sono pubblicate. Vanno tradotte e riviste da chi ne risponde,
non da un agente. Lo stesso vale, in misura minore, per i testi commerciali dei
piani.

---

## 9. Schema `cron.yaml`

File nella radice del repository dell'utente, letto a ogni push (R11–R13).

```yaml
version: 1

defaults:
  timezone: Europe/Rome
  timeout: 30s
  retries: { max: 3, backoff: exponential }

jobs:
  - name: daily-digest          # identità stabile del job: chiave della riconciliazione
    schedule: "0 9 * * *"       # espressione cron — granularità 1 minuto
    timezone: Europe/Rome       # sovrascrive defaults
    environments: [production]
    request:
      url: https://api.example.com/tasks/digest
      method: POST
      headers:
        Authorization: "Bearer ${DIGEST_TOKEN}"   # segreto del workspace, mai in chiaro
      body: '{"kind":"daily"}'
    timeout: 60s
    retries: { max: 5, backoff: exponential }
    alerts:
      on_failure: [email, slack]

  - name: healthcheck
    every: 10s                  # modalità a intervallo — risoluzione sub-minuto
    environments: [staging, production]
    request:
      url: https://api.example.com/health
      method: GET
    timeout: 5s
```

### Regole

- **`schedule` ed `every` sono mutuamente esclusivi.** `schedule` accetta un'espressione
  cron a 5 campi (minimo 1 minuto); `every` accetta una durata (`1s`, `10s`, `5m`, `1h`)
  e serve la risoluzione sub-minuto di R22. Un job che dichiara entrambi, o nessuno dei
  due, è un errore di validazione.
- **`name` è l'identità del job**, unica nel file: è la chiave su cui la riconciliazione
  decide se creare, aggiornare o disattivare (R13). Rinominare un job equivale a
  cancellarlo e crearne un altro.
- **I segreti non stanno nel file.** `${VAR}` è risolto contro i segreti del workspace
  al momento dell'esecuzione. Un riferimento non risolvibile fa fallire la validazione,
  non l'esecuzione.
- **La validazione è totale.** Un file non valido non modifica lo stato esistente: gli
  errori vengono riportati all'utente con riga e colonna (R13).
- `version` è obbligatorio: consente evoluzioni future dello schema senza rompere i
  file esistenti.
- Limiti di piano e risoluzione minima sono verificati al momento del sync: un `every:
  1s` su piano Free viene rifiutato con un messaggio esplicito, non silenziosamente
  degradato.

---

## 10. Target dei job: solo HTTP

PostQron esegue **esclusivamente chiamate HTTP** verso endpoint dell'utente. Non
esegue comandi shell, script o container.

**Motivazione.** Eseguire codice arbitrario dell'utente significa isolamento a livello
di container o microVM, gestione di immagini, quote di CPU e memoria, e una superficie
di sicurezza che su una singola VPS Hetzner condivisa non è difendibile. È un prodotto
diverso, con costi infrastrutturali di un altro ordine di grandezza.

**Posizionamento di mercato.** La divisione è netta: gli scheduler puri sono
HTTP-only — Upstash QStash invia solo richieste HTTP, Google Cloud Scheduler supporta
HTTP/S e Pub/Sub, EasyCron e cron-job.org sono servizi HTTP. Chi esegue comandi shell
(Render) lo fa perché ospita già il codice dell'utente in container effimeri: è una
funzione della piattaforma di hosting, non dello scheduler.

L'IP statico dedicato del piano Agency (R26) ha senso proprio in questo modello: è
utile perché le chiamate escono verso i clienti e devono attraversarne i firewall.
