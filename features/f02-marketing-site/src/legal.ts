import type {
  LegalDocumentKey,
  PublishedLegalDocument,
} from '../../f13-compliance/src/index.ts'

export const PUBLIC_LEGAL_DOCUMENTS = {
  termini: {
    key: 'terms_it',
    title: 'Termini e condizioni',
    description: 'Le condizioni che regolano l’uso di Postqron.',
  },
  privacy: {
    key: 'privacy_it',
    title: 'Privacy Policy',
    description: 'Come Postqron tratta e protegge i dati personali.',
  },
  cookie: {
    key: 'cookies_it',
    title: 'Cookie Policy',
    description: 'Cookie necessari, preferenze e strumenti opzionali.',
  },
} as const satisfies Record<string, {
  key: LegalDocumentKey
  title: string
  description: string
}>

export type PublicLegalSlug = keyof typeof PUBLIC_LEGAL_DOCUMENTS

export function isPublicLegalSlug(value: string): value is PublicLegalSlug {
  return Object.hasOwn(PUBLIC_LEGAL_DOCUMENTS, value)
}

export function parsePublishedLegalDocument(value: unknown): PublishedLegalDocument {
  if (!value || typeof value !== 'object') {
    throw new Error('Documento legale non disponibile.')
  }
  const document = value as Record<string, unknown>
  if (
    document.contentStatus !== 'approved'
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
