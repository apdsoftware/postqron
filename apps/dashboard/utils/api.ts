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
  let payload: unknown
  try {
    payload = JSON.parse(body)
  }
  catch {
    return null
  }

  if (typeof payload !== 'object' || payload === null) return null

  const error = (payload as { error?: unknown }).error
  if (typeof error !== 'object' || error === null) return null

  const code = (error as { code?: unknown }).code
  return typeof code === 'string' && code !== '' ? code : null
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
  /** Indirizzo chiamato, per il messaggio in console. */
  readonly url: string

  constructor(
    kind: ApiErrorKind,
    url: string,
    status: number | null,
    message: string,
    code: string | null = null,
  ) {
    super(`${message} (${kind}${status === null ? '' : ` ${status}`}${code === null ? '' : ` ${code}`}: ${url})`)
    this.name = 'ApiError'
    this.kind = kind
    this.status = status
    this.code = code
    this.url = url
  }

  /** Guasto di rete: nessuna risposta, quindi nessun codice. */
  static network(url: string, cause: unknown): ApiError {
    return new ApiError('network', url, null, cause instanceof Error ? cause.message : 'fetch failed')
  }

  /** Risposta arrivata con un codice fuori dal 2xx. */
  static fromStatus(url: string, status: number, statusText: string, code: string | null = null): ApiError {
    return new ApiError(apiErrorKind(status), url, status, statusText || 'HTTP error', code)
  }
}
