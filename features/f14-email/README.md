# F14 — Email transazionali Mailronix

Questa slice è l’unico confine autorizzato per le email transazionali originate
da Postqron. Le altre feature pubblicano il comando versionato
`contracts/email-command.schema.json`; non chiamano provider, SMTP o API email.
Il canale accettato è esclusivamente `transactional`: campagne e marketing sono
fuori da F14, mentre ricevute fiscali e di pagamento restano responsabilità di
Paddle.

## Contratto verificato e limiti

Il contratto ufficiale Mailronix verificato è documentato in
`contracts/mailronix-api-1.0.0.md`. L’adapter implementa esattamente
`POST /email/send` con contenuto diretto, Bearer API key, risposta 202
`queued/email_log_id` e gli errori pubblicati.

La specifica ufficiale non espone webhook, SMTP, sandbox remoto, Reply-To,
header di idempotenza, limiti numerici o stato di consegna. Queste capacità non
sono simulate come se fossero provider features: il documento del contratto
elenca i blocchi da chiudere con Mailronix prima del go-live. In particolare,
`help@postqron.com` non può ancora essere impostato come Reply-To senza un campo
ufficialmente supportato.

## Configurazione e segreti

`MailronixConfig` contiene solo configurazione non segreta:

- endpoint HTTPS con path obbligatorio `/email/send`;
- versione contratto obbligatoria `1.0.0`;
- nome del segreto della API key, consigliato
  `MAILRONIX_TRANSACTIONAL_API_KEY`;
- mittente transazionale;
- conferma di verifica dominio;
- soglia e cooldown del circuit breaker.

La API key viene risolta a runtime tramite `SecretProvider`, non entra in
frontend, repository, fixture o log. Un client HTTP con timeout esplicito è
obbligatorio. La costruzione live fallisce se la versione del contratto non
coincide, il dominio non è dichiarato verificato o la configurazione è
incompleta.

## Boundary pubblico

Il wiring runtime deve dipendere dall’API pubblica provider-neutral di F14:

```go
boundary, err := email.NewSenderBoundaryFromEnv(
    email.SenderBoundaryOptions{
        Environment: runtimeEnvironment,
        Production:  isProduction,
    },
    secretProvider,
)
```

Oppure, se serve solo l’interfaccia di invio:

```go
sender, err := email.NewSenderFromEnv(
    email.SenderBoundaryOptions{
        Environment: runtimeEnvironment,
        Production:  isProduction,
    },
    secretProvider,
)
```

`services/api` e `services/worker` non devono importare, nominare o
istanziare `MailronixClient`. Il boundary F14 legge esattamente queste variabili
d’ambiente già previste dal wiring `#220`:

- `POSTQRON_MAILRONIX_ENDPOINT`
- `POSTQRON_MAILRONIX_API_KEY_SECRET_NAME`
- `POSTQRON_MAILRONIX_SENDER_EMAIL`
- `POSTQRON_MAILRONIX_DOMAIN_VERIFIED`
- `POSTQRON_MAILRONIX_FAILURE_THRESHOLD`
- `POSTQRON_MAILRONIX_CIRCUIT_OPEN_FOR`

Il valore del segreto resta esterno al repository e viene risolto solo tramite
`SecretProvider`, usando il nome dichiarato in
`POSTQRON_MAILRONIX_API_KEY_SECRET_NAME`.

Il segnale di produzione non viene letto da una variabile globale implicita:
viene passato esplicitamente tramite `SenderBoundaryOptions{Production,
Environment}` così il comportamento resta testabile e fail-closed.
Se `#220` usa soltanto `NewSenderBoundaryFromEnv` o `NewSenderFromEnv`,
`TestNoEmailProviderClientExistsOutsideF14` può restare verde perché il
provider non viene nominato fuori da F14.

## Localizzazione e replay

Ogni template include oggetto, preheader, HTML responsive e plain text in
`en`, `it`, `es`, `fr` e `de`. La preferenza del destinatario accetta anche
tag regionali (`it-IT`, `de-DE`) e ricade su `en` per valori assenti o non
supportati. Locale e versione template vengono salvate nel messaggio renderizzato
prima dell’invio: retry e replay non possono cambiare la copia originale.

Date/orari, interi e valute vengono formattati per locale senza modificare il
valore sottostante. I link HTTPS restano identici; cambia solo l’etichetta
localizzata. Il layout usa markup semantico, alternative plain text, target
touch da 44 px e una regola mobile che regge testi lunghi in francese e tedesco.

## Verifica account

F3 produce il comando versionato `f14.account_verification_requested.v1` con
`template_id=account_verification` e idempotency key
`account-verification:{account_id}:{verification_request_id}`. Per questo
template `data.action_url` è obbligatorio e deve essere un URL HTTPS assoluto:
il token monouso può comparire solo in quel link di consegna.

Il modello F14 non introduce colonne dedicate al token, non lo replica in
diagnostica o metriche e non richiede segreti nel repository. La copia
renderizzata necessaria a invio/retry resta quella già prevista per replay
immutabile; ogni diagnostica operativa passa invece da redazione, includendo
indirizzi email, bearer credential e URL di verifica tokenizzati. Il wiring
runtime che fa emettere questo comando da F3 resta fuori scope e va chiuso in
`#220`.

## Matrice transazionale

`TransactionalEventMatrix` è il catalogo eseguibile con evento, produttore,
destinatario, template, priorità, origine locale, idempotency key e
responsabilità di invio. Copre:

- welcome/onboarding e inviti workspace;
- verifica account/password sign-in prodotta da F3;
- sicurezza, collegamento account e cambiamenti sensibili;
- social scaduti/da riconnettere;
- approvazioni e collaborazione;
- pubblicazione riuscita, fallita e retry manuale;
- pagamento fallito, piano, cancellazione e grace period;
- export, cancellazione e richieste privacy;
- richiesta accesso/pre-lancio;
- alert amministrativi/operativi rivolti a un utente.

Gli eventi di billing dichiarano esplicitamente che Paddle possiede la ricevuta
fiscale; Mailronix invia soltanto la notifica Postqron e non ne duplica il
contenuto.

## Affidabilità e ambiente fake

L’idempotency key è unica nel ledger F14 e il claim SQL usa
`FOR UPDATE SKIP LOCKED`. Errori 429/500/503, indisponibilità del secret store,
timeout e trasporto sono riprovabili con backoff limitato; gli errori 4xx
documentati sono permanenti. Il circuit breaker interrompe temporaneamente gli
invii dopo errori transitori consecutivi. Diagnostica, indirizzi e credenziali
sono redatti e il corpo completo non viene scritto nei log.

`FakeSender` è l’unica modalità di sviluppo/CI: conserva i messaggi in memoria e
rifiuta ogni destinatario che non appartenga ai domini riservati
`example.test`/`example.invalid`, impedendo invii reali accidentali.

Il boundary pubblico usa queste regole:

- `Production=true` richiede `Mode=live`; `fake` e `noop` sono rifiutati.
- senza `Mode` esplicito, `fake` è il default solo per `local`,
  `development`, `test` e `ci`;
- ambienti non di produzione ma non locali, come `staging`, richiedono una
  scelta esplicita del mode;
- `noop` è disponibile solo fuori produzione.

## Checklist go-live

Questi controlli richiedono accesso all’account Mailronix e non possono essere
certificati dal repository:

1. verificare il dominio mittente in console e acquisire i record ufficiali;
2. controllare SPF, DKIM e DMARC con esito positivo;
3. inviare le preview Mailronix a due provider destinatari;
4. verificare rendering nei client principali, VoiceOver, zoom e viewport
   320 px;
5. ottenere il contratto ufficiale per Reply-To, webhook/status delivery,
   idempotenza provider e test mode, oppure registrare la loro indisponibilità
   come rischio approvato.

## Test

```sh
cd features/f14-email
GOWORK=off go test -race ./...
```
