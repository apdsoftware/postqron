import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import test from 'node:test'
import { DOCUMENT_TYPES, LEGAL_LOCALES, sha256 } from '../src/index.ts'
import { draftPath, loadDraftArtifacts, parseDraftFile } from '../src/content.ts'

test('every draft artifact parses with a matching path, digest, and status', async () => {
  const artifacts = await loadDraftArtifacts()
  assert.equal(artifacts.length, 25)
  for (const artifact of artifacts) {
    assert.equal(artifact.jurisdiction, 'IT')
    assert.equal(artifact.version, '0.1')
    assert.equal(artifact.status, 'draft_pending_legal_review')
    assert.match(artifact.digestSha256, /^[a-f0-9]{64}$/u)
    assert.equal(await sha256(artifact.content), artifact.digestSha256)
    assert.ok(artifact.content.length >= 500, `${artifact.document}:${artifact.locale} is too short`)
    assert.ok(artifact.title.trim().length > 0)
    assert.ok(artifact.controllerName.trim().length > 0)
    assert.match(artifact.contactEmail, /^[^\s@]+@[^\s@]+\.[^\s@]+$/u)
  }
})

test('a malformed draft file without frontmatter is rejected', async () => {
  assert.throws(() => parseDraftFile('no frontmatter here', 'fixture.md'))
})

test('a draft file missing a required frontmatter field is rejected', async () => {
  const raw = [
    '---',
    'document: terms',
    'locale: en',
    '---',
    '',
    'Body text.',
  ].join('\n')
  assert.throws(() => parseDraftFile(raw, 'fixture.md'))
})

test('draftPath resolves the expected file for every document and locale', async () => {
  for (const document of DOCUMENT_TYPES) {
    for (const locale of LEGAL_LOCALES) {
      const path = draftPath(document, locale)
      assert.match(path, new RegExp(`content/drafts/${document}/${locale}\\.md$`, 'u'))
      await readFile(path, 'utf8')
    }
  }
})
