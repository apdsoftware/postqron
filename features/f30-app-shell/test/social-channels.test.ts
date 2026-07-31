import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import test from 'node:test'
import {
  APP_SHELL_CATALOGS,
  APP_SHELL_LOCALES,
} from '../components/core/catalogs.ts'
import {
  appRoute,
} from '../components/core/navigation.ts'

function source(path: string): Promise<string> {
  return readFile(new URL(path, import.meta.url), 'utf8')
}

test('social channels route remains available in every locale', () => {
  for (const locale of APP_SHELL_LOCALES) {
    assert.equal(
      appRoute(locale, 'social-channels'),
      `/${locale}/app/social-channels`,
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

test('the shell navigation exposes Social channels without access methods', async () => {
  const layout = await source('../layouts/app-shell.vue')
  assert.match(layout, /key: 'social', href: appRoute\(locale\.value, 'social-channels'\)/u)
  assert.doesNotMatch(layout, /key: 'providers'|appRoute\([^)]*'providers'/u)
})

test('the shell always signals transition completion and exposes recovery feedback', async () => {
  const [layout, composable] = await Promise.all([
    source('../layouts/app-shell.vue'),
    source('../components/core/use-app-shell.ts'),
  ])
  assert.match(layout, /finally \{[\s\S]*workspaceTransitionRevision\.value \+= 1/u)
  assert.match(layout, /serverCommitted = true[\s\S]*api\.selectWorkspace\(previousWorkspaceId\)[\s\S]*const recoveredSession = await api\.session\(\)/u)
  assert.match(layout, /recoveredSession\.current_workspace\?\.id !== previousWorkspaceId/u)
  assert.match(layout, /session\.value = undefined[\s\S]*workspaceRecoveryUnavailable\.value = true/u)
  assert.match(layout, /kind="unavailable"[\s\S]*@retry="retryWorkspaceRecovery"/u)
  assert.match(layout, /role="alert"[\s\S]*shell\.workspaceSwitch/u)
  assert.match(composable, /postqron\.app-shell\.workspace-transition-revision/u)
})

test('the social channels page follows the accessible retryable page contract', async () => {
  const page = await source('../pages/social-channels.vue')
  assert.match(page, /kind="loading"/u)
  assert.match(page, /:kind="pageState"/u)
  assert.match(page, /@retry="retry"/u)
  assert.match(page, /'unavailable'/u)
  assert.match(page, /useAsyncData\([\s\S]*\}, \{ server: false, watch: \[workspaceId\] \}\)/u)
  assert.match(page, /useSocialConnectionsApi/u)
  assert.match(page, /documentTitle\.socialChannels/u)
  // Availability bootstrap, connect, callback selection, reconnect, revoke.
  assert.match(page, /social\.bootstrap\(context\.workspaceId\)/u)
  assert.match(page, /social\.begin\([\s\S]*context\.workspaceId/u)
  assert.match(page, /social\.completeAuthorization\(/u)
  assert.match(page, /social\.selectResource\(context\.workspaceId/u)
  assert.match(page, /social\.reconnect\(context\.workspaceId/u)
  assert.match(page, /social\.revoke\(context\.workspaceId/u)
  assert.match(page, /function contextIsCurrent\([\s\S]*context\.epoch === loadEpoch/u)
  assert.match(page, /watch\(workspaceTransitionRevision,[\s\S]*void refresh\(\)/u)
  assert.match(page, /activePopups[\s\S]*closeActivePopups/u)
  // Fail-closed provider availability drives an explicit unavailable state.
  assert.match(page, /catalogState\(provider\) === 'available'/u)
  assert.match(page, /social\.catalogState\.\$\{catalogState\(provider\)\}/u)
  // Accessible feedback: errors announce as alert, successes as status.
  assert.match(page, /:role="notice\.tone === 'success' \? 'status' : 'alert'"/u)
})

test('the page distinguishes configuration, access denial, and temporary Meta errors', async () => {
  const [page, api] = await Promise.all([
    source('../pages/social-channels.vue'),
    source('../components/core/social-api.ts'),
  ])
  assert.match(page, /case 'provider-unavailable':[\s\S]*'social\.errorProviderUnavailable'/u)
  assert.match(page, /case 'provider-access-denied':[\s\S]*'social\.errorProviderAccessDenied'/u)
  assert.match(page, /case 'provider-temporary':[\s\S]*'social\.errorProviderTemporary'/u)
  assert.match(api, /provider_unavailable: 'provider-unavailable'/u)
  assert.match(api, /provider_access_denied: 'provider-access-denied'/u)
  assert.match(api, /provider_temporary: 'provider-temporary'/u)
  assert.match(APP_SHELL_CATALOGS.it['social.providerUnavailableHint'], /non è un errore temporaneo/u)
})

test('the social channels page never touches token or browser storage', async () => {
  const [page, api] = await Promise.all([
    source('../pages/social-channels.vue'),
    source('../components/core/social-api.ts'),
  ])
  const joined = `${page}\n${api}`.toLowerCase()
  assert.doesNotMatch(joined, /localstorage|sessionstorage/u)
  assert.doesNotMatch(joined, /\baccess_token\b|\brefresh_token\b|\bpage_access_token\b/u)
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
  assert.ok(socialKeys.includes('social.errorProviderAccessDenied'))
  assert.ok(socialKeys.includes('social.errorProviderTemporary'))

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
