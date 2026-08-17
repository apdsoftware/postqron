# emails/templates

Template HTML delle email transazionali (SPEC R19–R21).

## Perché stanno nel repository

Mailronix è **esclusivamente un motore di recapito**: non ospita template, non
esegue sostituzioni, non conosce la logica di prodotto. Il backend Go compila
l'HTML a partire da questi file e invia a Mailronix il **payload completo**
(R20). Di conseguenza i template sono codice: versionati, revisionati in PR e
coperti da test di rendering.

Il renderer è `services/api/internal/emailrender`. L'invio no: quello è il
client Mailronix della issue #419, che riceve da qui un messaggio già fatto.

## Come sono organizzati

```
layout.html.tmpl   layout.txt.tmpl     cornice condivisa da tutte le email
<evento>.html.tmpl <evento>.txt.tmpl   corpo e oggetto di un evento
locales/<lingua>.json                  i testi, una lingua per file
```

Il layout definisce `layout`, più i frammenti `button` e `detail`. Ogni evento
definisce `preheader` e `content` nel file HTML, `subject` e `content` in quello
testuale. L'oggetto sta nel file testuale perché è testo semplice: non ha senso
che viva in un file di markup.

I template **non contengono testo**: solo struttura e riferimenti a chiavi di
traduzione. È un vincolo di SPEC §8-bis — «nessuna stringa nei componenti» — e
un test lo verifica togliendo tag e azioni e controllando che non resti nemmeno
una lettera. Cinque lingue moltiplicate per nove file di markup sarebbero
quarantacinque file da tenere allineati; così restano nove.

## Convenzioni

- Un file `.html.tmpl` e uno `.txt.tmpl` per evento, con nome in inglese e in
  snake_case, uguale al valore di `emailrender.Event`.
- Sintassi `text/template` della standard library Go. L'HTML passa da
  `html/template`, che neutralizza da sé i dati interpolati.
- La variante testuale non è un ripiego: accompagna sempre l'HTML nel payload,
  perché è quella che leggono i client che rifiutano l'HTML ed è quella che
  evita ai filtri antispam un messaggio solo-HTML.
- **Nessun segreto e nessun dato personale nei template**: i valori arrivano
  sempre dal contesto passato dal renderer, e nessuna struttura del renderer ha
  un campo per una chiave o un token — c'è un test che lo verifica per nome.

## HTML per client di posta, non per browser

I client di posta hanno vent'anni di ritardo per scelta altrui, e Outlook per
Windows rende l'HTML con il motore di Word. Da qui le regole, che un test
verifica sull'output invece che sulla buona volontà:

| Regola | Perché |
|---|---|
| Tabelle per il layout, mai flexbox o grid | Il motore di Word non le implementa: il contenuto collasserebbe in colonna |
| CSS inline su tutto ciò che conta | Gmail scarta parte del `<style>`; il blocco in testa porta solo miglioramenti che si possono perdere senza danno |
| Larghezza 600px, in attributo **e** in stile | L'attributo serve a chi ignora il CSS, la proprietà a chi ignora l'attributo |
| Nessuna immagine | I client bloccano le immagini remote per impostazione predefinita: un logo che non carica non è un logo. Il marchio è testo |
| Riempimento del pulsante sulla `td`, non sull'`a` | Outlook ignora il padding sugli elementi inline: un pulsante con il padding sull'ancora si ridurrebbe lì a un link nudo |
| Niente JavaScript, niente `position`, niente `float` | Rimossi o resi in modo incoerente, e sospetti per i filtri antispam |
| Markup ben formato secondo XML | I test lo verificano con `encoding/xml`, senza dipendenze: un tag lasciato aperto è la differenza che si vede solo su Outlook |

**I commenti condizionali MSO non sono utilizzabili.** `html/template` rimuove i
commenti HTML dall'output, quindi un blocco `[if mso]` non arriverebbe nel
messaggio spedito: sopravvivrebbe il suo contenuto, senza la condizione che lo
racchiude — cioè il ramo per Outlook mostrato a tutti. Il layout è costruito per
non averne bisogno, e un test presidia l'assunzione.

## Multilingua

La lingua dell'utente decide quella dell'email (R33). I testi stanno in
[`locales/`](locales/README.md), l'inglese è la sorgente e il ripiego, e la
ricaduta avviene **per chiave**: una traduzione a metà mostra ciò che è tradotto
e l'inglese per il resto. Oggi solo `en.json` è popolato; riempire le altre
quattro lingue è la issue #446, e la struttura che le ospita è già qui e già
sotto test.

I link portano il prefisso di lingua — `/it/jobs/new` — perché i frontend sono
generati staticamente e ogni lingua è un albero di rotte a sé (SPEC §8-bis).

## Eventi previsti (R21)

| Evento | Template | Innesco | `notification_event` |
|---|---|---|---|
| Benvenuto / onboarding | `welcome` | registrazione utente | `welcome` |
| Alert di job fallito | `job_failed` | fallimenti persistenti di un job | `job_failed` |
| Variazione di piano | `plan_changed` | webhook Paddle di upgrade/downgrade | `plan_changed` |
| Evento di sicurezza | `security_alert` | reset password, chiave API creata o revocata, impersonificazione | `security` |

L'ultima colonna è il valore dell'enum `notification_event` della migrazione
0008: coincide con il nome del template tranne che per `security`. L'enum ha
anche `job_recovered`, che R21 non copre e per cui non esiste template.

L'aggancio agli eventi di dominio è la issue #420.

## Che cosa non dicono, e perché

Mailronix risponde `202` in modo identico sia che l'email venga recapitata sia
che il destinatario sia in suppression list per bounce o reclami pregressi: il
recapito **non è osservabile** dalla risposta (R20.1). Ne discende un vincolo
sui testi, non solo sul client: un'email non può affermare «ti abbiamo
avvisato», né presumere che un messaggio precedente sia arrivato.

L'alert di job fallito lo dice apertamente, e rimanda alla cronologia in
dashboard come unica fonte attendibile. Un test controlla che nella sorgente
inglese non compaia nessuna delle formule che darebbero per avvenuto un recapito
che non sappiamo se sia avvenuto.

Sulla stessa linea, l'email di sicurezza **notifica, non autorizza**: non
contiene link con token né valori di credenziali, e dichiara che PostQron non
chiede mai password o chiavi API per email. I flussi che consegnano un valore
monouso appartengono all'autenticazione, non a questo renderer.
