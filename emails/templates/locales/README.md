# emails/templates/locales

I testi delle email transazionali, uno schedario per lingua (SPEC §8-bis).

## Perché i testi non stanno nei template

Le lingue supportate sono cinque — inglese, italiano, spagnolo, tedesco,
francese — e i template sono nove file di HTML per email. Il prodotto cartesiano
sarebbe quarantacinque file da tenere allineati a mano: alla prima correzione di
un margine, quattro varianti resterebbero indietro senza che nessuno se ne
accorga.

Qui la struttura è una sola. Cambia solo il testo, e cambia in un file JSON che
si può dare a un traduttore senza fargli vedere una tabella HTML.

## Formato

Un oggetto per sezione, valori sempre stringa:

```json
{
  "welcome": {
    "subject": "Welcome to {product}"
  }
}
```

Le chiavi si compongono con il punto — `welcome.subject` — ed è così che le
citano i template.

- `{segnaposto}` è sostituito dal renderer. **I segnaposto vanno conservati
  nella traduzione**: uno mancante o scritto storto fa fallire il rendering, non
  produce un buco silenzioso nel testo.
- L'ordine delle parole può cambiare liberamente, i segnaposto si spostano con
  essa: sono nominali, non posizionali.
- Le chiavi che finiscono in `_one` e `_other` sono le due forme del plurale.
  La forma si sceglie per lingua: in francese anche lo zero vuole `_one`, nelle
  altre quattro no.

## L'inglese è la sorgente

`en.json` è l'unico file che deve essere completo, ed è quello in cui si scrive
per primo (SPEC §8-bis). Le altre lingue sono **sovrapposizioni parziali**: una
chiave che manca ricade sull'inglese, per singola chiave e non per file intero.
Un testo tradotto a metà mostra le parti tradotte e l'inglese per il resto,
invece di rovesciare l'intera email sulla lingua sbagliata.

Il contrario non è ammesso: una chiave presente in una traduzione ma assente in
`en.json` è un errore in fase di caricamento. Sarebbe il residuo di un testo
rimosso dalla sorgente, e resterebbe lì a far credere di essere in uso.

## Stato

`it.json`, `es.json`, `de.json` e `fr.json` sono vuoti: oggi tutte e cinque le
lingue producono email in inglese. Riempirli è la issue #446 — la struttura che
li ospita e il ripiego sull'inglese arrivano dalla #418 e sono già coperti dai
test.
