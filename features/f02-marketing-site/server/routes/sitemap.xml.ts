import { marketingSiteFeature } from '../../runtime'
import {
  SUPPORTED_LOCALES,
  localizeUrl,
} from '../../../f36-i18n/src/index.ts'

export default defineEventHandler((event) => {
  const config = useRuntimeConfig()
  const origin = String(config.public.siteUrl).replace(/\/+$/u, '')
  const urls = marketingSiteFeature.routes
    .flatMap(path => SUPPORTED_LOCALES.map(locale =>
      localizeUrl(locale, path)))
    .map(path => `  <url><loc>${origin}${path}</loc></url>`)
    .join('\n')
  setHeader(event, 'content-type', 'application/xml; charset=utf-8')
  return `<?xml version="1.0" encoding="UTF-8"?>
<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">
${urls}
</urlset>
`
})
