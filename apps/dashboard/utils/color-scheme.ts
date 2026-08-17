/**
 * Tema chiaro e scuro della dashboard.
 *
 * Il template Flowbite è disegnato in due temi: ogni sua classe ha una variante
 * `dark:`. Non è una funzione in più da aggiungere quando ci sarà tempo — è il
 * modo in cui il template è scritto, e una dashboard che nasce solo chiara
 * accumula componenti senza varianti `dark:` finché riaccenderlo costa una
 * revisione di tutto. Per questo il tema esiste dalla prima schermata.
 *
 * Come per la lingua, questo modulo è privo di dipendenze da Vue e da Nuxt: la
 * regola di precedenza è logica pura e si verifica senza montare niente. Il
 * trasporto — `localStorage`, `matchMedia`, la classe sul documento — sta in
 * `composables/useColorScheme.ts`.
 */

export const COLOR_SCHEMES = ['light', 'dark'] as const

export type ColorScheme = (typeof COLOR_SCHEMES)[number]

/**
 * Classe che accende il tema scuro sull'elemento radice.
 *
 * È la variante che il template dichiara (`@custom-variant dark` in
 * `assets/css/theme.css`): una classe e non `prefers-color-scheme`, perché la
 * media query non è sovrascrivibile da chi vuole il contrario solo qui.
 */
export const DARK_CLASS = 'dark'

/**
 * Chiave della scelta esplicita dell'utente in `localStorage`.
 *
 * Il template usa `color-theme`; qui il nome è nel nostro spazio, accanto a
 * `postqron:locale`. Le due preferenze hanno la stessa natura — scelte
 * dell'interfaccia che sopravvivono alla visita — e devono stare nello stesso
 * posto per essere trovate insieme quando serviranno al profilo utente (R33).
 */
export const COLOR_SCHEME_STORAGE_KEY = 'postqron:color-scheme'

export function isColorScheme(value: unknown): value is ColorScheme {
  return typeof value === 'string' && (COLOR_SCHEMES as readonly string[]).includes(value)
}

export interface ColorSchemePreferences {
  /** Scelta esplicita fatta con l'interruttore in una visita precedente. */
  stored?: unknown
  /** `prefers-color-scheme: dark` del sistema operativo. */
  prefersDark?: boolean
}

/**
 * Decide il tema: prima la scelta dell'utente, poi il sistema operativo.
 *
 * L'ordine è lo stesso della lingua e per lo stesso motivo: chi ha scelto ha
 * detto qualcosa di più preciso di quanto dica il suo sistema. Il ripiego è il
 * tema chiaro e non il sistema: `prefersDark` assente significa che non lo
 * sappiamo — `matchMedia` non c'è, o siamo nei test — e in quel caso indovinare
 * il buio è la scommessa peggiore delle due.
 */
export function resolveColorScheme({ stored, prefersDark }: ColorSchemePreferences): ColorScheme {
  if (isColorScheme(stored)) return stored

  return prefersDark === true ? 'dark' : 'light'
}

/**
 * Lo stesso ordine di precedenza, in una forma che gira **prima di Vue**.
 *
 * Con `ssr: false` l'HTML servito è un guscio vuoto: il browser dipinge lo
 * sfondo del documento e solo dopo l'applicazione si monta e mette la classe.
 * Per chi ha il tema scuro sarebbe un lampo bianco a ogni caricamento — il
 * difetto che tutto il resto del lavoro sul tema esiste per non avere.
 * `nuxt.config.ts` inserisce questo frammento in testa al documento.
 *
 * **È una duplicazione, e non c'è modo di evitarla:** uno script che deve girare
 * prima di qualunque modulo non può importarne uno. Ciò che si può evitare è che
 * le due copie divergano in silenzio, e sono due presidî: le costanti arrivano
 * da qui, e `test/color-scheme.test.ts` esegue questo frammento confrontandolo
 * con `resolveColorScheme()` su tutte le combinazioni di ingresso.
 *
 * Non usa `resolveColorScheme` né `isColorScheme` per la stessa ragione per cui
 * non importa: deve essere una stringa autosufficiente.
 */
export const COLOR_SCHEME_BOOT_SCRIPT = `
try {
  var stored = localStorage.getItem(${JSON.stringify(COLOR_SCHEME_STORAGE_KEY)})
  var dark = stored === 'dark'
    || (stored !== 'light' && matchMedia('(prefers-color-scheme: dark)').matches)
  if (dark) document.documentElement.classList.add(${JSON.stringify(DARK_CLASS)})
} catch (error) {
  /* Storage o matchMedia non disponibili: resta il tema chiaro. */
}
`.trim()
