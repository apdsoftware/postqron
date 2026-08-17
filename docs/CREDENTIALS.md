# Credenziali — procedura di configurazione

Cosa serve, dove ottenerlo e dove metterlo. **Nessuna credenziale reale entra mai nel
repository:** i file `.env` sono ignorati da git, `.env.example` contiene solo
segnaposto.

Le voci marcate 🔴 bloccano issue già aperte.

---

## Regole valide per tutti i servizi

1. **Due set separati**, staging e produzione. Non riutilizzare mai le chiavi di
   produzione in locale: un test che parte per sbaglio in produzione fattura clienti
   veri o invia email vere.
2. **Permessi minimi.** Ogni token riceve solo gli scope che servono, elencati qui
   sotto per ciascun servizio.
3. **Dove finiscono:**
   - in locale → `.env` nella radice del repository (già ignorato da git);
   - in produzione → variabili d'ambiente sulla VPS Hetzner, in un file leggibile
     solo da root (`chmod 600`), caricato dal servizio systemd.
4. **Come me le passi:** *non* incollarle in chat, in una issue o in un commit. Mettile
   direttamente nel tuo `.env` locale e dimmi solo "fatto": gli agenti leggono il file,
   non hanno bisogno che il valore transiti da me. Per la produzione, inseriscile a mano
   sulla VPS.
5. **Se una chiave finisce per sbaglio in un commit**, va **revocata e rigenerata**, non
   solo rimossa: resta nello storico e nel bundle di backup.

---

## 1. Paddle 🔴 — blocca #417, #403, #415

Merchant of Record: checkout, sottoscrizioni, MRR.

1. Crea l'account su [paddle.com](https://www.paddle.com) e completa la verifica del
   venditore (richiede dati fiscali dell'azienda; **può richiedere giorni**, è il
   percorso più lungo — conviene iniziare da qui).
2. Usa **Paddle Billing**, non Paddle Classic: le API divergono e la documentazione di
   Classic non si applica.
3. Attiva prima l'ambiente **Sandbox** (`sandbox-vendors.paddle.com`): lo sviluppo si fa
   lì, la produzione arriva dopo.
4. Nel dashboard, sezione sviluppatori → **API keys**: genera una chiave server-side.
5. Sempre lì → **Client-side tokens**: genera il token per il checkout nel browser.
6. Sezione **Notifications / Webhooks**: crea un endpoint verso
   `https://api.postqron.com/webhooks/paddle`, sottoscrivi gli eventi di sottoscrizione
   e transazione, e copia il **signing secret**.
7. **Catalogo prodotti:** crea i tre piani a pagamento con i prezzi di SPEC §8 — Pro
   $12/mese e $120/anno, Team $39/mese, Agency da $99/mese. Annota i `price_id` di
   ciascuno: il codice referenzia quelli, non i prezzi.

```
PADDLE_ENVIRONMENT=sandbox          # poi "production"
PADDLE_API_KEY=
PADDLE_CLIENT_TOKEN=
PADDLE_WEBHOOK_SECRET=
PADDLE_PRICE_PRO_MONTHLY=
PADDLE_PRICE_PRO_YEARLY=
PADDLE_PRICE_TEAM_MONTHLY=
PADDLE_PRICE_AGENCY_MONTHLY=
```

---

## 2. Mailronix 🔴 — blocca #419, #420

Motore di recapito. L'HTML lo compiliamo noi (R20): a Mailronix serve solo inviare.

1. Account su [mailronix.com](https://mailronix.com).
2. **Verifica il dominio** `postqron.com`: richiede record DNS **SPF**, **DKIM** e
   idealmente **DMARC**. Vanno aggiunti su Cloudflare (§4) — quindi Cloudflare va
   configurato prima.
3. Genera una **API key** per l'invio.
4. Definisci il mittente: `noreply@postqron.com` per le transazionali.

```
MAILRONIX_API_KEY=
MAILRONIX_API_URL=
MAILRONIX_FROM_EMAIL=noreply@postqron.com
MAILRONIX_FROM_NAME=PostQron
```

> Confermami l'URL base dell'API e il nome esatto dell'header di autenticazione: non
> sono documentati pubblicamente e l'agente di #419 non può indovinarli.

---

## 3. Hetzner 🔴 — blocca #425, #426

VPS con API, motore cron e PostgreSQL sulla stessa macchina (SPEC §2).

1. Account su [console.hetzner.cloud](https://console.hetzner.cloud).
2. Crea un **progetto** dedicato `postqron`.
3. Nel progetto → **Security → API tokens** → nuovo token con permesso
   **Read & Write**. È mostrato una sola volta.
4. Carica la tua **chiave SSH pubblica** nel progetto: il provisioning non deve usare
   password.
5. Per R26 (IP statico del piano Agency) servirà un **Floating IP** o un IP primario
   dedicato: annotalo quando lo crei.

```
HCLOUD_TOKEN=
HETZNER_SSH_KEY_NAME=
```

---

## 4. Cloudflare 🔴 — blocca #426

DNS, TLS, CDN e hosting statico dei due frontend.

1. Aggiungi `postqron.com` su [dash.cloudflare.com](https://dash.cloudflare.com) e
   punta i nameserver del registrar su Cloudflare.
2. Copia **Account ID** e **Zone ID** dalla pagina di panoramica del dominio.
3. **My Profile → API Tokens → Create Token**, permessi minimi:
   - `Zone → DNS → Edit` sulla zona `postqron.com`
   - `Account → Cloudflare Pages → Edit`
4. Crea due progetti Pages: `postqron-web` (sito pubblico) e `postqron-dashboard`.

```
CLOUDFLARE_API_TOKEN=
CLOUDFLARE_ACCOUNT_ID=
CLOUDFLARE_ZONE_ID=
```

---

## 5. GitHub App — blocca #421 (sync `cron.yaml`)

Serve una **GitHub App**, non un personal access token: l'utente la installa sui propri
repository e concede solo i permessi dichiarati.

1. **Settings → Developer settings → GitHub Apps → New GitHub App**.
2. Nome `PostQron`, homepage `https://postqron.com`.
3. **Webhook URL** `https://api.postqron.com/webhooks/github`, e genera un **webhook
   secret** casuale robusto (serve per la verifica HMAC di R11).
4. **Permessi del repository:** `Contents: Read-only`, `Metadata: Read-only`. Nient'altro:
   ci serve leggere `cron.yaml`, non scrivere.
5. **Eventi sottoscritti:** solo `Push`.
6. Rendi l'App installabile su **qualsiasi account**.
7. Dopo la creazione annota **App ID** e **Client ID**, genera un **client secret** e
   una **private key** (scarica il `.pem`).

```
GITHUB_APP_ID=
GITHUB_APP_CLIENT_ID=
GITHUB_APP_CLIENT_SECRET=
GITHUB_APP_WEBHOOK_SECRET=
GITHUB_APP_PRIVATE_KEY_PATH=/etc/postqron/github-app.pem
```

---

## 6. Segreti generati da noi

Non vengono da terzi: li generi tu e non li perdi, perché senza non si decifrano i dati.

```bash
openssl rand -base64 32   # per ciascuno
```

```
ENCRYPTION_KEY=     # cifratura delle chiavi AI degli utenti (R18)
SESSION_SECRET=     # firma delle sessioni (R14)
```

> `ENCRYPTION_KEY` **non è rigenerabile**: perderla significa rendere illeggibili tutte
> le chiavi AI salvate. Conservala anche fuori dalla VPS, in un password manager.

---

## Ordine consigliato

Le dipendenze fra servizi impongono una sequenza:

1. **Paddle** — la verifica del venditore è lenta, avviala subito e lasciala lavorare.
2. **Cloudflare** — serve prima di Mailronix, perché SPF/DKIM sono record DNS.
3. **Mailronix** — verifica dominio e chiave API.
4. **Hetzner** — token e chiave SSH.
5. **GitHub App** — indipendente dagli altri, in qualsiasi momento.
6. **Segreti locali** — `openssl rand`, subito.

Man mano che completi un punto dimmi quale: sblocco le issue corrispondenti.
