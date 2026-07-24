import assert from 'node:assert/strict'
import test from 'node:test'
import {
  isPublicLegalSlug,
  parsePublishedLegalDocument,
  toLegalBlocks,
} from '../src/legal.ts'

test('only public F13 legal routes are accepted', () => {
  assert.equal(isPublicLegalSlug('termini'), true)
  assert.equal(isPublicLegalSlug('privacy'), true)
  assert.equal(isPublicLegalSlug('cookie'), true)
  assert.equal(isPublicLegalSlug('draft'), false)
})

test('only approved F13 artifacts can be rendered', () => {
  assert.throws(
    () => parsePublishedLegalDocument({
      contentStatus: 'placeholder',
      content: '# Bozza',
      version: '0.0',
      digestSha256: 'a'.repeat(64),
      effectiveAt: '2026-07-24T00:00:00.000Z',
    }),
    /non è pubblicabile/,
  )

  const approved = {
    contentStatus: 'approved',
    content: '# Privacy\n\nTesto approvato.',
    version: '1.0',
    digestSha256: 'a'.repeat(64),
    effectiveAt: '2026-07-24T00:00:00.000Z',
  }
  assert.equal(parsePublishedLegalDocument(approved), approved)
})

test('approved markdown becomes semantic blocks without raw HTML rendering', () => {
  assert.deepEqual(
    toLegalBlocks('# Titolo\n\nTesto chiaro.\n\n- Uno\n- Due'),
    [
      { kind: 'heading', level: 2, text: 'Titolo' },
      { kind: 'paragraph', text: 'Testo chiaro.' },
      { kind: 'list', items: ['Uno', 'Due'] },
    ],
  )
})
