/**
 * Dati strutturati JSON-LD (R53-ter).
 *
 * **La regola che governa questo file: si dichiara solo ciò che la pagina
 * mostra.** I dati strutturati sono un'affermazione fatta a un motore di
 * ricerca, e un'affermazione che il contenuto visibile non sostiene è una
 * violazione delle sue linee guida — penalizzabile, non furba.
 *
 * Da qui discendono tre assenze deliberate, che non sono dimenticanze:
 *
 * - **nessun `aggregateRating` e nessun `review`.** Non abbiamo recensioni.
 *   Senza, `SoftwareApplication` non ottiene il risultato arricchito con le
 *   stelle: è il prezzo giusto da pagare. Inventarne uno sarebbe una
 *   dichiarazione falsa, non un dettaglio di marketing (issue #473).
 * - **nessun `sameAs`.** I profili social di `content/social.ts` sono
 *   segnaposto in attesa dei valori definitivi (SPEC §7, Q3): dichiararli
 *   come profili ufficiali dell'organizzazione significherebbe affermare che
 *   esistono.
 * - **nessuna `interactionStatistic`.** Non pubblichiamo numeri di utenti.
 *
 * I prezzi vengono da `content/<lingua>.ts`, che è ciò che la pagina rende, e
 * sono in euro in tutte le lingue (R61). Sono **al netto delle imposte**
 * (R61-bis): nello schema si dichiara con `valueAddedTaxIncluded: false`, che
 * è l'unico modo di dirlo — la nota accanto al prezzo è testo, e un motore non
 * la legge.
 */

import type { FaqItem, PricingPlan, SiteContent } from '~/types/content'
import type { LocaleCode } from '~/utils/locale'
import { ORGANIZATION_ADDRESS, ORGANIZATION_VAT_ID } from '~/content/organization'
import { localePath } from '~/utils/locale'
import { assetUrl, canonicalUrl } from '~/utils/site'

/** Un nodo del grafo: JSON, quindi niente di più preciso di questo. */
export type SchemaNode = Record<string, unknown>

/** Il prodotto si chiama così in tutte le lingue: è un marchio, non un testo. */
const BRAND = 'Postqron'

/**
 * `unitCode` UN/CEFACT dei due periodi di fatturazione: mese e anno.
 * Non si deducono da `plan.period`, che è testo tradotto («/month», «/mese»):
 * si sanno per struttura, perché è SPEC §8 a stabilire che i piani sono
 * mensili e R62 che l'annuale esiste solo su Pro.
 */
const MONTHLY = 'MON'
const ANNUAL = 'ANN'

function homeUrl(locale: LocaleCode, siteUrl: string): string {
  return canonicalUrl(localePath('/', locale), siteUrl)
}

/** Identificatore dell'organizzazione, per riferirla dagli altri nodi. */
export function organizationId(locale: LocaleCode, siteUrl: string): string {
  return `${homeUrl(locale, siteUrl)}#organization`
}

/**
 * L'impresa che gestisce Postqron.
 *
 * Nome, descrizione, recapito e indirizzo sono gli stessi che il piè di pagina
 * e la pagina dei contatti mostrano; il logo è il marchio (`design/marchio/`),
 * servito come icona dell'applicazione.
 */
export function organizationNode(
  content: SiteContent,
  locale: LocaleCode,
  siteUrl: string,
): SchemaNode {
  return {
    '@type': 'Organization',
    '@id': organizationId(locale, siteUrl),
    'name': content.company.name,
    'legalName': content.company.legalName,
    'description': content.company.about,
    'url': homeUrl(locale, siteUrl),
    'email': content.company.email,
    'vatID': ORGANIZATION_VAT_ID,
    'address': { '@type': 'PostalAddress', ...ORGANIZATION_ADDRESS },
    'logo': {
      '@type': 'ImageObject',
      'url': assetUrl('/apple-touch-icon.png', siteUrl),
      'width': 180,
      'height': 180,
    },
  }
}

/**
 * Un'offerta per piano, e una seconda per l'annuale di Pro.
 *
 * `price` c'è solo quando la cifra è esatta. Agency è dichiarata «da €79/mese»
 * (SPEC §8): un `price: 79` prometterebbe un prezzo fisso che non è quello, e
 * il campo giusto per una soglia è `minPrice`.
 */
function offerNodes(plan: PricingPlan, url: string): SchemaNode[] {
  const exact = plan.pricePrefix === undefined

  const specification = (amount: string, unitCode: string): SchemaNode => ({
    '@type': 'UnitPriceSpecification',
    'priceCurrency': 'EUR',
    ...(exact ? { price: Number(amount) } : { minPrice: Number(amount) }),
    // R61-bis: gli importi sono imposte escluse, Paddle aggiunge quella dovuta
    // nel paese del cliente in quanto Merchant of Record.
    'valueAddedTaxIncluded': false,
    'referenceQuantity': { '@type': 'QuantitativeValue', 'value': 1, unitCode },
  })

  const offer = (amount: string, unitCode: string): SchemaNode => ({
    '@type': 'Offer',
    'name': plan.name,
    url,
    'priceCurrency': 'EUR',
    ...(exact ? { price: Number(amount) } : {}),
    'priceSpecification': specification(amount, unitCode),
  })

  return [
    offer(plan.price, MONTHLY),
    ...(plan.annual ? [offer(plan.annual.price, ANNUAL)] : []),
  ]
}

/**
 * Il prodotto e il suo listino.
 *
 * Le offerte sono i quattro piani di `content/<lingua>.ts`, cioè esattamente le
 * card che la home e la pagina dei prezzi rendono.
 */
export function softwareApplicationNode(
  content: SiteContent,
  locale: LocaleCode,
  siteUrl: string,
): SchemaNode {
  const home = homeUrl(locale, siteUrl)
  const pricing = canonicalUrl(localePath('/pricing', locale), siteUrl)

  return {
    '@type': 'SoftwareApplication',
    '@id': `${home}#software`,
    'name': BRAND,
    'description': content.meta.description,
    'url': home,
    'applicationCategory': 'DeveloperApplication',
    // Non si installa: si usa da un browser e si chiama via HTTP.
    'operatingSystem': 'Web',
    'inLanguage': locale,
    'provider': { '@id': organizationId(locale, siteUrl) },
    'offers': content.plans.flatMap(plan => offerNodes(plan, pricing)),
  }
}

/**
 * Le domande frequenti, una a una come la pagina le mostra.
 *
 * `text` è la risposta intera: `FAQPage` vuole la risposta completa, e un
 * riassunto sarebbe di nuovo un contenuto che la pagina non ha.
 */
export function faqPageNode(items: readonly FaqItem[], pageUrl: string): SchemaNode {
  return {
    '@type': 'FAQPage',
    '@id': `${pageUrl}#faq`,
    'url': pageUrl,
    'mainEntity': items.map(item => ({
      '@type': 'Question',
      'name': item.question,
      'acceptedAnswer': { '@type': 'Answer', 'text': item.answer },
    })),
  }
}

/**
 * Serializza i nodi in un documento JSON-LD.
 *
 * `@graph` e non un nodo per `<script>`: i nodi si riferiscono l'un l'altro per
 * `@id` — il prodotto dichiara il proprio `provider` — e in un grafo solo quei
 * riferimenti si risolvono senza che il motore debba unire più documenti.
 *
 * Il `<` viene sostituito con la sua sequenza di escape: il contenuto finisce
 * dentro un elemento `<script>`, dove una risposta di FAQ contenente `</script>`
 * chiuderebbe l'elemento e riverserebbe il resto nella pagina. È JSON valido —
 * `<` è un carattere come un altro — e il parser lo rilegge identico.
 */
export function structuredData(nodes: readonly SchemaNode[]): string {
  return JSON.stringify({ '@context': 'https://schema.org', '@graph': nodes })
    .replace(/</g, '\\u003c')
}
