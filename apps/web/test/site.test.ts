import { describe, expect, it } from 'vitest'

import { canonicalUrl } from '~/utils/site'

describe('canonicalUrl', () => {
  it('unisce origin e percorso', () => {
    expect(canonicalUrl('/prezzi', 'https://postqron.com')).toBe('https://postqron.com/prezzi')
  })

  it('non duplica gli slash', () => {
    expect(canonicalUrl('/prezzi', 'https://postqron.com/')).toBe('https://postqron.com/prezzi')
    expect(canonicalUrl('prezzi', 'https://postqron.com')).toBe('https://postqron.com/prezzi')
  })

  it('rimuove lo slash finale dal percorso', () => {
    expect(canonicalUrl('/faq/', 'https://postqron.com')).toBe('https://postqron.com/faq')
  })

  it('mantiene lo slash per la home', () => {
    expect(canonicalUrl('/', 'https://postqron.com')).toBe('https://postqron.com/')
    expect(canonicalUrl('', 'https://postqron.com')).toBe('https://postqron.com/')
  })
})
