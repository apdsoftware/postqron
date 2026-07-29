import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import test from 'node:test'
import {
  APP_SHELL_CATALOGS,
  APP_SHELL_LOCALES,
} from '../components/core/catalogs.ts'

test('all five app catalogs contain the exact same keys', () => {
  const reference = Object.keys(APP_SHELL_CATALOGS.en).sort()
  assert.deepEqual(APP_SHELL_LOCALES, ['en', 'it', 'es', 'fr', 'de'])
  for (const locale of APP_SHELL_LOCALES) {
    assert.deepEqual(Object.keys(APP_SHELL_CATALOGS[locale]).sort(), reference)
    assert.ok(Object.values(APP_SHELL_CATALOGS[locale]).every(Boolean))
  }
  for (const key of [
    'documentTitle.profile',
    'documentTitle.security',
    'documentTitle.providers',
    'documentTitle.plan',
    'documentTitle.workspace',
    'documentTitle.privacy',
    'documentTitle.accountDeletionCancel',
    'documentTitle.verifyEmail',
    'shell.nav.home',
    'shell.nav.privacy',
    'home.card.profile.title',
    'profile.title',
    'security.title',
    'providers.title',
    'plan.title',
    'workspace.title',
    'privacy.title',
    'privacy.confirmAccountDeletion',
    'privacy.accountDeletionOwnedWorkspaces',
    'privacy.accountDeletionOwnershipUnavailable',
    'accountDeletionCancel.title',
    'accountDeletionCancel.securityNote',
    'verify.title',
    'auth.confirmation',
    'auth.registerSubmit',
  ] as const) {
    assert.ok(APP_SHELL_CATALOGS.en[key])
  }
})

test('all primary app routes declare a localized non-empty document title', async () => {
  const routes = {
    app: '../pages/app.vue',
    callback: '../pages/oauth-callback.vue',
    onboarding: '../pages/onboarding.vue',
    home: '../pages/home.vue',
    feature: '../pages/feature-slot.vue',
    accountDeletionCancel: '../pages/account-deletion-cancel.vue',
  } as const
  const sources = await Promise.all(
    Object.values(routes).map(path =>
      readFile(new URL(path, import.meta.url), 'utf8')),
  )

  for (const [index, route] of Object.keys(routes).entries()) {
    assert.match(sources[index], /useHead\(computed\(\(\) => \(\{/u)
    assert.match(
      sources[index],
      new RegExp(`title: t\\('documentTitle\\.${route}'\\)`, 'u'),
    )
  }

  for (const [index, locale] of APP_SHELL_LOCALES.entries()) {
    for (const route of Object.keys(routes)) {
      const key = `documentTitle.${route}` as keyof typeof APP_SHELL_CATALOGS.en
      const title = APP_SHELL_CATALOGS[locale][key]
      assert.notEqual(title.trim(), '', `${locale}.${key}`)
      assert.match(title, / — Postqron$/u, `${locale}.${key}`)
      if (index > 0) {
        assert.notEqual(title, APP_SHELL_CATALOGS.en[key], `${locale}.${key}`)
      }
    }
  }
})

test('manifest discovers public entry, callback, private routes, and no central registry', async () => {
  const manifest = await readFile(
    new URL('../feature.yaml', import.meta.url),
    'utf8',
  )
  assert.match(manifest, /path: \/app\n[\s\S]*visibility: public/u)
  assert.match(manifest, /path: \/app\/oauth\/callback/u)
  assert.match(manifest, /path: \/app\/home[\s\S]*visibility: private[\s\S]*middleware: \[app-session\]/u)
  assert.match(manifest, /path: \/app\/onboarding[\s\S]*visibility: private/u)
  assert.match(
    manifest,
    /path: \/app\/account-deletions\/:requestId\/cancel[\s\S]*visibility: public[\s\S]*middleware: \[\]/u,
  )
  for (const dependency of ['auth', 'account-privacy', 'workspaces', 'email', 'i18n']) {
    assert.match(manifest, new RegExp(`  - ${dependency}\\n`, 'u'))
  }
})

test('shell exposes accessible states and declarative slots', async () => {
  const [state, layout, home, feature] = await Promise.all([
    readFile(new URL('../components/AppState.vue', import.meta.url), 'utf8'),
    readFile(new URL('../layouts/app-shell.vue', import.meta.url), 'utf8'),
    readFile(new URL('../pages/home.vue', import.meta.url), 'utf8'),
    readFile(new URL('../pages/feature-slot.vue', import.meta.url), 'utf8'),
  ])
  assert.match(state, /aria-live="polite"/u)
  assert.match(state, /aria-busy=/u)
  assert.match(layout, /href="#app-main"/u)
  assert.match(layout, /data-postqron-slot="primary-navigation"/u)
  assert.match(layout, /data-postqron-slot="workspace-actions"/u)
  assert.match(layout, /appRoute\(locale, 'profile'\)[\s\S]*t\('shell\.logout'\)/u)
  assert.match(layout, /<summary[\s\S]*:aria-label="`\$\{t\('shell\.profile'\)\}/u)
  assert.match(home, /data-postqron-slot="home-primary"/u)
  assert.match(feature, /data-postqron-slot="feature-content"/u)
})

test('privacy flow requires explicit confirmation before deletion requests', async () => {
  const privacy = await readFile(
    new URL('../pages/privacy.vue', import.meta.url),
    'utf8',
  )
  assert.match(privacy, /confirmAction\(message\)/u)
  assert.match(privacy, /privacy\.confirmAccountDeletion/u)
  assert.match(privacy, /privacy\.confirmWorkspaceDeletion/u)
  assert.match(
    privacy,
    /issueAccountDeletionCancelCapability\(\)[\s\S]*requestDeletion\(/u,
  )
  assert.match(privacy, /buildAccountDeletionOwnershipActions\(accountArea\.value\)/u)
  assert.match(privacy, /ownershipActions/u)
  assert.match(privacy, /v-for="item in ownerWorkspaces"/u)
  assert.match(privacy, /cancelWorkspaceDeletion/u)
  assert.doesNotMatch(privacy, /useAppSessionState/u)
})

test('account deletion cancellation is public and never fetches private account state', async () => {
  const [page, middleware, api] = await Promise.all([
    readFile(
      new URL('../pages/account-deletion-cancel.vue', import.meta.url),
      'utf8',
    ),
    readFile(new URL('../middleware/app-session.ts', import.meta.url), 'utf8'),
    readFile(new URL('../components/core/api.ts', import.meta.url), 'utf8'),
  ])
  assert.match(page, /definePageMeta\(\{ layout: false \}\)/u)
  assert.match(page, /cancelAccountDeletion\(requestId\.value\)/u)
  assert.match(page, /role="alert"/u)
  assert.match(page, /role="status"/u)
  assert.doesNotMatch(
    page,
    /useAsyncData|useAppAccountAreaState|useAppSessionState|\.accountArea\(|\.session\(/u,
  )
  assert.doesNotMatch(
    `${page}\n${api}`,
    /localStorage|sessionStorage|console\.(?:log|debug|info)/u,
  )
  assert.match(
    middleware,
    /isPublicAccountDeletionCancellationDestination\(to\.fullPath\)[\s\S]*return[\s\S]*useAppShellApi\(\)\.session/u,
  )
})

test('account pages render retryable loading and failure states', async () => {
  const sources = await Promise.all([
    '../pages/home.vue',
    '../pages/profile.vue',
    '../pages/security.vue',
    '../pages/providers.vue',
    '../pages/plan.vue',
    '../pages/workspace.vue',
    '../pages/privacy.vue',
    '../pages/onboarding.vue',
  ].map(path => readFile(new URL(path, import.meta.url), 'utf8')))

  for (const source of sources) {
    assert.match(source, /kind="loading"/u)
    assert.match(source, /:kind="pageState"/u)
    assert.match(source, /@retry="retry"/u)
  }
})

test('marketing CTAs continue to target the runtime app URL', async () => {
  const [header, catalog, home] = await Promise.all([
    readFile(
      new URL('../../f02-marketing-site/components/SiteHeader.vue', import.meta.url),
      'utf8',
    ),
    readFile(
      new URL('../../f02-marketing-site/components/PlanCatalog.vue', import.meta.url),
      'utf8',
    ),
    readFile(
      new URL('../../f02-marketing-site/pages/index.vue', import.meta.url),
      'utf8',
    ),
  ])
  assert.match(header, /:href="config\.public\.appUrl"/u)
  assert.match(catalog, /config\.public\.appUrl\}\?plan=/u)
  assert.match(home, /:href="config\.public\.appUrl"/u)
})

test('shell implementation contains no email-provider client', async () => {
  const sources = await Promise.all([
    '../components/core/email-events.ts',
    '../pages/app.vue',
    '../pages/onboarding.vue',
    '../runtime.ts',
  ].map(path => readFile(new URL(path, import.meta.url), 'utf8')))
  const implementation = sources.join('\n').toLowerCase()
  assert.doesNotMatch(implementation, /mailronix|smtp|\/email\/send/u)
  assert.match(implementation, /channel: 'transactional'/u)
})
