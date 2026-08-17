import { describe, expect, it } from 'vitest'

import type { NavItem, SiteContent } from '~/types/content'
import { siteContent } from '~/content'
import { DEFAULT_LOCALE, LOCALE_CODES, localeFromPath } from '~/utils/locale'

const entries = LOCALE_CODES.map(code => [code, siteContent[code]] as const)
const source = siteContent[DEFAULT_LOCALE]

/** Tutte le destinazioni interne dichiarate nei contenuti di una lingua. */
function internalLinks(content: SiteContent): string[] {
  const fromNav = (items: readonly NavItem[]): string[] =>
    items.flatMap(item => [...(item.to ? [item.to] : []), ...(item.children ? fromNav(item.children) : [])])

  return [
    ...fromNav(content.nav.main),
    content.nav.cta.to,
    ...content.nav.footer.flatMap(group => fromNav(group.items)),
    ...content.apiBand.channels.map(channel => channel.to),
    ...content.plans.map(plan => plan.ctaTo),
    ...content.articles.map(article => article.to),
  ]
}

describe('contenuti', () => {
  it('esistono per tutte e cinque le lingue', () => {
    expect(Object.keys(siteContent).sort()).toEqual([...LOCALE_CODES].sort())
  })

  it.each(entries)('%s ha le stesse sezioni della lingua sorgente', (_code, content) => {
    expect(Object.keys(content).sort()).toEqual(Object.keys(source).sort())
    expect(Object.keys(content.ui).sort()).toEqual(Object.keys(source.ui).sort())
  })

  it.each(entries)('%s ha lo stesso numero di blocchi della lingua sorgente', (_code, content) => {
    // Una traduzione a cui manca una card non si vede in una build che passa:
    // si vede in una pagina più corta delle altre quattro.
    expect(content.features).toHaveLength(source.features.length)
    expect(content.showcases).toHaveLength(source.showcases.length)
    expect(content.testimonials).toHaveLength(source.testimonials.length)
    expect(content.plans).toHaveLength(source.plans.length)
    expect(content.stats).toHaveLength(source.stats.length)
    expect(content.articles).toHaveLength(source.articles.length)
    expect(content.apiBand.channels).toHaveLength(source.apiBand.channels.length)
  })

  it.each(entries)('%s scrive i link senza prefisso di lingua', (_code, content) => {
    // Il prefisso lo mette `localePath()` al rendering. Se fosse già nel dato,
    // le altre quattro lingue linkerebbero questa.
    for (const link of internalLinks(content)) {
      expect(link.startsWith('/')).toBe(true)
      expect(localeFromPath(link)).toBeNull()
    }
  })
})

describe('piani', () => {
  it.each(entries)('%s espone i quattro piani pubblici di SPEC §8', (_code, content) => {
    expect(content.plans.map(plan => plan.name)).toEqual(['Free', 'Pro', 'Team', 'Agency'])
  })

  it.each(entries)('%s tiene i prezzi approvati, che non sono testo da tradurre', (_code, content) => {
    // SPEC §8. R61: gli importi non seguono la lingua, sono gli stessi in tutte
    // e cinque — la conversione è competenza di Paddle, non nostra.
    expect(content.plans.map(plan => plan.price)).toEqual(['0', '9', '29', '79'])
    expect(content.plans.map(plan => plan.currency)).toEqual(['€', '€', '€', '€'])
  })

  it.each(entries)('%s non conserva residui in dollari', (_code, content) => {
    // R61: «ogni simbolo `$` è un residuo da correggere».
    expect(JSON.stringify(content)).not.toContain('$')
  })

  it.each(entries)('%s dichiara l\'imposta accanto al prezzo', (_code, content) => {
    // R61-bis: gli importi sono al netto, e una cifra senza indicazione
    // dell'imposta è un difetto.
    expect(content.money.taxNote.trim()).not.toBe('')
  })

  it('scrive l\'imposta nella lingua di ciascuna localizzazione', () => {
    // È testo tradotto e non un suffisso fisso: se lo fosse, il tedesco
    // sarebbe il primo a smentirlo.
    const notes = Object.fromEntries(entries.map(([code, content]) => [code, content.money.taxNote]))

    expect(notes).toEqual({
      en: '+ VAT',
      it: '+ IVA',
      es: '+ IVA',
      de: '+ MwSt.',
      fr: '+ TVA',
    })
  })

  it('mette il simbolo di valuta dove vuole la lingua', () => {
    // La valuta non segue la lingua (R61), la sua collocazione sì: «€9» in
    // inglese, «9 €» nelle altre quattro.
    const positions = Object.fromEntries(
      entries.map(([code, content]) => [code, content.money.currencyPosition]),
    )

    expect(positions).toEqual({
      en: 'before',
      it: 'after',
      es: 'after',
      de: 'after',
      fr: 'after',
    })
  })

  it.each(entries)('%s dichiara Agency come soglia e non come prezzo fisso', (_code, content) => {
    const agency = content.plans.at(-1)!

    expect(agency.name).toBe('Agency')
    expect(agency.pricePrefix).toBeTruthy()
  })

  it.each(entries)('%s confronta i piani sulle stesse voci', (_code, content) => {
    const lengths = content.plans.map(plan => plan.features.length)

    expect(new Set(lengths).size).toBe(1)
    expect(lengths[0]).toBe(source.plans[0]!.features.length)
  })

  it.each(entries)('%s mette in evidenza un solo piano', (_code, content) => {
    expect(content.plans.filter(plan => plan.featured)).toHaveLength(1)
  })
})

describe('testimonianze', () => {
  it.each(entries)('%s marca come segnaposto ogni citazione inventata', (_code, content) => {
    // Finché non arrivano citazioni reali devono essere marcate tutte: è il
    // dato su cui il percorso di deploy (#426) può decidere di fermarsi.
    expect(content.testimonials.every(testimonial => testimonial.placeholder)).toBe(true)
  })
})
