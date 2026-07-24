# F9 — Stati e notifiche

Questa slice autonoma mantiene una proiezione client-safe dello stato di ogni
post e destinazione e orchestra le email transazionali tramite F14.

## Stato delle pubblicazioni

La vista espone `draft`, `scheduled`, `publishing`, `published`, `failed` e
`cancelled`, con stato, ID remoto e diagnostica per destinazione. Gli eventi F8
`pending`, `publishing`, `retry_wait`, `published`, `dead_letter` e `cancelled`
sono mappati sugli stati di prodotto.

Il trasporto aggiunge `event_id` e `workspace_id` al payload F8. Quando
`event_id` non è disponibile, la slice calcola un fingerprint stabile
dell'intero payload. Il ledger rifiuta il riuso dello stesso ID con contenuto
diverso. Timestamp, revisione del lifecycle e precedenza degli stati rendono
innocui duplicati, eventi ritardati e consegne fuori ordine. `published` e
`cancelled` sono terminali; `failed` può tornare a `publishing` dopo un retry.
Il lifecycle F6/F7 accetta soltanto `draft`, `scheduled` e `cancelled`; gli
stati di esecuzione arrivano esclusivamente dal contratto F8.

La diagnostica grezza del provider non viene conservata. Codici noti diventano
messaggi utili in italiano; email, bearer token, password, secret, API key e
parametri sensibili vengono redatti, e il testo è limitato a 320 caratteri.

## Notifiche F14

Gli eventi `welcome`, `plan_changed` e `security_alert` entrano nell'outbox F9.
Un `dead_letter` F8 crea automaticamente `publication_failed`. Il resolver
recupera il destinatario al momento dell'invio, così F9 non persiste indirizzi
email. Ogni comando usa il contratto F14, canale `transactional`, template
versione `1.0.0` e una idempotency key deterministica.

Il claim dell'outbox è atomico e ha una lease recuperabile. Se il processo cade
dopo l'accodamento in F14, la riconsegna usa la stessa chiave e F14 restituisce
la consegna esistente senza creare un duplicato.

## Retry manuale

L'API accetta una idempotency key e registra un comando outbox soltanto se la
destinazione è ancora fallita. Oltre alla chiave del client, un vincolo unico
su `(destination_id, failure_event_id)` impedisce due richieste diverse per lo
stesso ciclo di errore. F8 conserva la propria idempotency key di pubblicazione,
quindi anche il replay del comando non può creare una seconda pubblicazione.
L'adapter considera riuscito anche un replay che trova lo stesso ciclo già
riaccodato da F8, chiudendo la finestra tra chiamata e conferma dell'outbox.

## Discovery e integrazioni

F16 scopre `feature.yaml`; non serve un registro centrale. L'host inietta:

- `Authorizer` dal boundary workspace;
- `RecipientResolver` dai profili/workspace;
- `EmailGateway` che adatta `EmailCommand` al comando F14;
- `RetryGateway` che invoca il retry autorizzato F8.

La migrazione crea la proiezione, il ledger eventi e le due code persistenti.
Le query di produzione devono applicare claim `FOR UPDATE SKIP LOCKED` e le
transizioni nello stesso modo definito da `Repository`.

## Verifica

```sh
cd features/f09-status-notifications
GOWORK=off go test -race ./...
GOWORK=off go vet ./...
```
