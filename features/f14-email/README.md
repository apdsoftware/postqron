# F14 — Email transazionali Mailrox

Questa slice worker autonoma implementa l'invio email di Postqron:

- template HTML responsive e semantici, sempre accompagnati da testo semplice;
- catalogo transazionale separato dal solo template marketing;
- brand, palette, font e logo risolti dalla feature F1, senza valori duplicati;
- adapter HTTP Mailrox con idempotency key e ricevuta del provider;
- credenziali transazionali e marketing distinte, lette da un secret provider;
- retry esponenziali limitati e classificazione degli errori permanenti;
- webhook firmati, eventi provider idempotenti, bounce e complaint;
- unsubscribe marketing cifrato/autenticato e suppression list atomica;
- diagnostica limitata e redatta da indirizzi email e credenziali.

## Discovery F16

`feature.yaml` dichiara la dipendenza da `brand`. Il worker include F1 e F14 tra
le root scoperte; non serve un registro centrale:

```sh
POSTQRON_FEATURE_ROOTS="services/worker/features:features/f01-brand:features/f14-email"
```

Per validare manifest e migrazione con il runtime F16:

```sh
go run ./services/api/cmd/migrate \
  --check \
  --roots "features/f01-brand:features/f14-email"
```

La migrazione crea la coda persistente, il ledger degli eventi provider e le
soppressioni. `f14_claim_email_delivery` usa `FOR UPDATE SKIP LOCKED`;
`f14_record_email_provider_event` applica stato e soppressione nella stessa
transazione.

## Brand e template

Il processo host passa a `LoadBrandFromF1`:

1. `features/f01-brand/tokens/tokens.json`;
2. il nome scoperto da `features/f01-brand/runtime.ts`;
3. l'URL HTTPS pubblicato di `logo-primary.svg`.

F14 non contiene palette, font, logo o fallback di brand. Se un token F1
richiesto manca, il renderer fallisce all'avvio.

I template transazionali sono:

- `welcome`;
- `plan_changed`;
- `publication_failed`;
- `security_alert`.

`marketing_update` è l'unico template marketing. Un template non può cambiare
canale e i messaggi transazionali rifiutano campi di unsubscribe, così non
possono incorporare promozioni per errore. Ogni messaggio marketing richiede
invece un URL HTTPS di cancellazione e produce sia il link nel corpo sia gli
header `List-Unsubscribe` e `List-Unsubscribe-Post`.

Prima del rilascio, oltre ai test automatici, verificare i template con
VoiceOver, zoom del testo, una viewport da 320 px e i client email supportati.

## Configurazione Mailrox e segreti

L'endpoint Mailrox è configurazione di deploy e deve usare HTTPS. I valori
sensibili non fanno parte di `MailroxConfig`: la configurazione contiene
soltanto i nomi da risolvere tramite `SecretProvider`.

Nomi consigliati:

```text
MAILROX_TRANSACTIONAL_API_KEY
MAILROX_MARKETING_API_KEY
MAILROX_WEBHOOK_SECRET
F14_UNSUBSCRIBE_SECRET
```

Le due API key devono avere nomi distinti; l'adapter seleziona chiave e mittente
in base al canale. Staging e produzione devono collegare `SecretProvider` al
secret manager esterno previsto da F15. Il provider a mappa è adatto soltanto a
test e sviluppo locale.

Il contratto HTTP di invio usa JSON, autenticazione Bearer, header
`Idempotency-Key` e richiede un `message_id` nella risposta. L'endpoint e la
versione Mailrox approvati per l'ambiente vengono fissati nella configurazione
di deploy, non nel repository.

## Idempotenza, retry e diagnostica

L'idempotency key è unica per canale nel database e viene inoltrata a Mailrox.
Il claim atomico impedisce a due worker di inviare lo stesso record. Errori di
trasporto, timeout, `425`, `429` e `5xx` sono riprovabili; `Retry-After` viene
rispettato entro il limite della policy. Gli altri `4xx` falliscono senza
retry. Raggiunto `max_attempts`, il messaggio passa a `failed`.

La diagnostica conserva solo codice, testo sicuro, retryability e timestamp.
Email, bearer token e parametri chiamati token/secret/password/api-key vengono
redatti; il corpo grezzo del provider non viene registrato.

## Bounce, complaint e unsubscribe

Il webhook accetta solo `POST` firmati con HMAC SHA-256, timestamp entro la
finestra configurata e payload conforme a
`contracts/mailrox-event.schema.json`. L'ID dell'evento rende i replay innocui.
Hard bounce e complaint sopprimono tutti gli invii al destinatario; un soft
bounce aggiorna invece l'esito senza creare una soppressione permanente.

L'unsubscribe usa un token opaco cifrato e autenticato con AES-GCM: l'URL non
espone email o recipient ID.
L'applicazione idempotente crea una soppressione `marketing`; le comunicazioni
strettamente transazionali restano abilitate. Una soppressione `all` non può
essere ridotta da un unsubscribe successivo.

## Test

```sh
cd features/f14-email
GOWORK=off go test -race ./...
```
