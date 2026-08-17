import { Renderer, marked } from 'marked'
import acceptableUseSource from '../../../legal/en/acceptable-use-policy.md?raw'
import cookieSource from '../../../legal/en/cookie-policy.md?raw'
import privacySource from '../../../legal/en/privacy-policy.md?raw'
import termsSource from '../../../legal/en/terms-of-service.md?raw'

export const LEGAL_DOCUMENT_IDS = [
  'terms-of-service',
  'privacy-policy',
  'cookie-policy',
  'acceptable-use-policy',
] as const

export type LegalDocumentId = (typeof LEGAL_DOCUMENT_IDS)[number]

export interface LegalDocument {
  id: LegalDocumentId
  title: string
  version: string
  effectiveDate: string
  language: 'en'
  html: string
}

const sources: Record<LegalDocumentId, string> = {
  'terms-of-service': termsSource,
  'privacy-policy': privacySource,
  'cookie-policy': cookieSource,
  'acceptable-use-policy': acceptableUseSource,
}

function isLegalDocumentId(value: string): value is LegalDocumentId {
  return (LEGAL_DOCUMENT_IDS as readonly string[]).includes(value)
}

/** Converte i link relativi fra documenti nelle rotte pubbliche della lingua corrente. */
function legalRenderer(locale: string): Renderer {
  const renderer = new Renderer()

  renderer.link = function ({ href, title, tokens }) {
    const target = href.replace(/^\.\//, '').replace(/\.md$/, '')
    const localizedHref = isLegalDocumentId(target) ? `/${locale}/legal/${target}/` : href
    const titleAttribute = title ? ` title="${title}"` : ''

    return `<a href="${localizedHref}"${titleAttribute}>${this.parser.parseInline(tokens)}</a>`
  }

  return renderer
}

/**
 * Legge front matter e corpo senza modificare il testo giuridico.
 *
 * I file Markdown restano l'unica fonte: versione, data, titolo e contenuto
 * vengono incorporati direttamente da `legal/en/` durante la generazione.
 */
export function legalDocument(id: LegalDocumentId, locale: string): LegalDocument {
  const source = sources[id]
  const match = source.match(/^---\n([\s\S]*?)\n---\n+([\s\S]*)$/)
  if (!match) throw new Error(`Front matter non valido in legal/en/${id}.md`)

  const metadata = Object.fromEntries(
    match[1]!.split('\n').map((line) => {
      const separator = line.indexOf(':')
      return [line.slice(0, separator).trim(), line.slice(separator + 1).trim()]
    }),
  )
  const body = match[2]!
  const heading = body.match(/^# (.+)\n+/)
  if (!heading) throw new Error(`Titolo mancante in legal/en/${id}.md`)

  return {
    id,
    title: heading[1]!,
    version: metadata.version ?? '',
    effectiveDate: metadata.effective_date ?? '',
    language: 'en',
    html: marked.parse(body.slice(heading[0].length), {
      async: false,
      renderer: legalRenderer(locale),
    }),
  }
}

export function isLegalDocument(value: unknown): value is LegalDocumentId {
  return typeof value === 'string' && isLegalDocumentId(value)
}
