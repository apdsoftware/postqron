import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import test from 'node:test'
import {
  APP_SHELL_CATALOGS,
  APP_SHELL_LOCALES,
} from '../components/core/catalogs.ts'
import {
  appRoute,
  type AppSection,
} from '../components/core/navigation.ts'

function source(path: string): Promise<string> {
  return readFile(new URL(path, import.meta.url), 'utf8')
}

test('social channels route is distinct from the access-methods route', () => {
  for (const locale of APP_SHELL_LOCALES) {
    assert.equal(
      appRoute(locale, 'social-channels' as AppSection),
      `/${locale}/app/social-channels`,
    )
    assert.notEqual(
      appRoute(locale, 'social-channels' as AppSection),
      appRoute(locale, 'providers'),
    )
  }
})

test('the manifest registers a private, session-guarded social channels page', async () => {
  const manifest = await source('../feature.yaml')
  assert.match(
    manifest,
    /path: \/app\/social-channels\n\s+file: \.\/pages\/social-channels\.vue\n\s+visibility: private\n\s+middleware: \[app-session\]/u,
  )
  // The UI consumes exactly the F5 contract slice.
  assert.match(manifest, /- social-connections\n/u)
})

test('the shell navigation exposes Social channels next to Access methods', async () => {
  const layout = await source('../layouts/app-shell.vue')
  assert.match(layout, /key: 'social', href: appRoute\(locale\.value, 'social-channels'\)/u)
  assert.match(layout, /key: 'providers'/u)
})

test('the social channels page follows the accessible retryable page contract', async () => {
  const page = await source('../pages/social-channels.vue')
  assert.match(page, /kind="loading"/u)
  assert.match(page, /:kind="pageState"/u)
  assert.match(page, /@retry="retry"/u)
  assert.match(page, /'unavailable'/u)
  assert.match(page, /useAsyncData\([\s\S]*\}, \{ server: false \}\)/u)
  assert.match(page, /useSocialConnectionsApi/u)
  assert.match(page, /documentTitle\.socialChannels/u)
  // Availability bootstrap, connect, callback selection, reconnect, revoke.
  assert.match(page, /social\.bootstrap\(workspaceId\.value\)/u)
  assert.match(page, /social\.begin\(workspaceId\.value/u)
  assert.match(page, /social\.completeAuthorization\(/u)
  assert.match(page, /social\.selectResource\(workspaceId\.value/u)
  assert.match(page, /social\.reconnect\(workspaceId\.value/u)
  assert.match(page, /social\.revoke\(workspaceId\.value/u)
  // Fail-closed provider availability drives an explicit unavailable state.
  assert.match(page, /provider\.status === 'available'/u)
  assert.match(page, /social\.providerUnavailable/u)
  // Accessible feedback: errors announce as alert, successes as status.
  assert.match(page, /:role="notice\.tone === 'success' \? 'status' : 'alert'"/u)
})

test('the social channels page never touches token or browser storage', async () => {
  const [page, api, contract] = await Promise.all([
    source('../pages/social-channels.vue'),
    source('../components/core/social-api.ts'),
    source('../components/core/social-connections.ts'),
  ])
  const joined = `${page}\n${api}\n${contract}`.toLowerCase()
  assert.doesNotMatch(joined, /localstorage|sessionstorage/u)
  assert.doesNotMatch(joined, /access_token|refresh_token|page_access_token/u)
})

test('every social catalog key is present, localized, and identical across locales', () => {
  const socialKeys = Object.keys(APP_SHELL_CATALOGS.en)
    .filter(key => key.startsWith('social.')
      || key === 'shell.nav.social'
      || key === 'documentTitle.socialChannels'
      || key.startsWith('home.card.social.'))
  assert.ok(socialKeys.includes('social.title'))
  assert.ok(socialKeys.includes('social.errorQuota'))
  assert.ok(socialKeys.includes('social.status.reconnect_required'))
  assert.ok(socialKeys.includes('social.providerUnavailable'))

  const reference = new Set(Object.keys(APP_SHELL_CATALOGS.en))
  for (const key of socialKeys) {
    for (const locale of APP_SHELL_LOCALES) {
      const value = APP_SHELL_CATALOGS[locale][key as keyof typeof APP_SHELL_CATALOGS.en]
      assert.ok(value && value.trim() !== '', `${locale}.${key}`)
    }
    assert.ok(reference.has(key))
  }

  // The document title stays branded and per-locale distinct.
  for (const locale of APP_SHELL_LOCALES) {
    assert.match(
      APP_SHELL_CATALOGS[locale]['documentTitle.socialChannels'],
      / — Postqron$/u,
    )
  }
  assert.notEqual(
    APP_SHELL_CATALOGS.it['shell.nav.social'],
    APP_SHELL_CATALOGS.en['shell.nav.social'],
  )
})

test('home surfaces a social channels shortcut card', async () => {
  const home = await source('../pages/home.vue')
  assert.match(home, /key: 'social', href: appRoute\([\s\S]*'social-channels'\)/u)
  for (const suffix of ['eyebrow', 'title', 'description']) {
    assert.ok(APP_SHELL_CATALOGS.en[`home.card.social.${suffix}` as keyof typeof APP_SHELL_CATALOGS.en])
  }
})
