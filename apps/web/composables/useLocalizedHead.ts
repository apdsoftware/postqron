import type { LocaleCode } from '~/utils/locale'
import { LOCALES, localePath } from '~/utils/locale'
import { alternateLinks, assetUrl, canonicalUrl } from '~/utils/site'

/** Nome del prodotto: non è contenuto traducibile, è un marchio. */
const BRAND = 'Postqron'

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
      /*
       * Il vettore è la favicon vera: nitida a ogni densità e a ogni misura.
       * Il `.ico` resta per chi non sa leggerlo — e va dichiarato *dopo*, così
       * i browser che li supportano entrambi scelgono comunque l'SVG.
       */
      { rel: 'icon', type: 'image/svg+xml', href: '/favicon.svg' },
      { rel: 'icon', sizes: '48x48', href: '/favicon.ico' },
      /*
       * iOS ignora sia l'SVG sia la trasparenza: senza questa, «aggiungi alla
       * schermata Home» ritaglia uno spezzone della pagina.
       */
      { rel: 'apple-touch-icon', href: '/apple-touch-icon.png' },
      ...alternates.map(alternate => ({
        rel: 'alternate',
        hreflang: alternate.hreflang,
        href: alternate.href,
      })),
    ],
    meta: [
      { name: 'description', content: options.description },
      { property: 'og:type', content: 'website' },
      { property: 'og:title', content: `${options.title} · ${BRAND}` },
      { property: 'og:description', content: options.description },
      { property: 'og:url', content: canonical },
      { property: 'og:locale', content: options.locale },
      /*
       * Card social (R34). L'indirizzo è assoluto perché chi la legge non è il
       * browser di chi visita il sito: è il crawler di una piattaforma, che
       * scarica l'immagine per conto proprio e non ha una pagina di base.
       *
       * L'immagine è il marchio su fondo profondo, senza testo tradotto: la
       * stessa card vale per le cinque lingue, e non c'è una sesta versione da
       * dimenticare di aggiornare.
       */
      { property: 'og:image', content: assetUrl('/social-card.png', config.siteUrl) },
      { property: 'og:image:width', content: '1200' },
      { property: 'og:image:height', content: '630' },
      { property: 'og:image:alt', content: BRAND },
      { name: 'twitter:card', content: 'summary_large_image' },
      ...LOCALES.filter(entry => entry.code !== options.locale).map(entry => ({
        property: 'og:locale:alternate',
        content: entry.code,
      })),
    ],
  })
}
