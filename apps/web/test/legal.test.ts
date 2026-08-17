import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { describe, expect, it } from 'vitest'

import { siteContent } from '~/content'
import { DEFAULT_LOCALE, LOCALE_CODES } from '~/utils/locale'
import { LEGAL_DOCUMENT_IDS, legalDocument } from '~/utils/legal'

const LEGAL_ROOT = resolve(process.cwd(), '../../legal/en')
const OPEN_PLACEHOLDER = /\[\[DA CONFERMARE:[\s\S]*?\]\]/

describe('documenti legali pubblicati', () => {
  it.each(LEGAL_DOCUMENT_IDS)('%s espone versione e data approvate', (id) => {
    const document = legalDocument(id, DEFAULT_LOCALE)

    expect(document.version).toBe('1.0.0')
    expect(document.effectiveDate).toBe('2026-08-17')
    expect(document.language).toBe('en')
    expect(document.html).not.toBe('')
  })

  it.each(LEGAL_DOCUMENT_IDS)('%s non contiene segnaposto aperti', (id) => {
    const source = readFileSync(resolve(LEGAL_ROOT, `${id}.md`), 'utf8')

    expect(source).not.toMatch(OPEN_PLACEHOLDER)
  })

  it('riscrive i collegamenti fra documenti nella lingua della rotta', () => {
    const document = legalDocument('terms-of-service', 'it')

    expect(document.html).toContain('href="/it/legal/acceptable-use-policy/"')
    expect(document.html).toContain('href="/it/legal/privacy-policy/"')
  })
})

describe('impegni non approvati', () => {
  it.each(LOCALE_CODES)('%s non promette trial, pagina di stato o canale video', (locale) => {
    const content = siteContent[locale]
    const footerLinks = content.nav.footer.flatMap(group => group.items.map(item => item.to))
    const serialized = JSON.stringify(content).toLowerCase()

    expect(content.hero.note).toBeUndefined()
    expect(content.hero.video).toBeUndefined()
    expect(footerLinks).not.toContain('/#stats')
    expect(serialized).not.toContain('youtube.com/@postqron')
  })
})
