import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import test from 'node:test'
import {
  APP_SHELL_CATALOGS,
  APP_SHELL_LOCALES,
} from '../components/core/catalogs.ts'
import { appRoute } from '../components/core/navigation.ts'

function source(path: string): Promise<string> {
  return readFile(new URL(path, import.meta.url), 'utf8')
}

test('publish and calendar are localized private routes in every locale', async () => {
  for (const locale of APP_SHELL_LOCALES) {
    assert.equal(appRoute(locale, 'publish'), `/${locale}/app/publish`)
    assert.equal(appRoute(locale, 'calendar'), `/${locale}/app/calendar`)
    assert.match(APP_SHELL_CATALOGS[locale]['documentTitle.publish'], / — Postqron$/u)
    assert.match(APP_SHELL_CATALOGS[locale]['documentTitle.calendar'], / — Postqron$/u)
  }
  const manifest = await source('../feature.yaml')
  for (const [path, file] of [
    ['publish', 'publish.vue'],
    ['calendar', 'calendar.vue'],
  ]) {
    assert.match(
      manifest,
      new RegExp(
        `path: /app/${path}\\n\\s+file: ./pages/${file.replace('.', '\\.')}\\n`
        + '\\s+visibility: private\\n\\s+middleware: \\[app-session\\]',
        'u',
      ),
    )
  }
  assert.match(manifest, /- f06-composer/u)
  assert.match(manifest, /- scheduling/u)
})

test('the authenticated shell exposes Publish, Calendar, and the global New post CTA', async () => {
  const layout = await source('../layouts/app-shell.vue')
  assert.match(layout, /key: 'publish', href: appRoute\(locale\.value, 'publish'\)/u)
  assert.match(layout, /key: 'calendar', href: appRoute\(locale\.value, 'calendar'\)/u)
  assert.match(layout, /class="pq-button product-topbar__primary"[\s\S]*shell\.newPost/u)
})

test('composer and calendar expose real loading, empty, access, offline, retry, and mutation states', async () => {
  const [composer, calendar] = await Promise.all([
    source('../pages/publish.vue'),
    source('../pages/calendar.vue'),
  ])
  for (const page of [composer, calendar]) {
    assert.match(page, /kind="loading"/u)
    assert.match(page, /:kind="pageState"/u)
    assert.match(page, /@retry="refresh"/u)
    assert.match(page, /'access-denied'/u)
    assert.match(page, /'offline'/u)
    assert.match(page, /'unavailable'/u)
  }
  assert.match(composer, /composer\.emptyTitle/u)
  assert.match(composer, /composerApi\.validateDraft/u)
  assert.match(composer, /composerApi\.authorizeMedia/u)
  assert.match(composer, /submitScheduledDraft/u)
  assert.match(calendar, /calendar\.emptyTitle/u)
  assert.match(calendar, /schedulingApi\.duplicate/u)
  assert.match(calendar, /schedulingApi\.reschedule/u)
  assert.match(calendar, /schedulingApi\.cancel/u)
})
