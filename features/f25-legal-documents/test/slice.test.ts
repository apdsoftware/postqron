import assert from 'node:assert/strict'
import { readdir, readFile } from 'node:fs/promises'
import { dirname, resolve } from 'node:path'
import test from 'node:test'
import { fileURLToPath } from 'node:url'

const featureRoot = resolve(dirname(fileURLToPath(import.meta.url)), '..')
const source = (path: string) => readFile(resolve(featureRoot, path), 'utf8')

test('manifest owns the three current routes and immutable history route', async () => {
  const manifest = await source('feature.yaml')
  assert.match(manifest, /^id: legal-documents$/mu)
  assert.match(manifest, /dependencies:\n {2}- i18n\n {2}- marketing-site/mu)
  assert.deepEqual(
    [...manifest.matchAll(/path: (\/legal\/[a-z]+)$/gmu)].map(match => match[1]),
    ['/legal/terms', '/legal/privacy', '/legal/cookies'],
  )
  assert.match(manifest, /path: \/legal\/:document\/:version/u)
  assert.equal((manifest.match(/visibility: public/gu) || []).length, 4)
})

test('page remains non-indexable and throws 503 while release is empty', async () => {
  const page = await source('pages/legal-document.vue')
  assert.match(page, /robots: 'noindex, nofollow'/u)
  assert.match(page, /statusCode: 503/u)
  assert.match(page, /legal_release_blocked/u)
  assert.match(page, /loadBundledRepository/u)
})

test('no approved-content directory or legal copy is committed', async () => {
  const entries = await readdir(featureRoot, { withFileTypes: true })
  assert.equal(entries.some(entry => entry.name === 'artifacts'), false)
  const bundle = await source('src/bundle.ts')
  assert.match(bundle, /artifacts: Object\.freeze\(\[\]\)/u)
  assert.match(bundle, /evidence: Object\.freeze\(\[\]\)/u)
  assert.match(bundle, /releases: Object\.freeze\(\[\]\)/u)
})
