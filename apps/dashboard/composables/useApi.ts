import type { QueryValue } from '~/utils/api'
import { ApiError, apiUrl, buildQuery, parseErrorCode } from '~/utils/api'

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
 * - **Le credenziali della sessione** (R14), che qui sono `credentials:
 *   'include'` — vedi sotto.
 * - **La reazione al 401.** Una sessione scaduta non è un errore da mostrare in
 *   mezzo alla pagina: è un ritorno all'accesso. Con un solo punto di uscita è
 *   una condizione sola; sparso, è una condizione per vista.
 * - **La classificazione dei guasti.** Chi chiama riceve un `ApiError` con una
 *   `kind` su cui può decidere, invece di un errore di `fetch` da interpretare.
 *
 * ## Perché non c'è nessun token da mettere in una testata
 *
 * Il seme lasciato dalla issue #24 prevedeva qui tre righe di questa forma:
 *
 * ```
 * const { token } = useSession()
 * if (token.value) headers.authorization = `Bearer ${token.value}`
 * ```
 *
 * **Quel token non esiste, e non deve esistere.** Il backend apre la sessione
 * con un cookie `pq_session` marcato `HttpOnly` (`internal/httpapi/identity.go`):
 * il valore non è leggibile da JavaScript, e non lo è per scelta — «una XSS
 * sulla dashboard non se lo porta via, cosa che accadrebbe se il token stesse in
 * localStorage». Per riempire quella testata bisognerebbe farsi restituire il
 * token in chiaro e conservarlo da qualche parte in pagina, cioè disfare
 * esattamente la difesa che il backend ha costruito. Il corpo del login, non a
 * caso, il token non lo contiene affatto, e un test del backend lo verifica.
 *
 * Al suo posto c'è `credentials: 'include'`, che chiede al browser di allegare
 * il cookie anche quando l'API sta su un'altra origin — ed è il caso in
 * produzione: la dashboard è su Cloudflare Pages, il backend è la VPS. Il lato
 * server è già predisposto (`Access-Control-Allow-Credentials: true` con
 * l'origin esplicito e `Vary: Origin` in `withCORS`), quindi qui non serve
 * nient'altro.
 *
 * Ne discende una proprietà che vale la pena dire per esteso: **la dashboard non
 * conserva la sessione, la osserva.** Ciò che tiene in memoria è la risposta di
 * `/auth/session`, non una credenziale; vedi `composables/useSession.ts`.
 *
 * `Authorization: Bearer` resta la strada dei client che non sono browser e non
 * hanno un cookie jar — la CLI, i CI di chi ci usa. Non è la nostra.
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

/**
 * Cosa fare quando il backend risponde 401.
 *
 * - `signOut` — la sessione non vale più: si chiude lo stato locale e si torna
 *   all'accesso ricordando dove si era. È il comportamento di **ogni** richiesta
 *   che serve a mostrare dati, ed è predefinito proprio per questo: una vista
 *   nuova che si dimentica di dichiararlo si comporta bene lo stesso.
 * - `throw` — il 401 è una risposta prevista di questa chiamata e chi l'ha fatta
 *   sa già cosa farne. Sono le rotte dell'autenticazione stessa: su
 *   `/auth/login` un 401 vuol dire «credenziali sbagliate» e va mostrato nel
 *   modulo, non trasformato in un rimbalzo verso la pagina su cui l'utente si
 *   trova già; su `/auth/session` vuol dire «non sei collegato», che è la
 *   domanda che quella chiamata stava facendo.
 */
export type UnauthorizedPolicy = 'signOut' | 'throw'

export interface ApiRequest {
  method?: ApiMethod
  /** Parametri di query; quelli non valorizzati vengono scartati. */
  query?: Record<string, QueryValue>
  /** Corpo della richiesta, serializzato in JSON. */
  body?: unknown
  /** Per annullare una richiesta che non interessa più. */
  signal?: AbortSignal
  /** Vedi [UnauthorizedPolicy]. Predefinito `signOut`. */
  onUnauthorized?: UnauthorizedPolicy
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

  /*
   * Catturata qui, in modo sincrono, e non dentro `request()` dopo l'`await`:
   * i composable di Nuxt hanno bisogno dell'istanza dell'applicazione, e dopo
   * un'attesa il contesto può non essere più quello.
   *
   * Non c'è ricorsione fra i due moduli: `useSession()` non chiama `useApi()`
   * mentre si costruisce, solo dentro i propri metodi.
   */
  const session = useSession()

  async function request<T>(path: string, options: ApiRequest = {}): Promise<T> {
    const { method = 'GET', query, body, signal, onUnauthorized = 'signOut' } = options
    const url = apiUrl(path, config.apiBaseUrl) + (query ? buildQuery(query) : '')

    const headers: Record<string, string> = { accept: 'application/json' }
    if (body !== undefined) headers['content-type'] = 'application/json'

    let response: Response
    try {
      response = await fetch(url, {
        method,
        headers,
        signal,
        // Il cookie di sessione, che il browser allega da sé: vedi sopra.
        credentials: 'include',
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

    if (!response.ok) {
      /*
       * Il corpo si legge anche quando la richiesta è fallita, e solo per
       * estrarne il `code`: è l'unica cosa che distingue `weak_password` da
       * `invalid_email` dentro lo stesso 400. Non può fallire — `text()` su una
       * risposta senza corpo dà stringa vuota, e `parseErrorCode` restituisce
       * `null` per tutto ciò che non ha la forma attesa.
       */
      const code = parseErrorCode(await response.text().catch(() => ''))
      const error = ApiError.fromStatus(url, response.status, response.statusText, code)

      /*
       * Qui, e in nessun altro posto, la scadenza di una sessione smette di
       * essere un errore da mostrare e diventa un ritorno all'accesso. Il
       * dettaglio di *come* — cosa si ricorda, cosa si dice all'utente — sta in
       * `useSession()`: questo modulo sa solo che è successo.
       */
      if (error.kind === 'unauthorized' && onUnauthorized === 'signOut') session.expire()

      throw error
    }

    // 204 e 205 non hanno corpo: `response.json()` lancerebbe su stringa vuota.
    if (response.status === 204 || response.status === 205) return undefined as T

    return await response.json() as T
  }

  return { request }
}
