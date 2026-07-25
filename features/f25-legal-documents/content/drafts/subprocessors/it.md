---
document: subprocessors
locale: it
version: "0.1"
title: "Registro dei sub-responsabili di Postqron"
controllerName: "Apdsoftware di Carlo Zuffetti — gestore di Postqron (marchio APDSoftware)"
contactEmail: help@postqron.com
status: draft_pending_legal_review
changeType: material
revisionSummary: "Bozza iniziale redatta da zero, in attesa di revisione legale."
---

## Identità del fornitore

Il presente registro è pubblicato da Apdsoftware di Carlo Zuffetti (Via C. Colombo 15, 24047 Treviglio (BG), Italia, P.IVA 03835250162), operante con il marchio APDSoftware quale gestore di Postqron (dati anagrafici verificati tramite fonte pubblica: https://mailronix.com/terms, consultata il 2026-07-25), raggiungibile all'indirizzo help@postqron.com e tramite https://apdsoftware.it.

## Finalità del presente registro

Questo è il registro pubblico, aggiornato periodicamente, dei sub-responsabili e degli altri terzi coinvolti da Postqron per fornire il proprio servizio, richiamato dai Termini di servizio, dall'Informativa sulla privacy e dall'Accordo sul trattamento dei dati (DPA) anziché duplicato in tali documenti. Distingue i fornitori che agiscono come nostri sub-responsabili ai sensi dell'articolo 28 del GDPR (trattamento di dati personali su istruzione di Postqron) dai terzi indipendenti (come i fornitori di identità OAuth) che agiscono come titolari autonomi per la fase del servizio da essi svolta. Ogni voce sottostante è costruita esclusivamente sulla base di fonti primarie e ufficiali citate tramite URL, con la data in cui ciascuna fonte è stata consultata. Laddove un fatto non abbia potuto essere verificato rispetto a una fonte ufficiale, tale lacuna è dichiarata espressamente anziché colmata.

L'aggiunta o la sostituzione di un sub-responsabile che tratterà dati relativi ai contenuti dei clienti segue la procedura di notifica e obiezione descritta nell'Accordo sul trattamento dei dati: un preavviso di almeno 30 giorni ai titolari degli spazi di lavoro (Owner), un canale per sollevare un'obiezione motivata e la sospensione dell'attivazione per il cliente che si oppone fino alla risoluzione dell'obiezione. Una cronologia dei fornitori rimossi è mantenuta al di sotto della tabella attiva non appena un fornitore viene dismesso.

## Sub-responsabili e terzi attivi

| Denominazione legale | Ruolo | Servizio | Categorie di dati | Sede | Luogo di trattamento | Meccanismo di trasferimento | Riferimento DPA | Fonte (consultata il 2026-07-25) |
|---|---|---|---|---|---|---|---|---|
| Paddle.com Market Limited (entità contraente); Paddle.com Inc. (responsabile ai sensi del DPA); Paddle Payments Limited; Paddle.com Canada Ltd | Sub-responsabile | Elaborazione dei pagamenti e fatturazione come Merchant of Record | Dati di contatto per la fatturazione; metadati di abbonamento/transazione | Regno Unito; Irlanda; Stati Uniti; Canada | Non dichiarato da Paddle; può essere trattato da qualsiasi entità del gruppo Paddle | Clausole contrattuali standard | [Addendum sul trattamento dei dati di Paddle](https://www.paddle.com/legal/data-processing-addendum) | [DPA di Paddle](https://www.paddle.com/legal/data-processing-addendum) |
| Hetzner Online GmbH | Sub-responsabile | Infrastruttura di hosting cloud (calcolo, archiviazione, backup) | Dati dell'account; dati dello spazio di lavoro e dei contenuti; backup cifrati | Germania | Unione Europea/SEE quando viene selezionata una sede server UE, coerentemente con la preferenza di hosting UE/SEE-first di Postqron | Trattamento in UE/SEE (nessun trasferimento verso paesi terzi quando viene utilizzata una sede UE) | [Auftragsverarbeitungsvertrag (DPA) di Hetzner](https://www.hetzner.com/AV/DPA_en.pdf) | [DPA di Hetzner](https://www.hetzner.com/AV/DPA_en.pdf) |
| Cloudflare, Inc. | Sub-responsabile | DNS, CDN, rete edge e terminazione TLS | Metadati di rete e traffico; indirizzi IP | Stati Uniti | Rete edge globale; può trattare dati al di fuori del SEE, della Svizzera e del Regno Unito a seconda dei servizi configurati | Clausole contrattuali standard (anche certificato EU-US Data Privacy Framework e Global CBPR) | [Customer DPA di Cloudflare](https://www.cloudflare.com/cloudflare-customer-dpa/) | [DPA di Cloudflare](https://www.cloudflare.com/cloudflare-customer-dpa/) |
| Apdsoftware di Carlo Zuffetti (opera mailronix.com) | Sub-responsabile | Invio di email transazionali (notifiche di account, sicurezza e servizio) | Indirizzo email del destinatario; nome del destinatario; contenuto del messaggio transazionale | Italia (Via C. Colombo 15, 24047 Treviglio (BG)) | Germania (infrastruttura primaria su Hetzner; consegna email tramite AWS SES, Frankfurt) | Trattamento in UE/SEE | DPA dichiarato parte integrante dei Termini di mailronix.com; nessun URL DPA separato pubblicato | [Termini di mailronix.com](https://mailronix.com/terms) |
| Google LLC; Google Ireland Limited | Terza parte indipendente | Accesso OAuth ("Accedi con Google") | Indirizzo email; nome visualizzato; immagine del profilo; identificativo dell'account Google | Stati Uniti; Irlanda | Globale | EU-US e Swiss-US Data Privacy Framework; clausole contrattuali standard laddove il Framework non si applichi | Non applicabile — nessun DPA dedicato pubblicato per questa funzionalità | [Termini di servizio delle API Google](https://developers.google.com/terms) |
| Apple Inc. | Terza parte indipendente | Accesso OAuth ("Accedi con Apple") | Indirizzo email (o email di inoltro privato Apple); nome (solo al primo accesso); identificativo dell'account Apple | Stati Uniti | Non verificato | Non verificato | Non applicabile — nessun DPA dedicato pubblicato per questa funzionalità | [Accedi con Apple e privacy](https://www.apple.com/legal/privacy/data/en/sign-in-with-apple/) |
| Meta Platforms, Inc.; Meta Platforms Ireland Limited | Terza parte indipendente | Accesso OAuth ("Facebook Login") e la connessione del cliente stesso a Pagine Facebook / Instagram Professional come destinazione di pubblicazione | Indirizzo email; nome; immagine del profilo; identificativo dell'account Facebook/Instagram; contenuti che il cliente sceglie di pubblicare sul proprio account collegato | Irlanda; Stati Uniti | Non verificato | Clausole contrattuali standard; Meta Platforms, Inc. è inoltre certificata Data Privacy Framework | Non applicabile — nessun DPA dedicato pubblicato per questa funzionalità | [Termini della piattaforma Meta](https://developers.facebook.com/terms/dfc_platform_terms/) |
| LinkedIn Corporation; LinkedIn Ireland Unlimited Company | Terza parte indipendente | Accesso OAuth ("Accedi con LinkedIn") | Indirizzo email; nome; immagine del profilo; identificativo dell'account LinkedIn | Stati Uniti; Irlanda | Stati Uniti | Clausole contrattuali standard; LinkedIn Corporation è inoltre certificata Data Privacy Framework | Riferimento incrociato soltanto — il DPA per lo sviluppo commerciale di LinkedIn è collegato dai suoi Termini di utilizzo delle API ma non menziona espressamente questa funzionalità | [Termini di utilizzo delle API di LinkedIn](https://www.linkedin.com/legal/l/api-terms-of-use) |

## Lacune note da risolvere prima della pubblicazione

- **Mailronix / Apdsoftware di Carlo Zuffetti**: la pagina ufficiale mailronix.com/terms dichiara pubblicamente l'entità operativa come "Apdsoftware di Carlo Zuffetti", Via C. Colombo 15, 24047 Treviglio (BG), Italia, P.IVA 03835250162, e indica che il descrittore di transazione "può essere comune ad altri prodotti della stessa ditta" — coerente con "APDSoftware" quale medesimo marchio richiamato altrove in questo corpus come gestore di Postqron, sebbene la pagina di mailronix.com non nomini espressamente Postqron. **Punto da risolvere in sede di revisione legale**: se Mailronix e Postqron risultano gestiti dalla medesima entità legale, non si tratta di un ordinario rapporto di sub-responsabile tra parti indipendenti — è la stessa entità titolare/responsabile che utilizza un sistema con marchio separato, il che incide su come si applica l'art. 28 GDPR (un'entità non può essere sub-responsabile di se stessa) e richiede una qualificazione precisa da parte del consulente, anziché la classificazione ordinaria mantenuta qui in via provvisoria.
- **Le dichiarazioni sul luogo di trattamento di Apple e Meta** non sono state riscontrate nelle pagine ufficiali esaminate e richiedono una conferma diretta prima della pubblicazione.
- **L'applicabilità del DPA per lo sviluppo commerciale di LinkedIn all'accesso OAuth specificamente** è dedotta solo tramite riferimento incrociato e dovrebbe essere confermata direttamente con LinkedIn o con il consulente legale.
- **L'elenco dei sub-responsabili del Trust Center di Paddle** (una pagina renderizzata tramite JavaScript) non ha potuto essere letto tramite ricerca automatizzata e i suoi contenuti non sono verificati in modo indipendente; il testo stesso del DPA rimanda inoltre a un link non aggiornato a un elenco legacy dei sub-responsabili, che dovrebbe essere chiarito con Paddle.

## Sub-responsabili rimossi

Nessuno registrato alla data di questa revisione della bozza.

## Contatti

Per domande relative al presente registro, o per obiezioni a un sub-responsabile elencato ai sensi dell'Accordo sul trattamento dei dati, scrivere a help@postqron.com.
