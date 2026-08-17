import { describe, expect, it } from 'vitest'

import { publicPages } from '~/content/pages'
import { siteContent } from '~/content'
import { LOCALE_CODES } from '~/utils/locale'
import { PUBLIC_PAGE_IDS, isPublicPageId } from '~/utils/public-pages'

describe('pagine pubbliche di contenuto', () => {
  it('esistono in tutte le lingue con le stesse sezioni', () => {
    for (const locale of LOCALE_CODES) {
      expect(Object.keys(publicPages[locale]).sort()).toEqual([...PUBLIC_PAGE_IDS].sort())
      expect(publicPages[locale].features.features).toHaveLength(4)
      expect(publicPages[locale].features.showcases).toHaveLength(2)
      expect(publicPages[locale].faq.items).toHaveLength(6)
      expect(publicPages[locale].contact.details).toHaveLength(4)
    }
  })

  it('riconosce soltanto le tre rotte pubbliche', () => {
    expect(PUBLIC_PAGE_IDS.every(isPublicPageId)).toBe(true)
    expect(isPublicPageId('pricing')).toBe(false)
  })

  it('collega header e footer alle nuove rotte in ogni lingua', () => {
    for (const locale of LOCALE_CODES) {
      const serializedNav = JSON.stringify(siteContent[locale].nav)
      expect(serializedNav).toContain('/features')
      expect(serializedNav).toContain('/faq')
      expect(serializedNav).toContain('/contact')
    }
  })

  it('usa solo il recapito societario approvato', () => {
    for (const locale of LOCALE_CODES) {
      expect(siteContent[locale].company.email).toBe('hello@postqron.com')
      expect(publicPages[locale].contact.details[0]).toMatchObject({
        value: 'hello@postqron.com',
        href: 'mailto:hello@postqron.com',
      })
    }
  })

  it('riporta nelle FAQ i limiti senza promettere una prova', () => {
    for (const locale of LOCALE_CODES) {
      const answers = publicPages[locale].faq.items.map(item => item.answer).join(' ')
      expect(answers).toContain('20')
      expect(publicPages[locale].faq.items).toHaveLength(6)
    }

    expect(publicPages.en.faq.items[0]?.answer).toContain('HTTP')
    expect(publicPages.en.faq.items[1]?.answer).toContain('one second')
    expect(publicPages.en.faq.items[2]?.answer).toContain('90 days')
    // R58 ha deciso la regola: si sospende tutto e sceglie l'utente. Prima
    // questo test presidiava l'ammissione di non sapere; ora presidia le due
    // parti che rendono la regola accettabile — che la scelta sia dell'utente
    // e che nulla venga cancellato.
    expect(publicPages.en.faq.items[3]?.answer).toContain('you choose')
    expect(publicPages.en.faq.items[3]?.answer).toContain('Nothing is deleted')
    expect(publicPages.en.faq.items[4]?.answer).toContain('no trial period')
  })
})
