import type {
  LegalDocumentKey,
  PublishedLegalDocument,
} from '../../f13-compliance/src/index.ts'
import {
  handleLegalApiRequest,
  loadBundledRepository,
  type DocumentType,
  type LegalApiResponse,
  type LegalRepository,
} from '../../f25-legal-documents/src/index.ts'

// Public route slugs are kept as-is for backward compatibility. `key` is the
// legacy F13 backend key, preserved so any existing consumer of this
// contract keeps working unchanged. `document` is the F25 gate's own
// document type, used to address the fail-closed repository/adapter
// instead of the unimplemented legacy backend endpoint.
export const PUBLIC_LEGAL_DOCUMENTS = {
  termini: {
    key: 'terms_it',
    document: 'terms',
    title: 'Termini e condizioni',
    description: 'Le condizioni che regolano l’uso di Postqron.',
  },
  privacy: {
    key: 'privacy_it',
    document: 'privacy',
    title: 'Privacy Policy',
    description: 'Come Postqron tratta e protegge i dati personali.',
  },
  cookie: {
    key: 'cookies_it',
    document: 'cookies',
    title: 'Cookie Policy',
    description: 'Cookie necessari, preferenze e strumenti opzionali.',
  },
} as const satisfies Record<string, {
  key: LegalDocumentKey
  document: DocumentType
  title: string
  description: string
}>

export type PublicLegalSlug = keyof typeof PUBLIC_LEGAL_DOCUMENTS

export function isPublicLegalSlug(value: string): value is PublicLegalSlug {
  return Object.hasOwn(PUBLIC_LEGAL_DOCUMENTS, value)
}

export interface LegalProxyRequest {
  method: string
  slug: string
  locale?: string | null
  market?: string | null
  now?: string
  // Injected only by tests; production requests always resolve the
  // fail-closed bundled repository below.
  repository?: LegalRepository
}

// Forwards to the F25 repository/adapter so the web runtime never talks to
// the unimplemented legacy backend endpoint. Unrecognized slugs are passed
// through unchanged so F25's own `legal_document_not_found` response (and
// its method/fail-closed checks, which run before route parsing) apply
// uniformly instead of being duplicated here.
export async function handleLegalProxyRequest(
  request: LegalProxyRequest,
): Promise<LegalApiResponse> {
  const document = isPublicLegalSlug(request.slug)
    ? PUBLIC_LEGAL_DOCUMENTS[request.slug].document
    : request.slug

  const params = new URLSearchParams()
  if (request.locale) {
    params.set('locale', request.locale)
  }
  if (request.market) {
    params.set('market', request.market)
  }
  const query = params.toString()

  const repository = request.repository ?? await loadBundledRepository()
  return handleLegalApiRequest(repository, {
    method: request.method,
    url: `/api/v1/legal-documents/${encodeURIComponent(document)}/current${query ? `?${query}` : ''}`,
    now: request.now,
  })
}

// Accepts both the legacy F13 backend shape (`contentStatus: 'approved'`)
// and F25's own gate shape (`status: 'approved'`, plus `requestedLocale`/
// `fallbackUsed`/`permanentUrl` metadata). Every field on the input is
// preserved on the returned value — this only validates a common subset
// (content, version, digest, effective date) that both contracts share.
export function parsePublishedLegalDocument(value: unknown): PublishedLegalDocument {
  if (!value || typeof value !== 'object') {
    throw new Error('Documento legale non disponibile.')
  }
  const document = value as Record<string, unknown>
  const isApproved = document.contentStatus === 'approved' || document.status === 'approved'
  if (
    !isApproved
    || typeof document.content !== 'string'
    || typeof document.version !== 'string'
    || typeof document.digestSha256 !== 'string'
    || !/^[a-f0-9]{64}$/.test(document.digestSha256)
    || typeof document.effectiveAt !== 'string'
  ) {
    throw new Error('Il documento legale non è pubblicabile.')
  }
  return value as PublishedLegalDocument
}

export type LegalBlock =
  | { kind: 'heading'; level: 2 | 3; text: string }
  | { kind: 'paragraph'; text: string }
  | { kind: 'list'; items: string[] }

export function toLegalBlocks(markdown: string): LegalBlock[] {
  const blocks: LegalBlock[] = []
  let paragraph: string[] = []
  let list: string[] = []

  const flushParagraph = () => {
    if (paragraph.length > 0) {
      blocks.push({ kind: 'paragraph', text: paragraph.join(' ') })
      paragraph = []
    }
  }
  const flushList = () => {
    if (list.length > 0) {
      blocks.push({ kind: 'list', items: list })
      list = []
    }
  }

  for (const rawLine of markdown.split(/\r?\n/)) {
    const line = rawLine.trim()
    if (!line) {
      flushParagraph()
      flushList()
    } else if (line.startsWith('### ')) {
      flushParagraph()
      flushList()
      blocks.push({ kind: 'heading', level: 3, text: line.slice(4) })
    } else if (line.startsWith('## ')) {
      flushParagraph()
      flushList()
      blocks.push({ kind: 'heading', level: 2, text: line.slice(3) })
    } else if (line.startsWith('# ')) {
      flushParagraph()
      flushList()
      blocks.push({ kind: 'heading', level: 2, text: line.slice(2) })
    } else if (/^[-*]\s/.test(line)) {
      flushParagraph()
      list.push(line.slice(2))
    } else {
      flushList()
      paragraph.push(line)
    }
  }
  flushParagraph()
  flushList()
  return blocks
}
