import {
  forwardedHeaders,
  normalizeUpstreamError,
  upstreamUrl,
} from '../utils/upstream'

export default defineEventHandler(async (event) => {
  const body = await readBody(event)
  try {
    return await $fetch(upstreamUrl('/api/v1/cookie-preferences'), {
      method: 'PUT',
      headers: forwardedHeaders(event),
      body,
    })
  } catch (error) {
    return normalizeUpstreamError(error)
  }
})
