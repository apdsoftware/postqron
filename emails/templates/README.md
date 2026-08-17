# emails/templates

Template HTML delle email transazionali (SPEC R19–R21).

## Perché stanno nel repository

Mailronix è **esclusivamente un motore di recapito**: non ospita template, non
esegue sostituzioni, non conosce la logica di prodotto. Il backend Go compila
l'HTML a partire da questi file e invia a Mailronix il **payload completo**
(R20). Di conseguenza i template sono codice: versionati, revisionati in PR e
coperti da test di rendering.

## Convenzioni

- Un file `.html.tmpl` per evento, con nome in inglese e in snake_case.
- Sintassi `text/template` della standard library Go.
- HTML compatibile con i client di posta: tabelle per il layout, CSS inline,
  larghezza massima 600px, nessun JavaScript, nessuna risorsa remota
  obbligatoria.
- Ogni template ha una variante testuale `.txt.tmpl` per il fallback.
- **Nessun segreto e nessun dato personale nei template**: i valori arrivano
  sempre dal contesto passato dal renderer.

## Eventi previsti (R21)

| Evento | Template | Innesco |
|---|---|---|
| Benvenuto / onboarding | `welcome` | registrazione utente |
| Alert di job fallito | `job_failed` | fallimenti persistenti di un job |
| Variazione di piano | `plan_changed` | webhook Paddle di upgrade/downgrade |
| Evento di sicurezza | `security_alert` | reset password, revoca chiave, impersonificazione |

## Stato

La cartella è vuota: i template e il renderer Go arrivano con le issue di E7 —
vedi [docs/BACKLOG.md](../../docs/BACKLOG.md).
