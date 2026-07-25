import { LOCALE_COOKIE_CONTRACT } from '../../../../f36-i18n/src/cookie.ts'
import { resolveLocale } from '../../../../f36-i18n/src/resolver.ts'
import { handleLegalProxyRequest } from '../../../src/legal'

export default defineEventHandler(async (event) => {
  const slug = getRouterParam(event, 'document') || ''
  const query = getQuery(event)

  const requestedLocale = typeof query.locale === 'string' ? query.locale : undefined
  const locale = requestedLocale ?? resolveLocale({
    acceptLanguage: getRequestHeader(event, 'accept-language'),
    cookie: getCookie(event, LOCALE_COOKIE_CONTRACT.name),
    url: event.path,
  }).locale
  const market = typeof query.market === 'string' ? query.market : undefined

  const response = await handleLegalProxyRequest({
    method: event.method,
    slug,
    locale,
    market,
  })

  setResponseStatus(event, response.status)
  for (const [name, value] of Object.entries(response.headers)) {
    setResponseHeader(event, name, value)
  }
  return response.body
})
