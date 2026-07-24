import { marketingSiteFeature } from '../../runtime'

export default defineEventHandler((event) => {
  const config = useRuntimeConfig()
  const urls = marketingSiteFeature.routes
    .map(path => `  <url><loc>${config.public.siteUrl}${path}</loc></url>`)
    .join('\n')
  setHeader(event, 'content-type', 'application/xml; charset=utf-8')
  return `<?xml version="1.0" encoding="UTF-8"?>
<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">
${urls}
</urlset>
`
})
