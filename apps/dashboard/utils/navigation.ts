/**
 * Registro della navigazione della dashboard.
 *
 * È l'unico posto in cui è scritto **quali sezioni esistono**. La barra
 * laterale non contiene un elenco di link: itera su questo. Aggiungere una
 * sezione — i cronjob (#26), i log (#27), il billing (#28), le impostazioni
 * (#29) — è una voce qui, una pagina in `pages/`, e una chiave di testo in
 * `content/`; il resto (evidenziazione della voce corrente, cassetto mobile,
 * ordine, accessibilità) è già fatto e non va rifatto per ogni sezione.
 *
 * I tre campi sono deliberatamente stretti:
 *
 * - `id` è la chiave del testo in `content.shell.nav`, e i tipi impongono che
 *   esista in tutte e cinque le lingue: una sezione senza traduzioni non
 *   compila, invece di comparire in inglese a un utente tedesco (SPEC §8-bis).
 * - `path` è la rotta, e deve corrispondere a una pagina reale: la dashboard è
 *   una SPA con fallback su `index.html` (`public/_redirects`), quindi una voce
 *   che punta al nulla non dà 404 al server — dà la pagina «non trovata» dopo
 *   il caricamento, che è peggio perché sembra un guasto.
 * - `icon` è un nome del registro in `utils/icons.ts`, non un percorso SVG:
 *   così le icone stanno tutte insieme e il registro resta leggibile.
 *
 * Questo modulo non dipende da Vue né da Nuxt: è dato e logica pura, quindi
 * verificabile senza montare niente (`test/navigation.test.ts`).
 */
import type { IconName } from '~/utils/icons'

/**
 * Identificatori delle sezioni.
 *
 * Sono anche le chiavi dei testi: `NavId` è ciò che lega il registro ai file di
 * traduzione, ed è il motivo per cui è un'unione letterale e non `string`.
 */
export const NAV_IDS = ['overview'] as const

export type NavId = (typeof NAV_IDS)[number]

export interface NavEntry {
  id: NavId
  path: string
  icon: IconName
}

/**
 * Le sezioni della dashboard, nell'ordine in cui compaiono nella barra laterale.
 *
 * Oggi ce n'è una sola, e l'elenco è corto perché è onesto: le altre arrivano
 * con le issue che le implementano. Una voce che rimanda a una pagina «in
 * arrivo» sarebbe contenuto segnaposto in produzione, che SPEC R37 vieta.
 */
export const NAVIGATION: readonly NavEntry[] = [
  { id: 'overview', path: '/', icon: 'overview' },
]

/**
 * Dice se una voce di menu è quella della pagina che si sta guardando.
 *
 * La regola non è l'uguaglianza fra i percorsi, e il motivo si vede appena
 * esiste una sezione con un dettaglio: chi guarda `/jobs/42` sta in «Cronjob», e
 * una barra laterale che non evidenzia niente gli fa perdere il punto in cui si
 * trova. Il confronto è quindi sul prefisso, ma **con il segmento intero**:
 * senza quel vincolo `/jobs` risulterebbe attiva anche su `/jobs-archive`.
 *
 * La radice è l'eccezione necessaria: come prefisso corrisponderebbe a tutto, e
 * la panoramica risulterebbe attiva su ogni pagina del sito. Vale solo esatta.
 *
 * @param entryPath percorso dichiarato nel registro
 * @param currentPath percorso corrente, tipicamente `useRoute().path`
 */
export function isActivePath(entryPath: string, currentPath: string): boolean {
  // Uno slash finale non cambia la pagina: `/jobs` e `/jobs/` sono la stessa.
  const entry = entryPath.replace(/\/+$/, '')
  const current = currentPath.replace(/\/+$/, '')

  if (entry === '') return current === ''

  return current === entry || current.startsWith(`${entry}/`)
}
