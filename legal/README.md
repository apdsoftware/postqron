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

I punti che richiedono un dato aziendale o una decisione non tecnica sono marcati
così:

```
[[DA CONFERMARE: descrizione del dato mancante]]
```

**Nessun documento va pubblicato finché ne resta uno.** Un controllo automatico ne
verifica l'assenza prima del deploy (#473).

## Rapporto con Paddle

Paddle è **Merchant of Record**: è Paddle a vendere all'utente finale, non noi. I
Termini devono riflettere questa struttura, e i Termini di Paddle si applicano
all'operazione di acquisto in aggiunta ai nostri. Non è un dettaglio contrattuale
astratto: cambia chi risponde di rimborsi, imposte e fatturazione.
