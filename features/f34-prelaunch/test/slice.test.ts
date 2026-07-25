import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import test from 'node:test'

async function source(path: string) {
  return readFile(new URL(path, import.meta.url), 'utf8')
}

test('slice declares global middleware and owned routes', async () => {
  const manifest = await source('../feature.yaml')
  assert.match(manifest, /path: \/prelaunch\n/u)
  assert.match(manifest, /path: \/prelaunch\/access/u)
  assert.match(manifest, /name: prelaunch-mode[\s\S]*global: true/u)
  assert.match(manifest, /- prelaunch-access/u)
})

test('landing includes SEO, legal, support and accessible CTA contracts', async () => {
  const [page, layout] = await Promise.all([
    source('../pages/prelaunch.vue'),
    source('../layouts/prelaunch.vue'),
  ])
  assert.match(page, /useSeoMeta/u)
  assert.match(page, /rel: 'canonical'/u)
  assert.match(page, /hreflang/u)
  assert.match(page, /brand\/social-card\.svg/u)
  assert.match(page, /<h1>/u)
  assert.match(page, /class="pq-button"/u)
  assert.match(layout, /mailto:help@postqron\.com/u)
  assert.match(layout, /\/legal\/privacy/u)
  assert.match(layout, /\/legal\/cookies/u)
  assert.match(layout, /\/legal\/terms/u)
})

test('access form requires explicit consent and sends marketing false', async () => {
  const page = await source('../pages/access.vue')
  assert.match(page, /type="email"/u)
  assert.match(page, /type="checkbox"/u)
  assert.match(page, /required/u)
  assert.match(page, /name="access_consent"/u)
  assert.match(page, /name="marketing_consent"[\s\S]*value="false"/u)
  assert.match(page, /application\/x-www-form-urlencoded/u)
  assert.match(page, /noindex, nofollow/u)
  assert.doesNotMatch(page, /newsletter|subscribe/iu)
})
