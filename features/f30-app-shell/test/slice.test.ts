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
  for (const dependency of ['auth', 'workspaces', 'email', 'i18n']) {
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
  assert.match(home, /data-postqron-slot="home-primary"/u)
  assert.match(feature, /data-postqron-slot="feature-content"/u)
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
