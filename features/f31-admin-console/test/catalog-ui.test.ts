import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import test from 'node:test'
import {
  translateCatalog,
  validateCatalogs,
} from '../../f36-i18n/src/catalog.ts'
import { SUPPORTED_LOCALES } from '../../f36-i18n/src/locales.ts'
import { ADMIN_CATALOGS } from '../core/catalogs.ts'

test('all five admin catalogs are complete and retain technical identifiers', () => {
  validateCatalogs(ADMIN_CATALOGS)
  assert.deepEqual(Object.keys(ADMIN_CATALOGS).sort(), [
    'de',
    'en',
    'es',
    'fr',
    'it',
  ])
  const keys = Object.keys(ADMIN_CATALOGS.en).sort()
  for (const locale of SUPPORTED_LOCALES) {
    assert.deepEqual(Object.keys(ADMIN_CATALOGS[locale]).sort(), keys)
    assert.ok(Object.values(ADMIN_CATALOGS[locale]).every(Boolean))
  }
  assert.equal(
    translateCatalog(
      ADMIN_CATALOGS.en,
      'en',
      'error.ADMIN_REAUTH_REQUIRED',
    ),
    'Sign in again before performing this sensitive operation.',
  )
  assert.equal(
    translateCatalog(
      ADMIN_CATALOGS.de,
      'de',
      'error.ADMIN_REAUTH_REQUIRED',
    ),
    'Melden Sie sich vor diesem sensiblen Vorgang erneut an.',
  )
  // Error and audit identifiers are catalog keys/data, never localized values.
  for (const locale of SUPPORTED_LOCALES) {
    assert.ok('error.ADMIN_CSRF_INVALID' in ADMIN_CATALOGS[locale])
    assert.equal('internal_plan.assign' in ADMIN_CATALOGS[locale], false)
  }
})

test('admin route declares a localized non-empty document title', async () => {
  const page = await readFile(
    new URL('../pages/admin.vue', import.meta.url),
    'utf8',
  )
  assert.match(page, /useHead\(computed\(\(\) => \(\{/u)
  assert.match(page, /title: t\('document\.title'\)/u)

  const titles = SUPPORTED_LOCALES.map((locale) => {
    const title = ADMIN_CATALOGS[locale]['document.title']
    assert.notEqual(title.trim(), '', `${locale}.document.title`)
    assert.match(title, /Postqron$/u, `${locale}.document.title`)
    return title
  })
  assert.equal(new Set(titles).size, SUPPORTED_LOCALES.length)
})

test('admin UI exposes accessible en/de confirmations and never requests secrets', async () => {
  const [page, plans, loginGate, state, pageHeader, layout, api, useAdmin] = await Promise.all([
    readFile(new URL('../pages/admin.vue', import.meta.url), 'utf8'),
    readFile(new URL('../pages/plans.vue', import.meta.url), 'utf8'),
    readFile(new URL('../components/AdminLoginGate.vue', import.meta.url), 'utf8'),
    readFile(new URL('../components/AdminState.vue', import.meta.url), 'utf8'),
    readFile(new URL('../components/AdminPageHeader.vue', import.meta.url), 'utf8'),
    readFile(new URL('../layouts/admin-console.vue', import.meta.url), 'utf8'),
    readFile(new URL('../core/api.ts', import.meta.url), 'utf8'),
    readFile(new URL('../core/use-admin.ts', import.meta.url), 'utf8'),
  ])
  assert.match(page, /<AdminPageHeader/u)
  assert.match(pageHeader, /<h1>/u)
  assert.match(page, /middleware: 'admin-access'/u)
  assert.match(loginGate, /aria-labelledby=/u)
  assert.match(state, /aria-live="polite"/u)
  assert.match(plans, /middleware: 'admin-access'/u)
  assert.match(plans, /aria-labelledby=/u)
  assert.match(plans, /<dialog/u)
  assert.match(plans, /confirm\.checkbox/u)
  assert.match(plans, /minlength="8"/u)
  assert.match(plans, /globalThis\.crypto\.randomUUID\(\)/u)
  assert.match(useAdmin, /globalThis\.\$fetch/u)
  assert.match(layout, /href="#admin-main"/u)
  assert.match(layout, /AdminLanguageSelect/u)
  assert.doesNotMatch(
    `${page}\n${plans}\n${api}`.toLowerCase(),
    /social[_-]?token|payment[_-]?method|card[_-]?number|client[_-]?secret/u,
  )
  assert.notEqual(
    ADMIN_CATALOGS.en['confirm.description'],
    ADMIN_CATALOGS.de['confirm.description'],
  )
})

test('the admin shell declares every dedicated section with the shared layout and guard', async () => {
  const [layout, languageSelect, nav, feature] = await Promise.all([
    readFile(new URL('../layouts/admin-console.vue', import.meta.url), 'utf8'),
    readFile(new URL('../components/AdminLanguageSelect.vue', import.meta.url), 'utf8'),
    readFile(new URL('../components/nav.ts', import.meta.url), 'utf8'),
    readFile(new URL('../feature.yaml', import.meta.url), 'utf8'),
  ])
  assert.match(layout, /<AdminLanguageSelect/u)
  assert.match(languageSelect, /<select/u)
  assert.doesNotMatch(layout, /<ul/u)
  for (const path of [
    '/admin',
    '/admin/users',
    '/admin/workspaces',
    '/admin/plans',
    '/admin/audit',
    '/admin/profile',
  ]) {
    assert.match(nav, new RegExp(`path: '${path.replace(/\//gu, '\\/')}'`, 'u'))
    assert.match(feature, new RegExp(`path: ${path.replace(/\//gu, '\\/')}\\n`, 'u'))
  }
})

test('manifest owns only protected web and server routes with security dependencies', async () => {
  const manifest = await readFile(
    new URL('../feature.yaml', import.meta.url),
    'utf8',
  )
  assert.match(manifest, /path: \/admin/u)
  assert.match(manifest, /visibility: private/u)
  assert.match(manifest, /middleware: \[admin-access\]/u)
  assert.match(
    manifest,
    /plugins:\n {4}- \.\/runtime\.ts\n {2}middleware:/u,
  )
  for (const dependency of [
    'app-shell',
    'auth',
    'f10-entitlements',
    'f11-internal-plan',
    'i18n',
    'operations',
    'workspaces',
  ]) {
    assert.match(manifest, new RegExp(`  - ${dependency}\\n`, 'u'))
  }
  assert.doesNotMatch(manifest, /visibility: public/u)
  assert.match(manifest, /entrypoints:\n {2}server: \.\/module\.go\n {2}web: \.\/runtime\.ts/u)
  for (const path of [
    '/admin/session',
    '/admin/dashboard',
    '/admin/search',
    '/admin/workspaces/{workspace_id}/internal-plan',
    '/admin/admins/{account_id}',
  ]) {
    assert.ok(manifest.includes(`path: ${path}`))
  }
  assert.equal(
    manifest.match(/visibility: private/gu)?.length,
    11,
  )
})
