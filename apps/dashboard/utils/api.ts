/**
 * Utility per comporre le richieste verso il backend Go, l'unica origin
 * dinamica del prodotto (docs/SPEC.md §2). La base URL arriva da
 * `runtimeConfig.public.apiBaseUrl` e viene incorporata al momento della build.
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
