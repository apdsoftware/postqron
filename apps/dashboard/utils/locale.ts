/**
 * Lingue della dashboard e regole per sceglierne una (SPEC §8-bis, R31–R33).
 *
 * La dashboard **non** è il sito pubblico, e la differenza si vede tutta qui.
 * Il sito è pre-renderizzato: la lingua sta nel percorso — `/en/`, `/it/`, … —
 * perché ogni lingua è un insieme di file diverso, e il selettore è un link che
 * cambia indirizzo. La dashboard ha `ssr: false` (vedi `nuxt.config.ts`): è un
 * guscio unico servito su ogni rotta, dietro autenticazione. Non ha pagine da
 * pre-renderizzare per lingua, non va indicizzata, e quindi non ha alcun motivo
 * per mettere la lingua nell'URL: nessun crawler deve trovarne cinque varianti
 * e nessun `hreflang` deve dichiararle.
 *
 * Ne discende che la lingua qui è **stato dell'applicazione**, non parte
 * dell'indirizzo: vive in `useLocale()`, persiste in `localStorage` e cambia
 * senza navigare. Il rovescio della medaglia — due schede della stessa
 * dashboard non possono stare su lingue diverse — è coerente con dove la lingua
 * andrà a finire: nel profilo utente (R33), dove è una preferenza sola per
 * persona e non una proprietà della pagina che si sta guardando.
 *
 * Questo modulo è deliberatamente privo di dipendenze da Vue e da Nuxt: è
 * logica pura, quindi verificabile senza montare nulla.
 */

/** Codici ISO 639-1 delle lingue supportate. L'ordine è quello del selettore. */
export const LOCALE_CODES = ['en', 'it', 'es', 'de', 'fr'] as const

export type LocaleCode = (typeof LOCALE_CODES)[number]

/**
 * Lingua predefinita e **sorgente** dei contenuti (SPEC §8-bis): si scrive in
 * inglese e si traduce, non il contrario. È anche il ripiego quando il browser
 * non chiede nessuna delle cinque.
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
 *
 * È la stessa chiave del sito pubblico di proposito: se un giorno le due app
 * finissero sulla stessa origin, chi ha scelto lo spagnolo su postqron.com si
 * ritroverebbe la dashboard in spagnolo, che è il comportamento voluto. Finché
 * stanno su sottodomini diversi i due valori restano semplicemente separati.
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

/**
 * Le tre sorgenti da cui può arrivare la lingua, in ordine di autorità.
 *
 * Ogni campo è `unknown` per costruzione: sono valori che entrano dall'esterno
 * — una risposta dell'API, una chiave di `localStorage` scritta da una versione
 * precedente, la lista del browser — e la validazione è compito di questo
 * modulo, non di chi lo chiama.
 */
export interface LocalePreferences {
  /**
   * **Punto di innesto di R33** (issue #445, backend).
   *
   * La lingua che l'utente autenticato ha impostato nel proprio profilo vale su
   * tutte le sue sessioni: è una preferenza della persona, non del browser da
   * cui si è collegata oggi. Per questo sta in cima all'ordine e batte sia il
   * rilevamento sia la scelta locale — altrimenti un nuovo portatile
   * mostrerebbe una lingua diversa dal telefono, pur essendo lo stesso utente.
   *
   * Oggi vale sempre `null`: l'API del profilo non esiste ancora. Quando
   * esisterà, l'unico punto da cambiare è `useLocale()`, che passa qui il campo
   * `locale` del profilo; il selettore, a quel punto, oltre a `setLocale()`
   * dovrà anche scriverlo sul profilo, così che la scelta segua la persona.
   */
  profile?: unknown
  /** Scelta esplicita fatta col selettore in una visita precedente (R32). */
  stored?: unknown
  /** Preferenze del browser, tipicamente `navigator.languages` (R31). */
  browser?: readonly string[]
}

/**
 * Applica l'ordine di precedenza fra le sorgenti: profilo, scelta memorizzata,
 * browser, e infine l'inglese.
 *
 * È l'unico posto in cui quell'ordine è scritto. Sta qui, in logica pura,
 * perché è la regola che i requisiti descrivono davvero (R31–R33) ed è la sola
 * cosa che vale la pena verificare senza un browser: tutto il resto —
 * `localStorage`, `navigator`, lo stato di Vue — è trasporto.
 */
export function resolveLocale({ profile, stored, browser }: LocalePreferences): LocaleCode {
  if (isLocaleCode(profile)) return profile
  if (isLocaleCode(stored)) return stored

  return detectLocale(browser)
}
