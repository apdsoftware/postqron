/**
 * Utility per comporre le richieste verso il backend Go, l'unica origin
 * dinamica del prodotto (docs/SPEC.md §2). La base URL arriva da
 * `runtimeConfig.public.apiBaseUrl` e viene incorporata al momento della build.
 *
 * Qui sta la parte pura: comporre un indirizzo, serializzare una query,
 * classificare un guasto. La richiesta vera la fa `composables/useApi.ts`, che è
 * il solo posto della dashboard da cui parte una `fetch`.
 */

/** Valori ammessi come parametro di query. `undefined` e `null` sono omessi. */
export type QueryValue = string | number | boolean | undefined | null

/**
 * Compone l'URL assoluto di un endpoint dell'API.
 *
 * @param path percorso dell'endpoint, con o senza slash iniziale
 * @param baseUrl origin dell'API, con o senza slash finale
 */
export function apiUrl(path: string, baseUrl: string): string {
  const base = baseUrl.replace(/\/+$/, '')
  const suffix = path.startsWith('/') ? path : `/${path}`
  return `${base}${suffix}`
}

/**
 * Serializza una query string, scartando i parametri non valorizzati.
 *
 * @returns la query con il `?` iniziale, oppure stringa vuota se non c'è nulla
 *   da serializzare
 */
export function buildQuery(params: Record<string, QueryValue>): string {
  const search = new URLSearchParams()
  for (const [key, value] of Object.entries(params)) {
    if (value === undefined || value === null || value === '') continue
    search.append(key, String(value))
  }
  const query = search.toString()
  return query === '' ? '' : `?${query}`
}

/**
 * Come è andata male una richiesta, in termini che l'interfaccia sa mostrare.
 *
 * Sono sei categorie e non il codice HTTP, perché è a questo livello che cambia
 * ciò che si dice all'utente e ciò che si può fare dopo:
 *
 * - `network` — la richiesta non è mai arrivata: backend spento, connessione
 *   assente, CORS. È uno dei due casi in cui «riprova» ha senso senza cambiare
 *   niente.
 * - `unauthorized` — 401. La sessione non c'è o è scaduta: l'utente non deve
 *   leggere un errore, deve tornare all'accesso. Il rimedio è installato in
 *   `useApi()`, che su un 401 chiude la sessione e riporta all'accesso
 *   ricordando dov'era; la categoria resta perché è ciò che permette di
 *   distinguerlo senza guardare il numero in dieci componenti.
 * - `forbidden` — 403. Autenticato ma non autorizzato: riprovare non serve.
 * - `notFound` — 404. La risorsa non c'è (più).
 * - `invalid` — 4xx restante: la richiesta è sbagliata, tipicamente un modulo.
 * - `server` — 5xx. Non è colpa di chi guarda, e riprovare può funzionare.
 */
export type ApiErrorKind
  = | 'network'
    | 'unauthorized'
    | 'forbidden'
    | 'notFound'
    | 'invalid'
    | 'server'

/**
 * Classifica un codice di stato HTTP.
 *
 * Un 3xx non compare fra le categorie perché `fetch` segue i reindirizzamenti
 * da solo: se uno arriva fin qui è una configurazione rotta, e va trattato come
 * un guasto del server invece che ignorato.
 */
export function apiErrorKind(status: number): ApiErrorKind {
  if (status === 401) return 'unauthorized'
  if (status === 403) return 'forbidden'
  if (status === 404) return 'notFound'
  if (status >= 400 && status < 500) return 'invalid'

  return 'server'
}

/**
 * Un motivo di rifiuto ancorato al campo che lo causa — il `details[]` di
 * `ErrorDetail` in `internal/httpapi`.
 *
 * `field` usa la notazione a punti del corpo mandato (`request.url`,
 * `retries.max`), non un nome di colonna: è il percorso dentro il JSON, ed è
 * quello che permette a un modulo di evidenziare il campo giusto senza
 * interpretare una frase.
 *
 * Il `message` che il backend manda accanto **non arriva fin qui**, ed è
 * deliberato: è in italiano, e mostrarlo sarebbe una frase non tradotta in mezzo
 * a cinque lingue (SPEC §8-bis). Ciò che si mostra è il testo di `content/`
 * scelto in base a `code`.
 */
export interface ApiFieldError {
  field: string
  code: string
}

/**
 * Ciò che si può leggere dal corpo di un errore. Tutto facoltativo, perché il
 * corpo di un errore è la cosa che meno si può dare per buona.
 */
export interface ApiErrorPayload {
  code: string | null
  details: ApiFieldError[]
  /**
   * Il limite di piano che è scattato (`jobs`, `resolution`, `retention`…) e il
   * piano su cui è scattato. Sono i due campi su cui un client decide se
   * mostrare un invito ad aggiornare: un `429` tecnico non li ha, apposta,
   * perché lì l'aggiornamento non servirebbe (R10, `quota.go`).
   */
  limit: string | null
  plan: string | null
}

/**
 * Estrae il codice stabile dell'errore dal corpo di una risposta.
 *
 * Il backend risponde `{"error": {"code": "...", "message": "..."}}` e dichiara
 * `code` come **il** campo su cui il client decide: è stabile, non tradotto, e
 * non cambia quando cambia la frase (`ErrorBody` in `internal/httpapi`). Il
 * `message` accanto è in italiano e serve alla diagnostica — mostrarlo sarebbe
 * una frase fissa in mezzo a cinque lingue (SPEC §8-bis).
 *
 * Prende il testo grezzo e non un oggetto già deserializzato perché il corpo di
 * un errore è la cosa che meno si può dare per buona: un 502 del proxy davanti
 * al backend è HTML, un 401 può non avere corpo affatto. Tutto ciò che non è la
 * forma attesa vale `null`, che è esattamente «il backend non ha detto quale
 * errore»: chi chiama ricade sulla categoria HTTP, che c'è sempre.
 */
export function parseErrorCode(body: string): string | null {
  return parseErrorPayload(body).code
}

/**
 * Come `parseErrorCode`, ma legge anche i campi che servono a un modulo:
 * l'elenco dei rifiuti per campo e il limite di piano.
 *
 * Vale la stessa regola di prudenza: qualunque cosa non abbia la forma attesa
 * vale «non detto» — `null` per i codici, lista vuota per i dettagli — e chi
 * chiama ricade sulla categoria HTTP, che c'è sempre. Un 502 del proxy davanti
 * al backend è HTML, e non deve produrre un `undefined` che attraversa
 * l'interfaccia.
 */
export function parseErrorPayload(body: string): ApiErrorPayload {
  const empty: ApiErrorPayload = { code: null, details: [], limit: null, plan: null }

  let payload: unknown
  try {
    payload = JSON.parse(body)
  }
  catch {
    return empty
  }

  if (typeof payload !== 'object' || payload === null) return empty

  const error = (payload as { error?: unknown }).error
  if (typeof error !== 'object' || error === null) return empty

  const text = (key: string): string | null => {
    const value = (error as Record<string, unknown>)[key]
    return typeof value === 'string' && value !== '' ? value : null
  }

  const raw = (error as { details?: unknown }).details
  const details: ApiFieldError[] = Array.isArray(raw)
    ? raw.flatMap((entry) => {
        if (typeof entry !== 'object' || entry === null) return []
        const { field, code } = entry as { field?: unknown, code?: unknown }
        if (typeof field !== 'string' || typeof code !== 'string') return []
        return [{ field, code }]
      })
    : []

  return { code: text('code'), details, limit: text('limit'), plan: text('plan') }
}

/**
 * Guasto di una chiamata all'API.
 *
 * Estende `Error` — così `catch` senza filtri continua a funzionare e lo stack
 * resta leggibile in console — ma ciò che l'interfaccia legge è `kind`, non il
 * messaggio. **Il messaggio non si mostra mai all'utente:** arriva dal backend o
 * da `fetch`, è in inglese, e sarebbe una frase non tradotta in mezzo a cinque
 * lingue (SPEC §8-bis). Serve a chi sviluppa, nella console.
 */
export class ApiError extends Error {
  /** Categoria del guasto, quella su cui l'interfaccia decide cosa mostrare. */
  readonly kind: ApiErrorKind
  /** Codice HTTP, se una risposta è arrivata. `null` per i guasti di rete. */
  readonly status: number | null
  /**
   * Codice dichiarato dal backend, quando la risposta ne portava uno.
   *
   * Sta accanto a `kind` e non al suo posto perché risponde a una domanda
   * diversa. `kind` dice *cosa può fare l'utente adesso* ed è definito per ogni
   * guasto, rete compresa: è su quello che `<AsyncState>` sceglie il messaggio.
   * `code` dice *quale regola del backend è scattata* — `weak_password` invece
   * di `invalid_email` dentro lo stesso 400 — ed esiste solo dove il backend l'ha
   * scritto. Serve ai moduli, che devono indicare il campo sbagliato: senza,
   * l'unica cosa dicibile su un 400 è «controlla i dati inseriti».
   */
  readonly code: string | null
  /**
   * I rifiuti per campo, quando il backend ne ha mandati (`validation_failed`,
   * e i limiti di piano che nominano un campo).
   *
   * Lista vuota è la norma: la maggior parte degli errori non riguarda un campo.
   * Serve ai moduli, che senza direbbero «controlla i dati inseriti» su una
   * schermata con quindici caselle.
   */
  readonly details: readonly ApiFieldError[]
  /** Vedi `ApiErrorPayload.limit`. `null` sui tetti tecnici, apposta. */
  readonly limit: string | null
  /** Vedi `ApiErrorPayload.plan`. */
  readonly plan: string | null
  /** Indirizzo chiamato, per il messaggio in console. */
  readonly url: string

  constructor(
    kind: ApiErrorKind,
    url: string,
    status: number | null,
    message: string,
    payload: Partial<ApiErrorPayload> = {},
  ) {
    const code = payload.code ?? null
    super(`${message} (${kind}${status === null ? '' : ` ${status}`}${code === null ? '' : ` ${code}`}: ${url})`)
    this.name = 'ApiError'
    this.kind = kind
    this.status = status
    this.code = code
    this.details = payload.details ?? []
    this.limit = payload.limit ?? null
    this.plan = payload.plan ?? null
    this.url = url
  }

  /** Guasto di rete: nessuna risposta, quindi niente da leggere. */
  static network(url: string, cause: unknown): ApiError {
    return new ApiError('network', url, null, cause instanceof Error ? cause.message : 'fetch failed')
  }

  /** Risposta arrivata con un codice fuori dal 2xx. */
  static fromStatus(
    url: string,
    status: number,
    statusText: string,
    payload: Partial<ApiErrorPayload> = {},
  ): ApiError {
    return new ApiError(apiErrorKind(status), url, status, statusText || 'HTTP error', payload)
  }
}
