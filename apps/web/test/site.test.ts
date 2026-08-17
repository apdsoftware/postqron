import { describe, expect, it } from 'vitest'

import { LOCALE_CODES } from '~/utils/locale'
import { alternateLinks, canonicalUrl, interpolate } from '~/utils/site'

describe('canonicalUrl', () => {
  it('unisce origin e percorso', () => {
    expect(canonicalUrl('/it/pricing', 'https://postqron.com')).toBe('https://postqron.com/it/pricing/')
  })

  it('non duplica gli slash', () => {
    expect(canonicalUrl('/it/', 'https://postqron.com/')).toBe('https://postqron.com/it/')
    expect(canonicalUrl('it', 'https://postqron.com')).toBe('https://postqron.com/it/')
  })

  it('chiude sempre con lo slash', () => {
    // Ogni rotta pre-renderizzata è una directory con dentro `index.html`, e la
    // forma senza slash finale è un redirect: dichiararla canonica significa
    // indicare ai motori un indirizzo che risponde 308.
    expect(canonicalUrl('/en/faq', 'https://postqron.com')).toBe('https://postqron.com/en/faq/')
    expect(canonicalUrl('/', 'https://postqron.com')).toBe('https://postqron.com/')
    expect(canonicalUrl('', 'https://postqron.com')).toBe('https://postqron.com/')
  })

  it('scarta l\'ancora, che non è una pagina diversa', () => {
    expect(canonicalUrl('/it/#pricing', 'https://postqron.com')).toBe('https://postqron.com/it/')
  })
})

describe('alternateLinks', () => {
  const links = alternateLinks('/', 'https://postqron.com')

  it('dichiara una traduzione per lingua, più x-default', () => {
    expect(links.map(link => link.hreflang)).toEqual([...LOCALE_CODES, 'x-default'])
  })

  it('comprende la lingua stessa: senza autoreferenza il gruppo non è chiuso', () => {
    expect(links).toContainEqual({ hreflang: 'it', href: 'https://postqron.com/it/' })
  })

  it('manda x-default sull\'inglese, come il rilevamento', () => {
    expect(links.at(-1)).toEqual({ hreflang: 'x-default', href: 'https://postqron.com/en/' })
  })

  it('conserva il percorso in tutte le lingue', () => {
    for (const link of alternateLinks('/pricing', 'https://postqron.com')) {
      expect(link.href).toMatch(/^https:\/\/postqron\.com\/[a-z]{2}\/pricing\/$/)
    }
  })
})

describe('interpolate', () => {
  it('sostituisce i segnaposto', () => {
    expect(interpolate('Photo of {name}', { name: 'Elena Nardi' })).toBe('Photo of Elena Nardi')
  })

  it('lascia intatto un segnaposto senza valore', () => {
    expect(interpolate('Photo of {name}', {})).toBe('Photo of {name}')
  })
})
