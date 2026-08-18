import { describe, expect, it } from 'vitest'

import type { DashboardContent } from '~/types/content'
import { dashboardContent } from '~/content'
import { MIN_PASSWORD_LENGTH } from '~/utils/auth'
import { DEFAULT_LOCALE, LOCALE_CODES } from '~/utils/locale'

const entries = LOCALE_CODES.map(code => [code, dashboardContent[code]] as const)
const source = dashboardContent[DEFAULT_LOCALE]
const translations = entries.filter(([code]) => code !== DEFAULT_LOCALE)

/**
 * Percorsi di tutte le foglie di un oggetto di testi — `home.title`, … —
 * ordinati.
 *
 * Il confronto è ricorsivo e non sulle sole chiavi di primo livello: una
 * traduzione a cui manca `home.checking` ha comunque `home`, e un confronto
 * superficiale la darebbe per completa.
 */
function keyPaths(value: unknown, prefix = ''): string[] {
  if (typeof value !== 'object' || value === null) return [prefix]

  return Object.entries(value)
    .flatMap(([key, child]) => keyPaths(child, prefix ? `${prefix}.${key}` : key))
    .sort()
}

/** Valori di tutte le foglie, nell'ordine dei rispettivi percorsi. */
function leaves(content: DashboardContent): string[] {
  return keyPaths(content).map((path) => {
    return path
      .split('.')
      .reduce<unknown>((node, key) => (node as Record<string, unknown>)[key], content) as string
  })
}

describe('testi', () => {
  it('esistono per tutte e cinque le lingue', () => {
    expect(Object.keys(dashboardContent).sort()).toEqual([...LOCALE_CODES].sort())
  })

  it.each(entries)('%s ha esattamente le chiavi della lingua sorgente', (_code, content) => {
    expect(keyPaths(content)).toEqual(keyPaths(source))
  })

  it.each(entries)('%s non lascia nessun testo vuoto', (_code, content) => {
    for (const value of leaves(content)) {
      expect(typeof value).toBe('string')
      expect(value.trim()).not.toBe('')
    }
  })

  it.each(translations)('%s è tradotto e non una copia dell\'inglese', (_code, content) => {
    // Un file creato per copia e mai tradotto compila, supera il confronto
    // delle chiavi e si nota solo aprendo la pagina. Qui si nota prima.
    expect(content).not.toEqual(source)
  })

  it.each(entries)('%s dichiara la lunghezza minima della password davvero richiesta', (_code, content) => {
    // Il requisito è scritto a mano in cinque frasi, e il numero dentro deve
    // restare quello del backend: cambiarlo senza toccare le traduzioni
    // prometterebbe una password che il backend rifiuta, in cinque lingue.
    expect(content.auth.fields.passwordHint).toContain(String(MIN_PASSWORD_LENGTH))
  })

  it.each(translations)('%s traduce il nome del selettore di lingua', (_code, content) => {
    // È l'unica etichetta che deve essere leggibile *prima* di aver scelto la
    // lingua giusta: se resta in inglese, chi non lo parla non trova il modo di
    // uscirne.
    expect(content.shell.languageLabel).not.toBe(source.shell.languageLabel)
  })
})
