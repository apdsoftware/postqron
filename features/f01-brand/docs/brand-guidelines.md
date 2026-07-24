# Identità visiva Postqron

Versione 0.1.0 — 24 luglio 2026

## Fondamento

Postqron aiuta professionisti, creator e piccoli team a vedere con chiarezza cosa
verrà pubblicato, dove e quando. L'identità combina ordine e calma con un accento
energico: il verde pino rappresenta affidabilità e controllo, il corallo evidenzia
momenti e azioni senza creare urgenza artificiale.

Il simbolo unisce tre elementi:

- il contenitore arrotondato richiama un post;
- il quadrante rappresenta una programmazione esplicita;
- la coda diagonale completa la `q` distintiva del nome.

Il nome canonico è **Postqron**. La descrizione breve è “Programma e gestisci i tuoi
contenuti social da un unico spazio.” La promessa di prodotto è “Postqron rende
chiaro cosa verrà pubblicato, dove e quando.”

## Marchio e asset

| Asset | Uso |
| --- | --- |
| `logo-primary.svg` | Marchio predefinito su fondo chiaro e uniforme |
| `logo-reversed.svg` | Marchio su fondo pino 800–950 o immagine scura |
| `logo-monochrome.svg` | Stampa a un colore, documenti tecnici e contesti senza colore |
| `mark.svg` | Spazi compatti in cui il nome Postqron è già visibile nel contesto |
| `favicon.svg` | Favicon del browser, scalabile da 16 a 64 px |
| `app-icon.svg` | Icona applicazione e sorgente per eventuali raster di piattaforma |
| `social-card.svg` | Anteprima sociale 1200 × 630; il testo può essere localizzato |

Il logo orizzontale non deve essere più piccolo di 120 px di larghezza sullo
schermo; il simbolo non deve essere più piccolo di 24 × 24 px. Intorno al marchio
lasciare uno spazio libero almeno pari a metà dell'altezza del simbolo. In testate
e navigazione il componente `PqLogo` garantisce una dimensione iniziale adatta.

Non deformare, ruotare, ricomporre o applicare ombre al marchio. Non cambiare la
grafia in “PostQron”, non sostituire i colori del simbolo con colori di stato e non
usare la variante primaria su sfondi che riducono il contrasto. Il testo contenuto
negli SVG usa una pila locale; per esportazioni finali destinate alla stampa è
consigliato convertire il testo in tracciato nello strumento grafico.

Le immagini informative devono avere un testo alternativo che descriva lo scopo.
Se il logo è accanto al nome già visibile, trattarlo come decorativo; altrimenti
usare “Postqron”. Il componente `PqLogo` espone già questo nome accessibile.

## Colore

I componenti usano solo token semantici `--pq-color-*`. I primitivi servono per
costruire o aggiornare il tema, non per assegnare significati di prodotto.

| Ruolo light | Valore | Uso |
| --- | --- | --- |
| Testo | `#13231d` | Testo principale su canvas e superfici |
| Canvas | `#f4f8f5` | Fondo pagina |
| Superficie | `#ffffff` | Card, modali, campi |
| Brand | `#185c43` | Azione primaria, link e identità |
| Accento/focus | `#c43d12` | Focus visibile e dettagli distintivi |
| Testo attenuato | `#4c5c54` | Informazioni secondarie, mai placeholder essenziale |
| Pericolo | `#a42d35` | Errore e azione distruttiva, sempre con testo/icona |

Le principali coppie light superano WCAG AA: testo/canvas 15.24:1,
bianco/brand 7.92:1, testo attenuato/superficie 7.08:1,
pericolo/superficie pericolo 6.53:1, avviso/superficie avviso 6.99:1 e
informazione/superficie informazione 7.11:1. Il focus corallo ha almeno 3:1 sia
contro il bianco sia contro il testo scuro adiacente.

Sono inclusi temi light, dark e system. Non dedurre mai uno stato dal solo colore:
aggiungere etichetta, testo o icona. Nuove coppie devono avere almeno 4.5:1 per
testo normale, 3:1 per testo grande e componenti grafici essenziali.

## Tipografia

Inter è il carattere preferito per interfaccia e comunicazione. La pila di sistema
mantiene leggibilità e prestazioni quando Inter non è disponibile. JetBrains Mono
è riservato a identificativi tecnici, payload o date in colonne allineate.

- Corpo: 16 px, interlinea 1.6.
- Etichette: 14 px, peso 600.
- Titoli: interlinea 1.1–1.2 e spaziatura `-0.025em`.
- Hero: scala fluida da 40 a 72 px.
- Testo minimo: 12 px solo per metadati non essenziali; non per azioni o errori.

Usare sentence case. Evitare tutto maiuscolo salvo brevi soprattitoli non
interattivi, con spaziatura ampia. Il layout deve tollerare zoom al 200% e stringhe
localizzate più lunghe senza troncare informazioni essenziali.

## Spazio, forma e movimento

La scala spaziatura parte da 4 px e privilegia multipli di 4. Le azioni e i campi
hanno target minimo 44 × 44 px. I raggi sono moderati: 10 px per controlli, 16 px
per card e messaggi, 24 px per contenitori promozionali.

Le animazioni comunicano una transizione, durano normalmente 120–320 ms e non
bloccano mai l'interazione. `prefers-reduced-motion` azzera le transizioni e
sostituisce la rotazione dell'indicatore di caricamento con una forma statica;
l'etichetta testuale continua a comunicare l'attività.

## Componenti

### PqButton

Usare un'unica azione primaria per gruppo. `secondary` accompagna o annulla,
`quiet` serve ad azioni a bassa enfasi e `danger` solo a conseguenze distruttive.
Durante il caricamento il pulsante è disabilitato, espone `aria-busy` e mantiene
un'etichetta testuale. `responsive` porta il controllo a tutta larghezza su schermi
piccoli.

### PqField

Richiede sempre un'etichetta visibile. Help ed errore vengono collegati con
`aria-describedby`; l'errore imposta `aria-invalid` e usa un live region. Specificare
`autocomplete` quando esiste un valore standard. Il placeholder non sostituisce
mai etichetta o istruzioni.

### PqAlert

`danger` usa `role="alert"` per errori che richiedono attenzione immediata; gli
altri toni usano `role="status"`. Il controllo di chiusura ha nome accessibile.
Non inserire più live region simultanee del necessario.

### PqCard

Raggruppa contenuto correlato in un `article`. Impostare `headingLevel` in base
alla gerarchia reale della pagina, senza saltare livelli. Nel layout mobile azioni
e spaziature si adattano senza overflow.

### PqLogo e PqSkipLink

`PqLogo` offre variante completa, compatta e reversed mantenendo il nome
accessibile. `PqSkipLink` deve essere il primo elemento focalizzabile della pagina;
il target predefinito è `#main-content`.

## Checklist WCAG 2.2 AA

| Requisito | Regola del sistema |
| --- | --- |
| 1.4.3 Contrasto minimo | Coppie semantiche testate automaticamente a 4.5:1 |
| 1.4.11 Contrasto non testuale | Focus e bordi essenziali almeno 3:1 |
| 1.4.12 Spaziatura testo | Nessuna altezza fissa sui contenitori testuali |
| 1.4.10 Reflow | Card e alert passano a una colonna; nessun layout richiede 2D a 320 px |
| 2.1.1 Tastiera | Controlli HTML nativi; nessun handler solo puntatore |
| 2.4.1 Salto blocchi | `PqSkipLink` raggiunge il contenuto principale |
| 2.4.7 Focus visibile | Outline da 3 px con offset, anche in forced colors |
| 2.4.11 Focus non oscurato | Z-index del salto e offset evitano sovrapposizioni locali |
| 2.5.8 Dimensione target | Azioni e campi fondamentali almeno 44 × 44 px |
| 3.3.1 Identificazione errori | Testo, `aria-invalid`, relazione con il campo e live region |
| 4.1.2 Nome, ruolo, valore | Nomi accessibili e semantica nativa nei componenti |

Prima del rilascio di una superficie effettuare anche test manuali:

1. percorrere tutte le azioni con Tab, Shift+Tab, Invio, Spazio ed Esc dove previsto;
2. verificare focus visibile e ordine coerente al 200% di zoom e a 320 CSS px;
3. provare VoiceOver/Safari con etichette, errori e aggiornamenti dinamici;
4. attivare Reduce Motion e Increase Contrast/macOS;
5. controllare che stato, errore e selezione non dipendano dal solo colore.

## Voce e credito

La voce è chiara, competente, calma, utile e trasparente. Ci si rivolge con il
“tu”; azioni e titoli usano sentence case. Un errore spiega cosa è successo,
l'effetto e l'azione disponibile. “Programmare” e “pubblicare” non sono sinonimi.

Il credito canonico del sito pubblico è:

> Sviluppato da [APDSoftware](https://apdsoftware.it)

Non alterare il nome APDSoftware e non usare il credito come endorsement dei
contenuti dell'utente.
