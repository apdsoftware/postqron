# D02 — Social network e formati della prima release

- **Stato:** Accettata
- **Data:** 2026-07-30
- **Ambito:** F5, F6, F8; coerenza con F7; dipendenza da F9/F23 per le destinazioni notification-only; preparazione di F18
- **Fonte:** decisione D2 in `.context/SPEC.md` (F5, F6, F7, F8, sezione «Rischi ed Edge Cases», «Dipendenze tra Funzionalità»); issue #301; epic #300.
- **Sostituisce:** la versione Meta-only del 2026-07-24 (`page_id` Facebook e Instagram Professional soltanto). Questa revisione estende la copertura ai social gestiti da Buffer al 30 luglio 2026, mantenendo l'integrazione **diretta** con le API ufficiali di ogni provider.

## Decisione

Postqron copre l'insieme dei social network gestibili da Buffer al 30 luglio 2026. Buffer **non** è una dipendenza runtime: è usato solo come riferimento di copertura e di classificazione della modalità di pubblicazione. Ogni canale è integrato tramite un **adapter diretto verso l'API ufficiale del provider**, con contratto capability-driven: nessun formato o vincolo è codificato solo per Meta.

Ogni provider e ogni risorsa remota collegabile (Pagina, account, organizzazione, board, location, profilo) è una risorsa conteggiabile separatamente ai fini di F5 e F10, con adapter, login e stato di connessione indipendenti: un guasto, una revoca o una sospensione su un canale non ne coinvolge automaticamente un altro. Le destinazioni **notification-only** (vedi sotto) non sono connessioni API — non hanno token né login OAuth di pubblicazione — ma restano destinazioni conteggiabili per F10 come le altre, marcate `notification-only`.

Ogni canale è classificato in **una** delle tre modalità di F8:

- **Auto-publishing** — l'API ufficiale consente al worker di pubblicare direttamente per conto dell'utente. Vale la semantica completa di F8: stato per destinazione, ID remoto, retry idempotente, riconciliazione.
- **Notification publishing** — non esiste un'API ufficiale di pubblicazione né un OAuth di publishing compatibile. La destinazione **non è una connessione API**: è una **destinazione logica notification-only** aggiunta manualmente dall'utente, **senza token di pubblicazione** e senza grant che autorizzi Postqron a postare. Postqron valida, programma e, all'orario previsto, **non chiama alcuna API del provider**: invia all'utente una notifica (F9 email e/o F23 push/deep-link) con il contenuto pronto perché lo pubblichi manualmente sull'app del provider. Nessun byte è pubblicato lato server; non c'è ID remoto né retry di pubblicazione. Una destinazione notification-only richiede **obbligatoriamente** almeno un canale di notifica configurato e verificato (F9 o F23): senza di esso non può essere creata né programmata.
- **Non supportato** — il canale non è offerto nella prima release.

La programmazione resta di Postqron (F7). Per l'auto-publishing il worker pubblica immediatamente all'orario previsto tramite l'API del provider e non usa lo scheduling nativo del provider, così stato, annullamento, retry e fuso orario hanno una sola semantica. Per il notification publishing l'orario previsto determina l'invio della notifica.

## Modalità di pubblicazione per provider

Classificazione al 30 luglio 2026. È **version-sensitive**: va riverificata alla data di implementazione di ogni adapter (vedi «Fonti ufficiali» e «Versionamento»).

| Provider | Risorsa collegabile | Modalità F8 | Note |
| --- | --- | --- | --- |
| Facebook Pages | `page_id` (Pagina amministrata) | **Auto** | Graph API Pages; Page access token cifrato. |
| Facebook Groups | Gruppo (destinazione logica) | **Notifica** | Nessuna connessione API: Meta ha dismesso la pubblicazione via Groups API. Destinazione notification-only senza token; richiede F9/F23. |
| Instagram Business | `ig-user-id` (account Professional Business) | **Auto** | Content Publishing API (container + publish). |
| Instagram Creator | `ig-user-id` (account Professional Creator) | **Auto** | Stesso contratto di Business. |
| Instagram personale | Account personale (destinazione logica) | **Notifica** | Nessuna connessione API: nessuna Content Publishing API per account personali. Destinazione notification-only senza token; richiede F9/F23. |
| X | Account | **Auto** | API v2; tier a sottoscrizione/consumo. |
| LinkedIn profilo | Membro | **Auto** | Posts API, autore membro (`w_member_social`). |
| LinkedIn Pagina | Organizzazione | **Auto** | Posts API, autore organizzazione (`w_organization_social`) + Community Management API. |
| Pinterest | Board di destinazione | **Auto** | Pins API v5; una board è obbligatoria per il pin. |
| TikTok | Account creator | **Auto**, gate su audit | Content Posting API / Direct Post; senza audit si pubblica solo privato. Resta fail-closed finché l'audit non è approvato. |
| Google Business Profile | Location verificata | **Auto** | Business Profile API `localPosts`. |
| Mastodon | Account su istanza | **Auto** | API per-istanza; OAuth e limiti letti dall'istanza. |
| YouTube Shorts | Canale | **Auto** | Data API v3 `videos.insert` (upload resumable). |
| Threads | Account Threads | **Auto** | Threads API (Meta), container + publish. |
| Bluesky | Account (PDS) | **Auto** | AT Protocol XRPC `com.atproto.repo.createRecord`. |
| Facebook profilo personale | — | **Non supportato** | Nessuna API ufficiale di pubblicazione per profili personali. |
| Start Page (Buffer) | — | **Non supportato** | Non è un social network né è pubblicabile via API; è una landing link-in-bio, fuori ambito. |

## Matrice capacità F5/F6/F8/F18

Legenda: **Sì** = incluso nella prima release; **Notifica** = consegnato come notification publishing; **Preparata** = contratto e permessi definiti, consegna nella feature Should Have F18; **No** = fuori scope. Nella colonna **F5** «Sì» indica una connessione API/OAuth reale con token cifrato; «Destinazione notification-only» indica una destinazione logica aggiunta manualmente, senza connessione API né token di pubblicazione.

I formati F6 usano le sigle: **T** testo/link, **I** immagine singola, **C** carousel/multi-immagine, **V** video, **R** reel/short verticale, **Th** thread/catena.

| Risorsa | F5 connessione | F6 formati | F8 pubblicazione | F18 analytics essenziali |
| --- | --- | --- | --- | --- |
| Facebook Pages | Sì | T, I, V, R | Auto: stato per destinazione + remote ID | Preparata: insight post/Pagina (`read_insights`), insieme minimo confermato con la versione Graph. |
| Facebook Groups | Destinazione notification-only (no API/token) | T, I, V | Notifica: reminder F9/F23 all'orario, nessun remote publish | No (nessuna metrica via API). |
| Instagram Business | Sì | I, C (2–10), R | Auto: container + `media_publish` + remote ID | Preparata: `reach`, `likes`, `comments`, `shares`, `saved`, `views/plays` quando disponibili. |
| Instagram Creator | Sì | I, C (2–10), R | Auto: come Business | Preparata: come Business, valori assenti distinti dallo zero. |
| Instagram personale | Destinazione notification-only (no API/token) | I, V | Notifica: reminder F9/F23 all'orario | No. |
| X | Sì | T, I, V, Th | Auto: `POST /2/tweets` + remote ID | Preparata: metriche public del Tweet quando il tier lo consente. |
| LinkedIn profilo | Sì | T, I, C (documenti), V | Auto: Posts API + remote ID (URN) | Preparata: solo metriche consentite al membro; molte richiedono contesto organizzazione. |
| LinkedIn Pagina | Sì | T, I, C (documenti), V | Auto: Posts API autore organizzazione + URN | Preparata: analytics organizzazione via Community Management API con permesso dedicato. |
| Pinterest | Sì | I, V (una board) | Auto: `POST /v5/pins` + remote ID | Preparata: metriche pin quando l'accesso è approvato. |
| TikTok | Sì (gate audit) | V, I (photo mode) | Auto se audit approvato; altrimenti disabilitato fail-closed | Preparata: metriche video quando l'audit e i permessi lo consentono. |
| Google Business Profile | Sì | T, I, V | Auto: `localPosts.create` + remote ID | Preparata: insight `localPosts`/location con permesso dedicato. |
| Mastodon | Sì | T, I, V, C (fino a 4) | Auto: `POST /api/v1/statuses` + remote ID | Preparata: contatori pubblici dello status quando esposti dall'istanza. |
| YouTube Shorts | Sì | R (short verticale) | Auto: `videos.insert` resumable + remote ID | Preparata: YouTube Analytics API con permesso e quota dedicati. |
| Threads | Sì | T, I, V, C, Th | Auto: container + publish + remote ID | Preparata: Threads insight con permesso dedicato. |
| Bluesky | Sì | T, I, V, Th | Auto: `createRecord` + URI/CID | Preparata: contatori public quando disponibili via AppView. |
| Facebook profilo personale | No | No | No | No |
| Instagram Stories/Live, X Spaces, annunci, shopping, collaborazioni | No | No | No | No |
| Start Page e altri provider | No | No | No | No |

Per F18 una metrica mancante, ritirata o non applicabile è `unavailable`, mai `0`. Postqron conserva nome originale della metrica, periodo, timestamp e versione API accanto alla mappatura normalizzata. Le metriche di contenuti organici non vengono presentate come comprensive delle performance degli annunci.

## Risorse, operazioni e prerequisiti per provider

Per ogni canale la risorsa remota canonica è persistita insieme al token cifrato; il token non è mai esposto al client. «Review/audit» indica i gate esterni prima del go-live pubblico del canale.

### Meta — Facebook Pages (auto)

- **Risorsa:** `page_id`; Page access token cifrato, derivato dopo il Facebook Login for Business.
- **Operazioni:** elencare le Pagine gestite con task di creazione contenuti; pubblicare testo/link su `/{page-id}/feed`; immagine su `/{page-id}/photos`; Reel su `/{page-id}/video_reels` (avvio upload → upload → processing → finalize); salvare remote ID e permalink.
- **OAuth (F5/F8):** `pages_show_list`, `pages_read_engagement`, `pages_manage_posts`. **F18:** `read_insights`.
- **Review/audit:** App Review con Advanced Access per i permessi usati su risorse non possedute dal team; organizzazione e dominio verificati; Data Use Checkup periodico.
- **Rate limit:** platform rate limiting Graph API; l'adapter registra `x-app-usage` e `x-business-use-case-usage` (per-Pagina) e rispetta `Retry-After`.
- **Idempotenza:** nessuna idempotency key end-to-end; si applica la chiave interna di F8.

### Meta — Facebook Groups (notification-only, nessuna connessione API)

- **Natura della destinazione:** **non è una connessione API/OAuth**. È una **destinazione logica notification-only** che l'utente aggiunge manualmente indicando il Gruppo (nome + URL/ID pubblico come etichetta). Postqron **non richiede né conserva un token di pubblicazione** e non riceve alcun grant per postare nel Gruppo.
- **Identificazione e verifica:** la destinazione è identificata dall'URL/handle del Gruppo fornito dall'utente, normalizzato e mostrato come etichetta con un badge `notification-only`. La verifica è **di forma** (URL Facebook Group valido e raggiungibile) e di **intenzione** (l'utente dichiara di esserne membro con diritto di pubblicare); Postqron non può verificare i permessi via API perché l'API non esiste. Nessuna pubblicazione avviene mai lato server.
- **Prerequisito obbligatorio (F9/F23):** la destinazione può essere creata e programmata **solo se** il workspace ha almeno un canale di notifica attivo e verificato — email transazionale (F9) e/o push/deep-link (F23). Senza notifica configurata la destinazione è rifiutata alla creazione: un promemoria non recapitabile equivarrebbe a una pubblicazione persa.
- **Operazioni:** validazione e programmazione come per gli altri canali; all'orario previsto Postqron **non chiama alcuna API Meta** e invia la notifica (F9 email e/o F23 push con deep-link all'app Facebook e contenuto/asset pronti) perché l'utente pubblichi manualmente. Nessun remote ID, nessun retry di pubblicazione; lo stato registra «notifica inviata» ed eventuale conferma manuale.
- **Conteggio F10:** conta come destinazione verso i limiti di piano al pari delle altre, marcata `notification-only`; non consuma quota API di alcun provider.
- **Motivo:** Meta ha dismesso la pubblicazione via Groups API; non esiste auto-publishing né OAuth di publishing compatibile con lo scheduling SaaS.
- **Condizione per promuovere ad auto:** disponibilità di un'API ufficiale di pubblicazione sui Gruppi con OAuth e permessi compatibili.

### Meta — Instagram Business e Creator (auto)

- **Risorsa:** `ig-user-id`; si usa **Instagram Login / Business Login for Instagram**, senza obbligo di collegare una Pagina Facebook.
- **Operazioni:** immagine/Reel via container `POST /{ig-user-id}/media` → attesa stato `FINISHED` → `POST /{ig-user-id}/media_publish`; carousel via container per ogni figlio + container `CAROUSEL` (2–10) → publish; salvare IG Media ID e permalink.
- **OAuth (F5/F8):** `instagram_business_basic`, `instagram_business_content_publish`. **F18:** `instagram_business_manage_insights`.
- **Review/audit:** App Review/Advanced Access; verifica business; nessun post testuale (il media è obbligatorio).
- **Rate limit:** limite di content publishing esposto dall'API (consultato prima di raffiche o quota incerta) oltre agli usage header Graph.
- **Idempotenza:** container ID come ancora di riconciliazione; publish finale non duplicato alla cieca.

### Meta — Instagram personale (notification-only, nessuna connessione API)

- **Natura della destinazione:** **non è una connessione API/OAuth**. Nessuna Content Publishing API è disponibile per gli account personali, quindi è una **destinazione logica notification-only** aggiunta manualmente dall'utente, **senza token di pubblicazione** e senza grant di publishing. La Content Publishing API resta possibile solo convertendo l'account in Professional (Business/Creator), che è un canale auto distinto.
- **Identificazione e verifica:** identificata dallo username Instagram fornito dall'utente, normalizzato e mostrato come etichetta con badge `notification-only`; verifica di forma (username valido) e di intenzione (l'utente dichiara di gestirlo). Postqron non può verificarne i permessi via API.
- **Prerequisito obbligatorio (F9/F23):** creazione e programmazione consentite **solo con** almeno un canale di notifica attivo e verificato (F9 email e/o F23 push/deep-link). Senza notifica la destinazione è rifiutata alla creazione.
- **Operazioni:** all'orario previsto Postqron **non chiama alcuna API Meta** e invia la notifica con contenuto/asset pronti perché l'utente pubblichi manualmente dall'app Instagram. Nessun remote ID, nessun retry di pubblicazione.
- **Conteggio F10:** conta come destinazione verso i limiti di piano, marcata `notification-only`; non consuma quota API.
- **Condizione per promuovere ad auto:** conversione dell'account in Professional (canale IG Business/Creator) oppure un flusso ufficiale di pubblicazione per account personali compatibile con SaaS.

### Meta — Threads (auto)

- **Risorsa:** account Threads.
- **Operazioni:** creazione container `POST /{threads-user-id}/threads` (testo, immagine, video o carousel) → `POST /{threads-user-id}/threads_publish`; catene tramite `reply_to_id`; salvare Threads Media ID e permalink.
- **OAuth (F5/F8):** `threads_basic`, `threads_content_publish`. **F18:** `threads_manage_insights`.
- **Review/audit:** App Review Meta; publishing rate limit dedicato per utente/giorno.
- **Idempotenza:** container ID come ancora; publish finale non duplicato.

### X (auto)

- **Risorsa:** account X.
- **Operazioni:** upload media (endpoint media) poi `POST /2/tweets`; thread tramite `reply.in_reply_to_tweet_id`; salvare Tweet ID.
- **OAuth:** OAuth 2.0 con PKCE; scope `tweet.read`, `tweet.write`, `users.read`, `offline.access`.
- **Review/audit:** nessuna App Review classica ma **tier a sottoscrizione/consumo** (Free/Basic/Pro/Enterprise) con costo e cap mensili di scrittura per progetto e per utente. Il costo per workspace deve essere coperto da F10 prima di dichiarare X disponibile.
- **Rate limit:** cap mensili di POST per tier e rate limit per endpoint; l'adapter legge gli header di rate limit e sospende oltre soglia.
- **Idempotenza:** nessuna key nativa; chiave interna F8.

### LinkedIn — profilo e Pagina (auto)

- **Risorsa:** membro (profilo) oppure organizzazione (Pagina). Autore distinto nel payload.
- **Operazioni:** Posts API `POST /rest/posts` con `author` membro o organizzazione; upload asset immagini/video/documenti via Assets/Images API; salvare l'URN del post.
- **OAuth:** `w_member_social` (profilo), `w_organization_social` (Pagina), più `r_organization_social`/`rw_organization_admin` per gestione/analytics organizzazione dove richiesto.
- **Review/audit:** **Community Management API** con review (Development Tier → Standard Tier), verifica dell'organizzazione e screencast; obbligatoria per pubblicare come organizzazione. L'approvazione esterna è nel percorso critico della sola Pagina.
- **Rate limit:** throttle per applicazione e per membro (limiti giornalieri); rispettare le risposte 429.
- **Idempotenza:** nessuna key nativa; chiave interna F8.

### Pinterest (auto)

- **Risorsa:** board di destinazione (obbligatoria). L'account è collegato ma il pin richiede una board.
- **Operazioni:** `POST /v5/pins` con immagine o video, `board_id`, `title`, `description`, `link`; salvare Pin ID.
- **OAuth:** `boards:read`, `pins:read`, `pins:write` (più scope insight per F18).
- **Review/audit:** trial access → **standard access** tramite app review Pinterest prima dell'uso in produzione a pieno regime.
- **Rate limit:** limiti per app documentati dall'API; rispettare `Retry-After`.
- **Idempotenza:** nessuna key nativa; chiave interna F8.

### TikTok (auto, gate su audit)

- **Risorsa:** account creator.
- **Operazioni:** Content Posting API — inizializzazione, upload del video, `POST /v2/post/publish/...` in modalità **Direct Post**; salvare publish ID/video ID.
- **OAuth:** `video.upload`, `video.publish`.
- **Review/audit:** **audit obbligatorio**. Senza audit i client pubblicano solo contenuti privati/`SELF_ONLY`; anche dopo l'audit esistono cap per creator e per client. TikTok resta **disabilitato fail-closed** finché l'audit non è approvato e i requisiti UX/video non sono pronti.
- **Rate limit:** cap giornalieri per creator e per client; rispettare le finestre indicate.
- **Idempotenza:** publish ID persistito; nessun re-publish cieco.

### Google Business Profile (auto)

- **Risorsa:** location verificata dell'account.
- **Operazioni:** Business Profile API `accounts.locations.localPosts.create` (post `STANDARD`, `EVENT`, `OFFER`, `ALERT`) con `summary`, media e call to action; salvare il nome/ID del localPost.
- **OAuth:** scope `https://www.googleapis.com/auth/business.manage`.
- **Review/audit:** **richiesta di accesso all'API** e approvazione quota da parte di Google prima dell'uso; location verificata.
- **Rate limit:** quote per progetto (QPM) e quota giornaliera di modifica; rispettare i 429/`RESOURCE_EXHAUSTED`.
- **Idempotenza:** nessuna key nativa; chiave interna F8.

### Mastodon (auto)

- **Risorsa:** account su una specifica istanza. Ogni istanza è un provider a sé (host + credenziali app dinamiche).
- **Operazioni:** upload media `POST /api/v2/media` → attesa processing → `POST /api/v1/statuses` (supporta `Idempotency-Key`); catene tramite `in_reply_to_id`; salvare lo status ID e l'URL.
- **OAuth:** registrazione app per istanza; scope `write:statuses` (+ `read` per verifica). Nessuna review centrale: valgono le policy della singola istanza.
- **Rate limit:** limiti per utente e per media letti dall'istanza (`/api/v1/instance`); rispettare gli header di rate limit.
- **Idempotenza:** l'endpoint `statuses` accetta `Idempotency-Key` → usarlo insieme alla chiave interna F8; leggere anche `max_toot_chars`/`configuration` per il limite testo effettivo.

### YouTube Shorts (auto)

- **Risorsa:** canale YouTube.
- **Operazioni:** `videos.insert` con upload resumable; un video è uno Short se verticale e ≤ 3 minuti; salvare il video ID.
- **OAuth:** `https://www.googleapis.com/auth/youtube.upload` (+ `youtube.readonly`/`yt-analytics.readonly` per F18).
- **Review/audit:** progetti non verificati caricano video **privati**; audit/verifica Google per pubblicare come pubblico e per aumentare la quota.
- **Rate limit:** quota giornaliera default 10.000 unità; `videos.insert` ≈ 1.600 unità (≈ 6 upload/giorno di default). L'adapter tratta la quota come risorsa scarsa e non promette più di quanto verificato.
- **Idempotenza:** nessuna key nativa; il video ID persistito è l'ancora; upload resumable ripetibile solo secondo le regole del protocollo.

### Bluesky (auto)

- **Risorsa:** account su un PDS (host AT Protocol).
- **Operazioni:** `com.atproto.repo.uploadBlob` per gli asset → `com.atproto.repo.createRecord` collection `app.bsky.feed.post`; facet per link/menzioni; catene via `reply` (root/parent); salvare URI (`at://`) e CID.
- **OAuth/auth:** OAuth AT Protocol dove supportato, altrimenti **app password**; host del PDS memorizzato con la risorsa.
- **Review/audit:** nessuna review centrale (rete federata); valgono i limiti del PDS.
- **Rate limit:** sistema a punti del PDS (creazioni/aggiornamenti/cancellazioni con budget orario e giornaliero); rispettare gli header di rate limit.
- **Idempotenza:** la rkey/record key generata lato client rende la creazione ripetibile senza duplicare; combinata con la chiave interna F8.

## Contratto del composer

I limiti seguenti sono il **contratto Postqron di lancio**, intenzionalmente più conservativo di alcuni massimi dei provider. F6 valida nel browser per feedback rapido e ripete la stessa validazione lato server prima di salvare come programmato e prima di pubblicare. Un cambiamento del provider può restringere il contratto, mai ampliarlo automaticamente.

Il testo è UTF-8, normalizzato NFC. I conteggi sono per Unicode code point dopo la normalizzazione (per Bluesky per grapheme, come richiede la rete); newline, URL, menzioni, emoji e hashtag rientrano nello stesso limite. Postqron **non** effettua crop o transcoding silenzioso nella prima release: un file non conforme è rifiutato, preservando il controllo creativo e rendendo deterministica la validazione.

| Canale | Testo | Media, codec, dimensioni | Ratio e durata | Note |
| --- | --- | --- | --- | --- |
| Facebook Pages testo/link | 1–5.000 caratteri; max un URL assoluto HTTPS pubblico. | Nessun media. | n/a | Nessuna garanzia su titolo/immagine del link preview. |
| Facebook Pages immagine | Caption 0–5.000. | Una JPEG/PNG sRGB, ≤ 8 MB; larghezza 320–1.440 px. | Da 4:5 a 1,91:1. | GIF animate, WebP e trasparenza esclusi. |
| Facebook Pages / Instagram Reel | Caption ≤ 5.000 (FB) / ≤ 2.200 (IG). | MP4 H.264, audio AAC 48 kHz; 23–60 fps; video ≤ 25 Mbps, audio ≤ 128 kbps; ≤ 100 MB; 720×1.280–1.080×1.920. | 9:16; 4–60 s. | Un solo video, `moov atom` prima dei dati; audio opzionale. |
| Instagram immagine | Caption 0–2.200. | Una JPEG sRGB ≤ 8 MB; larghezza 320–1.440 px. | Da 4:5 a 1,91:1. | Media obbligatorio: non esiste post solo testo. |
| Instagram carousel | Caption 0–2.200. | 2–10 JPEG, ciascuna ≤ 8 MB, totale ≤ 80 MB; larghezza 320–1.440 px. | Stesso ratio per tutti, 4:5–1,91:1. | Carousel misti immagine/video esclusi; ordine salvato vincolante. |
| X post | 1–280 caratteri (contratto di lancio; long-post premium non usato); max un URL. | Fino a 4 immagini JPEG/PNG oppure 1 video oppure 1 GIF. | Immagini ≤ 5 MB; video secondo i limiti X. | Thread come catena di reply; ogni elemento rispetta il limite. |
| LinkedIn (profilo/Pagina) | 1–3.000 caratteri. | Fino a 9 immagini, 1 video o 1 documento (PDF). | Secondo i limiti LinkedIn. | Autore membro o organizzazione; personalizzazione per destinazione. |
| Pinterest pin | Titolo ≤ 100; descrizione ≤ 500. | Una immagine (JPEG/PNG) o un video; board obbligatoria; link opzionale. | Ratio consigliato 2:3 per l'immagine. | Un pin per board; niente pubblicazione senza board. |
| TikTok | Caption ≤ 2.200. | Un video MP4/MOV; photo mode dove supportato. | 9:16 consigliato; durata secondo i limiti TikTok. | Disponibile solo dopo audit; senza audit non pubblicabile pubblicamente. |
| Google Business Profile | Summary ≤ 1.500. | Una immagine o un video per post; CTA opzionale. | Secondo i limiti GBP. | Tipi `STANDARD`/`EVENT`/`OFFER`; niente selezione multi-canale con vincoli incompatibili. |
| Mastodon | 1 – limite dell'istanza (default 500). | Fino a 4 immagini oppure 1 video. | Secondo i limiti dell'istanza. | Limite testo letto dall'istanza; content warning opzionale. |
| YouTube Shorts | Titolo ≤ 100; descrizione ≤ 5.000. | Un video MP4 verticale. | 9:16; ≤ 3 minuti. | Short pubblico solo dopo verifica/audit del progetto. |
| Threads | 1–500 caratteri. | Una immagine o un video; carousel dove supportato. | Secondo i limiti Threads. | Thread come catena; media opzionale. |
| Bluesky | 1–300 grapheme. | Fino a 4 immagini o 1 video; embed esterno opzionale. | Immagini ≤ 1 MB ciascuna (blob). | Facet per link/menzioni; thread via reply root/parent. |

Regole di selezione multi-canale (intersezione dei vincoli):

- Un'immagine condivisa Facebook/Instagram deve essere JPEG e rispettare il contratto Instagram.
- Un Reel condiviso Facebook/Instagram usa il contratto Reel comune.
- Un post solo testo/link non può selezionare canali che richiedono un media (Instagram, Pinterest, TikTok, YouTube).
- Un carousel di sole immagini può selezionare solo Instagram (e Threads dove supportato).
- Caption e asset sono personalizzabili per destinazione; senza override si applica il limite più restrittivo tra le destinazioni scelte.
- Se anche una sola destinazione è invalida, la programmazione è bloccata e l'errore indica canale, campo e regola violata (coerente con US3 e con «Media incompatibili» della sezione Rischi).
- Un canale in **notification publishing** può essere combinato con canali auto: la destinazione notifica produce un promemoria, non una pubblicazione server-side.

La cancellazione di contenuti già pubblicati, la modifica remota, commenti e inbox non fanno parte della prima release. Modificare in Postqron un elemento già pubblicato crea una nuova bozza e non altera il post remoto.

## OAuth, token e review

I permessi sono richiesti progressivamente e separati tra pubblicazione e analytics: i permessi F18 non vengono richiesti al lancio F5/F8. Non vengono richiesti permessi per messaggi, commenti, ads o gestione del business oltre al minimo necessario per pubblicare.

I flussi OAuth usano `state` one-time, PKCE quando supportato, redirect URI esatte e callback server-side (coerente con i requisiti non funzionali di sicurezza). Token e refresh token sono cifrati con chiavi esterne al database, non compaiono in log o URL applicativi e sono accessibili solo all'adapter del provider. Il collegamento salva grant, scope effettivi, scadenza nota, host/risorsa remota, proprietario e data dell'ultima verifica.

Prerequisiti esterni prima di dichiarare un canale disponibile (gate per-provider):

| Provider | Gate esterni principali |
| --- | --- |
| Facebook Pages, Instagram, Threads | App Review Meta, Advanced Access dei permessi usati, verifica organizzazione/dominio, Data Use Checkup. |
| Facebook Groups, Instagram personale | Nessuna connessione API né OAuth di pubblicazione (destinazioni notification-only). Nessun token, nessuna review di pubblicazione; prerequisito obbligatorio: almeno un canale di notifica F9/F23 attivo e verificato. |
| X | Tier a sottoscrizione/consumo attivo e coperto da F10; nessuna review classica. |
| LinkedIn profilo | Accesso Sign In + `w_member_social`. |
| LinkedIn Pagina | Community Management API review, Development→Standard Tier, verifica organizzazione, screencast. |
| Pinterest | Standard access via app review. |
| TikTok | Audit approvato (obbligatorio); requisiti UX/video pronti. |
| Google Business Profile | Richiesta di accesso all'API e approvazione quota; location verificata. |
| Mastodon | Registrazione app e consenso per-istanza; nessuna review centrale. |
| YouTube Shorts | Verifica/audit del progetto per upload pubblici e per aumento quota. |
| Bluesky | Nessuna review centrale; credenziali/PDS validi. |

Prima del go-live pubblico dei canali Meta e degli altri canali soggetti a review sono gate obbligatori: organizzazione e dominio verificati; privacy policy e istruzioni di cancellazione dati pubbliche; Advanced Access/App Review per i permessi usati su risorse non possedute dal team; screencast e credenziali di test che mostrano connessione, selezione risorsa, composer, pubblicazione e disconnessione; ogni requisito periodico del provider; test in sviluppo con risorse di test e smoke test in produzione dopo l'approvazione.

Un controllo giornaliero leggero e ogni errore OAuth verificano token e scope. Token scaduto ma rinnovabile viene rinnovato una sola volta sotto lock; revoca, risorsa non più amministrabile o scope mancante porta il canale a `Da riconnettere` e sospende i job successivi (coerente con US2).

## Rate limit e retry

Le quote dei provider non vengono codificate come costanti di prodotto. L'adapter, per ogni provider:

- legge `Retry-After` quando presente e non ritenta mai prima;
- registra, senza token o dati personali, gli header di utilizzo applicabili (es. Meta `x-app-usage` e `x-business-use-case-usage`, header di rate limit X/LinkedIn/Mastodon, budget a punti Bluesky, unità quota YouTube);
- per Instagram/Threads consulta il limite di content publishing esposto dall'API prima di accettare raffiche o quando la quota locale è incerta;
- mantiene contatori per app e per risorsa in UTC con margine di sicurezza;
- riduce polling e analytics oltre l'80% della quota osservata e sospende operazioni non essenziali oltre il 90%;
- non promette nel piano commerciale (F10) una quota superiore a quella verificata sul provider (rilevante per X e YouTube, a quota/costo vincolati).

Errori `429`, `5xx` sicuramente precedenti alla creazione, timeout di lettura e indisponibilità temporanee usano al massimo cinque tentativi con full jitter e finestre base di 30 s, 2 min, 5 min, 15 min e 30 min, mai prima di `Retry-After`. Errori `400`, `401`, `403` e media rifiutati non sono ritentati automaticamente; auth revocata richiede riconnessione. Esauriti i tentativi la destinazione passa a `Fallito` con causa utile e notifica (F9, US4).

Analytics F18 usa code a priorità inferiore rispetto alle pubblicazioni e non può consumare l'ultima capacità disponibile di una risorsa.

## Idempotenza e riconciliazione

Le API di pubblicazione non sono trattate come se offrissero tutte una idempotency key end-to-end; dove esiste (Mastodon `Idempotency-Key`, record key generata client su Bluesky) viene sfruttata **in aggiunta** alla chiave interna. F8 applica:

1. La chiave interna univoca è `(destination_id, post_revision_id, operation=publish)`. Un lock persistente consente un solo worker attivo; una nuova revisione produce una nuova chiave.
2. Prima della prima chiamata si salva un tentativo con fingerprint immutabile di testo, asset e destinazione.
3. Container ID, upload ID/session, record key e remote post/media ID sono persistiti immediatamente, prima del passo successivo.
4. Ripetere upload o polling con lo stesso ID è consentito solo se la documentazione del passo lo rende sicuro; il comando finale di pubblicazione non viene duplicato alla cieca.
5. Se la connessione cade prima di inviare byte, il tentativo è ritentabile. Se cade dopo un comando di pubblicazione senza risposta, lo stato è `Esito incerto`.
6. Un `Esito incerto` viene riconciliato tramite container/upload status, remote ID già noto o lettura dei contenuti recenti della risorsa. Senza corrispondenza univoca nessun retry automatico crea un possibile duplicato: l'utente vede lo stato e conferma il post remoto o autorizza un nuovo tentativo.
7. Dopo conferma, l'ID remoto è vincolato univocamente alla destinazione; webhook, polling e retry successivi possono solo aggiornare stato e metriche.

Per il **notification publishing** non esiste pubblicazione server-side: lo stato registra «notifica inviata» e, dove il prodotto lo consente, la conferma manuale dell'utente; non ci sono retry di pubblicazione né rischio di duplicato lato server.

Successo o fallimento è registrato per singola destinazione (sezione Rischi «Consistenza multi-canale»): una pubblicazione riuscita su un canale e fallita su un altro non viene annullata né ripubblicata sul primo.

## Rollout fail-closed

Ogni provider è dietro un **feature flag fail-closed**: è invisibile e non collegabile finché non ha credenziali configurate, review/audit approvati (dove richiesti) e smoke test superato. Nessun provider può essere dichiarato disponibile senza credenziali, review e smoke test (vincolo epic #300). La sequenza di attivazione è incrementale e non blocca gli altri canali:

1. **Fondamenta Meta** — Facebook Pages e Instagram Business/Creator (auto), riuso della gestione errori/quote già consolidata. Le destinazioni notification-only Facebook Groups e Instagram personale si abilitano **solo dopo** che F9 (email) e/o F23 (push/deep-link) sono operativi: non hanno connessione API da approvare, ma dipendono dal canale di notifica per funzionare.
2. **Canali auto senza review pesante** — Mastodon e Bluesky (self-serve), poi Threads (App Review Meta nello stesso ecosistema).
3. **Canali auto con review/tier** — LinkedIn profilo, poi LinkedIn Pagina (Community Management API), Pinterest (standard access), Google Business Profile (accesso API), X (tier/costo coperto da F10).
4. **Canali gated** — TikTok solo dopo audit e composer video; YouTube Shorts dopo verifica/audit del progetto e budget di quota.

Se un provider perde l'approvazione, cambia permessi o supera le quote, il suo flag torna a chiuso: l'interfaccia smette di mostrarlo come supportato senza impattare gli altri. Non si ricorre a scraping, automazione browser o credenziali dell'utente per nessun canale.

## Versionamento, verifica e criteri di uscita

L'implementazione deve usare una versione API esplicita e supportata per ogni provider, mai endpoint non versionati. Prima di iniziare l'adapter e a ogni upgrade:

1. confrontare modalità (auto/notifica), permessi, review, endpoint, errori e formati con la documentazione ufficiale del provider;
2. eseguire contract test su una risorsa di test reale del provider;
3. verificare almeno: connessione, un formato rappresentativo, token revocato, quota simulata, timeout ambiguo e riconciliazione; per i canali notifica verificare l'invio del promemoria;
4. aggiornare questo documento se cambia una capacità osservabile dal prodotto (incluso il passaggio di un canale tra auto e notifica);
5. disabilitare tramite feature flag il solo adapter/formato incompatibile, senza bloccare gli altri.

Il go-live di un canale è bloccato finché review, audit, accesso o tier dichiarati non sono approvati. Se un canale non supera il gate, l'interfaccia non lo mostra come supportato.

## Esclusioni esplicite

| Risorsa | Motivo | Condizione per rivalutarla |
| --- | --- | --- |
| Facebook profili personali | Nessuna API ufficiale di pubblicazione per profili personali. | Disponibilità di un'API ufficiale compatibile. |
| Buffer Start Page | Landing link-in-bio, non un social network; non pubblicabile via API. | Nessuna: fuori dall'ambito prodotto. |
| Instagram Stories/Live, Facebook Stories/Live, X Spaces | Permessi, durata, targeting o semantiche differenti. | Decisione e criteri di accettazione dedicati. |
| Annunci, post sponsorizzati, shopping, collaborazioni | Permessi e review distinti, fuori dall'MVP organico. | Decisione di prodotto dedicata. |
| Buffer come dipendenza runtime | Postqron integra direttamente le API dei provider; Buffer è solo riferimento di copertura. | Nessuna: scelta architetturale. |

## Fonti ufficiali

Fonti consultate il 2026-07-30; modalità, limiti, scope, quote e metriche sono **version-sensitive** e vanno ricontrollati all'implementazione di ogni adapter (vedi «Versionamento»).

Riferimento di copertura Buffer (non dipendenza runtime):

- Buffer, [Connecting your channels to Buffer](https://support.buffer.com/article/564-connecting-your-channels-to-buffer).
- Buffer, [Does Buffer have an API?](https://support.buffer.com/article/859-does-buffer-have-an-api).

Fonti ufficiali dei provider:

- Meta, [Pages API — Posts](https://developers.facebook.com/docs/pages-api/posts/), [Page access tokens](https://developers.facebook.com/docs/facebook-login/guides/access-tokens/get-page), [Graph API rate limiting](https://developers.facebook.com/docs/graph-api/overview/rate-limiting/) e [App Review](https://developers.facebook.com/docs/app-review/).
- Meta, [Instagram Content Publishing](https://developers.facebook.com/docs/instagram-platform/content-publishing/) e [Instagram Insights](https://developers.facebook.com/docs/instagram-platform/instagram-api-with-instagram-login/insights/).
- Meta, [Threads API — Posts](https://developers.facebook.com/docs/threads/posts) e [Threads publishing limits](https://developers.facebook.com/docs/threads/troubleshooting#rate-limiting).
- X, [Manage Tweets — POST /2/tweets](https://docs.x.com/x-api/posts/creation-of-a-post) e [API pricing e tier](https://docs.x.com/x-api/getting-started/pricing).
- LinkedIn, [Posts API](https://learn.microsoft.com/en-us/linkedin/marketing/community-management/shares/posts-api), [Community Management App Review](https://learn.microsoft.com/en-us/linkedin/marketing/community-management-app-review) e [API rate limiting](https://learn.microsoft.com/en-us/linkedin/shared/api-guide/concepts/rate-limits).
- Pinterest, [Create Pin — API v5](https://developers.pinterest.com/docs/api/v5/pins-create/) e [App review e access](https://developers.pinterest.com/docs/getting-started/set-up-app/).
- TikTok, [Content Posting API — Direct Post](https://developers.tiktok.com/doc/content-posting-api-media-transfer-guide/) e [Content Sharing Guidelines / audit](https://developers.tiktok.com/doc/content-sharing-guidelines).
- Google, [Business Profile API — Local Posts](https://developers.google.com/my-business/reference/rest/v4/accounts.locations.localPosts) e [richiesta di accesso all'API](https://developers.google.com/my-business/content/prereqs).
- Mastodon, [Statuses API](https://docs.joinmastodon.org/methods/statuses/) e [Rate limits](https://docs.joinmastodon.org/api/rate-limits/).
- YouTube, [Data API — videos.insert](https://developers.google.com/youtube/v3/docs/videos/insert) e [Quota and Compliance Audits](https://developers.google.com/youtube/v3/guides/quota_and_compliance_audits).
- Bluesky / AT Protocol, [Creating a post](https://docs.bsky.app/docs/tutorials/creating-a-post) e [Rate limits](https://docs.bsky.app/docs/advanced-guides/rate-limits).
