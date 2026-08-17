# Postqron — Specifica Funzionale

> Fonte di verità del progetto. Ogni issue deve riferirsi a un requisito di questo
> documento. Modifiche alla spec richiedono approvazione umana.

Versione: 1.0 · Data: 2026-08-17

---

## 1. Panoramica

**Postqron** è un SaaS developer-first per la gestione, la sincronizzazione e il
monitoraggio di cronjob. L'utente definisce job schedulati (via UI o via file
`cron.yaml` nel proprio repository GitHub), Postqron li esegue in modo affidabile,
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
- **R10 — Rate limiting** e quote applicate **lato server** in base al piano. Sono
  **due cose distinte**, e confonderle produce un prodotto ostile:
  - il **tetto tecnico** per identità è una **difesa del servizio**, uguale su tutti i
    piani, scelto nel codice e documentato come tale. Non è una voce di listino, e
    §8 non lo contiene deliberatamente;
  - le **quote di piano** derivano da §8 e si applicano alle operazioni che consumano
    capacità — scritture e trigger — **non alle letture**. Far consumare budget alle
    letture renderebbe la dashboard inutilizzabile sul piano Free, cioè punirebbe
    l'uso normale per difendersi dall'abuso.

  I due rifiuti dicono cose diverse: il `429` **di piano** nomina il piano che
  consente di più; il `429` **tecnico** non promette nulla, perché nessun piano ne
  concede di più. Un rifiuto che suggerisce un aggiornamento quando l'aggiornamento
  non servirebbe è una bugia commerciale.
- **R10-bis — La retention si applica anche in lettura.** I limiti di §8 (3, 15, 30,
  90 giorni) valgono sul registro delle esecuzioni: una richiesta che chiede
  esplicitamente oltre la retention del piano viene **rifiutata dicendo perché**, non
  ristretta in silenzio — un utente che non vede le proprie righe senza sapere il
  motivo apre un ticket.
  **È metà del lavoro:** copre l'intervallo fra due passate della cancellazione
  periodica, non la sostituisce. La privacy policy dichiara che i log sono conservati
  per il periodo del piano **e poi cancellati**, e nascondere righe che continuano a
  esistere renderebbe quel documento inesatto.

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

### 3.7 Sicurezza dell'esecuzione

Postqron esegue richieste HTTP verso URL scelti dall'utente, **dalla stessa macchina
su cui girano l'API e il database** (§2). Senza i vincoli che seguono il prodotto è
uno strumento d'attacco, non un servizio.

- **R38 — Nessuna richiesta verso l'interno.** Sono rifiutati loopback, indirizzi
  privati, link-local, l'endpoint di metadata delle piattaforme cloud
  (`169.254.169.254`) e gli indirizzi riservati, in IPv4 e IPv6. Il controllo si
  applica **all'indirizzo risolto, non al nome**, e **si ripete su ogni redirect**:
  un nome che risolve a un indirizzo pubblico al momento della validazione può
  risolvere a uno interno al momento della richiesta. Il traffico in uscita del
  motore non deve poter raggiungere il database né l'API.
- **R39 — Difesa dall'abuso.** Un job `every: 1s` sono 86.400 richieste al giorno
  verso un bersaglio scelto dall'utente, dal nostro IP. Servono un tetto alla
  frequenza aggregata per host di destinazione, il rilevamento di più account che
  puntano allo stesso bersaglio, e una procedura di sospensione. La reputazione
  dell'IP in uscita è un bene condiviso da tutti i clienti.
- **R40 — Tetti di esecuzione.** Timeout massimo, dimensione massima della risposta
  letta e conservata, numero massimo di redirect seguiti. Sono limiti del servizio,
  non preferenze del job: un job non può alzarli oltre il tetto del proprio piano.
- **R41 — Esecuzioni sovrapposte.** Quando un'occorrenza scatta mentre la precedente
  è ancora in corso, il comportamento è **esplicito e configurabile per job**:
  saltare, accodare o consentire la sovrapposizione, con un valore predefinito
  dichiarato. Con la risoluzione al secondo non è un caso raro, è la norma.

### 3.8 Segreti del workspace

Lo schema `cron.yaml` (§9) risolve `${VAR}` contro i segreti del workspace: è una
funzionalità a sé, non un dettaglio del parser.

- **R42 — Gestione dei segreti.** Creazione, aggiornamento, elenco e revoca dei
  segreti di un workspace. **Cifrati a riposo**, mai restituiti in chiaro dall'API né
  mostrati dopo il salvataggio, mai scritti nei log né nelle risposte conservate.
- **R43 — Uso in esecuzione.** I segreti sono risolti al momento dell'esecuzione e
  iniettati in URL, header e corpo. Un riferimento non risolvibile fa fallire la
  **validazione** al sync, non l'esecuzione alle tre di notte. Il valore non compare
  nei log di esecuzione, che sono visibili all'utente.

### 3.9 Dati personali e conformità

- **R44 — Esportazione dei dati.** L'utente ottiene i propri dati in formato
  leggibile da una macchina: profilo, job, esecuzioni, workspace.
- **R45 — Cancellazione.** Cancellazione dell'account e del workspace con conferma e
  **periodo di sicurezza configurabile** prima della rimozione definitiva. La
  cancellazione interrompe le esecuzioni e revoca le chiavi.
- **R46 — Tracce del consenso.** Versione, data e lingua in cui il consenso è stato
  prestato (§8-bis), per ogni documento legale.

---

## 4. Interfacce

### 4.0 Identità di marca

- **R34 — Marchio proprio.** Postqron ha un logo originale: simbolo, logotipo,
  varianti (positiva, negativa, monocromatica), dimensioni minime e favicon.
  **Vincolo:** il marchio attualmente in uso è il ridisegno di quello del template
  Hexagon. La licenza ThemeForest copre l'uso del template in un prodotto finale, ma
  il marchio dell'autore non è nostro e non è distintivo — ce l'ha chiunque abbia
  comprato lo stesso tema. **Va sostituito prima di qualunque pubblicazione.**
- **R35 — Sistema visivo dichiarato.** Palette, tipografia, scala tipografica,
  spaziature e raggi vivono come token, non come valori sparsi nei componenti.
  Palette e tipografia attuali derivano dal template: vanno confermate come scelta
  deliberata o sostituite, non ereditate per inerzia.
- **R36 — Tono di voce.** Registro, persona e regole di scrittura dell'interfaccia,
  definiti in **inglese** come lingua sorgente (§8-bis) e coerenti nelle cinque
  lingue. Serve anche agli agenti: senza, ogni issue inventa il proprio.
- **R37 — Nessun contenuto segnaposto in produzione.** Testimonianze, fotografie,
  mockup di prodotto e dati di esempio provenienti dal template non devono
  raggiungere il sito pubblicato. Il contenuto segnaposto va marcato come dato, non
  segnalato con un commento.

> L'identità di marca è una **decisione di business**: un agente può produrre
> proposte e applicare in modo coerente una scelta fatta, non deciderla.

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
- Gestione delle proprie chiavi API Postqron.

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
- Vulnerabilità note delle dipendenze Go controllate a ogni push (`govulncheck`
  nella CI locale): bloccano solo se il nostro codice le raggiunge, e quando il
  database delle vulnerabilità non è raggiungibile la CI lo dichiara invece di
  passare in silenzio.

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
| Prezzo | €0 | €9/mese · €90/anno | €29/mese | da €79/mese |
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
| Multi-workspace | — | — | — | ✓ **fino a 10** isolati, fatturazione unificata |
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
  **Il piano Agency ne include fino a dieci** al prezzo d'ingresso; oltre, il prezzo è
  concordato — ed è questo che il «da €79» di §8 significa. Il numero non è arbitrario:
  è la variabile su cui il piano scala, e senza un tetto dichiarato ogni altro limite
  per workspace sarebbe aggirabile creando workspace.
- **R25-bis — Capacità dei trigger manuali su Agency.** Il budget dei trigger manuali
  è quello del piano Team, **applicato per workspace**. Non è un numero scelto: Agency
  non è «Team con più potenza», è «Team moltiplicato» — ciò che lo distingue sono i
  workspace isolati, non la portata di ciascuno. Un workspace Agency serve un cliente
  finale con gli stessi job di un cliente Team, quindi non ha ragione di eseguire di
  più. La capacità totale dell'account scala col numero di workspace, cioè con ciò per
  cui l'agenzia paga.
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

Postqron esegue **esclusivamente chiamate HTTP** verso endpoint dell'utente. Non
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

---

## 11. Affidabilità e impegni di servizio

Il prodotto vende affidabilità: ciò che promette dev'essere misurabile e mantenuto.

- **R47 — Precisione del dispatch.** I piani vendono risoluzioni di 1 minuto, 10
  secondi e 1 secondo (§8). Va dichiarata la **tolleranza** con cui un'occorrenza
  viene dispatchata rispetto all'orario dovuto, e misurata in esercizio. Senza un
  numero, «risoluzione 1 secondo» è una frase di marketing.
- **R48 — Backup e ripristino provato.** Backup periodici **e una procedura di
  ripristino effettivamente eseguita**, non solo documentata, con tempo di recupero
  e perdita massima di dati dichiarati. Un backup mai ripristinato non è un backup.
- **R49 — Rischio dichiarato.** API, motore e database stanno sulla stessa VPS
  (§2): è una scelta deliberata per la latenza, e comporta che il guasto di quella
  macchina fermi il servizio. Il rischio è accettato e **va dichiarato**: nessun
  impegno di servizio può promettere più di quanto una macchina sola garantisca.
- **R50 — Comunicazione degli incidenti.** Quando il servizio è degradato, gli
  utenti devono poterlo sapere da un canale che non dipende dal servizio stesso.

---

## 12. API pubblica

Postqron è un prodotto *developer-first*: l'API non è un dettaglio realizzativo, è
parte dell'offerta.

- **R51 — Contratto documentato.** L'API pubblica ha una specifica OpenAPI
  versionata insieme al codice, che descrive ciò che il servizio fa davvero.
- **R52 — Versionamento e deprecazione.** Regola esplicita su come l'API evolve e
  con quale preavviso una funzionalità viene ritirata.
- **R53 — Semantica prevedibile.** Idempotenza sulle operazioni di scrittura,
  errori con codici stabili adatti al branching applicativo, paginazione coerente.

---

## 13. Qualità dell'interfaccia

- **R53-bis — Prestazioni e reperibilità del sito pubblico.** Il sito è la prima
  cosa che un potenziale cliente vede, ed è statico: non ha scuse per essere lento.

  **Obiettivo misurabile: almeno 95/100 in tutte e quattro le categorie Lighthouse
  — prestazioni, accessibilità, buone pratiche, SEO — in modalità mobile.** La
  mobile è quella che vincola: la desktop passa quasi da sola e misurarla soltanto
  significherebbe darsi un obiettivo già raggiunto.

  La misura che conta è **sul sito pubblicato**, non sulla build locale: la
  compressione e la rete di distribuzione di Cloudflare cambiano il risultato in
  meglio, e una soglia tarata in locale sarebbe più severa del vero. Serve però anche
  un controllo in locale, perché una regressione va vista prima del deploy.

  Stato misurato il 2026-08-17, che dice dove sono i problemi:

  | | |
  |---|---|
  | `hero.jpg`, elemento LCP | 208 KB, nessun formato moderno |
  | Immagini in formato moderno | **zero** WebP o AVIF |
  | Immagini senza dimensioni dichiarate | 9 — spostamenti di contenuto |
  | JavaScript | 377 KB su 15 file, per un sito quasi tutto statico |
  | `robots.txt`, `sitemap.xml` | assenti |
  | Dati strutturati JSON-LD | assenti |

- **R53-ter — Reperibilità.** `robots.txt` e una `sitemap.xml` che dichiara tutte le
  rotte in tutte e cinque le lingue con i rispettivi `hreflang`; dati strutturati
  JSON-LD coerenti con ciò che la pagina dice davvero — organizzazione, prodotto,
  prezzi, FAQ. **I dati strutturati devono corrispondere al contenuto visibile:**
  dichiarare prezzi o valutazioni che la pagina non mostra è una violazione delle
  linee guida dei motori di ricerca, non un'ottimizzazione.
- **R54 — Accessibilità.** L'interfaccia è conforme a **WCAG 2.2 livello AA**:
  contrasto, navigazione da tastiera, focus visibile, ruoli e nomi accessibili,
  rispetto della riduzione del movimento. È anche materia normata in UE.
- **R55 — Percorso al primo job.** Dalla registrazione al primo cronjob eseguito con
  successo: il percorso va progettato, non lasciato emergere. È la metrica che
  decide se un utente resta.
- **R56 — Stati vuoti e di errore.** Ogni vista dichiara cosa mostra senza dati, in
  caricamento, e quando la richiesta fallisce.
- **R57 — Notifiche in prodotto.** Gli eventi rilevanti sono visibili
  nell'applicazione, non solo via email — il cui recapito non è osservabile (R20.1).

---

## 14. Fatturazione: comportamenti dichiarati

- **R58 — Downgrade: si ferma tutto, riattiva l'utente.** Quando i job attivi
  superano il tetto del piano di destinazione, **vengono sospesi tutti**, e l'utente
  ne riattiva quanti ne consente il nuovo piano.

  Non scegliamo noi quali salvare, e la ragione è che **non possiamo saperlo**: due
  job identici per schedulazione e destinazione possono valere uno la fatturazione
  mensile e l'altro un promemoria. Qualunque criterio automatico — i più recenti, i
  più frequenti, i primi creati — sarebbe una supposizione presentata come regola, e
  sbaglierebbe in silenzio proprio nel caso che conta.

  Fermare tutto è più brusco ma è **onesto**: dice all'utente che la scelta è sua e
  gliela mette davanti, invece di fargliela scoprire quando il job che serviva non è
  partito.

  Tre conseguenze da rispettare:
  - **Se i job attivi rientrano già nel nuovo tetto, non si tocca niente.** Fermare
    tutto quando non serve sarebbe un danno gratuito.
  - **Nulla viene cancellato.** I job sospesi restano visibili, modificabili ed
    esportabili, con il loro storico di esecuzioni.
  - **La risoluzione è un secondo vincolo, indipendente dal numero.** Un job
    `every: 1s` non è riattivabile su un piano che si ferma al minuto, nemmeno se c'è
    posto: va prima cambiata la schedulazione. L'interfaccia deve dirlo, non
    limitarsi a rifiutare.

  Lo stesso comportamento vale per il **mancato pagamento** e per la **scadenza**
  dell'abbonamento, che portano al piano Free. Nessuna cancellazione silenziosa di
  lavoro dell'utente, in nessuno dei tre casi.
- **R59 — Nessuna prova gratuita.** Il listino (§8) non prevede periodi di prova: il
  piano Free è l'ingresso. Ogni affermazione contraria nell'interfaccia è un difetto.
- **R60 — Accesso ai documenti fiscali.** L'utente accede a fatture e ricevute
  emesse da Paddle in quanto Merchant of Record.
- **R61-bis — I prezzi sono al netto delle imposte.** Gli importi di §8 sono
  **imposte escluse**: Paddle calcola e aggiunge l'imposta dovuta nel paese del
  cliente in quanto Merchant of Record. Il cliente italiano paga €9 + 22%, non €9.

  **La formula è «imposte escluse», non «+ IVA».** L'IVA è una imposta specifica, e
  Paddle applica anche sales tax e GST fuori dall'Unione Europea: «+ IVA» sarebbe
  inesatto per una parte dei clienti, e inesatto sul prezzo è il posto peggiore dove
  esserlo. La forma generale è corretta ovunque.

  **Il sito espone il netto con l'indicazione accanto al prezzo**, in ogni punto in cui
  compare una cifra. Un «€9/mese» privo di indicazione è un difetto.

  L'indicazione è **testo tradotto** e segue le regole di §8-bis. Si traduce il
  *concetto*, con la convenzione commerciale di ciascuna lingua — non si sostituisce
  il nome dell'imposta locale:

  | | |
  |---|---|
  | `en` | excluding tax |
  | `it` | imposte escluse |
  | `es` | impuestos excluidos |
  | `de` | zzgl. Steuern |
  | `fr` | hors taxes |
- **R63 — Postqron è offerto per uso professionale.** Questa non è una preferenza
  commerciale, è **il presupposto che rende legittima l'esposizione del netto**. Verso i
  consumatori l'Unione Europea richiede che il prezzo esposto sia quello finale,
  comprensivo di imposta; verso le imprese il netto è corretto ed è il numero utile,
  perché è quello che l'acquirente detrae.
  **Il vincolo sta all'acquisto, non alla registrazione.** Il piano Free è aperto a
  chiunque e non è un acquisto: nessun prezzo pagato, nessuna cifra da esporre.
  Chiedere lo status professionale per aprire un account gratuito sarebbe attrito senza
  contropartita. La difesa serve dove nasce il problema — al checkout:
  - i Termini dichiarano che **i piani a pagamento** sono offerti a professionisti e
    organizzazioni;
  - il checkout chiede **conferma esplicita di agire nell'esercizio di un'attività**,
    non una casella preselezionata sepolta nel modulo;
  - raccoglie i dati di fatturazione e la **partita IVA dove esiste**, con inversione
    contabile applicata da Paddle dove dovuta.

  La partita IVA **non va resa obbligatoria**: diversi regimi minimi europei ne sono
  privi — i *Kleinunternehmer* tedeschi, per esempio — e pretenderla escluderebbe
  acquirenti legittimi. Si conferma sempre lo status, si raccoglie il numero quando c'è.

  Dichiarare l'uso professionale e poi lasciar acquistare chiunque senza chiederlo è
  la posizione peggiore delle due: si perde la difesa senza guadagnare il mercato.
- **R61 — Valuta unica in euro.** I prezzi sono in **euro** e **non seguono la
  lingua**: le cinque localizzazioni (§8-bis) mostrano gli stessi importi. La
  conversione e la presentazione in valuta locale, dove avvengono, sono competenza di
  Paddle in quanto Merchant of Record — non nostra. Il **catalogo Paddle è la fonte
  di verità**: qualunque cifra diversa nell'interfaccia è un difetto, e ogni simbolo
  `$` è un residuo da correggere.
  I prezzi non inseguono il cambio: sono punti scelti, non conversioni ricalcolate.
  L'annuale di Pro è esattamente **dieci mensilità**, cioè due mesi in regalo — una
  promessa leggibile dal cliente che gli arrotondamenti futuri non devono rompere.
- **R62 — Fatturazione annuale solo su Pro.** È una scelta deliberata, non una
  lacuna: Team e Agency sono esclusivamente mensili.
