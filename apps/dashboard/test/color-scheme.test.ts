import { describe, expect, it } from 'vitest'

import {
  COLOR_SCHEME_BOOT_SCRIPT,
  COLOR_SCHEME_STORAGE_KEY,
  DARK_CLASS,
  resolveColorScheme,
} from '~/utils/color-scheme'

describe('resolveColorScheme', () => {
  it('la scelta dell\'utente prevale sul sistema operativo', () => {
    expect(resolveColorScheme({ stored: 'light', prefersDark: true })).toBe('light')
    expect(resolveColorScheme({ stored: 'dark', prefersDark: false })).toBe('dark')
  })

  it('senza scelta segue il sistema operativo', () => {
    expect(resolveColorScheme({ prefersDark: true })).toBe('dark')
    expect(resolveColorScheme({ prefersDark: false })).toBe('light')
  })

  it('ripiega sul tema chiaro quando non sa', () => {
    // `prefersDark` assente significa che non lo sappiamo — `matchMedia` non
    // c'è — e indovinare il buio è la peggiore delle due scommesse.
    expect(resolveColorScheme({})).toBe('light')
  })

  it('ignora un valore memorizzato che non è un tema', () => {
    // La chiave di `localStorage` sopravvive agli aggiornamenti: un valore
    // scritto da una versione precedente non deve impedire l'avvio.
    expect(resolveColorScheme({ stored: 'sepia', prefersDark: true })).toBe('dark')
    expect(resolveColorScheme({ stored: null, prefersDark: false })).toBe('light')
  })
})

/**
 * Esegue il frammento che `nuxt.config.ts` mette in testa al documento, in un
 * ambiente finto, e dice se ha acceso il tema scuro.
 *
 * Il frammento gira prima di qualunque modulo e quindi non può importarne: le
 * sue tre righe duplicano per forza la precedenza di `resolveColorScheme()`.
 * Questo è il presidio che tiene allineate le due copie.
 */
function runBootScript(stored: string | null, prefersDark: boolean): boolean {
  const classes = new Set<string>()

  const localStorage = {
    getItem: (key: string) => (key === COLOR_SCHEME_STORAGE_KEY ? stored : null),
  }
  const matchMedia = (query: string) => ({
    matches: query.includes('dark') ? prefersDark : false,
  })
  const document = {
    documentElement: { classList: { add: (name: string) => classes.add(name) } },
  }

  new Function('localStorage', 'matchMedia', 'document', COLOR_SCHEME_BOOT_SCRIPT)(
    localStorage,
    matchMedia,
    document,
  )

  return classes.has(DARK_CLASS)
}

describe('frammento che applica il tema prima del primo pixel', () => {
  const cases: [string | null, boolean][] = [
    ['dark', false],
    ['dark', true],
    ['light', false],
    ['light', true],
    [null, false],
    [null, true],
    ['sepia', false],
    ['sepia', true],
  ]

  it.each(cases)('decide come resolveColorScheme (memorizzato %s, sistema scuro %s)', (stored, prefersDark) => {
    expect(runBootScript(stored, prefersDark)).toBe(
      resolveColorScheme({ stored, prefersDark }) === 'dark',
    )
  })

  it('non si rompe se lo storage non è disponibile', () => {
    // Safari in navigazione privata, cookie di terze parti bloccati in un
    // iframe: `localStorage` lancia sull'accesso. Un'eccezione qui fermerebbe
    // lo script prima del primo pixel — cioè romperebbe l'avvio per difendere
    // un dettaglio estetico.
    const classes = new Set<string>()
    const document = {
      documentElement: { classList: { add: (name: string) => classes.add(name) } },
    }

    expect(() => {
      new Function('localStorage', 'matchMedia', 'document', COLOR_SCHEME_BOOT_SCRIPT)(
        { getItem: () => { throw new Error('SecurityError') } },
        () => ({ matches: true }),
        document,
      )
    }).not.toThrow()

    expect(classes.size).toBe(0)
  })
})
