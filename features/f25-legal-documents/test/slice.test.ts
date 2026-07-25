import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import { dirname, resolve } from 'node:path'
import test from 'node:test'
import { fileURLToPath } from 'node:url'
import { DOCUMENT_TYPES, LEGAL_LOCALES, LegalRepository } from '../src/index.ts'
import { loadDraftArtifacts } from '../src/content.ts'

const featureRoot = resolve(dirname(fileURLToPath(import.meta.url)), '..')
const source = (path: string) => readFile(resolve(featureRoot, path), 'utf8')

test('manifest owns the five current routes and immutable history route', async () => {
  const manifest = await source('feature.yaml')
  assert.match(manifest, /^id: legal-documents$/mu)
  assert.match(manifest, /dependencies:\n {2}- i18n\n {2}- marketing-site/mu)
  assert.deepEqual(
    [...manifest.matchAll(/path: (\/legal\/[a-z]+)$/gmu)].map(match => match[1]),
    ['/legal/terms', '/legal/privacy', '/legal/cookies', '/legal/dpa', '/legal/subprocessors'],
  )
  assert.match(manifest, /path: \/legal\/:document\/:version/u)
  assert.equal((manifest.match(/visibility: public/gu) || []).length, 6)
})

test('page remains non-indexable and still throws 503 for a request the bundle cannot serve', async () => {
  const page = await source('pages/legal-document.vue')
  assert.match(page, /robots: 'noindex, nofollow'/u)
  assert.match(page, /statusCode: 503/u)
  assert.match(page, /legal_release_blocked/u)
  assert.match(page, /loadBundledRepository/u)
})

test('the bundled release is generated from the approved corpus and references the external approval', async () => {
  const bundle = await source('src/bundle.ts')
  assert.match(bundle, /LEGAL-APPROVAL-2026-07-25-F25/u)
  assert.doesNotMatch(bundle, /artifacts: Object\.freeze\(\[\]\)/u)
  assert.doesNotMatch(bundle, /evidence: Object\.freeze\(\[\]\)/u)
  assert.doesNotMatch(bundle, /releases: Object\.freeze\(\[\]\)/u)
})

test('the committed corpus covers every document and locale as approved', async () => {
  const artifacts = await loadDraftArtifacts()
  assert.equal(artifacts.length, DOCUMENT_TYPES.length * LEGAL_LOCALES.length)
  for (const document of DOCUMENT_TYPES) {
    for (const locale of LEGAL_LOCALES) {
      const artifact = artifacts.find(item => item.document === document && item.locale === locale)
      assert.ok(artifact, `missing artifact for ${document}:${locale}`)
      assert.equal(artifact.status, 'approved')
    }
  }
})

test('artifacts without evidence or a release can never satisfy the publication gate', async () => {
  const artifacts = await loadDraftArtifacts()
  const repository = await LegalRepository.create({
    artifacts,
    evidence: [],
    releases: [],
    marketAllowlist: [],
  })
  assert.equal(repository.ready, false)
})
