import { describe, expect, it } from 'vitest'

import {
  DEFAULT_LOCALE,
  LOCALES,
  LOCALE_CODES,
  detectLocale,
  isLocaleCode,
  localeFromPath,
  localePath,
  stripLocale,
} from '~/utils/locale'

describe('LOCALES', () => {
  it('descrive tutte e sole le lingue dichiarate', () => {
    expect(LOCALES.map(entry => entry.code)).toEqual([...LOCALE_CODES])
  })

  it('parte dall\'inglese, che è la lingua sorgente', () => {
    expect(DEFAULT_LOCALE).toBe('en')
    expect(LOCALE_CODES[0]).toBe('en')
  })
})

describe('isLocaleCode', () => {
  it('accetta i cinque codici e rifiuta tutto il resto', () => {
    expect(isLocaleCode('it')).toBe(true)
    expect(isLocaleCode('pt')).toBe(false)
    expect(isLocaleCode('EN')).toBe(false)
    expect(isLocaleCode(undefined)).toBe(false)
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

describe('localePath', () => {
  it('antepone la lingua e chiude con lo slash', () => {
    expect(localePath('/', 'en')).toBe('/en/')
    expect(localePath('/pricing', 'de')).toBe('/de/pricing/')
    expect(localePath('/pricing/', 'de')).toBe('/de/pricing/')
  })

  it('tiene l\'ancora in coda, dopo lo slash', () => {
    expect(localePath('/#pricing', 'it')).toBe('/it/#pricing')
    expect(localePath('/legal/privacy#data', 'fr')).toBe('/fr/legal/privacy/#data')
  })
})

describe('stripLocale', () => {
  it('riporta il percorso alla forma neutra di content/', () => {
    expect(stripLocale('/it/')).toBe('/')
    expect(stripLocale('/it/#pricing')).toBe('/#pricing')
    expect(stripLocale('/es/pricing/')).toBe('/pricing/')
  })

  it('lascia intatto un percorso che non ha lingua', () => {
    expect(stripLocale('/')).toBe('/')
    expect(stripLocale('/pricing/')).toBe('/pricing/')
  })

  it('è l\'inverso di localePath per ogni lingua', () => {
    for (const code of LOCALE_CODES) {
      expect(stripLocale(localePath('/#api', code))).toBe('/#api')
      expect(stripLocale(localePath('/pricing', code))).toBe('/pricing/')
    }
  })
})

describe('localeFromPath', () => {
  it('riconosce il prefisso di lingua', () => {
    expect(localeFromPath('/de/')).toBe('de')
    expect(localeFromPath('/de/pricing/')).toBe('de')
  })

  it('non ne trova nessuna sulla radice, che non ha lingua propria', () => {
    expect(localeFromPath('/')).toBeNull()
    expect(localeFromPath('/pricing/')).toBeNull()
  })
})
