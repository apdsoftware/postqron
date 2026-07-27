import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import test from 'node:test'
import {
  translateCatalog,
  validateCatalogs,
} from '../../f36-i18n/src/catalog.ts'
import { SUPPORTED_LOCALES } from '../../f36-i18n/src/locales.ts'
import { ADMIN_CATALOGS } from '../core/catalogs.ts'

const PAGE_FILES = [
  '../pages/admin.vue',
  '../pages/admin-users.vue',
  '../pages/admin-workspaces.vue',
  '../pages/admin-plans.vue',
  '../pages/admin-audit.vue',
  '../pages/admin-profile.vue',
]

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
  assert.equal(
    translateCatalog(
      ADMIN_CATALOGS.fr,
      'fr',
      'pagination.status',
      { page: 2, count: 5 },
    ),
    'Page 2 sur 5',
  )
  // Error and audit identifiers are catalog keys/data, never localized values.
  for (const locale of SUPPORTED_LOCALES) {
    assert.ok('error.ADMIN_CSRF_INVALID' in ADMIN_CATALOGS[locale])
    assert.equal('internal_plan.assign' in ADMIN_CATALOGS[locale], false)
  }
})

test('every admin route declares a localized non-empty document title', async () => {
  const pages = await Promise.all(
    PAGE_FILES.map(path => readFile(new URL(path, import.meta.url), 'utf8')),
  )
  for (const page of pages) {
    assert.match(page, /useHead\(computed\(\(\) => \(\{/u)
    assert.match(page, /title: t\('document\.title'\)/u)
    assert.match(page, /middleware: 'admin-access'/u)
    assert.match(page, /layout: 'admin-console'/u)
  }

  const titles = SUPPORTED_LOCALES.map((locale) => {
    const title = ADMIN_CATALOGS[locale]['document.title']
    assert.notEqual(title.trim(), '', `${locale}.document.title`)
    assert.match(title, /Postqron$/u, `${locale}.document.title`)
    return title
  })
  assert.equal(new Set(titles).size, SUPPORTED_LOCALES.length)
})

test('admin shell exposes an accessible sidebar, drawer, and inline login gate', async () => {
  const layout = await readFile(new URL('../layouts/admin-console.vue', import.meta.url), 'utf8')

  assert.match(layout, /href="#admin-main"/u)
  assert.match(layout, /PostqronLanguageSwitcher/u)
  assert.match(layout, /aria-labelledby="admin-login-title"/u)
  assert.match(layout, /<h1>/u)
  assert.match(layout, /minlength="8"|minlength="12"/u)
  assert.match(layout, /:aria-current="item\.active \? 'page' : undefined"/u)
  assert.match(layout, /:data-open="menuOpen"/u)
  assert.match(layout, /:aria-expanded="menuOpen"/u)
  assert.match(layout, /@keydown\.esc="menuOpen = false"/u)
  assert.match(layout, /AdminLogoutButton/u)
})

test('profile exposes account metadata and an accessible password change form', async () => {
  const profile = await readFile(
    new URL('../pages/admin-profile.vue', import.meta.url),
    'utf8',
  )
  assert.match(profile, /session\?\.account\.id/u)
  assert.match(profile, /aria-labelledby="admin-password-title"/u)
  assert.match(profile, /aria-describedby="admin-password-policy"/u)
  assert.match(profile, /role="alert"/u)
  assert.match(profile, /role="status"/u)
})

test('admin plans page exposes an accessible confirmation dialog and never requests secrets', async () => {
  const [plansPage, api] = await Promise.all([
    readFile(new URL('../pages/admin-plans.vue', import.meta.url), 'utf8'),
    readFile(new URL('../core/api.ts', import.meta.url), 'utf8'),
  ])
  assert.match(plansPage, /<dialog/u)
  assert.match(plansPage, /confirm\.checkbox/u)
  assert.match(plansPage, /minlength="8"/u)
  assert.match(plansPage, /globalThis\.crypto\.randomUUID\(\)/u)
  assert.doesNotMatch(
    `${plansPage}\n${api}`.toLowerCase(),
    /social[_-]?token|payment[_-]?method|card[_-]?number|client[_-]?secret/u,
  )
  assert.notEqual(
    ADMIN_CATALOGS.en['confirm.description'],
    ADMIN_CATALOGS.de['confirm.description'],
  )
})

test('sidebar declares one route per required admin section', () => {
  const paths = new Set(
    (Object.entries(ADMIN_CATALOGS.en) as Array<[string, string]>)
      .filter(([key]) => key.startsWith('nav.'))
      .map(([key]) => key),
  )
  assert.deepEqual([...paths].sort(), [
    'nav.audit',
    'nav.dashboard',
    'nav.plans',
    'nav.profile',
    'nav.users',
    'nav.workspaces',
  ])
})

test('manifest owns only protected web and server routes with security dependencies', async () => {
  const manifest = await readFile(
    new URL('../feature.yaml', import.meta.url),
    'utf8',
  )
  assert.match(manifest, /path: \/admin/u)
  assert.match(manifest, /visibility: private/u)
  assert.match(manifest, /middleware: \[admin-access\]/u)
  assert.match(manifest, /components:\n {4}- \.\/components/u)
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
  for (const path of [
    '/admin',
    '/admin/users',
    '/admin/workspaces',
    '/admin/plans',
    '/admin/audit',
    '/admin/profile',
  ]) {
    assert.ok(manifest.includes(`path: ${path}\n`), path)
  }
  assert.equal(
    manifest.match(/visibility: private/gu)?.length,
    11,
  )
})
