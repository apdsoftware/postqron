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

   Gli eventi che cambiano il piano sono i `subscription.*`, e li trattiamo **tutti**
   allo stesso modo: ognuno porta lo stato corrente della sottoscrizione, quindi
   applichiamo «com'è adesso» e non «cosa è successo». Gli eventi di transazione
   vanno sottoscritti lo stesso — servono a capire cosa è arrivato quando un piano
   non cambia — ma **non** producono entitlement: un pagamento fallito non degrada
   l'account, perché i Termini §4.2 promettono che durante i tentativi di Paddle il
   servizio continua.

   Senza questo segreto la rotta `/webhooks/paddle` **non viene registrata**: un
   endpoint di fatturazione che accetta corpi non verificati è un modo per farsi
   regalare un piano a pagamento da chiunque ne conosca l'indirizzo.
7. **Catalogo prodotti:** crea i tre piani a pagamento con i prezzi di SPEC §8 — Pro
   €9/mese e €90/anno, Team €29/mese, Agency da €79/mese. Annota i `price_id` di
   ciascuno: il codice referenzia quelli, non i prezzi.

   I prezzi vanno inseriti **in euro e al netto delle imposte** (R61, R61-bis):
   Paddle calcola e aggiunge l'imposta dovuta nel paese del cliente. Un solo
   `price_id` per riga di listino, e mai lo stesso su due piani — l'avvio si
   rifiuta, perché il webhook tornando indietro dal prezzo assegnerebbe un piano a
   caso.

   **L'annuale esiste solo su Pro** (R62): non creare prezzi annuali per Team e
   Agency, perché non c'è una variabile in cui metterli e nessuno li venderebbe.

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
MAILRONIX_API_KEY=          # formato mrx_live_<segreto>
MAILRONIX_API_URL=https://api.mailronix.com
MAILRONIX_FROM_EMAIL=noreply@postqron.com
MAILRONIX_FROM_NAME=Postqron
```

### Contratto dell'API

Specifica completa in [`docs/reference/mailronix-openapi.json`](reference/mailronix-openapi.json).

**Esiste un solo endpoint** raggiungibile con API key: `POST /email/send`. Tutto il
resto della console richiede una sessione browser.

```
POST https://api.mailronix.com/email/send
Authorization: Bearer mrx_live_<segreto>
Content-Type: application/json

{"from": "noreply@postqron.com", "to": "utente@example.com",
 "subject": "…", "html_body": "<html>…</html>"}
```

Risposta `202`: `{"status": "queued", "email_log_id": "<uuid>"}`.

**Usiamo la modalità a contenuto diretto**, non i template Mailronix: l'HTML lo compila
il backend Go (R20) e Mailronix resta solo motore di recapito. `template_id` è
mutuamente esclusivo con `subject`/`html_body`/`text_body`.

### Quattro vincoli che il client deve rispettare

1. **`202` non significa recapitato.** La risposta è identica anche se il destinatario
   è in suppression list per bounce o reclami precedenti: è deliberato, per non offrire
   un modo di verificare l'esistenza di indirizzi altrui. Il recapito non è osservabile
   dalla risposta — registra `email_log_id` e non dedurne il successo.
2. **Un solo destinatario per chiamata.** `to` è una stringa, non un array.
3. **Solo alcuni errori sono ritentabili.** `429 rate_limited` (il limite è per chiave,
   non per IP) e `503 auth_unavailable` sono transitori. `400`, `403` e `404` no:
   ritentarli consuma quota senza cambiare esito.
4. **Il dominio del mittente dev'essere verificato**, altrimenti `403
   domain_not_verified`. Per `postqron.com` la verifica è **completa e collaudata**:
   SPF e DKIM sono già nella zona Cloudflare e un invio di prova ha restituito `202`
   (2026-08-17).
5. **`api.mailronix.com` sta dietro la protezione bot di Cloudflare.** Un client che
   non imposta uno `User-Agent` esplicito riceve **`403` con corpo `error code: 1010`**
   — una pagina di blocco Cloudflare, non un errore Mailronix. Il tranello è che
   somiglia a un problema di autenticazione o di dominio non verificato, mentre la
   richiesta non ha mai raggiunto Mailronix: il corpo non è il JSON `{"error":{...}}`
   documentato. Il client Go deve impostare uno `User-Agent` proprio (per esempio
   `Postqron/1.0 (+https://postqron.com)`) e trattare una risposta non-JSON come
   errore di trasporto, non come errore applicativo.

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
2. Nome `Postqron`, homepage `https://postqron.com`.
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
