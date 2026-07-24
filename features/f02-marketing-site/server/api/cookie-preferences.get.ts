import {
  forwardedHeaders,
  normalizeUpstreamError,
  upstreamUrl,
} from '../utils/upstream'

export default defineEventHandler(async (event) => {
  try {
    return await $fetch(upstreamUrl('/api/v1/cookie-preferences'), {
      headers: forwardedHeaders(event),
    })
  } catch (error) {
    return normalizeUpstreamError(error)
  }
})
