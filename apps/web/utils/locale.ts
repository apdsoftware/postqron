/**
 * Lingue del sito pubblico e regole per passare dall'una all'altra.
 *
 * Il sito è generato staticamente (SPEC §2): non esiste un server che legga
 * `Accept-Language`. Di conseguenza la lingua sta nel percorso — `/en/`, `/it/`,
 * … — e tutto ciò che riguarda il rilevamento avviene nel browser (SPEC §8-bis).
 *
 * Questo modulo è deliberatamente privo di dipendenze da Vue e da Nuxt: è
 * logica pura, quindi verificabile senza montare nulla.
 */

/** Codici ISO 639-1 delle lingue supportate. L'ordine è quello del selettore. */
export const LOCALE_CODES = ['en', 'it', 'es', 'de', 'fr'] as const

export type LocaleCode = (typeof LOCALE_CODES)[number]

/**
 * Lingua predefinita e **sorgente** dei contenuti (SPEC §8-bis): si scrive in
 * inglese e si traduce, non il contrario. È anche la destinazione di
 * `x-default` e il ripiego quando il browser non chiede nessuna delle cinque.
 */
export const DEFAULT_LOCALE: LocaleCode = 'en'

export interface LocaleDescriptor {
  code: LocaleCode
  /**
   * Nome della lingua **nella lingua stessa**. Non è contenuto traducibile: in
   * un selettore «Deutsch» si chiama così anche per chi legge in italiano, ed è
   * l'unica forma che chi non capisce la lingua corrente sa riconoscere.
   */
  label: string
  /** Valore dell'attributo `lang` del documento. */
  htmlLang: string
}

export const LOCALES: readonly LocaleDescriptor[] = [
  { code: 'en', label: 'English', htmlLang: 'en' },
  { code: 'it', label: 'Italiano', htmlLang: 'it' },
  { code: 'es', label: 'Español', htmlLang: 'es' },
  { code: 'de', label: 'Deutsch', htmlLang: 'de' },
  { code: 'fr', label: 'Français', htmlLang: 'fr' },
]

/**
 * Chiave della scelta esplicita dell'utente in `localStorage`.
 *
 * R32: la scelta fatta col selettore prevale sul rilevamento e persiste fra le
 * visite. Il valore è il solo codice della lingua.
 */
export const LOCALE_STORAGE_KEY = 'postqron:locale'

export function isLocaleCode(value: unknown): value is LocaleCode {
  return typeof value === 'string' && (LOCALE_CODES as readonly string[]).includes(value)
}

/**
 * Sceglie la lingua a partire dalle preferenze del browser (R31).
 *
 * Confronta il solo sottotag primario: `it-CH` e `it` vogliono entrambi
 * l'italiano, e non abbiamo varianti regionali da distinguere. Se nessuna delle
 * preferenze corrisponde si usa l'inglese.
 *
 * @param preferred lista in ordine di preferenza, tipicamente `navigator.languages`
 */
export function detectLocale(preferred: readonly string[] | undefined): LocaleCode {
  for (const tag of preferred ?? []) {
    const primary = tag.toLowerCase().split('-')[0]
    if (isLocaleCode(primary)) return primary
  }

  return DEFAULT_LOCALE
}

/** Separa la parte di percorso dall'ancora, che va tenuta in coda. */
function splitHash(path: string): [string, string] {
  const index = path.indexOf('#')
  return index === -1 ? [path, ''] : [path.slice(0, index), path.slice(index)]
}

/**
 * Antepone la lingua a un percorso del sito.
 *
 * I percorsi in `content/` sono scritti **senza lingua** (`/`, `/#pricing`,
 * `/pricing`) perché la stessa voce di menu vale per tutte e cinque: è qui che
 * diventano `/it/`, `/it/#pricing`, `/it/pricing/`.
 *
 * Il risultato termina sempre con `/` prima dell'eventuale ancora: ogni rotta
 * pre-renderizzata è una directory con dentro `index.html`, e su Cloudflare
 * Pages la forma senza slash finale è un redirect verso quella con lo slash.
 * Un `canonical` che punta a un redirect è un canonical sbagliato.
 */
export function localePath(path: string, locale: LocaleCode): string {
  const [rawPath, hash] = splitHash(path)
  const segments = rawPath.split('/').filter(Boolean)

  return `/${[locale, ...segments].join('/')}/${hash}`
}

/**
 * Toglie il prefisso di lingua da un percorso, restituendo la forma neutra
 * accettata da `localePath`. Serve al selettore, che deve costruire l'indirizzo
 * della pagina corrente nelle altre quattro lingue.
 */
export function stripLocale(path: string): string {
  const [rawPath, hash] = splitHash(path)
  const segments = rawPath.split('/').filter(Boolean)

  if (isLocaleCode(segments[0])) segments.shift()

  return segments.length === 0 ? `/${hash}` : `/${segments.join('/')}/${hash}`
}

/**
 * Estrae la lingua da un percorso, o `null` se non ne ha una.
 *
 * Sul sito statico ogni pagina vive sotto un prefisso: l'unico percorso senza
 * lingua è la radice, che non ha contenuto e si limita a smistare.
 */
export function localeFromPath(path: string): LocaleCode | null {
  const [rawPath] = splitHash(path)
  const first = rawPath.split('/').filter(Boolean)[0]

  return isLocaleCode(first) ? first : null
}
