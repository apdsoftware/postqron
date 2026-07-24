import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import { dirname, resolve } from 'node:path'
import test from 'node:test'
import { fileURLToPath } from 'node:url'
import { marketingSiteFeature } from '../runtime.ts'

const featureRoot = resolve(dirname(fileURLToPath(import.meta.url)), '..')
const source = (path: string) => readFile(resolve(featureRoot, path), 'utf8')

test('the discoverable slice declares only its explicit feature dependencies', async () => {
  const manifest = await source('feature.yaml')
  assert.match(manifest, /^id: marketing-site$/m)
  assert.match(manifest, /^kind: web$/m)
  assert.match(manifest, /^entrypoint: \.\/runtime\.ts$/m)
  assert.match(
    manifest,
    /dependencies:\n[ ]{2}- brand\n[ ]{2}- compliance\n[ ]{2}- f10-entitlements/,
  )
})

test('home, features, prices, FAQ, and legal routes are SSR-owned by F2', async () => {
  const nuxtConfig = await source('nuxt.config.ts')
  assert.equal(marketingSiteFeature.rendering, 'ssr')
  assert.match(nuxtConfig, /ssr: true/)

  for (const route of marketingSiteFeature.routes) {
    const page = route === '/'
      ? 'pages/index.vue'
      : route.startsWith('/legal/')
        ? 'pages/legal/[document].vue'
        : `pages${route}.vue`
    await assert.doesNotReject(() => source(page))
  }
})

test('pricing consumes F10 at request time and contains no duplicated catalog', async () => {
  const pricingPage = await source('pages/prezzi.vue')
  const pricingProxy = await source('server/api/plans.get.ts')
  const pricingComponent = await source('components/PlanCatalog.vue')

  assert.match(pricingPage, /useFetch\('\/api\/plans'/)
  assert.match(pricingProxy, /\/api\/v1\/billing\/plans/)
  assert.match(pricingComponent, /catalog\.plans/)
  assert.doesNotMatch(pricingPage, /amount_cents/)
  assert.doesNotMatch(pricingComponent, /amount_cents:\s*\d/)
})

test('brand, APDSoftware credit, and F13 cookie controls are reused', async () => {
  const config = await source('nuxt.config.ts')
  const layout = await source('layouts/default.vue')
  const footer = await source('components/SiteFooter.vue')
  const cookies = await source('components/CookiePreferences.vue')

  assert.match(config, /f01-brand\/components\/components\.css/)
  assert.match(layout, /PqSkipLink/)
  assert.match(footer, /config\.public\.apdSoftwareUrl/)
  assert.match(footer, /Sviluppato da/)
  assert.match(cookies, /COOKIE_BANNER_FIRST_LEVEL_ACTIONS/)
  assert.match(cookies, /COOKIE_CATEGORIES/)
  assert.match(cookies, /\/api\/cookie-preferences/)
  assert.match(cookies, /cookie-action/g)
})

test('every public content page defines metadata and a canonical URL', async () => {
  for (const page of [
    'pages/index.vue',
    'pages/funzionalita.vue',
    'pages/prezzi.vue',
    'pages/faq.vue',
    'pages/legal/[document].vue',
  ]) {
    const content = await source(page)
    assert.match(content, /useSeoMeta/)
    assert.match(content, /rel: 'canonical'/)
  }

  const layout = await source('layouts/default.vue')
  assert.match(layout, /id="main-content"/)
  assert.match(await source('server/routes/robots.txt.ts'), /Sitemap:/)
  assert.match(await source('server/routes/sitemap.xml.ts'), /urlset/)
})
