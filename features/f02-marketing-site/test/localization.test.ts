import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import { dirname, resolve } from 'node:path'
import test from 'node:test'
import { fileURLToPath } from 'node:url'
import { validateCatalogs } from '../../f36-i18n/src/catalog.ts'
import { SUPPORTED_LOCALES } from '../../f36-i18n/src/locales.ts'
import { localizeUrl } from '../../f36-i18n/src/routing.ts'
import { MARKETING_FAQ_CATALOGS } from '../locales/faq.ts'
import { MARKETING_FEATURES_CATALOGS } from '../locales/features.ts'
import { MARKETING_HOME_CATALOGS } from '../locales/home.ts'
import { MARKETING_LEGAL_CATALOGS } from '../locales/legal.ts'
import { MARKETING_NAV_CATALOGS } from '../locales/nav.ts'
import { MARKETING_PLANNER_PREVIEW_CATALOGS } from '../locales/planner-preview.ts'

const featureRoot = resolve(dirname(fileURLToPath(import.meta.url)), '..')
const source = (path: string) => readFile(resolve(featureRoot, path), 'utf8')

const NAMESPACED_CATALOGS = {
  'marketing-nav': MARKETING_NAV_CATALOGS,
  'marketing-home': MARKETING_HOME_CATALOGS,
  'marketing-features': MARKETING_FEATURES_CATALOGS,
  'marketing-faq': MARKETING_FAQ_CATALOGS,
  'marketing-legal': MARKETING_LEGAL_CATALOGS,
  'marketing-planner-preview': MARKETING_PLANNER_PREVIEW_CATALOGS,
} as const

test('every marketing-site catalog is valid and complete for all five locales', () => {
  for (const [namespace, catalogs] of Object.entries(NAMESPACED_CATALOGS)) {
    validateCatalogs(catalogs)
    assert.deepEqual(
      Object.keys(catalogs).sort(),
      ['de', 'en', 'es', 'fr', 'it'],
      `${namespace} must define all five locales`,
    )

    const referenceKeys = Object.keys(catalogs.en).sort()
    for (const locale of SUPPORTED_LOCALES) {
      assert.deepEqual(
        Object.keys(catalogs[locale]).sort(),
        referenceKeys,
        `${namespace}.${locale} must expose the same keys as english`,
      )
    }
  }
})

test('the legal catalog never claims a single locale is legally binding', () => {
  for (const locale of SUPPORTED_LOCALES) {
    for (const text of Object.values(MARKETING_LEGAL_CATALOGS[locale])) {
      assert.doesNotMatch(text.toLowerCase(), /italian only|solo italiano|solo in italiano|only in italian/)
    }
  }
})

test('public marketing routes resolve to the correct localized path for every locale', () => {
  for (const path of ['/', '/funzionalita', '/faq', '/legal/termini', '/legal/privacy', '/legal/cookie']) {
    assert.equal(localizeUrl('en', path), path)
    for (const locale of SUPPORTED_LOCALES.filter(candidate => candidate !== 'en')) {
      const expected = path === '/' ? `/${locale}` : `/${locale}${path}`
      assert.equal(localizeUrl(locale, path), expected)
    }
  }
})

test('SiteHeader, FeatureCard, and PlannerPreview use the shared i18n runtime', async () => {
  const header = await source('components/SiteHeader.vue')
  assert.match(header, /useMarketingSiteI18n/)
  assert.match(header, /marketing-nav\./)
  assert.match(header, /i18n\.localize\(/)
  assert.match(header, /PostqronLanguageSwitcher/)
  assert.doesNotMatch(header, /Funzionalità|Prezzi|Inizia ora/)

  const planner = await source('components/PlannerPreview.vue')
  assert.match(planner, /useMarketingSiteI18n/)
  assert.match(planner, /marketing-planner-preview\./)
  assert.doesNotMatch(planner, /Luglio 2026|Lun<br>|Dietro le quinte/)
})

test('every public content page is localized with SEO and hreflang for all five locales', async () => {
  const pagesByNamespace = {
    'pages/index.vue': 'marketing-home',
    'pages/funzionalita.vue': 'marketing-features',
    'pages/faq.vue': 'marketing-faq',
    'pages/legal/[document].vue': 'marketing-legal',
  } as const

  for (const [page, namespace] of Object.entries(pagesByNamespace)) {
    const content = await source(page)
    assert.match(content, /useMarketingSiteI18n/, `${page} must use the shared i18n runtime`)
    assert.match(content, new RegExp(`${namespace}\\.`), `${page} must translate from ${namespace}`)
    assert.match(content, /useSeoMeta/)
    assert.match(content, /rel: 'canonical'/)
    assert.match(content, /rel: 'alternate'/)
    assert.match(content, /hreflang: locale/)
    assert.match(content, /x-default/)
    assert.match(content, /SUPPORTED_LOCALES/)
  }
})

test('the home, features, and FAQ pages no longer hardcode Italian copy', async () => {
  const home = await source('pages/index.vue')
  assert.doesNotMatch(home, /Più chiarezza\. Meno schede aperte\.|Inizia ora|Prova Postqron/)

  const features = await source('pages/funzionalita.vue')
  assert.doesNotMatch(features, /Pianifica|Nessuna pubblicazione duplicata/)

  const faq = await source('pages/faq.vue')
  assert.doesNotMatch(faq, /Posso provare Postqron prima di scegliere un piano\?/)
})

test('the legal page keeps the fail-closed gate and only shows a localized generic unavailable state', async () => {
  const legalPage = await source('pages/legal/[document].vue')
  assert.match(legalPage, /query: computed\(\(\) => \(\{ locale: i18n\.locale\.value \}\)\)/)
  assert.match(legalPage, /watch: \[i18n\.locale\]/)
  assert.match(legalPage, /responseLocale === i18n\.locale\.value/)
  assert.match(legalPage, /t\('state\.unavailableTitle'\)/)
  assert.match(legalPage, /t\('state\.unavailableBody'\)/)
  assert.match(legalPage, /t\('state\.loading'\)/)
  assert.match(legalPage, /t\('state\.retry'\)/)
  assert.doesNotMatch(legalPage, /solo italiano|italian only|solo in italiano/i)
})
