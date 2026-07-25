# D08 — Mercati globali e governance legale/DPO

- **Stato della decisione tecnica:** proposta; diventa accettata con il merge
  della PR relativa alla issue #120
- **Stato dell'approvazione legale:** obbligatoria per ciascun mercato, prima
  della relativa attivazione — nessuna data o riferimento di approvazione
  legale è riportato in questo documento in assenza di un identificativo
  verificabile (si veda §11)
- **Data:** 25 luglio 2026
- **Issue di origine:** [#120 — Ridefinire mercati globali e governance
  legale/DPO](https://github.com/apdsoftware/postqron/issues/120)
- **Decisione parzialmente sostituita:** [D04 — Mercati e requisiti
  legali](./d04-legal-scope.md) — si veda §9 per il dettaglio di cosa resta
  valido e cosa è sostituito
- **Dipendenze:** [D07 — Prezzi Buffer e modello commerciale
  Paddle](./d07-buffer-pricing-paddle.md), da cui questo documento eredita
  Paddle come Merchant of Record esclusivo e la localizzazione di valuta e
  imposte
- **Blocca:** issue #85 (riscrittura di Termini, Privacy Policy, Cookie
  Policy, DPA e registro sub-responsabili). La #85 non può essere chiusa né i
  suoi documenti pubblicati come `approved`/`current` finché la matrice di
  §2 non risulta approvata da un consulente legale abilitato per ciascun
  mercato incluso
- **Ambito:** perimetro territoriale di lancio, requisiti per mercato,
  regole di blocco tecnico per registrazione/trial/acquisto, applicabilità
  GDPR/UK GDPR ed extra-SEE, valutazione DPO, qualificazione del referente
  privacy, processo di approvazione legale per mercato

> Questo documento traduce un perimetro commerciale dichiarato "mondiale" in
> una matrice giurisdizionale verificabile. Non sostituisce un parere legale.
> Nessun mercato è considerato "live" finché non risulta, per quel mercato
> specifico, una release legale registrata secondo il processo di §10. Il
> termine "mondiale" non è usato in nessuna sezione di questo documento come
> sinonimo di copertura already-attiva: descrive soltanto l'ambizione
> commerciale che ha originato la issue #120, tradotta qui in un elenco
> esplicito di paesi/territori e in un gate di attivazione per ciascuno.

## Decisione sintetica

1. Il perimetro di lancio non è "mondiale": è l'insieme dei paesi/territori
   effettivamente supportati da Paddle come Merchant of Record (tutto il
   mondo **tranne** un elenco esplicito di paesi esclusi da Paddle per
   sanzioni internazionali, elencato in §1.2), organizzato in blocchi con
   profondità di analisi decrescente. Nessun paese fuori da questo perimetro
   è raggiungibile per registrazione, trial o acquisto.
2. Sei blocchi hanno una riga di matrice dettagliata con analisi normativa
   propria: `IT` (già live sotto D04), il blocco SEE (altri Stati membri
   dello Spazio Economico Europeo), Regno Unito, Stati Uniti, Canada,
   Australia. Tutti gli altri paesi ammessi da Paddle ricadono nel blocco
   "Resto dei mercati Paddle" (§2.7), con una baseline generica esplicita e
   **nessuna attivazione** senza una revisione legale locale dedicata.
3. La registrazione, il trial e l'acquisto sono bloccati lato backend per
   qualunque paese il cui stato in allowlist non sia `active`. Una feature
   flag o la sola presenza in questo documento non costituiscono
   approvazione (principio ereditato da D04 §1.1).
4. L'applicabilità di GDPR, UK GDPR e delle normative extra-SEE rilevanti è
   analizzata per blocco in §5, inclusi rappresentanti locali e meccanismi di
   trasferimento.
5. La nomina di un DPO non è obbligatoria allo stato attuale secondo la
   valutazione di §6; Carlo Zuffetti è qualificato come referente privacy
   interno, non DPO, per il conflitto di interesse strutturale analizzato in
   §7.
6. Ogni mercato richiede un'approvazione legale distinta da un consulente
   abilitato nella relativa giurisdizione prima dell'attivazione tecnica
   (§10); l'aggiunta di un nuovo mercato non richiede la modifica di questo
   documento, ma una nuova riga nella matrice e una nuova approvazione
   registrata.
7. Questo documento sostituisce il perimetro territoriale a mercato singolo
   di D04, ma **non** ne invalida il modello di versione dei documenti
   legali, le categorie cookie, la matrice generale delle basi giuridiche, il
   principio di allowlist server-side né la sezione fornitori/trasferimenti
   extra-SEE, che restano pienamente in vigore (dettaglio in §9).

## 1. Perimetro dei mercati

### 1.1 Criterio di inclusione

Il criterio di inclusione non è discrezionale: un paese/territorio è
**ammissibile** per questo documento se e solo se è supportato da Paddle
come Merchant of Record per la vendita a quel paese. Paddle dichiara di
operare "in tutto il mondo, con l'eccezione dei paesi non supportati"
elencati di seguito — è quindi un elenco per esclusione, non per inclusione,
e viene riportato integralmente per evitare che "mondiale" resti un
segnaposto.

### 1.2 Paesi/territori esclusi (non ammissibili, indipendentemente da qualunque blocco)

Fonte: [Paddle Help Center — Which countries are supported by
Paddle?](https://www.paddle.com/help/start/intro-to-paddle/which-countries-are-supported-by-paddle),
consultata il 25 luglio 2026.

| Paese/territorio escluso | Motivazione dichiarata da Paddle |
| --- | --- |
| Afghanistan | Restrizioni operative del fornitore |
| Antartide | Nessuna giurisdizione commerciale applicabile |
| Bielorussia\* | Sanzioni internazionali |
| Birmania (Myanmar) | Restrizioni operative del fornitore |
| Repubblica Centrafricana | Restrizioni operative del fornitore |
| Cuba | Sanzioni internazionali |
| Crimea (regione ucraina)\* | Sanzioni internazionali |
| Repubblica Democratica del Congo | Restrizioni operative del fornitore |
| Donetsk (regione ucraina)\* | Sanzioni internazionali |
| Haiti | Restrizioni operative del fornitore |
| Iran | Sanzioni internazionali |
| Iraq | Restrizioni operative del fornitore |
| Kherson (regione ucraina)\* | Sanzioni internazionali |
| Libia | Restrizioni operative del fornitore |
| Luhansk (regione ucraina)\* | Sanzioni internazionali |
| Mali | Restrizioni operative del fornitore |
| Antille Olandesi | Restrizioni operative del fornitore |
| Nicaragua | Restrizioni operative del fornitore |
| Corea del Nord | Sanzioni internazionali |
| Russia\* | Sanzioni internazionali |
| Somalia | Restrizioni operative del fornitore |
| Sud Sudan | Restrizioni operative del fornitore |
| Sudan | Restrizioni operative del fornitore |
| Siria | Sanzioni internazionali |
| Venezuela | Sanzioni internazionali |
| Yemen | Restrizioni operative del fornitore |
| Zaporizhzhia (regione ucraina)\* | Sanzioni internazionali |
| Zimbabwe | Restrizioni operative del fornitore |

\* Paesi/regioni espressamente indicati da Paddle come soggetti a normative
sanzionatorie. Questo elenco è determinato unilateralmente da Paddle e può
cambiare senza che questo documento venga aggiornato in tempo reale: prima di
ogni gate di attivazione (§8) va riverificato lo stato corrente sulla stessa
fonte, indipendentemente dalla presenza del paese in questo elenco.

### 1.3 Struttura a blocchi

Ogni paese ammissibile secondo §1.1 ricade in uno dei blocchi seguenti. I
primi sei hanno una riga di matrice dettagliata (§2); tutti gli altri paesi
ammissibili ricadono nel blocco "Resto dei mercati Paddle" (§2.7).

| Blocco | Composizione | Criterio di priorità |
| --- | --- | --- |
| `IT` | Italia | Già live sotto D04; riga completata qui con le colonne mancanti (recesso, ADR, età, in forma tabellare esplicita) |
| SEE | Gli altri 29 Stati membri dello Spazio Economico Europeo (Austria, Belgio, Bulgaria, Cipro, Croazia, Danimarca, Estonia, Finlandia, Francia, Germania, Grecia, Irlanda, Islanda, Lettonia, Liechtenstein, Lituania, Lussemburgo, Malta, Norvegia, Paesi Bassi, Polonia, Portogallo, Repubblica Ceca, Romania, Slovacchia, Slovenia, Spagna, Svezia, Ungheria) | Stesso regime GDPR diretto dell'Italia; nessun rappresentante extra-SEE necessario per il fornitore |
| `GB` | Regno Unito | Regime "quasi equivalente" (UK GDPR + Data Protection Act 2018), mercato anglofono ad alto valore B2B/B2C, Paddle opera come MoR |
| `US` | Stati Uniti | Mercato SaaS maturo, Paddle MoR operativo, ma regime privacy frammentato per stato — richiede più lavoro legale prima dell'attivazione |
| `CA` | Canada | Regime privacy federale (PIPEDA) più normativa provinciale (Quebec) da segnalare |
| `AU` | Australia | Privacy Act 1988 e Australian Privacy Principles, mercato anglofono maturo |
| Resto dei mercati Paddle | Ogni altro paese/territorio ammissibile secondo §1.1 non incluso sopra | Baseline generica, nessuna analisi puntuale, attivazione soggetta a revisione legale locale caso per caso |

L'inclusione in un blocco non equivale ad attivazione: ogni riga della
matrice di §2 ha uno stato di gate indipendente (§8), e nessun paese passa ad
`active` per il solo fatto di comparire in questo documento.

## 2. Matrice per mercato

Colonne obbligatorie per ciascuna riga: territorio, blocco, B2C ammesso, B2B
ammesso, lingua obbligatoria, valuta di visualizzazione, imposte/Merchant of
Record, recesso, rinnovi, reclami/ADR, legge applicabile, età minima,
marketing/cookie, rappresentante locale richiesto, meccanismo di
trasferimento, stato del gate, owner, evidenza.

La colonna "Imposte/Merchant of Record" non definisce regole fiscali
proprie: rimanda a D07 §"Paddle come Merchant of Record", che stabilisce che
Paddle vende al compratore, applica `tax_mode` localizzato e calcola,
raccoglie e versa IVA/sales tax nelle giurisdizioni supportate. La colonna
"Marketing/cookie" rimanda alle quattro categorie di D04 §4
(`necessary`/`preferences`/`analytics`/`marketing`), che restano invariate
per tutti i mercati.

### 2.1 Italia (`IT`)

| Colonna | Valore |
| --- | --- |
| Blocco | `IT` (già live sotto D04) |
| B2C | Sì |
| B2B | Sì |
| Lingua obbligatoria | Italiano (`it-IT`) |
| Valuta | EUR |
| Imposte/MoR | Paddle MoR, IVA italiana secondo `tax_mode` localizzato (D07) |
| Recesso | 14 giorni, salvo le eccezioni per servizi digitali già erogati previste dal Codice del consumo (d.lgs. 206/2005) |
| Rinnovi | Rinnovo automatico con disclosure di prezzo, imposte e data del rinnovo prima del consenso (D07) |
| Reclami/ADR | Piattaforma ODR UE, organismi ADR nazionali, Garante per la protezione dei dati personali |
| Legge applicabile e foro | Legge italiana; foro del consumatore inderogabile per i clienti B2C |
| Età minima | 18 anni |
| Marketing/cookie | Opt-in separato, quattro categorie D04 §4, nessun soft spam (D04 §1.4) |
| Rappresentante locale richiesto | No (stabilimento in Italia) |
| Meccanismo di trasferimento | Non richiesto per trattamenti interni SEE; si applica D04 §5.3 per fornitori extra-SEE |
| Stato del gate | `pending_legal_approval` — la matrice di questo documento non era mai stata completata in forma tabellare per queste colonne sotto D04; il go-live tecnico preesistente non equivale a superamento del gate di §8 per questo documento |
| Owner | Referente privacy interno (§7) |
| Evidenza | Da registrare secondo il processo di §10 |

### 2.2 Spazio Economico Europeo, esclusa Italia (blocco SEE)

Riga unica di blocco; ogni singolo Stato membro richiede comunque una
verifica delle specificità nazionali indicate nella colonna "Note" prima
della propria attivazione — questa riga non è un'approvazione collettiva.

| Colonna | Valore |
| --- | --- |
| Composizione | I 29 Stati SEE elencati in §1.3, esclusa l'Italia |
| B2C | Sì, subordinato a verifica locale |
| B2B | Sì |
| Lingua obbligatoria | Lingua ufficiale locale per le informazioni precontrattuali al consumatore, salvo deroghe verificate (es. Francia: legge Toubon sull'uso obbligatorio del francese per le informazioni al consumatore; Germania: Impressumspflicht e requisiti aggiuntivi cookie ai sensi di TTDSG); per i clienti B2B l'inglese può essere sufficiente previa verifica |
| Valuta | EUR salvo Stati SEE non-euro (es. Svezia, Polonia, Repubblica Ceca, Danimarca, Ungheria, Romania, Bulgaria), localizzati automaticamente da Paddle |
| Imposte/MoR | Paddle MoR, IVA locale secondo `tax_mode` (D07) |
| Recesso | 14 giorni secondo la direttiva 2011/83/UE sui diritti dei consumatori, recepita in ciascuno Stato membro |
| Rinnovi | Come `IT`, disclosure pre-consenso (D07) |
| Reclami/ADR | Piattaforma ODR UE; organismo ADR nazionale dello Stato membro dell'acquirente |
| Legge applicabile e foro | Legge italiana con la clausola-ombrello di §4, fatti salvi i fori inderogabili del consumatore dello Stato membro (art. 18 Regolamento (UE) 1215/2012 — Bruxelles I-bis) |
| Età minima | 18 anni (superiore al minimo GDPR di 13-16 anni previsto da alcuni Stati membri per il consenso ai servizi della società dell'informazione — nessun conflitto, la soglia Postqron è più alta) |
| Marketing/cookie | Opt-in separato secondo ePrivacy come recepita nello Stato membro; per la Svezia resta valido il chiarimento di D04 §1.3 sul "Marketing Act" (SFS 2008:486): la Svezia diventa un candidato di questo blocco, ma la sua attivazione resta subordinata alla verifica del testo autentico vigente da parte di un consulente locale svedese, non del solo consulente italiano |
| Rappresentante locale richiesto | No, GDPR si applica direttamente |
| Meccanismo di trasferimento | Non richiesto per trattamenti interni SEE; D04 §5.3 si applica ai fornitori extra-SEE per l'intero blocco |
| Stato del gate | `not_reviewed` per ciascuno Stato membro finché non superato singolarmente il gate di §8 |
| Owner | Referente privacy interno |
| Evidenza | Da registrare per Stato membro secondo §10 |

### 2.3 Regno Unito (`GB`)

| Colonna | Valore |
| --- | --- |
| B2C | Sì |
| B2B | Sì |
| Lingua obbligatoria | Inglese |
| Valuta | GBP, localizzato da Paddle |
| Imposte/MoR | Paddle MoR nel Regno Unito, VAT locale secondo `tax_mode` |
| Recesso | 14 giorni secondo il Consumer Contracts (Information, Cancellation and Additional Charges) Regulations 2013 |
| Rinnovi | Disclosure pre-consenso come D07; verificare eventuali obblighi aggiuntivi delle Consumer Rights (Hidden Fees) o Digital Markets, Competition and Consumers Act 2024 su rinnovi automatici |
| Reclami/ADR | Nessuna piattaforma ODR UE (Regno Unito extra-UE); ADR tramite organismo certificato locale se applicabile al settore, altrimenti reclamo diretto |
| Legge applicabile e foro | Clausola-ombrello di §4; diritti inderogabili del consumatore britannico prevalgono |
| Età minima | 18 anni |
| Marketing/cookie | Opt-in secondo Privacy and Electronic Communications Regulations (PECR) e ICO guidance, equivalente funzionale a D04 §4 |
| Rappresentante locale richiesto | Sì, ex art. 27 UK GDPR, se Postqron non ha stabilimento nel Regno Unito e tratta dati di interessati UK su base regolare — da nominare prima dell'attivazione |
| Meccanismo di trasferimento | Verificare la vigenza dell'adequacy reciproca UK↔UE alla data del go-live (l'ICO pubblica aggiornamenti periodici, non va assunta permanente); se necessario, addendum UK alle SCC UE (International Data Transfer Addendum) o International Data Transfer Agreement (IDTA), distinti dalle SCC standard SEE→extra-SEE |
| Stato del gate | `not_reviewed` |
| Owner | Referente privacy interno |
| Evidenza | Da registrare secondo §10 |

### 2.4 Stati Uniti (`US`)

| Colonna | Valore |
| --- | --- |
| B2C | Sì, subordinato a verifica stato-per-stato |
| B2B | Sì |
| Lingua obbligatoria | Inglese |
| Valuta | USD, localizzato da Paddle |
| Imposte/MoR | Paddle MoR, sales tax locale secondo `tax_mode`; nessuna aliquota o regola fiscale propria definita da Postqron |
| Recesso | Nessun diritto di recesso federale equivalente al modello UE per servizi digitali; verificare eventuali cooling-off period statali specifici |
| Rinnovi | Verificare le "automatic renewal laws" (es. California ARL, e leggi analoghe in altri stati) prima dell'attivazione: richiedono disclosure specifica e talvolta un promemoria prima del rinnovo |
| Reclami/ADR | Nessuna piattaforma ODR federale; reclami gestiti tramite `legal@postqron.com` secondo la regola di default di §5; FTC come autorità di riferimento per pratiche commerciali sleali |
| Legge applicabile e foro | Clausola-ombrello di §4 |
| Età minima | 18 anni (superiore al limite COPPA di 13 anni per il trattamento dei minori — nessun conflitto; COPPA resta rilevante solo se il servizio fosse mai rivolto a minori, il che non è previsto) |
| Marketing/cookie | Nessun regime ePrivacy federale equivalente; email marketing regolato da CAN-SPAM Act (opt-out, non necessariamente opt-in) — Postqron applica comunque il proprio standard opt-in più stringente di D04 §1.4, indipendentemente dal minimo legale locale |
| Rappresentante locale richiesto | No obbligo federale equivalente all'art. 27 GDPR; verificare requisiti specifici di singoli stati (es. California) prima dell'attivazione |
| Meccanismo di trasferimento | Trasferimenti verso gli USA regolati da D04 §5.3: EU-US Data Privacy Framework se l'entità destinataria è certificata per i dati interessati, altrimenti percorso SCC/TIA |
| Stato del gate | `not_reviewed`; nessuno stato USA è considerato analizzato in dettaglio da questo documento — riferimento minimo CCPA/CPRA (California) più elenco di stati con legge comprehensive in vigore (Virginia VCDPA, Colorado CPA, Connecticut CTDPA, Utah UCPA, e ulteriori stati la cui legge sia entrata in vigore alla data di consultazione) da aggiornare a cura del consulente prima dell'attivazione, perché il panorama normativo statale cambia più rapidamente della cadenza di revisione di questo documento |
| Owner | Referente privacy interno |
| Evidenza | Da registrare per `US` e, se necessario, per singolo stato, secondo §10 |

### 2.5 Canada (`CA`)

| Colonna | Valore |
| --- | --- |
| B2C | Sì |
| B2B | Sì |
| Lingua obbligatoria | Inglese; francese obbligatorio per le informazioni al consumatore in Quebec (Charte de la langue française) |
| Valuta | CAD, localizzato da Paddle |
| Imposte/MoR | Paddle MoR, GST/HST/PST locali secondo `tax_mode` |
| Recesso | Nessun diritto di recesso federale generale equivalente al modello UE; verificare le leggi provinciali sulla protezione del consumatore |
| Rinnovi | Disclosure pre-consenso come D07; verificare eventuali obblighi provinciali specifici sui rinnovi automatici |
| Reclami/ADR | Nessuna piattaforma ODR federale equivalente; reclami tramite `legal@postqron.com` secondo la regola di default di §5; Office of the Privacy Commissioner of Canada (OPC) come autorità di riferimento privacy |
| Legge applicabile e foro | Clausola-ombrello di §4 |
| Età minima | 18 anni |
| Marketing/cookie | Marketing elettronico regolato da CASL (Canada's Anti-Spam Legislation), che richiede consenso espresso o implicito documentato — Postqron applica comunque il proprio standard opt-in di D04 §1.4 |
| Rappresentante locale richiesto | No obbligo equivalente all'art. 27 GDPR sotto PIPEDA |
| Meccanismo di trasferimento | Trasferimenti verso il Canada regolati da D04 §5.3; nessuna decisione di adeguatezza UE formale verso il Canada per il settore privato commerciale — verificare garanzie appropriate (SCC/TIA) prima dell'attivazione |
| Stato del gate | `not_reviewed`; PIPEDA a livello federale, segnalare la legge provinciale del Quebec (Loi 25/Law 25) come sostanzialmente più stringente ed equivalente, da trattare come riferimento se il mercato include utenti del Quebec |
| Owner | Referente privacy interno |
| Evidenza | Da registrare secondo §10 |

### 2.6 Australia (`AU`)

| Colonna | Valore |
| --- | --- |
| B2C | Sì |
| B2B | Sì |
| Lingua obbligatoria | Inglese |
| Valuta | AUD, localizzato da Paddle |
| Imposte/MoR | Paddle MoR, GST australiana secondo `tax_mode` |
| Recesso | Nessun diritto di recesso generale equivalente al modello UE per servizi digitali; si applicano le garanzie del consumatore australiano (Australian Consumer Law) per difetti di conformità del servizio |
| Rinnovi | Disclosure pre-consenso come D07 |
| Reclami/ADR | Nessuna piattaforma ODR equivalente; reclami tramite `legal@postqron.com` secondo la regola di default di §5; Office of the Australian Information Commissioner (OAIC) come autorità di riferimento privacy |
| Legge applicabile e foro | Clausola-ombrello di §4 |
| Età minima | 18 anni |
| Marketing/cookie | Marketing elettronico regolato dallo Spam Act 2003 (opt-in per messaggi commerciali); Postqron applica comunque il proprio standard opt-in di D04 §1.4 |
| Rappresentante locale richiesto | No obbligo equivalente all'art. 27 GDPR sotto il Privacy Act 1988 |
| Meccanismo di trasferimento | Trasferimenti verso l'Australia regolati da D04 §5.3; nessuna decisione di adeguatezza UE formale verso l'Australia — verificare garanzie appropriate (SCC/TIA) prima dell'attivazione |
| Stato del gate | `not_reviewed`; segnalare eventuali riforme del Privacy Act 1988 in consultazione/approvazione alla data di attivazione, da verificare col consulente prima di procedere |
| Owner | Referente privacy interno |
| Evidenza | Da registrare secondo §10 |

### 2.7 Resto dei mercati Paddle (baseline generica)

Questa riga copre ogni paese/territorio ammissibile secondo §1.1 non incluso
nei blocchi precedenti (a titolo di esempio non esaustivo: gran parte dei
paesi dell'America Latina, dell'area Asia-Pacifico non coperta da `AU`, e del
Medio Oriente non esclusi da §1.2). Non sostituisce un'analisi locale: è
esplicitamente una **baseline temporanea**, non una conclusione legale.

| Colonna | Valore |
| --- | --- |
| Composizione | Tutti i paesi ammissibili secondo §1.1, esclusi quelli già coperti da `IT`, SEE, `GB`, `US`, `CA`, `AU` |
| B2C | No, finché non superato il gate di §8 per il paese specifico |
| B2B | No, finché non superato il gate di §8 per il paese specifico |
| Lingua obbligatoria (baseline) | Inglese |
| Valuta | Localizzata automaticamente da Paddle secondo le valute supportate (si veda D07 e la pagina ufficiale Paddle sulle valute) |
| Imposte/MoR | Paddle MoR, imposte locali secondo `tax_mode` dove supportato; nessuna verifica locale aggiuntiva effettuata da questo documento |
| Recesso (baseline) | Standard Paddle/locale minimo, non verificato paese per paese |
| Rinnovi (baseline) | Disclosure pre-consenso generica come D07, nessuna verifica di leggi locali specifiche sui rinnovi automatici |
| Reclami/ADR (baseline) | Nessuna partecipazione a piattaforme ADR/ODR transfrontaliere; reclami gestiti internamente tramite `legal@postqron.com` (regola di default, §5) |
| Legge applicabile e foro | Clausola-ombrello di §4 |
| Età minima | 18 anni |
| Marketing/cookie (baseline) | Opt-in secondo lo standard D04 §4, indipendentemente dal minimo legale locale eventualmente inferiore |
| Rappresentante locale richiesto | Da verificare caso per caso prima dell'attivazione; non escluso a priori |
| Meccanismo di trasferimento | Da verificare caso per caso secondo D04 §5.3 prima dell'attivazione |
| Stato del gate | **Baseline temporanea, attivazione soggetta a revisione legale locale** — nessun paese di questo blocco può passare ad `active` senza un'approvazione dedicata equivalente a quella richiesta per i blocchi dettagliati (§8, §10) |
| Owner | Referente privacy interno |
| Evidenza | Da registrare per singolo paese al momento dell'estrazione dalla baseline generica, secondo §10 |

## 3. Regole di blocco tecnico (descrizione, non implementazione)

Questa sezione descrive un requisito di prodotto; non implementa codice, non
definisce un'allowlist tecnica e non configura alcun deploy — resta fuori
scope come indicato dalla issue #120.

- L'allowlist server-side di D04 §1.1 (oggi a valore singolo `IT`) diventa
  concettualmente una lista dinamica multi-paese con tre stati per
  paese/territorio: `not_reviewed` (default per qualunque paese non ancora
  presente in una riga di matrice con gate superato), `pending_legal_approval`
  (riga presente ma gate non ancora superato), `active` (gate superato e
  record di approvazione registrato secondo §10).
- Registrazione, avvio del trial e acquisto sono bloccati salvo che il paese
  dichiarato dall'utente, verificato rispetto ai dati di fatturazione (non
  dedotto dal solo indirizzo IP, principio ereditato da D04 §1.1), risulti
  `active`.
- Le pagine pubbliche restano consultabili ovunque, incluso nei paesi non
  ammissibili secondo §1.2, ma non devono promuovere né consentire
  l'attivazione del servizio in un paese non `active` (principio ereditato da
  D04 §1.1).
- Una feature flag, una configurazione di deploy o la sola presenza di un
  paese in questo documento non costituiscono approvazione: solo il record
  di rilascio legale di §10 abilita il passaggio a `active` (ripreso
  invariato da D04 §1.1).
- L'aggiunta di un paese del blocco "Resto dei mercati Paddle" a uno stato
  diverso da `not_reviewed` richiede lo stesso livello di evidenza previsto
  per i blocchi dettagliati: non esiste un percorso di attivazione
  semplificato per il solo fatto di partire da una baseline generica.

## 4. Clausola su legge applicabile e foro

Per tutte le giurisdizioni di common law (blocchi `GB`, `US`, `CA`, `AU`, e
"Resto dei mercati Paddle") e come riferimento generale per l'intero
documento, si adotta una clausola-ombrello unica invece di clausole distinte
per ciascun paese:

> "I presenti Termini sono regolati dalla legge italiana, fatta salva
> l'applicazione delle disposizioni imperative e dei diritti non
> rinunciabili previsti dalle normative a tutela del consumatore o della
> protezione dei dati del paese di residenza abituale dell'utente."

Questa formulazione imposta l'Italia come base per l'interpretazione del
contratto, riconoscendo automaticamente che i diritti inalienabili locali
(es. garanzie di conformità in Australia, leggi statali sulla privacy negli
Stati Uniti) prevalgono dove applicabili. Per i mercati SEE resta comunque
necessaria la verifica di eventuali fori inderogabili locali già prevista da
D04 §1.2 e dal Regolamento (UE) 1215/2012 (Bruxelles I-bis) — la
clausola-ombrello non sostituisce quella verifica per l'Europa, la
integra per gli altri blocchi.

Questo testo è un **input tecnico-normativo** per la redazione dei Termini
pubblici della issue #85: non li sostituisce e non è di per sé un testo
legale pubblicabile senza revisione del consulente abilitato per ciascun
mercato in cui viene utilizzato.

## 5. Reclami e ADR fuori dallo Spazio Economico Europeo

Regola di default per tutti i blocchi extra-SEE (`GB`, `US`, `CA`, `AU`,
"Resto dei mercati Paddle"), in assenza di un'infrastruttura ADR/ODR
centralizzata paragonabile a quella europea:

> "Gestione dei reclami di natura commerciale ed esercizio dei diritti
> privacy gestiti internamente tramite legal@postqron.com. Nessuna
> partecipazione a piattaforme ADR/ODR transfrontaliere al di fuori del SEE,
> salvo obblighi normativi locali specifici da verificare."

Per l'Italia e il blocco SEE resta valido il rimando alla piattaforma ODR
dell'Unione Europea e agli organismi ADR nazionali già previsto da D04.

## 6. Analisi GDPR / UK GDPR / extra-SEE — sintesi e rimandi

L'analisi normativa di dettaglio per blocco è riportata nella matrice di §2
(colonne "Legge applicabile", "Rappresentante locale richiesto",
"Meccanismo di trasferimento"). Questa sezione riepiloga i rimandi e i punti
che richiedono revisione esplicita del consulente prima dell'attivazione di
ciascun blocco:

| Blocco | Base normativa privacy | Rappresentante locale | Trasferimenti |
| --- | --- | --- | --- |
| `IT` | GDPR diretto | Non richiesto | D04 §5.3 per fornitori extra-SEE |
| SEE | GDPR diretto | Non richiesto | D04 §5.3 per fornitori extra-SEE |
| `GB` | UK GDPR + Data Protection Act 2018 | Richiesto ex art. 27 UK GDPR se assente stabilimento UK | Verifica vigenza adequacy UK↔UE; IDTA/addendum UK alle SCC se necessario |
| `US` | Nessuna legge federale unica; CCPA/CPRA come riferimento minimo, più stati con legge comprehensive da aggiornare | Da verificare per singolo stato | EU-US Data Privacy Framework se entità certificata, altrimenti SCC/TIA (D04 §5.3) |
| `CA` | PIPEDA federale + Quebec Law 25 | Non richiesto sotto PIPEDA | Nessuna adequacy UE formale; SCC/TIA (D04 §5.3) |
| `AU` | Privacy Act 1988 / Australian Privacy Principles | Non richiesto | Nessuna adequacy UE formale; SCC/TIA (D04 §5.3) |
| Resto dei mercati Paddle | Da determinare per paese | Da verificare caso per caso | Da verificare caso per caso secondo D04 §5.3 |

D04 §5.3 (trasferimenti extra-SEE: destinazione, importatore, adequacy o
garanzia appropriata, SCC 2021/914 con Transfer Impact Assessment, verifica
periodica) resta la base tecnica invariata e si applica a tutti i blocchi di
questo documento, non solo al perimetro italiano originario.

## 7. Valutazione della necessità di un DPO (art. 37 GDPR)

Questa sezione documenta il ragionamento giuridico a supporto della
conclusione già registrata nei commenti della issue #85 — non la rimette in
discussione, la argomenta.

### 7.1 Criteri dell'art. 37(1) GDPR applicati al modello di business Postqron

| Criterio | Descrizione | Applicabilità a Postqron |
| --- | --- | --- |
| (a) Trattamento da autorità pubblica | Il trattamento è svolto da un'autorità pubblica o da un organismo pubblico | Non applicabile: Postqron è un'impresa privata |
| (b) Monitoraggio regolare e sistematico su larga scala come attività principale | L'attività principale del titolare consiste in operazioni che richiedono il monitoraggio regolare e sistematico degli interessati su larga scala | Non ricorre: l'attività principale di Postqron è la programmazione e pubblicazione di contenuti social per conto dei clienti (per lo più in qualità di responsabile del trattamento sui contenuti, D07/D04 §3); non svolge profilazione comportamentale sistematica su larga scala come attività *core*. L'eventuale funzione di analytics (F18 della SPEC) è "Should Have", non inclusa nell'MVP oggetto di questo documento |
| (c) Trattamento su larga scala di categorie particolari (art. 9) o dati relativi a condanne penali | L'attività principale consiste nel trattamento su larga scala di categorie particolari di dati | Non ricorre: D04 §3 esclude la richiesta di tali dati come pratica; se compaiono nei contenuti caricati dai clienti, sono trattati su istruzione del cliente (DPA B2B), non come finalità propria di Postqron |

### 7.2 Conclusione motivata

Alla data di questo documento, **nessuno dei tre criteri dell'art. 37(1)
GDPR/UK GDPR risulta soddisfatto** dal modello di business descritto dalla
SPEC per l'MVP multi-mercato qui delineato. La nomina di un DPO non è quindi
obbligatoria.

Questa conclusione va rivalutata se si verifica anche solo uno dei seguenti
eventi (trigger di revisione):

- l'introduzione di una funzione di analytics/profilazione oltre quanto oggi
  escluso dall'MVP core (SPEC F18);
- una crescita del volume di interessati o della sistematicità del
  monitoraggio tale da configurare "attività principale" ai sensi dell'art.
  37(1)(b);
- un requisito equivalente al DPO imposto da un mercato specifico
  indipendentemente dai criteri GDPR (es. alcuni stati USA impongono
  designazioni di responsabile privacy con requisiti analoghi, pur non
  essendo un obbligo di "DPO" in senso GDPR — da verificare per `US` prima
  dell'attivazione, come indicato in §2.4);
- l'attivazione di un mercato del blocco "Resto dei mercati Paddle" la cui
  normativa locale imponga un obbligo equivalente.

### 7.3 Nomina volontaria di un referente privacy interno

In assenza dell'obbligo, Postqron nomina comunque un referente privacy
interno come punto di contatto per titolarità del trattamento, coerente con
quanto già confermato nei commenti della issue #85 (si veda §8).

## 8. Qualificazione di Carlo Zuffetti

### 8.1 Conflitto strutturale ex art. 38(6) GDPR

Carlo Zuffetti ricopre ruoli decisionali apicali di prodotto, commerciali e
finanziari in Postqron (Product Owner e Finance Owner, come già registrato
in D07). L'art. 38(6) GDPR richiede che il DPO possa svolgere i propri
compiti in modo indipendente e senza ricevere istruzioni riguardo al loro
esercizio; un soggetto che determina le finalità e i mezzi del trattamento
(ruolo apicale) presenta per definizione un conflitto di interesse
strutturale con il ruolo di DPO, indipendentemente dalla sua competenza
personale in materia di protezione dei dati.

### 8.2 Qualificazione operativa

Carlo Zuffetti è qualificato come **titolare del trattamento / referente
privacy interno**, non come DPO, coerentemente con:

- la conclusione di §7 (DPO non obbligatorio nell'MVP descritto);
- l'impossibilità di escludere il conflitto di interesse ex art. 38(6) senza
  una valutazione indipendente documentata, che allo stato non risulta
  effettuata da un soggetto terzo.

### 8.3 Profilo per un eventuale DPO esterno futuro

Se la valutazione di §7 dovesse concludere per l'obbligatorietà del DPO (per
crescita, nuovi mercati o nuove funzioni), il profilo richiesto per un DPO
esterno è:

- esperienza documentata in GDPR/UK GDPR e, ove rilevanti per i mercati
  attivati, nei framework extra-SEE della matrice (CCPA/CPRA, PIPEDA,
  Privacy Act 1988/APP);
- indipendenza contrattuale, senza alcun ruolo decisionale in prodotto,
  commerciale o tecnico di Postqron;
- reperibilità e canale diretto con l'autorità di controllo competente per
  ciascun mercato attivato;
- assenza di conflitti di interesse verificata da un soggetto terzo (es.
  parere scritto di un consulente legale abilitato), non autocertificata da
  Postqron.

Questa sezione non nomina un DPO: registra la valutazione e il profilo per
un'eventuale nomina futura, coerentemente con quanto la issue #120 indica
come fuori scope (implementazioni operative definitive).

## 9. Rapporto con D04 — cosa resta valido, cosa è sostituito

Sul modello della clausola "Autorità della decisione" di D07 rispetto a D03:

### 9.1 Cosa di D04 resta pienamente valido (non riscritto, richiamato per riferimento)

- Il modello di versione dei documenti legali: artefatti immutabili,
  versione `major.minor`, digest `sha256`, identificativo di approvazione
  senza dati personali nel repository (D04 §2.2).
- Gli eventi di prova append-only (`accepted`, `acknowledged`, `granted`,
  `rejected`, `withdrawn`) e le relative regole di non modificabilità (D04
  §2.3).
- Le quattro categorie cookie (`necessary`, `preferences`, `analytics`,
  `marketing`) e il comportamento della CMP, incluso il blocco preventivo
  (D04 §4).
- La matrice generale delle basi giuridiche GDPR (D04 §3), applicabile come
  default anche ai nuovi mercati SEE/UK, salvo le integrazioni locali
  specifiche indicate in §2 e §6 di questo documento.
- Il principio dell'allowlist server-side e la regola "una feature flag da
  sola non costituisce approvazione" (D04 §1.1), estesi in questo documento
  da singolo paese a lista dinamica multi-paese (§3).
- La sezione fornitori/DPA/sub-responsabili/trasferimenti extra-SEE (D04
  §5), incluso l'uso di SCC 2021/914, Transfer Impact Assessment e EU-US Data
  Privacy Framework (D04 §5.3), ora applicata all'intero perimetro
  multi-mercato di questo documento e non solo al mercato italiano.
- La separazione tra decisioni tecniche e approvazioni legali riservate al
  consulente (D04 §6.1/§6.2), estesa qui a un processo esplicitamente
  per-mercato (§10).
- La risoluzione dell'ambiguità "Marketing Act" = legge svedese SFS
  2008:486 (D04 §1.3): l'identificazione della norma resta valida; cambia
  solo lo stato di ammissibilità della Svezia (si veda §9.2).

### 9.2 Cosa D04 non copre più o è esplicitamente sostituito

- Il perimetro territoriale limitato al solo `IT` (D04 §1.1, tabella
  "Perimetro territoriale") è sostituito dalla struttura a blocchi di §1 e
  dalla matrice di §2 di questo documento.
- L'allowlist tecnica a valore singolo `IT` (D04 §6.1, primo punto) è
  sostituita dalla lista dinamica multi-paese descritta in §3.
- L'assunzione implicita "un solo mercato, quindi un solo record di release
  legale" è sostituita dal processo di approvazione esplicitamente
  per-mercato di §10.
- Lo stato della Svezia passa da "esclusa a prescindere" (D04 §1.3) a
  "candidata nel blocco SEE, subordinata a verifica del testo autentico
  SFS 2008:486 da parte di un consulente locale svedese" (§1.3, §2.2 di
  questo documento).
- Il gate di go-live di D04 §7, scritto specificamente per `IT`, è
  sostituito dal gate di attivazione generico per-mercato di §8, applicabile
  sia a `IT` sia a ogni nuovo mercato aggiunto in futuro.

D04 resta nel repository come riferimento storico e normativo per l'analisi
italiana originaria; non deve essere usata per determinare il perimetro
territoriale attivo, che è governato esclusivamente da questo documento e
dalla sua matrice.

## 10. Processo di approvazione per mercato

- **Chi approva:** un consulente legale abilitato nella giurisdizione del
  mercato considerato, o con competenza comprovata su quel regime specifico
  (es. un consulente competente sul diritto di uno o più stati USA per
  l'attivazione di `US`). L'approvazione di un consulente italiano non è
  sufficiente per estendere l'attivazione ad altri mercati.
- **Evidenza richiesta prima della release per un dato mercato** (estensione
  del "record di release legale" di D04 §6.2): riga completa della matrice
  di §2 per quel mercato; analisi normativa specifica secondo §6; nomina del
  rappresentante locale se richiesto; verifica del meccanismo di
  trasferimento se applicabile; testi legali localizzati approvati secondo
  il modello di versione di D04 §2.2 (i testi stessi restano fuori scope di
  questo documento, sono oggetto della issue #85); identificativo
  dell'approvazione legale, senza firme o dati personali nel repository.
- **Aggiunta di un nuovo mercato non ancora elencato:** si aggiunge una
  nuova riga alla matrice di §2 con una nuova approvazione registrata
  secondo questo stesso processo. Non richiede la modifica di D04 né di
  questo documento: la struttura di D08 resta stabile, la matrice è
  l'unico artefatto che cresce.
- **Estrazione di un paese dal blocco "Resto dei mercati Paddle":** segue lo
  stesso processo dei blocchi dettagliati, con lo stesso livello di
  evidenza richiesto — non esiste un percorso semplificato per i paesi che
  partono dalla baseline generica di §2.7.
- **Gate tecnico:** il deploy/allowlist rifiuta l'attivazione di un paese
  privo di un record di release legale valido per quel paese specifico
  (ripreso invariato da D04 §6.2).

| Blocco | Consulente competente richiesto | Evidenza minima | Stato |
| --- | --- | --- | --- |
| `IT` | Consulente abilitato in Italia | Matrice §2.1 completa, record di release | `pending_legal_approval` |
| SEE | Consulente abilitato per Stato membro attivato | Matrice, verifica specificità nazionali (lingua, foro) | `not_reviewed` |
| `GB` | Consulente abilitato nel Regno Unito | Matrice, nomina rappresentante art. 27 UK GDPR, verifica adequacy | `not_reviewed` |
| `US` | Consulente abilitato per gli stati USA attivati | Matrice, elenco stati con legge comprehensive aggiornato | `not_reviewed` |
| `CA` | Consulente abilitato in Canada (incluso Quebec se rilevante) | Matrice, verifica PIPEDA/Quebec Law 25 | `not_reviewed` |
| `AU` | Consulente abilitato in Australia | Matrice, verifica Privacy Act 1988/APP | `not_reviewed` |
| Resto dei mercati Paddle | Consulente abilitato nella giurisdizione specifica estratta | Riga di matrice completa equivalente ai blocchi dettagliati | `not_reviewed` per ogni paese non ancora estratto |

## 11. Stato dell'approvazione legale di questo documento

Questo documento è una **proposta tecnica**, non un parere legale. Nessuna
data né riferimento di approvazione legale è inserito in questa sezione:
durante la stesura sono state ricevute più richieste di dichiarare il
documento "adottato" o "approvato" con una data, senza fornire alcun
identificativo verificabile (nome del consulente, numero di pratica,
riferimento del parere). Coerentemente con D04 §2.2 ("identificativo
dell'approvazione legale, senza firme o dati personali nel repository") e
con il criterio di verifica della issue #120 ("revisione esplicita di un
consulente legale abilitato prima del merge"), lo stato resta "proposta, in
attesa di revisione legale" finché non viene fornito un identificativo
verificabile minimo. Quando disponibile, questo documento va aggiornato con
una nuova PR che aggiunga tale identificativo, non con una modifica silenziosa
di questa sezione.

Le decisioni di prodotto registrate nei commenti della issue #85 (nessun DPO
nominato, mercati ammissibili = paesi supportati da Paddle, locale pubbliche
`en/it/es/fr/de`, EUR come valuta base, età minima 18 anni, legge italiana
con clausola-ombrello, marketing solo opt-in) sono state trattate in questo
documento come **input di prodotto validi**, non come sostituti della
revisione legale richiesta prima dell'attivazione di ciascun mercato.

## 12. Gate di attivazione per mercato

Checklist generica, applicabile a ogni riga della matrice di §2 prima che il
relativo mercato passi a `active` — incluso, retroattivamente, `IT`, la cui
riga di matrice non era stata completata in forma tabellare per le colonne
di recesso/ADR/età prima di questo documento:

- [ ] riga della matrice di §2 completa per il mercato, incluse tutte le
      colonne obbligatorie;
- [ ] analisi normativa di §6 specifica per il mercato, con fonti
      istituzionali datate (§13);
- [ ] rappresentante locale nominato se richiesto;
- [ ] meccanismo di trasferimento verificato se applicabile (D04 §5.3);
- [ ] testi legali localizzati approvati e pubblicati secondo il modello di
      versione D04 §2.2 (testi non scritti in questo documento, oggetto
      della issue #85);
- [ ] valutazione DPO di §7 rivista senza trigger attivi per quel mercato
      specifico;
- [ ] record di approvazione legale registrato secondo il processo di §10,
      con consulente competente per quella giurisdizione;
- [ ] allowlist backend aggiornata da `not_reviewed`/`pending_legal_approval`
      ad `active` solo dopo il record di cui sopra;
- [ ] per i paesi del blocco "Resto dei mercati Paddle": verifica che il
      paese non compaia nell'elenco di esclusione di §1.2 alla data
      dell'attivazione, indipendentemente da quanto riportato in questo
      documento.

La issue #85 resta bloccata finché almeno la riga `IT` non risulta con
questo gate superato, anche se il mercato italiano è tecnicamente già
operativo sotto D04.

## Fonti istituzionali

Fonti consultate il 25 luglio 2026; prima di ogni gate di attivazione per
mercato occorre ricontrollarne versione e vigenza specificamente per quel
mercato.

Fonti generali GDPR/SEE, riprese invariate da D04 (da ricontrollare alla
data di ogni gate):

- [Regolamento (UE) 2016/679 — GDPR, EUR-Lex](https://eur-lex.europa.eu/eli/reg/2016/679/oj?locale=it)
- [Direttiva 2002/58/CE — ePrivacy, EUR-Lex](https://eur-lex.europa.eu/eli/dir/2002/58/oj)
- [Consumer Rights Directive 2011/83/UE, Commissione europea](https://commission.europa.eu/law/law-topic/consumer-protection-law/consumer-contract-law/consumer-rights-directive_en)
- [Regolamento (UE) 1215/2012 — Bruxelles I-bis, EUR-Lex](https://eur-lex.europa.eu/legal-content/IT/TXT/?uri=CELEX%3A32012R1215)
- [Trasferimenti internazionali di dati, Commissione europea](https://commission.europa.eu/law/law-topic/data-protection/international-dimension-data-protection/rules-international-data-transfers_en)
- [Standard Contractual Clauses, Commissione europea](https://commission.europa.eu/law/law-topic/data-protection/international-dimension-data-protection/standard-contractual-clauses-scc_en)
- [The Marketing Act (Marknadsföringslagen), SFS 2008:486, Governo svedese](https://www.government.se/government-policy/consumer-affairs/the-marketing-act-marknadsforingslagen/)
- [European Data Protection Board (EDPB) — Linee guida](https://edpb.europa.eu/)

Fonti specifiche per blocco, aggiunte da questo documento:

- [ICO — UK GDPR guidance and resources](https://ico.org.uk/for-organisations/uk-gdpr-guidance-and-resources/)
- [ICO — Guidance on representatives, art. 27 UK GDPR](https://ico.org.uk/for-organisations/uk-gdpr-guidance-and-resources/data-protection-officers/representatives/)
- [Data Protection Act 2018, legislation.gov.uk](https://www.legislation.gov.uk/ukpga/2018/12/contents)
- [California Privacy Protection Agency — CCPA/CPRA](https://cppa.ca.gov/)
- [Federal Trade Commission — Privacy and security guidance](https://www.ftc.gov/business-guidance/privacy-security)
- [Office of the Privacy Commissioner of Canada — PIPEDA](https://www.priv.gc.ca/)
- [Canadian Radio-television and Telecommunications Commission — CASL](https://crtc.gc.ca/eng/internet/anti.htm)
- [Office of the Australian Information Commissioner — Privacy Act 1988/APP](https://www.oaic.gov.au/)

Fonti Paddle per il perimetro dei mercati (base di §1):

- [Paddle Help Center — Which countries are supported by Paddle?](https://www.paddle.com/help/start/intro-to-paddle/which-countries-are-supported-by-paddle), consultata il 25 luglio 2026 — elenco dei paesi esclusi riportato in §1.2
- [Paddle Developer — Supported currencies](https://developer.paddle.com/concepts/sell/supported-currencies/), consultata il 25 luglio 2026
- Fonti Paddle già citate in D07 (Billing, Pricing, localize prices), richiamate per riferimento e non riprodotte qui

## Checklist documentale

- [x] Lista esplicita di mercati per blocchi (§1), nessun uso di "mondiale"
      come sinonimo di copertura attiva: il perimetro è definito per
      esclusione a partire dall'elenco Paddle (§1.2).
- [x] Matrice per mercato con tutte le colonne richieste dal criterio di
      accettazione della issue #120 (§2).
- [x] Regole di blocco tecnico per registrazione/trial/acquisto descritte,
      non implementate (§3).
- [x] Analisi GDPR/UK GDPR/extra-SEE per ciascun blocco, con rappresentanti
      e trasferimenti (§2, §6).
- [x] Valutazione DPO documentata con conclusione motivata e trigger di
      revisione (§7).
- [x] Qualificazione di Carlo Zuffetti come referente privacy interno,
      profilo DPO esterno definito per il futuro (§8).
- [x] Processo di approvazione per mercato ed evidenza richiesta descritti
      (§10).
- [x] Clausola di sostituzione di D04 esplicita: cosa resta valido, cosa è
      sostituito (§9).
- [x] Fonti istituzionali con URL e data di consultazione per ogni blocco
      incluso.
- [x] Stato "proposta, in attesa di revisione legale" dichiarato
      esplicitamente, senza data o riferimento di approvazione non
      verificabile (§11); issue #85 dichiarata bloccata fino a matrice
      approvata (intestazione, §12).
