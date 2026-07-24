# F23 — PWA e notifiche push

Slice autonoma per l’esperienza installabile di Postqron e le notifiche Web
Push per errori di pubblicazione e revisioni editoriali.

## Cosa include

- manifest installabile, icone PNG `192x192`, `512x512` e maskable;
- shell offline pubblica e service worker network-first per le navigazioni;
- client browser per installazione, opt-in esplicito e revoca;
- API autenticata per registrare o revocare il dispositivo corrente;
- fan-out per più dispositivi, ledger evento e outbox per dispositivo;
- Web Push con VAPID, retry esponenziale e disattivazione su `404`/`410`;
- cifratura AES-256-GCM di endpoint e chiavi browser nel repository PostgreSQL;
- adapter evento per errori F9 e richieste/esiti di approvazione F17.

Il service worker precache-a esclusivamente `offline.html` e le icone pubbliche.
Non scrive mai in Cache Storage pagine visitate, risposte API, richieste di
autenticazione o contenuti dell’account.

## Integrazione tramite discovery

`feature.yaml` dichiara le dipendenze `status-notifications` e
`f17-collaboration`; non è richiesto alcun registro centrale. L’adapter web
scoperto deve esporre gli asset feature-owned con queste URL:

| File nella slice | URL pubblica |
| --- | --- |
| `web/manifest.webmanifest` | `/manifest.webmanifest` |
| `web/service-worker.js` | `/service-worker.js` |
| `web/offline.html` | `/pwa/offline.html` |
| `web/icon-192.png` | `/pwa/icon-192.png` |
| `web/icon-512.png` | `/pwa/icon-512.png` |
| `web/icon-maskable-512.png` | `/pwa/icon-maskable-512.png` |

Il service worker è servito dalla root per poter controllare lo scope `/`
senza ampliare lo scope tramite header speciali. La pagina applicativa collega
il manifest, imposta `theme-color` e istanzia `PostqronPWA` da
`web/pwa-client.mjs`. `enablePush` deve essere invocato esclusivamente dal click
su un controllo di opt-in; nessun permesso è richiesto durante il caricamento.

Il backend costruisce `PostgresRepository` con una chiave a 32 byte proveniente
dal secret store e `WebPushGateway` con subject e coppia VAPID provenienti
dall’ambiente. Chiavi VAPID, chiavi di cifratura, endpoint e token non vanno
salvati nel repository o nei log.

Gli adapter F9/F17 risolvono lato server i destinatari autorizzati prima di
chiamare `ConsumeStatusEvent` o `ConsumeCollaborationEvent`. Le push contengono
solo testo client-safe e URL relative same-origin; commenti, contenuti dei post
ed errori raw del provider non vengono accettati.

## Verifica

```sh
GOWORK=off go test -race ./...
node --test test/web.test.mjs
GOWORK=off go vet ./...
```

Dal root del repository, il controllo completo della discovery e delle
migration feature-owned è:

```sh
go run ./services/api/cmd/migrate --check \
  --roots "services/api/features:features"
```
