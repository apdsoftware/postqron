import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import { dirname, resolve } from 'node:path'
import test from 'node:test'
import { fileURLToPath } from 'node:url'
import { DOCUMENT_TYPES, LEGAL_LOCALES, LegalRepository, loadDraftArtifacts } from '../src/index.ts'

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

test('page remains non-indexable and throws 503 while release is empty', async () => {
  const page = await source('pages/legal-document.vue')
  assert.match(page, /robots: 'noindex, nofollow'/u)
  assert.match(page, /statusCode: 503/u)
  assert.match(page, /legal_release_blocked/u)
  assert.match(page, /loadBundledRepository/u)
})

test('the bundled release stays empty even though a draft corpus is committed', async () => {
  const bundle = await source('src/bundle.ts')
  assert.match(bundle, /artifacts: Object\.freeze\(\[\]\)/u)
  assert.match(bundle, /evidence: Object\.freeze\(\[\]\)/u)
  assert.match(bundle, /releases: Object\.freeze\(\[\]\)/u)
})

test('the committed draft corpus covers every document and locale as a draft', async () => {
  const artifacts = await loadDraftArtifacts()
  assert.equal(artifacts.length, DOCUMENT_TYPES.length * LEGAL_LOCALES.length)
  for (const document of DOCUMENT_TYPES) {
    for (const locale of LEGAL_LOCALES) {
      const artifact = artifacts.find(item => item.document === document && item.locale === locale)
      assert.ok(artifact, `missing draft for ${document}:${locale}`)
      assert.equal(artifact.status, 'draft_pending_legal_review')
    }
  }
})

test('draft artifacts alone can never satisfy the publication gate', async () => {
  const artifacts = await loadDraftArtifacts()
  const repository = await LegalRepository.create({ artifacts, evidence: [], releases: [] })
  assert.equal(repository.ready, false)
})
