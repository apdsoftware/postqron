/**
 * `robots.txt` e `sitemap.xml` del sito pubblico (R53-ter).
 *
 * Sono **file**, non rotte. Il sito è generato staticamente e in produzione non
 * gira alcun Nitro (SPEC §2): una rotta che li servisse a runtime non
 * risponderebbe a nessuno. Vengono quindi scritti in `.output/public` alla fine
 * del pre-rendering, da `modules/seo-files.ts`.
 *
 * Questo modulo è logica pura e senza dipendenze da Vue: lo importa la build in
 * Node, e i test lo verificano senza montare niente.
 */

import { LOCALES, localePath } from '~/utils/locale'
import { LEGAL_DOCUMENT_IDS } from '~/utils/legal-documents'
import { PUBLIC_PAGE_IDS } from '~/utils/public-pages'
import { alternateLinks, assetUrl, canonicalUrl } from '~/utils/site'
import type { AlternateLink } from '~/utils/site'

/**
 * Ogni pagina del sito, **senza** prefisso di lingua.
 *
 * L'elenco è derivato, non scritto: le pagine di contenuto vengono da
 * `PUBLIC_PAGE_IDS` e i documenti legali da `LEGAL_DOCUMENT_IDS`, che sono già
 * le sorgenti di verità delle rispettive rotte. Aggiungere una pagina la fa
 * comparire qui da sola — ed è il punto: una sitemap che va aggiornata a mano
 * è una sitemap che prima o poi mente.
 *
 * La radice `/` non c'è. Non è una pagina: è lo smistamento per lingua, non ha
 * contenuto proprio e dichiara canonica la home inglese (SPEC §8-bis).
 * Elencarla proporrebbe ai motori un indirizzo che rimanda altrove.
 */
export const SITE_PATHS: readonly string[] = [
  '/',
  '/pricing',
  ...PUBLIC_PAGE_IDS.map(id => `/${id}`),
  ...LEGAL_DOCUMENT_IDS.map(id => `/legal/${id}`),
]

export interface SitemapEntry {
  /** Indirizzo assoluto della pagina in una lingua. */
  loc: string
  /** Percorso della pagina **con** prefisso di lingua, per i controlli di build. */
  path: string
  /** Le cinque traduzioni più `x-default`, uguali per tutte le voci del gruppo. */
  alternates: readonly AlternateLink[]
}

/**
 * Una voce per pagina **per lingua**: 5 × il numero di pagine.
 *
 * Le cinque versioni sono cinque indirizzi distinti e vanno dichiarati tutti.
 * Una sitemap che elenca solo l'inglese dice a un motore che le altre quattro
 * lingue non esistono, ed è peggio di nessuna sitemap: le pagine ci sono, e
 * restano fuori dall'indice.
 *
 * Ogni voce porta l'intero gruppo di `hreflang` — sé compresa, più `x-default`
 * sull'inglese. L'autoreferenza non è ridondante: senza, il gruppo di
 * traduzioni non è chiuso e i motori lo ignorano.
 */
export function sitemapEntries(siteUrl: string): readonly SitemapEntry[] {
  return SITE_PATHS.flatMap((path) => {
    const alternates = alternateLinks(path, siteUrl)

    return LOCALES.map((locale) => {
      const localized = localePath(path, locale.code)

      return { loc: canonicalUrl(localized, siteUrl), path: localized, alternates }
    })
  })
}

/** I cinque caratteri che un documento XML non può contenere così come sono. */
function escapeXml(value: string): string {
  return value
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;')
    .replace(/'/g, '&apos;')
}

/**
 * La sitemap nel formato di sitemaps.org, con le traduzioni in `xhtml:link`.
 *
 * Non ci sono `<lastmod>`, `<changefreq>` né `<priority>`. I secondi due i
 * motori li ignorano da anni; il primo sarebbe una data di modifica che non
 * abbiamo — la build non sa quando è cambiato il testo di una pagina, e
 * metterci la data della build significherebbe dichiarare ogni pagina
 * aggiornata a ogni deploy. Un dato falso dichiarato con precisione resta un
 * dato falso.
 */
export function renderSitemap(siteUrl: string): string {
  const urls = sitemapEntries(siteUrl).map((entry) => {
    const links = entry.alternates
      .map(alternate =>
        `    <xhtml:link rel="alternate" hreflang="${escapeXml(alternate.hreflang)}"`
        + ` href="${escapeXml(alternate.href)}"/>`,
      )
      .join('\n')

    return `  <url>\n    <loc>${escapeXml(entry.loc)}</loc>\n${links}\n  </url>`
  })

  return `<?xml version="1.0" encoding="UTF-8"?>
<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9" xmlns:xhtml="http://www.w3.org/1999/xhtml">
${urls.join('\n')}
</urlset>
`
}

/**
 * `robots.txt` del solo sito pubblico.
 *
 * Qui non c'è niente da nascondere: ogni pagina servita da questo dominio è
 * una pagina di marketing o un documento legale, e va indicizzata.
 *
 * La dashboard cliente e quella di amministrazione (SPEC §4.2, §4.3) non vanno
 * indicizzate, ma non si difendono da qui: sono un'altra applicazione su
 * un'altra origin, e `robots.txt` vale per una origin sola. A tenerle fuori
 * dall'indice è il `noindex` che il loro guscio dichiara — vedi
 * `apps/dashboard/public/robots.txt`, che spiega perché lì un `Disallow` sarebbe
 * controproducente.
 *
 * `Sitemap:` vuole un indirizzo assoluto — è l'unica direttiva del formato che
 * non ammette un percorso relativo.
 */
export function renderRobots(siteUrl: string): string {
  return `# postqron.com — sito pubblico (SPEC R53-ter).
# La dashboard è un'altra origin e ha il proprio robots.txt.
# Generato da apps/web/utils/sitemap.ts: non modificare nell'output.
User-agent: *
Allow: /

Sitemap: ${assetUrl('/sitemap.xml', siteUrl)}
`
}
