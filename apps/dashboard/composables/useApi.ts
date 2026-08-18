import type { QueryValue } from '~/utils/api'
import { ApiError, apiUrl, buildQuery } from '~/utils/api'

/**
 * L'unico punto della dashboard da cui parte una richiesta al backend.
 *
 * ## Perché uno solo
 *
 * La dashboard non ha un server proprio: `ssr: false`, nessun Nitro in
 * produzione (SPEC §2). Ogni dato passa da qui, e «qui» deve restare uno perché
 * è il posto in cui vanno appese le cose che valgono per *tutte* le chiamate e
 * che nessuno si ricorderebbe di ripetere:
 *
 * - **Le credenziali della sessione** (issue #25, R14). Vanno in `headers`
 *   sotto, in tre righe. Se invece ogni pagina chiamasse `fetch` da sé, quelle
 *   tre righe andrebbero in una dozzina di posti e la pagina dimenticata
 *   sarebbe quella che nessuno prova.
 * - **La reazione al 401.** Una sessione scaduta non è un errore da mostrare in
 *   mezzo alla pagina: è un ritorno all'accesso. Con un solo punto di uscita è
 *   una condizione sola; sparso, è una condizione per vista.
 * - **La classificazione dei guasti.** Chi chiama riceve un `ApiError` con una
 *   `kind` su cui può decidere, invece di un errore di `fetch` da interpretare.
 *
 * ## Perché `fetch` e non `$fetch`
 *
 * `$fetch` di Nuxt aggiunge ritentativi e interceptor, ma restituisce un
 * `FetchError` la cui forma cambia a seconda di dove si è rotto — ed è
 * esattamente ciò che questo modulo esiste per normalizzare. Il ritentativo
 * automatico, poi, è una scelta che va presa per endpoint: su una scrittura non
 * idempotente ripetere è peggio che fallire (R53, idempotenza sulle scritture).
 * Qui non si ritenta niente da soli: chi vuole riprovare lo chiede, e lo chiede
 * l'utente premendo «riprova».
 */

/** Metodi che la dashboard usa. Non è l'elenco di HTTP: è ciò che serve. */
export type ApiMethod = 'GET' | 'POST' | 'PATCH' | 'PUT' | 'DELETE'

export interface ApiRequest {
  method?: ApiMethod
  /** Parametri di query; quelli non valorizzati vengono scartati. */
  query?: Record<string, QueryValue>
  /** Corpo della richiesta, serializzato in JSON. */
  body?: unknown
  /** Per annullare una richiesta che non interessa più. */
  signal?: AbortSignal
}

export interface DashboardApi {
  /**
   * Esegue la richiesta e restituisce il corpo deserializzato.
   *
   * @throws {ApiError} sempre e solo questo, sia che la risposta non sia
   *   arrivata sia che sia arrivata fuori dal 2xx.
   */
  request: <T>(path: string, options?: ApiRequest) => Promise<T>
}

export function useApi(): DashboardApi {
  const { public: config } = useRuntimeConfig()

  async function request<T>(path: string, options: ApiRequest = {}): Promise<T> {
    const { method = 'GET', query, body, signal } = options
    const url = apiUrl(path, config.apiBaseUrl) + (query ? buildQuery(query) : '')

    const headers: Record<string, string> = { accept: 'application/json' }
    if (body !== undefined) headers['content-type'] = 'application/json'

    /*
     * Punto di innesto della sessione (issue #25):
     *   const { token } = useSession()
     *   if (token.value) headers.authorization = `Bearer ${token.value}`
     * Sta qui e non in un plugin globale perché il token va solo alla nostra
     * origin: un interceptor su `fetch` lo manderebbe a chiunque.
     */

    let response: Response
    try {
      response = await fetch(url, {
        method,
        headers,
        signal,
        body: body === undefined ? undefined : JSON.stringify(body),
      })
    }
    catch (cause) {
      /*
       * `AbortError` non è un guasto: è qualcuno che ha cambiato pagina. Va
       * rilanciato com'è, perché chi chiama lo riconosce e non deve mostrarlo.
       */
      if (cause instanceof DOMException && cause.name === 'AbortError') throw cause

      throw ApiError.network(url, cause)
    }

    if (!response.ok) throw ApiError.fromStatus(url, response.status, response.statusText)

    // 204 e 205 non hanno corpo: `response.json()` lancerebbe su stringa vuota.
    if (response.status === 204 || response.status === 205) return undefined as T

    return await response.json() as T
  }

  return { request }
}
