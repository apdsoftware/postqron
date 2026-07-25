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
  const body = await readBody(event)
  setResponseHeader(event, 'cache-control', 'no-store')
  try {
    const response = await $fetch.raw(
      upstreamUrl('/api/v1/cookie-preferences'),
      {
        method: 'PUT',
        headers: forwardedHeaders(event),
        body,
      },
    )
    forwardSetCookies(event, response.headers)
    const replay = response.headers.get('idempotent-replay')
    if (replay) {
      setResponseHeader(event, 'idempotent-replay', replay)
    }
    return response._data
  } catch (error) {
    return normalizeUpstreamError(error)
  }
})
