import {
  isPublicLegalSlug,
  parsePublishedLegalDocument,
  PUBLIC_LEGAL_DOCUMENTS,
} from '../../../src/legal'
import { normalizeUpstreamError, upstreamUrl } from '../../utils/upstream'

export default defineEventHandler(async (event) => {
  const slug = getRouterParam(event, 'document') || ''
  if (!isPublicLegalSlug(slug)) {
    throw createError({ statusCode: 404, statusMessage: 'Documento non trovato' })
  }

  const documentKey = PUBLIC_LEGAL_DOCUMENTS[slug].key
  try {
    const document = await $fetch(
      upstreamUrl(`/api/v1/legal-documents/${encodeURIComponent(documentKey)}/current`),
    )
    return parsePublishedLegalDocument(document)
  } catch (error) {
    return normalizeUpstreamError(error)
  }
})
