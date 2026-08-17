import type { LocaleCode } from '~/utils/locale'
import { LOCALES, localePath } from '~/utils/locale'
import { alternateLinks, canonicalUrl } from '~/utils/site'

export interface LocalizedHeadOptions {
  /** Percorso **senza** prefisso di lingua, nella forma di `content/`. */
  path: string
  locale: LocaleCode
  title: string
  description: string
}

/**
 * Intestazione SEO di una pagina tradotta.
 *
 * Mette insieme le tre cose che le cinque versioni della stessa pagina devono
 * dichiarare per non farsi concorrenza da sole nei motori di ricerca
 * (SPEC §8-bis):
 *
 * - `lang` del documento, che è anche ciò che leggono le sintesi vocali;
 * - un `canonical` proprio, l'indirizzo con cui questa versione va indicizzata;
 * - un `alternate hreflang` verso **tutte** le lingue, sé compresa, più
 *   `x-default` sull'inglese. L'autoreferenza non è ridondante: senza, il
 *   gruppo di traduzioni non è chiuso e i motori lo ignorano.
 */
export function useLocalizedHead(options: LocalizedHeadOptions): void {
  const { public: config } = useRuntimeConfig()

  const descriptor = LOCALES.find(entry => entry.code === options.locale)
  const canonical = canonicalUrl(localePath(options.path, options.locale), config.siteUrl)
  const alternates = alternateLinks(options.path, config.siteUrl)

  useHead({
    title: options.title,
    htmlAttrs: { lang: descriptor?.htmlLang ?? options.locale },
    link: [
      { rel: 'canonical', href: canonical },
      { rel: 'icon', type: 'image/svg+xml', href: '/favicon.svg' },
      ...alternates.map(alternate => ({
        rel: 'alternate',
        hreflang: alternate.hreflang,
        href: alternate.href,
      })),
    ],
    meta: [
      { name: 'description', content: options.description },
      { property: 'og:type', content: 'website' },
      { property: 'og:title', content: `${options.title} · PostQron` },
      { property: 'og:description', content: options.description },
      { property: 'og:url', content: canonical },
      { property: 'og:locale', content: options.locale },
      ...LOCALES.filter(entry => entry.code !== options.locale).map(entry => ({
        property: 'og:locale:alternate',
        content: entry.code,
      })),
    ],
  })
}
