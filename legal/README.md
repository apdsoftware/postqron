# Documenti legali

Testi sorgente in **inglese** (SPEC §8-bis), da cui derivano le traduzioni. Le
versioni tradotte vivono in `legal/<lingua>/` e sono lavoro della issue #447.

## Documenti

| File | Copre |
|---|---|
| `terms-of-service.md` | Contratto con l'utente, piani, pagamenti, recesso, responsabilità |
| `acceptable-use-policy.md` | Cosa non si può fare con il servizio, e quando sospendiamo |
| `privacy-policy.md` | Trattamento dei dati personali, sub-responsabili, diritti |
| `cookie-policy.md` | Cookie e tecnologie simili |

## Versionamento

Ogni documento ha in testa `version` e `effective_date`. **Non si modifica un
documento in vigore**: si crea una versione nuova e si aggiorna la data. Il motivo è
R46 — la prova del consenso registra *quale versione* l'utente ha accettato e *in che
lingua*, e riscrivere il testo sotto un consenso già prestato lo rende privo di
oggetto.

Le traduzioni portano lo **stesso numero di versione** dell'originale inglese. Una
traduzione che resta indietro va segnalata come tale, non pubblicata come se fosse
allineata: il consenso vale su ciò che l'utente ha effettivamente letto.

## Segnaposto

I punti che richiedono un dato aziendale o una decisione non tecnica sono marcati con
un blocco `DA CONFERMARE` fra doppie parentesi quadre, seguito dalla descrizione del
dato mancante e, dove possibile, da una proposta.

**Nessun documento va pubblicato finché ne resta uno.** Un controllo automatico ne
verifica l'assenza prima del deploy (#473).

### Stato attuale

**Nessun segnaposto aperto, e i quattro documenti sono approvati.** La revisione
umana è arrivata il **2026-08-18** e non ha chiesto modifiche: i testi restano alle
versioni qui sotto, con le loro date di entrata in vigore.

L'approvazione non fa avanzare le versioni, ed è voluto: la versione descrive **il
contenuto**, e il contenuto non è cambiato. Farla avanzare avrebbe reso indistinguibile
«rivisto» da «riscritto» proprio nel registro che serve a distinguerli (R46).

**Da qui in avanti le regole cambiano di peso.** Un documento approvato non si
modifica per comodità: ogni cambiamento è una versione nuova, con una data nuova, e
torna in revisione. Vale anche per una virgola, perché la prova del consenso registra
la versione e non il testo.

| Documento | Versione | In vigore dal | Cosa ha mosso la versione |
|---|---|---|---|
| `terms-of-service.md` | `1.2.0` | 2026-08-18 | 1.1.0: il downgrade sospende tutto e riattiva l'utente (R58). 1.2.0: chiudere l'account non annulla l'abbonamento Paddle |
| `privacy-policy.md` | `1.1.0` | 2026-08-18 | 1.1.0: la traccia delle azioni di un admin sopravvive alla cancellazione, senza più riferimenti all'utente |
| `acceptable-use-policy.md` | `1.0.0` | 2026-08-17 | — |
| `cookie-policy.md` | `1.0.0` | 2026-08-17 | — |

Le due modifiche del 18 agosto sono nate dalla issue #460, e in entrambi i casi il
codice ha trovato **un silenzio del documento su qualcosa che facciamo** — non una
contraddizione. È la direzione giusta: quando la frase è difficile da rendere vera, è
il codice a essere sbagliato; quando il codice fa una cosa che il documento non
nomina, è il documento a essere incompleto.

Sulla data: è una proprietà della **versione del documento**, non del lancio. Il
momento in cui un utente è vincolato è quello in cui accetta, e viene registrato per
utente con versione e lingua (R46) — quindi datare la 1.0.0 al giorno in cui è stata
completata è corretto anche con il sito non ancora pubblico. Se la revisione legale
cambia i testi, cambia la versione e con essa la data, che è già la regola qui sopra.

Le decisioni incorporate: giurisdizione italiana e foro di Bergamo, Hetzner in
Germania, Paddle nel Regno Unito, Mailronix operato dalla stessa entità che opera
Postqron, nessun rimborso pro-rata, responsabilità esclusa nella misura massima
consentita, nessun DPO nominato, piani a pagamento riservati all'uso professionale.

## Rapporto con Paddle

Paddle è **Merchant of Record**: è Paddle a vendere all'utente finale, non noi. I
Termini devono riflettere questa struttura, e i Termini di Paddle si applicano
all'operazione di acquisto in aggiunta ai nostri. Non è un dettaglio contrattuale
astratto: cambia chi risponde di rimborsi, imposte e fatturazione.
