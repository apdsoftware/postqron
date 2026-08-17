import { describe, expect, it } from 'vitest'

import { LEGAL_DOCUMENT_IDS } from '~/utils/legal-documents'
import { LOCALE_CODES } from '~/utils/locale'
import { PUBLIC_PAGE_IDS } from '~/utils/public-pages'
import { SITE_PATHS, renderRobots, renderSitemap, sitemapEntries } from '~/utils/sitemap'

const SITE = 'https://postqron.com'

/** Le pagine sono `/`, `/pricing`, le tre di contenuto e i documenti legali. */
const EXPECTED_PATHS = 2 + PUBLIC_PAGE_IDS.length + LEGAL_DOCUMENT_IDS.length

function parse(xml: string): Document {
  const document = new DOMParser().parseFromString(xml, 'application/xml')

  // `parseFromString` non lancia: su XML malformato restituisce un documento il
  // cui elemento radice è `<parsererror>`. Senza questo controllo qualunque
  // asserzione successiva verificherebbe un documento d'errore.
  expect(document.querySelector('parsererror')).toBeNull()

  return document
}

describe('SITE_PATHS', () => {
  it('copre ogni pagina del sito', () => {
    expect(SITE_PATHS).toHaveLength(EXPECTED_PATHS)
    expect(SITE_PATHS).toContain('/')
    expect(SITE_PATHS).toContain('/pricing')

    for (const id of PUBLIC_PAGE_IDS) expect(SITE_PATHS).toContain(`/${id}`)
    for (const id of LEGAL_DOCUMENT_IDS) expect(SITE_PATHS).toContain(`/legal/${id}`)
  })

  it('non elenca due volte lo stesso percorso', () => {
    expect(new Set(SITE_PATHS).size).toBe(SITE_PATHS.length)
  })

  it('è privo di prefisso di lingua: il prefisso lo mette la sitemap', () => {
    for (const path of SITE_PATHS) {
      expect(path).not.toMatch(/^\/(?:en|it|es|de|fr)(?:\/|$)/)
    }
  })
})

describe('sitemapEntries', () => {
  const entries = sitemapEntries(SITE)

  it('dichiara ogni pagina in tutte e cinque le lingue', () => {
    // Una sitemap che elenca solo l'inglese dice a un motore che le altre
    // quattro lingue non esistono: sono pagine reali che restano fuori
    // dall'indice.
    expect(entries).toHaveLength(EXPECTED_PATHS * LOCALE_CODES.length)

    for (const locale of LOCALE_CODES) {
      const localized = entries.filter(entry => entry.path.startsWith(`/${locale}/`))
      expect(localized).toHaveLength(EXPECTED_PATHS)
    }
  })

  it('usa indirizzi assoluti che chiudono con lo slash', () => {
    for (const entry of entries) {
      expect(entry.loc.startsWith(`${SITE}/`)).toBe(true)
      expect(entry.loc.endsWith('/')).toBe(true)
    }
  })

  it('non elenca la radice, che smista e non ha contenuto proprio', () => {
    expect(entries.map(entry => entry.loc)).not.toContain(`${SITE}/`)
  })

  it('porta su ogni voce le cinque traduzioni più x-default', () => {
    for (const entry of entries) {
      expect(entry.alternates.map(alternate => alternate.hreflang))
        .toEqual([...LOCALE_CODES, 'x-default'])
    }
  })

  it('manda x-default sull\'inglese, come il rilevamento', () => {
    for (const entry of entries) {
      expect(entry.alternates.at(-1)!.href).toMatch(/^https:\/\/postqron\.com\/en\//)
    }
  })

  it('dà alle cinque versioni di una pagina lo stesso gruppo di traduzioni', () => {
    // Il gruppo `hreflang` è reciproco per definizione: se le cinque voci
    // dichiarassero insiemi diversi, i motori scarterebbero tutto il gruppo.
    const faq = entries.filter(entry => entry.path.endsWith('/faq/'))
    expect(faq).toHaveLength(LOCALE_CODES.length)

    for (const entry of faq) {
      expect(entry.alternates).toEqual(faq[0]!.alternates)
    }
  })

  it('non ripete lo stesso indirizzo', () => {
    expect(new Set(entries.map(entry => entry.loc)).size).toBe(entries.length)
  })
})

describe('renderSitemap', () => {
  const xml = renderSitemap(SITE)
  const document = parse(xml)

  it('è XML valido nello spazio dei nomi di sitemaps.org', () => {
    expect(xml.startsWith('<?xml version="1.0" encoding="UTF-8"?>')).toBe(true)
    expect(document.documentElement.tagName).toBe('urlset')
    expect(document.documentElement.getAttribute('xmlns'))
      .toBe('http://www.sitemaps.org/schemas/sitemap/0.9')
    expect(document.documentElement.getAttribute('xmlns:xhtml'))
      .toBe('http://www.w3.org/1999/xhtml')
  })

  it('ha un <url> con <loc> per ogni voce', () => {
    const urls = [...document.querySelectorAll('url')]
    expect(urls).toHaveLength(EXPECTED_PATHS * LOCALE_CODES.length)

    const locations = urls.map(url => url.querySelector('loc')?.textContent)
    expect(locations).toEqual(sitemapEntries(SITE).map(entry => entry.loc))
  })

  it('accompagna ogni <loc> con sei alternative', () => {
    for (const url of document.querySelectorAll('url')) {
      const links = [...url.getElementsByTagName('xhtml:link')]
      expect(links.map(link => link.getAttribute('hreflang')))
        .toEqual([...LOCALE_CODES, 'x-default'])

      for (const link of links) {
        expect(link.getAttribute('rel')).toBe('alternate')
        expect(link.getAttribute('href')).toMatch(/^https:\/\/postqron\.com\//)
      }
    }
  })

  it('non dichiara date di modifica che non conosciamo', () => {
    // La build non sa quando è cambiato il testo di una pagina: un `lastmod`
    // pari alla data del deploy direbbe che sono cambiate tutte, ogni volta.
    expect(xml).not.toContain('<lastmod>')
  })

  it('produce indirizzi validi anche con un origin che finisce per slash', () => {
    const document = parse(renderSitemap('https://postqron.com/'))
    const locations = [...document.querySelectorAll('loc')].map(loc => loc.textContent!)

    for (const location of locations) {
      expect(location.slice('https://'.length)).not.toContain('//')
    }
  })
})

describe('renderRobots', () => {
  const robots = renderRobots(SITE)

  it('rimanda alla sitemap con un indirizzo assoluto', () => {
    // È l'unica direttiva del formato che non ammette un percorso relativo.
    expect(robots).toContain('Sitemap: https://postqron.com/sitemap.xml')
  })

  it('lascia indicizzare tutto il sito pubblico', () => {
    expect(robots).toContain('User-agent: *')
    expect(robots).toContain('Allow: /')
    expect(robots).not.toMatch(/^Disallow: \/\s*$/m)
  })

  it('non duplica lo slash se l\'origin ne ha uno finale', () => {
    expect(renderRobots('https://postqron.com/'))
      .toContain('Sitemap: https://postqron.com/sitemap.xml')
  })
})
