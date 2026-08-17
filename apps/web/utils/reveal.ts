/**
 * Comparsa allo scroll — il rimpiazzo di scrollReveal.js.
 *
 * Nel template ogni blocco animato porta un attributo come
 * `data-scroll-reveal="enter bottom move 50px over 0.6s after 0.2s"`, che una
 * libreria di 20 kB interpreta a runtime. Qui la stessa informazione è un
 * oggetto tipizzato, e l'animazione la fa il CSS: al JavaScript resta solo
 * decidere *quando* aggiungere una classe.
 */

/** Lato da cui l'elemento entra. `bottom` significa che parte più in basso. */
export type RevealDirection = 'top' | 'bottom' | 'left' | 'right'

export interface RevealOptions {
  direction?: RevealDirection
  /** Corsa dello spostamento, con unità CSS. */
  distance?: string
  /** Durata in secondi. */
  duration?: number
  /** Ritardo in secondi: è così che il tema scala le card di una griglia. */
  delay?: number
}

export const revealDefaults = {
  direction: 'bottom',
  distance: '50px',
  duration: 0.6,
  delay: 0,
} as const satisfies Required<RevealOptions>

/**
 * Traduce le opzioni nelle variabili CSS lette da `.pq-reveal`.
 *
 * È una funzione pura, condivisa fra il rendering lato server e quello lato
 * client, perché l'HTML pre-renderizzato deve già contenere lo stato iniziale:
 * altrimenti il contenuto comparirebbe e poi verrebbe rinascosto all'idratazione.
 */
export function revealStyle(options: RevealOptions = {}): Record<string, string> {
  const { direction, distance, duration, delay } = { ...revealDefaults, ...options }

  const style: Record<string, string> = {
    '--pq-reveal-duration': `${duration}s`,
    '--pq-reveal-delay': `${delay}s`,
  }

  if (direction === 'bottom' || direction === 'top') {
    style['--pq-reveal-y'] = direction === 'bottom' ? distance : `-${distance}`
  }
  else {
    style['--pq-reveal-x'] = direction === 'right' ? distance : `-${distance}`
  }

  return style
}

/** Serializza lo stile per l'attributo `style` dell'HTML lato server. */
export function revealStyleAttribute(options: RevealOptions = {}): string {
  return Object.entries(revealStyle(options))
    .map(([property, value]) => `${property}:${value}`)
    .join(';')
}
