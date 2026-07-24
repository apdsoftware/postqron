# D02 — Social network e formati della prima release

- **Stato:** Accettata
- **Data:** 2026-07-24
- **Ambito:** F5, F6, F8; preparazione di F18
- **Fonte:** decisione D2 in `.context/SPEC.md`

## Decisione

La prima release supporta due tipi di canale, entrambi tramite API ufficiali Meta:

1. **Pagine Facebook** amministrate dall'utente.
2. **Account Instagram Professional**, sia Business sia Creator.

Ogni Pagina Facebook e ogni account Instagram Professional è una risorsa collegabile e conteggiabile separatamente. Non sono supportati profili Facebook personali, Gruppi Facebook o account Instagram personali.

I formati di lancio sono:

| Canale | Testo/link | Immagine singola | Carousel immagini | Reel |
| --- | --- | --- | --- | --- |
| Pagina Facebook | Sì | Sì | No | Sì |
| Instagram Professional | No: serve un media | Sì | Sì, da 2 a 10 immagini | Sì |

Postqron esegue la programmazione. Al momento previsto il worker pubblica il contenuto immediatamente tramite il provider; non usa la programmazione nativa di Meta. In questo modo stato, annullamento, retry e fuso orario hanno una sola semantica.

## Motivazione e provider esclusi

Facebook Pages e Instagram Professional coprono i casi d'uso principali di creator, professionisti e piccoli team. Appartengono allo stesso ecosistema, permettono pubblicazione e insight tramite API ufficiali e consentono di riusare gestione degli errori, versionamento Graph API, review e monitoraggio quote. Due adapter e due login restano comunque separati, così un guasto o una revoca non coinvolge automaticamente l'altro canale.

Sono esclusi dalla prima release:

| Provider o risorsa | Motivo dell'esclusione | Condizione per rivalutarlo |
| --- | --- | --- |
| LinkedIn, profili e Pagine | La Community Management API richiede verifica dell'organizzazione, Development Tier, successivo Standard Tier e review con screencast. Aggiungerla ora rende l'approvazione esterna parte del percorso critico. | Adapter Meta stabile e accesso LinkedIn Standard Tier approvato. |
| TikTok | I client non sottoposti ad audit pubblicano solo contenuti privati; anche dopo l'audit esistono cap per creator e per client. Il prodotto richiederebbe inoltre un composer video dedicato. | Audit superato e requisiti UX/video approvati. |
| YouTube e Shorts | Il prodotto di lancio non è una piattaforma video; i progetti non verificati caricano video privati e l'aumento quota richiede audit. | Roadmap video, storage e transcoding dedicati. |
| X | L'API è a consumo, con credito prepagato e prezzi per richiesta soggetti a variazione. Il costo per workspace non è ancora coperto da D2 né dai piani di F10. | Modello di costo approvato insieme a F10. |
| Pinterest e Threads | Aggiungerebbero formati, OAuth, review e casi di errore distinti senza essere indispensabili per validare l'MVP. | Evidenza di domanda e adapter Meta di lancio stabile. |
| Profili e Gruppi Facebook | La Pages API scelta pubblica per conto di Pagine, non di profili personali; i Gruppi hanno autorizzazioni e policy differenti. | Nuova decisione di prodotto e verifica delle API ufficiali disponibili. |
| Account Instagram personali | La Instagram API supportata è destinata ad account Professional. | Il provider offre un flusso ufficiale compatibile con scheduling SaaS. |
| Stories, Live, annunci, post sponsorizzati, collaborazioni e shopping | Hanno permessi, durata, targeting, review o semantiche differenti. | Decisione e criteri di accettazione dedicati. |

## Risorse e operazioni

### Pagina Facebook

La risorsa remota canonica è il `page_id`; il relativo Page access token è cifrato e non viene esposto al client.

| Operazione Postqron | Operazione Meta prevista |
| --- | --- |
| Elencare e selezionare una Pagina | Facebook Login for Business e lettura delle Pagine gestite, incluso il task di creazione contenuti. |
| Collegare/verificare | Leggere `id`, nome, immagine e task correnti; rifiutare una Pagina senza capacità di pubblicazione. |
| Pubblicare testo o link | Creare un post su `/{page-id}/feed`. |
| Pubblicare immagine | Caricare e pubblicare su `/{page-id}/photos`. |
| Pubblicare Reel | Avviare upload su `/{page-id}/video_reels`, caricare, controllare processing e finalizzare. |
| Confermare l'esito | Salvare l'ID remoto restituito e leggere lo stato/permalink quando disponibile. |
| Preparare F18 | Leggere insight della Pagina o del post con permesso separato e metriche disponibili per quel tipo di contenuto. |
| Scollegare | Revocare l'associazione locale e cancellare token/materiale cifrato; revocare lato Meta solo quando ciò non interrompe altri canali autorizzati dallo stesso grant. |

### Instagram Professional

La risorsa remota canonica è l'ID dell'account Instagram Professional. Si usa **Business Login for Instagram**, che non obbliga l'utente a collegare una Pagina Facebook. Il token Instagram è indipendente dall'eventuale collegamento di una Pagina.

| Operazione Postqron | Operazione Meta prevista |
| --- | --- |
| Collegare/verificare | Ottenere ID, username, tipo account e immagine; accettare solo Business o Creator. |
| Pubblicare immagine o Reel | Creare un container con `POST /{ig-user-id}/media`, attenderne lo stato `FINISHED`, quindi pubblicare con `POST /{ig-user-id}/media_publish`. |
| Pubblicare carousel | Creare un container per ogni immagine, poi un container `CAROUSEL` con i child ID, attenderlo e pubblicarlo. |
| Confermare l'esito | Salvare l'IG Media ID restituito e leggere media, stato e permalink quando disponibili. |
| Preparare F18 | Leggere `/{ig-media-id}/insights` e gli insight account con permesso separato; conservare nome metrica, periodo e timestamp oltre al valore. |
| Scollegare | Eliminare token/materiale cifrato e revocare il grant/provider subscription quando supportato. |

La cancellazione di contenuti già pubblicati, la modifica remota, commenti e inbox non fanno parte della prima release. Modificare un elemento già pubblicato in Postqron crea una nuova bozza e non altera il post remoto.

## Contratto del composer

I limiti seguenti sono il **contratto Postqron di lancio**, intenzionalmente più conservativo di alcuni massimi del provider. F6 valida nel browser per feedback rapido e ripete la stessa validazione lato server prima di salvare come programmato e prima di pubblicare. Un cambiamento del provider può restringere il contratto, ma non lo amplia automaticamente.

Il testo è UTF-8, normalizzato NFC. I conteggi sono effettuati per Unicode code point dopo la normalizzazione; newline, URL, menzioni, emoji e hashtag fanno parte dello stesso limite.

| Formato | Testo | Media, codec e dimensioni | Ratio e durata | Note |
| --- | --- | --- | --- | --- |
| Facebook testo/link | Da 1 a 5.000 caratteri; massimo un URL assoluto HTTPS. | Nessun media. | Non applicabile. | Il link deve essere pubblico, senza credenziali nell'URL e non deve risolvere a indirizzi privati. Postqron non garantisce titolo, immagine o descrizione del link preview. |
| Facebook immagine | Caption da 0 a 5.000 caratteri. | Una immagine JPEG o PNG, sRGB, massimo 8 MB; larghezza 320–1.440 px. | Da 4:5 a 1,91:1 inclusi. | GIF animate, WebP e trasparenza che richiede resa identica sono escluse. |
| Instagram immagine | Caption da 0 a 2.200 caratteri. | Una immagine JPEG, sRGB, massimo 8 MB; larghezza 320–1.440 px. | Da 4:5 a 1,91:1 inclusi. | Non esiste post testuale: l'immagine è obbligatoria. |
| Instagram carousel | Caption da 0 a 2.200 caratteri. | Da 2 a 10 JPEG, ciascuno sRGB e massimo 8 MB; totale massimo 80 MB; larghezza 320–1.440 px. | Tutti gli elementi devono avere lo stesso ratio, da 4:5 a 1,91:1. | Carousel misti immagine/video esclusi. L'ordine salvato è vincolante. |
| Reel Facebook o Instagram | Caption/descrizione: massimo 5.000 caratteri per Facebook e 2.200 per Instagram. | MP4; video H.264; audio AAC 48 kHz; 23–60 fps; bitrate video massimo 25 Mbps e audio massimo 128 kbps; massimo 100 MB; risoluzione da 720×1.280 a 1.080×1.920. | 9:16; da 4 a 60 secondi inclusi. | Un solo video, senza edit list; `moov atom` prima dei dati media. Audio opzionale. Copertina personalizzata, sottotitoli separati e remix sono esclusi. |

Per una pubblicazione multi-canale vale l'intersezione dei vincoli:

- Un'immagine condivisa Facebook/Instagram deve essere JPEG e rispettare il contratto Instagram.
- Un Reel condiviso usa il contratto Reel comune della tabella.
- Un post testo/link non può selezionare Instagram.
- Un carousel può selezionare solo Instagram.
- Caption e asset possono essere personalizzati per destinazione; senza override si usa il limite più restrittivo.
- Se anche una sola destinazione è invalida, la programmazione è bloccata e l'errore indica canale, campo e regola violata.

Postqron non effettua crop o transcoding silenzioso nella prima release. Il file non conforme viene rifiutato, preservando il controllo creativo dell'utente e rendendo deterministica la validazione.

## OAuth, token e review

### Permessi minimi

I permessi sono richiesti progressivamente e separati tra pubblicazione e analytics:

| Canale | Lancio F5/F8 | Solo quando viene attivata F18 |
| --- | --- | --- |
| Pagina Facebook | `pages_show_list`, `pages_read_engagement`, `pages_manage_posts` | `read_insights` e ogni ulteriore permesso che la versione Graph corrente richieda esplicitamente. |
| Instagram Professional con Instagram Login | `instagram_business_basic`, `instagram_business_content_publish` | `instagram_business_manage_insights`. |

Permessi per messaggi, commenti, ads o gestione del business non vengono richiesti. Se Meta cambia nomi o dipendenze, l'implementazione deve aggiornare questa decisione prima di ampliare lo scope.

I flussi OAuth usano `state`, PKCE quando supportato, redirect URI esatte e callback server-side. Token e refresh token sono cifrati con chiavi esterne al database, non compaiono in log o URL applicativi e sono accessibili solo all'adapter del provider. Il collegamento salva grant, scope effettivi, scadenza nota, proprietario remoto e data dell'ultima verifica.

Prima del lancio pubblico sono gate obbligatori:

1. organizzazione e dominio verificati nel portale Meta;
2. privacy policy e istruzioni di cancellazione dati pubbliche;
3. Advanced Access/App Review per tutti i permessi usati con account non posseduti dal team;
4. screencast e credenziali di test che mostrano connessione, selezione risorsa, composer, pubblicazione e disconnessione;
5. Data Use Checkup e ogni requisito periodico Meta completati;
6. test in modalità sviluppo con risorse di test e smoke test in produzione dopo l'approvazione.

Un controllo giornaliero leggero e ogni errore OAuth verificano token e scope. Token scaduto ma rinnovabile viene rinnovato una sola volta sotto lock; revoca, risorsa non più amministrabile o scope mancante porta il canale a `Da riconnettere` e sospende i job successivi.

## Rate limit e retry

Le quote Meta non vengono codificate come costanti di prodotto. L'adapter:

- legge `Retry-After` quando presente;
- registra, senza token o dati personali, gli header di utilizzo applicabili come `x-app-usage` e `x-business-use-case-usage`;
- per Instagram consulta il limite di content publishing esposto dall'API prima di accettare raffiche o quando la quota locale è incerta;
- mantiene contatori per app e per risorsa in UTC, con margine di sicurezza;
- riduce polling e analytics oltre l'80% della quota osservata e sospende operazioni non essenziali oltre il 90%;
- non promette nel piano commerciale una quota superiore a quella verificata sul provider.

Errori `429`, `5xx` sicuramente precedenti alla creazione, timeout di lettura e indisponibilità temporanee usano al massimo cinque tentativi con full jitter e finestre base di 30 s, 2 min, 5 min, 15 min e 30 min, mai prima di `Retry-After`. Errori `400`, `401`, `403` e media rifiutati non sono ritentati automaticamente; auth revocata richiede riconnessione. Esauriti i tentativi, la destinazione passa a `Fallito` con causa utile.

Analytics F18 usa code a priorità inferiore rispetto alle pubblicazioni e non può consumare l'ultima capacità disponibile di una risorsa.

## Idempotenza e riconciliazione

Le API di pubblicazione Meta non sono trattate come se offrissero una idempotency key end-to-end. F8 applica quindi queste regole:

1. La chiave interna univoca è `(destination_id, post_revision_id, operation=publish)`. Un lock persistente consente un solo worker attivo e una nuova revisione produce una nuova chiave.
2. Prima della prima chiamata viene salvato un tentativo con fingerprint immutabile di testo, asset e destinazione.
3. Container ID, upload ID e remote post/media ID sono persistiti immediatamente, prima del passo successivo.
4. Ripetere upload o polling con lo stesso ID è consentito solo se la documentazione del passo lo rende sicuro; il comando finale di pubblicazione non viene duplicato alla cieca.
5. Se la connessione cade prima di inviare byte, il tentativo è ritentabile. Se cade dopo un comando di pubblicazione e manca la risposta, lo stato è `Esito incerto`.
6. Un `Esito incerto` viene riconciliato tramite container/upload status, remote ID già noto o lettura dei contenuti recenti della risorsa. Se non si trova una corrispondenza univoca, nessun retry automatico crea un possibile duplicato: l'utente vede lo stato e può confermare il post remoto o autorizzare un nuovo tentativo.
7. Dopo conferma, l'ID remoto è vincolato in modo univoco alla destinazione; webhook, polling e retry successivi possono solo aggiornare stato e metriche.

Il successo o fallimento è registrato per singola destinazione. Una pubblicazione riuscita su Facebook e fallita su Instagram non viene annullata né ripubblicata su Facebook.

## Matrice capacità F5/F6/F8/F18

Legenda: **Sì** = inclusa nella prima release; **Preparata** = contratto e permessi definiti, consegna nella feature Should Have F18; **No** = fuori scope.

| Risorsa | F5 connessione | F6 testo/link | F6 immagine | F6 carousel | F6 Reel | F8 pubblicazione e riconciliazione | F18 analytics essenziali |
| --- | --- | --- | --- | --- | --- | --- | --- |
| Pagina Facebook | Sì | Sì | Sì | No | Sì | Sì, stato per destinazione e remote ID | Preparata: insight post/Pagina disponibili per permesso e tipo; insieme minimo da confermare con la versione Graph implementata. |
| Instagram Business | Sì | No | Sì | Sì, solo immagini | Sì | Sì, container + media publish + remote ID | Preparata: `reach`, `likes`, `comments`, `shares`, `saved`, `views/plays` quando disponibili per il media. |
| Instagram Creator | Sì | No | Sì | Sì, solo immagini | Sì | Sì, container + media publish + remote ID | Preparata: stesso contratto Business, con valori assenti distinti dallo zero. |
| Facebook profilo/Gruppo | No | No | No | No | No | No | No |
| Instagram personale | No | No | No | No | No | No | No |
| Altri provider | No | No | No | No | No | No | No |

Per F18 una metrica mancante, ritirata o non applicabile è `unavailable`, non `0`. Postqron conserva il nome originale e la versione API insieme alla mappatura normalizzata, perché disponibilità e definizioni delle metriche possono cambiare. Le metriche di contenuti organici non vengono presentate come comprensive delle performance degli annunci.

## Versionamento, verifica e criteri di uscita

L'implementazione deve usare una versione Graph esplicita e supportata, mai endpoint non versionati. Prima di iniziare F5/F6/F8 e poi a ogni upgrade:

1. confrontare permessi, review, endpoint, errori e formati con la documentazione ufficiale;
2. eseguire contract test su una Pagina Facebook e un account Instagram Business/Creator di test;
3. verificare almeno testo/link Facebook, immagine comune, carousel Instagram, Reel comune, token revocato, quota simulata, timeout ambiguo e riconciliazione;
4. aggiornare questo documento se cambia una capacità osservabile dal prodotto;
5. disabilitare tramite feature flag il solo adapter/formato incompatibile, senza bloccare gli altri.

Il go-live è bloccato finché review e Advanced Access non sono approvati per entrambi i canali dichiarati disponibili. Se uno dei due non supera la review, l'interfaccia non lo mostra come supportato: non si ricorre a scraping, automazione browser o credenziali dell'utente.

## Fonti ufficiali

Fonti consultate il 2026-07-24; limiti, scope e metriche sono version-sensitive e vanno ricontrollati all'implementazione:

- Meta, [Instagram API official workspace](https://www.postman.com/meta/instagram/documentation/6yqw8pt/instagram-api) e [Content Publishing](https://developers.facebook.com/docs/instagram-platform/content-publishing/).
- Meta, [Pages API — Posts](https://developers.facebook.com/docs/pages-api/posts/), [Page access tokens](https://developers.facebook.com/docs/facebook-login/guides/access-tokens/get-page) e [Graph API rate limiting](https://developers.facebook.com/docs/graph-api/overview/rate-limiting/).
- Meta, [App Review](https://developers.facebook.com/docs/app-review/) e [Instagram Insights](https://developers.facebook.com/docs/instagram-platform/instagram-api-with-instagram-login/insights/).
- LinkedIn, [Community Management App Review](https://learn.microsoft.com/en-us/linkedin/marketing/community-management-app-review) e [API rate limiting](https://learn.microsoft.com/en-us/linkedin/shared/api-guide/concepts/rate-limits).
- TikTok, [Content Posting API — Get Started](https://developers.tiktok.com/doc/content-posting-api-get-started) e [Content Sharing Guidelines](https://developers.tiktok.com/doc/content-sharing-guidelines).
- YouTube, [Videos resource](https://developers.google.com/youtube/v3/docs/videos) e [Quota and Compliance Audits](https://developers.google.com/youtube/v3/guides/quota_and_compliance_audits).
- X, [API pricing](https://docs.x.com/x-api/getting-started/pricing).
