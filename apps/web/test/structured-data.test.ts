import { describe, expect, it } from 'vitest'

import { siteContent } from '~/content'
import { ORGANIZATION_ADDRESS, ORGANIZATION_VAT_ID } from '~/content/organization'
import { publicPages } from '~/content/pages'
import { LOCALE_CODES } from '~/utils/locale'
import {
  faqPageNode,
  organizationNode,
  softwareApplicationNode,
  structuredData,
} from '~/utils/structured-data'

const SITE = 'https://postqron.com'

interface Graph {
  '@context': string
  '@graph': Record<string, unknown>[]
}

/** Rilegge il documento come lo rileggerebbe un motore: parsing, non ispezione. */
function parse(json: string): Graph {
  return JSON.parse(json) as Graph
}

/** Ogni valore annidato nel grafo, per le asserzioni su ciò che non deve esserci. */
function keys(value: unknown): string[] {
  if (Array.isArray(value)) return value.flatMap(keys)
  if (value === null || typeof value !== 'object') return []

  return Object.entries(value as Record<string, unknown>)
    .flatMap(([key, nested]) => [key, ...keys(nested)])
}

describe('structuredData', () => {
  it('produce un grafo JSON-LD rileggibile', () => {
    const graph = parse(structuredData([organizationNode(siteContent.en, 'en', SITE)]))

    expect(graph['@context']).toBe('https://schema.org')
    expect(graph['@graph']).toHaveLength(1)
  })

  it('neutralizza il < che chiuderebbe lo <script> in anticipo', () => {
    const json = structuredData([{ '@type': 'Thing', 'name': 'a </script> b' }])

    expect(json).not.toContain('</script>')
    // Resta JSON valido e il parser lo rilegge identico: `<` è un carattere
    // come un altro, e la sequenza di escape è ammessa ovunque in una stringa.
    expect((parse(json)['@graph'][0] as { name: string }).name).toBe('a </script> b')
  })
})

describe('organizationNode', () => {
  it.each(LOCALE_CODES)('%s dichiara i campi che il sito mostra', (locale) => {
    const node = organizationNode(siteContent[locale], locale, SITE) as Record<string, never>
    const content = siteContent[locale]

    expect(node['@type']).toBe('Organization')
    expect(node['@id']).toBe(`${SITE}/${locale}/#organization`)
    expect(node.name).toBe(content.company.name)
    expect(node.legalName).toBe(content.company.legalName)
    expect(node.description).toBe(content.company.about)
    expect(node.email).toBe(content.company.email)
    expect(node.url).toBe(`${SITE}/${locale}/`)
  })

  it('usa il marchio come logo, con un indirizzo che è un file e non una directory', () => {
    const logo = organizationNode(siteContent.en, 'en', SITE).logo as Record<string, unknown>

    expect(logo['@type']).toBe('ImageObject')
    // `/apple-touch-icon.png/` sarebbe un 404 e un'anteprima muta.
    expect(logo.url).toBe(`${SITE}/apple-touch-icon.png`)
  })

  it.each(LOCALE_CODES)('%s: l\'indirizzo strutturato non diverge da quello mostrato', (locale) => {
    // I dati strutturati vogliono i campi separati, la pagina una riga sola:
    // sono due forme dello stesso indirizzo e devono restare lo stesso
    // indirizzo. Il codice paese resta fuori — è `IT`, mentre la riga scrive
    // «Italy», «Italia», «Italien»…
    const shown = siteContent[locale].company.address

    for (const part of [
      ORGANIZATION_ADDRESS.streetAddress,
      ORGANIZATION_ADDRESS.postalCode,
      ORGANIZATION_ADDRESS.addressLocality,
      ORGANIZATION_ADDRESS.addressRegion,
      ORGANIZATION_VAT_ID,
    ]) {
      expect(shown).toContain(part)
    }
  })
})

describe('softwareApplicationNode', () => {
  const node = softwareApplicationNode(siteContent.en, 'en', SITE) as Record<string, never>
  const offers = node.offers as unknown as Record<string, never>[]

  it('si riferisce all\'organizzazione per @id, non ripetendola', () => {
    expect(node['@type']).toBe('SoftwareApplication')
    expect(node.provider).toEqual({ '@id': `${SITE}/en/#organization` })
  })

  it('ha un\'offerta per piano, più l\'annuale di Pro', () => {
    // R62: la fatturazione annuale esiste solo su Pro. Cinque offerte per
    // quattro piani è esattamente questo.
    expect(offers).toHaveLength(siteContent.en.plans.length + 1)
    expect(offers.map(offer => offer.name))
      .toEqual(['Free', 'Pro', 'Pro', 'Team', 'Agency'])
  })

  it.each(LOCALE_CODES)('%s espone gli stessi importi in euro (R61)', (locale) => {
    const localized = softwareApplicationNode(siteContent[locale], locale, SITE)
    const localizedOffers = localized.offers as Record<string, unknown>[]

    // I prezzi non seguono la lingua: le cinque localizzazioni mostrano gli
    // stessi importi, e il catalogo Paddle è la fonte di verità.
    expect(localizedOffers.map(offer => offer.priceSpecification))
      .toEqual(offers.map(offer => offer.priceSpecification))

    for (const offer of localizedOffers) {
      expect(offer.priceCurrency).toBe('EUR')
      expect((offer.priceSpecification as Record<string, unknown>).priceCurrency).toBe('EUR')
    }
  })

  it('dichiara i prezzi imposte escluse (R61-bis)', () => {
    // La nota accanto al prezzo è testo tradotto e un motore non la legge:
    // `valueAddedTaxIncluded` è l'unico posto in cui la qualifica esiste per lui.
    for (const offer of offers) {
      const specification = offer.priceSpecification as unknown as Record<string, unknown>
      expect(specification['@type']).toBe('UnitPriceSpecification')
      expect(specification.valueAddedTaxIncluded).toBe(false)
    }
  })

  it('riprende gli importi da content, senza reinserirli a mano', () => {
    const pro = siteContent.en.plans.find(plan => plan.name === 'Pro')!

    expect(offers[1]!.price).toBe(Number(pro.price))
    expect(offers[2]!.price).toBe(Number(pro.annual!.price))
    expect((offers[1]!.priceSpecification as unknown as Record<string, unknown>).referenceQuantity)
      .toEqual({ '@type': 'QuantitativeValue', 'value': 1, 'unitCode': 'MON' })
    expect((offers[2]!.priceSpecification as unknown as Record<string, unknown>).referenceQuantity)
      .toEqual({ '@type': 'QuantitativeValue', 'value': 1, 'unitCode': 'ANN' })
  })

  it('dichiara Agency come soglia, non come prezzo fisso', () => {
    // La pagina dice «da €79/mese» (SPEC §8): un `price` prometterebbe una
    // cifra esatta che il prodotto non ha.
    const agency = offers.at(-1)!
    const specification = agency.priceSpecification as unknown as Record<string, unknown>

    expect(agency.price).toBeUndefined()
    expect(specification.price).toBeUndefined()
    expect(specification.minPrice).toBe(79)
  })

  it('non dichiara valutazioni, recensioni o numeri di utenti che non abbiamo', () => {
    // Un `aggregateRating` inventato è una dichiarazione falsa a un motore di
    // ricerca (issue #473). Il prezzo è rinunciare al risultato arricchito.
    const present = new Set(keys(node))

    for (const forbidden of ['aggregateRating', 'review', 'ratingValue', 'reviewCount',
      'interactionStatistic', 'userInteractionCount', 'sameAs']) {
      expect(present).not.toContain(forbidden)
    }
  })
})

describe('faqPageNode', () => {
  const url = `${SITE}/en/faq/`

  it.each(LOCALE_CODES)('%s dichiara le domande che la pagina mostra, tutte', (locale) => {
    const items = publicPages[locale].faq.items
    const node = faqPageNode(items, url)
    const questions = node.mainEntity as Record<string, unknown>[]

    expect(node['@type']).toBe('FAQPage')
    expect(questions).toHaveLength(items.length)

    for (const [index, item] of items.entries()) {
      expect(questions[index]).toEqual({
        '@type': 'Question',
        'name': item.question,
        'acceptedAnswer': { '@type': 'Answer', 'text': item.answer },
      })
    }
  })

  it('riporta la risposta intera, non un riassunto', () => {
    // Un riassunto sarebbe di nuovo un contenuto che la pagina non ha.
    const items = publicPages.en.faq.items
    const questions = faqPageNode(items, url).mainEntity as Record<string, unknown>[]
    const answers = questions.map(question =>
      (question.acceptedAnswer as Record<string, unknown>).text,
    )

    expect(answers).toEqual(items.map(item => item.answer))
  })
})
