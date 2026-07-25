import {
  isDocumentType,
  type DocumentType,
  type PublishedLegalDocument,
} from './types.ts'
import { LegalRepository } from './repository.ts'
import { normalizeLegalLocale } from './validation.ts'

export interface LegalApiRequest {
  method: string
  url: string
  now?: string
}

export interface LegalApiResponse {
  status: number
  headers: Readonly<Record<string, string>>
  body: unknown
}

const DOCUMENT_ALIASES: Readonly<Record<string, DocumentType>> = Object.freeze({
  terms_it: 'terms',
  privacy_it: 'privacy',
  cookies_it: 'cookies',
})

function resolveDocument(value: string): DocumentType | undefined {
  return isDocumentType(value) ? value : DOCUMENT_ALIASES[value]
}

function decodePathSegment(value: string | undefined): string | undefined {
  if (value === undefined) {
    return undefined
  }
  try {
    return decodeURIComponent(value)
  } catch {
    return undefined
  }
}

function jsonResponse(
  status: number,
  body: unknown,
  headers: Record<string, string> = {},
): LegalApiResponse {
  return Object.freeze({
    status,
    headers: Object.freeze({
      'content-type': 'application/json; charset=utf-8',
      ...headers,
    }),
    body,
  })
}

function publishedResponse(document: PublishedLegalDocument): LegalApiResponse {
  return jsonResponse(200, document, {
    'cache-control': 'public, max-age=300, must-revalidate',
    etag: `"sha256-${document.digestSha256}"`,
    'x-legal-document-version': document.version,
  })
}

export function handleLegalApiRequest(
  repository: LegalRepository,
  request: LegalApiRequest,
): LegalApiResponse {
  if (request.method.toUpperCase() !== 'GET') {
    return jsonResponse(405, {
      error: { code: 'method_not_allowed', message: 'Only GET is supported.' },
    }, { allow: 'GET', 'cache-control': 'no-store' })
  }
  if (!repository.ready) {
    return jsonResponse(503, {
      error: {
        code: 'legal_release_blocked',
        message: 'No complete legally approved release is installed.',
      },
    }, { 'cache-control': 'no-store' })
  }

  let url: URL
  try {
    url = new URL(request.url, 'https://postqron.local')
  } catch {
    return jsonResponse(400, {
      error: { code: 'invalid_request', message: 'The request URL is invalid.' },
    }, { 'cache-control': 'no-store' })
  }

  const currentMatch =
    /^\/api\/v1\/legal-documents\/([^/]+)\/current$/u.exec(url.pathname)
  const versionMatch =
    /^\/api\/v1\/legal-documents\/([^/]+)\/versions\/([^/]+)$/u.exec(url.pathname)
  const match = currentMatch || versionMatch
  const decodedDocument = decodePathSegment(match?.[1])
  const document = decodedDocument
    ? resolveDocument(decodedDocument)
    : undefined
  if (!match || !document) {
    return jsonResponse(404, {
      error: { code: 'legal_document_not_found', message: 'Document not found.' },
    }, { 'cache-control': 'no-store' })
  }

  const locale = normalizeLegalLocale(url.searchParams.get('locale'))
  const at = request.now || new Date().toISOString()
  const decodedVersion = decodePathSegment(versionMatch?.[2])
  const published = currentMatch
    ? repository.current(document, locale, at)
    : decodedVersion
      ? repository.version(document, decodedVersion, locale, at)
      : undefined
  if (!published) {
    return jsonResponse(404, {
      error: {
        code: 'legal_document_version_not_found',
        message: 'No approved effective artifact matches the request.',
      },
    }, { 'cache-control': 'no-store' })
  }
  return publishedResponse(published)
}
