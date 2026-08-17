import { describe, expect, it } from 'vitest'

import {
  DEFAULT_LOCALE,
  LOCALES,
  LOCALE_CODES,
  LOCALE_STORAGE_KEY,
  detectLocale,
  isLocaleCode,
  resolveLocale,
} from '~/utils/locale'

describe('LOCALES', () => {
  it('descrive tutte e sole le lingue dichiarate', () => {
    expect(LOCALES.map(entry => entry.code)).toEqual([...LOCALE_CODES])
  })

  it('parte dall\'inglese, che è la lingua sorgente', () => {
    expect(DEFAULT_LOCALE).toBe('en')
    expect(LOCALE_CODES[0]).toBe('en')
  })

  it('nomina ogni lingua nella lingua stessa', () => {
    expect(LOCALES.map(entry => entry.label)).toEqual([
      'English',
      'Italiano',
      'Español',
      'Deutsch',
      'Français',
    ])
  })
})

describe('isLocaleCode', () => {
  it('accetta i cinque codici e rifiuta tutto il resto', () => {
    expect(isLocaleCode('it')).toBe(true)
    expect(isLocaleCode('pt')).toBe(false)
    expect(isLocaleCode('EN')).toBe(false)
    expect(isLocaleCode(undefined)).toBe(false)
    expect(isLocaleCode(null)).toBe(false)
    expect(isLocaleCode(['it'])).toBe(false)
  })
})

describe('detectLocale', () => {
  it('sceglie la prima preferenza che corrisponde', () => {
    expect(detectLocale(['pt-BR', 'es-ES', 'en'])).toBe('es')
  })

  it('confronta il solo sottotag primario', () => {
    expect(detectLocale(['it-CH'])).toBe('it')
    expect(detectLocale(['DE-at'])).toBe('de')
  })

  it('ripiega sull\'inglese quando nessuna preferenza corrisponde', () => {
    expect(detectLocale(['pt-BR', 'ja'])).toBe('en')
    expect(detectLocale([])).toBe('en')
    expect(detectLocale(undefined)).toBe('en')
  })
})

describe('resolveLocale', () => {
  it('usa il browser quando non c\'è nient\'altro (R31)', () => {
    expect(resolveLocale({ browser: ['fr-FR', 'en'] })).toBe('fr')
    expect(resolveLocale({})).toBe('en')
  })

  it('fa prevalere la scelta esplicita sul rilevamento (R32)', () => {
    // È il punto del selettore: chi ha scelto l'italiano su un browser tedesco
    // continua a vedere l'italiano, altrimenti la scelta non sarebbe una scelta.
    expect(resolveLocale({ stored: 'it', browser: ['de-DE'] })).toBe('it')
  })

  it('fa prevalere il profilo utente su tutto il resto (R33)', () => {
    // Punto di innesto della issue #445: oggi `profile` è sempre `null`, e
    // questo test descrive il contratto che il backend dovrà rispettare.
    expect(resolveLocale({ profile: 'es', stored: 'it', browser: ['de-DE'] })).toBe('es')
  })

  it('ignora i valori non validi invece di fidarsene', () => {
    // `stored` arriva da `localStorage`, `profile` da una risposta dell'API:
    // entrambi possono contenere il residuo di una versione precedente o una
    // lingua che non supportiamo, e nessuno dei due deve rompere l'avvio.
    expect(resolveLocale({ stored: 'pt', browser: ['de-DE'] })).toBe('de')
    expect(resolveLocale({ profile: 'zz', stored: 'it' })).toBe('it')
    expect(resolveLocale({ profile: null, stored: null, browser: ['it'] })).toBe('it')
    expect(resolveLocale({ stored: { code: 'it' }, browser: ['ja'] })).toBe('en')
  })
})

describe('LOCALE_STORAGE_KEY', () => {
  it('è spaziata col nome del prodotto', () => {
    // La chiave convive con quelle di altre librerie sulla stessa origin.
    expect(LOCALE_STORAGE_KEY).toBe('postqron:locale')
  })
})
