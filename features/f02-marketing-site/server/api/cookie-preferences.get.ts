import {
  forwardedHeaders,
  normalizeUpstreamError,
  upstreamUrl,
} from '../utils/upstream'

function forwardSetCookies(event: Parameters<typeof appendResponseHeader>[0], headers: Headers) {
  const values = (headers as Headers & {
    getSetCookie?: () => string[]
  }).getSetCookie?.() ?? []
  if (!values.length) {
    const combined = headers.get('set-cookie')
    if (combined) {
      values.push(combined)
    }
  }
  for (const value of values) {
    appendResponseHeader(event, 'set-cookie', value)
  }
}

export default defineEventHandler(async (event) => {
  setResponseHeader(event, 'cache-control', 'no-store')
  try {
    const response = await $fetch.raw(
      upstreamUrl('/api/v1/cookie-preferences'),
      {
        headers: forwardedHeaders(event),
      },
    )
    forwardSetCookies(event, response.headers)
    return response._data
  } catch (error) {
    return normalizeUpstreamError(error)
  }
})
