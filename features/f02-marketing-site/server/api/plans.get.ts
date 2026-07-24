import { parsePublicCatalog } from '../../src/catalog'
import { normalizeUpstreamError, upstreamUrl } from '../utils/upstream'

export default defineEventHandler(async () => {
  try {
    const catalog = await $fetch(upstreamUrl('/api/v1/billing/plans'))
    return parsePublicCatalog(catalog)
  } catch (error) {
    return normalizeUpstreamError(error)
  }
})
