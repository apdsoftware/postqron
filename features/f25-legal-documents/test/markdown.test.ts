import assert from 'node:assert/strict'
import test from 'node:test'
import { BUNDLED_LEGAL_RELEASE } from '../src/bundle.ts'
import {
  parseLegalInline,
  parseLegalMarkdown,
  safeLegalHref,
  type LegalInline,
} from '../src/markdown.ts'
import { DOCUMENT_TYPES, LEGAL_LOCALES } from '../src/types.ts'

function walkInline(nodes: LegalInline[]): LegalInline[] {
  return nodes.flatMap(node =>
    node.type === 'strong' || node.type === 'emphasis' || node.type === 'link'
      ? [node, ...walkInline(node.children)]
      : [node])
}

test('parses the approved semantic subset and keeps the corpus H1 distinguishable', () => {
  const blocks = parseLegalMarkdown([
    '# Page title',
    '',
    '## Section',
    '',
    'A **strong** and *emphasized* paragraph with `inline code` and [a link](https://example.com).',
    '',
    '- one',
    '- two',
    '',
    '1. first',
    '2. second',
    '',
    '| Name | Value |',
    '|---|---|',
    '| A | B |',
  ].join('\n'))
  assert.deepEqual(blocks.map(block => block.type), [
    'heading', 'heading', 'paragraph', 'list', 'list', 'table',
  ])
  assert.equal(blocks[0]?.type === 'heading' && blocks[0].level, 1)
  assert.equal(blocks[1]?.type === 'heading' && blocks[1].level, 2)
  assert.equal(blocks[3]?.type === 'list' && blocks[3].ordered, false)
  assert.equal(blocks[4]?.type === 'list' && blocks[4].ordered, true)
  assert.equal(blocks[5]?.type === 'table' && blocks[5].rows.length, 1)
  const inline = blocks[2]?.type === 'paragraph' ? walkInline(blocks[2].children) : []
  assert.ok(inline.some(node => node.type === 'strong'))
  assert.ok(inline.some(node => node.type === 'emphasis'))
  assert.ok(inline.some(node => node.type === 'code'))
  assert.ok(inline.some(node => node.type === 'link'))
})

test('allows only web and email links and never interprets raw HTML', () => {
  assert.equal(safeLegalHref('javascript:alert(1)'), null)
  assert.equal(safeLegalHref('data:text/html,<script>alert(1)</script>'), null)
  assert.equal(safeLegalHref('https://example.com/legal'), 'https://example.com/legal')
  assert.equal(safeLegalHref('privacy@postqron.com'), 'mailto:privacy@postqron.com')

  const nodes = parseLegalInline(
    '<script>alert(1)</script> [unsafe](javascript:alert(1)) privacy@postqron.com',
  )
  assert.equal(nodes[0]?.type, 'text')
  assert.ok(walkInline(nodes).some(node =>
    node.type === 'text' && node.value.includes('<script>')))
  assert.ok(walkInline(nodes).some(node =>
    node.type === 'link' && node.href === 'mailto:privacy@postqron.com'))
  assert.ok(!walkInline(nodes).some(node =>
    node.type === 'link' && node.href.startsWith('javascript:')))
})

test('parses all five documents in every locale and one immutable historical version', () => {
  const current = BUNDLED_LEGAL_RELEASE.artifacts.filter(artifact =>
    artifact.version === (artifact.document === 'terms' ? '0.2' : '0.1'))
  for (const document of DOCUMENT_TYPES) {
    for (const locale of LEGAL_LOCALES) {
      const artifact = current.find(item =>
        item.document === document && item.locale === locale)
      assert.ok(artifact, `missing current ${document}:${locale}`)
      const blocks = parseLegalMarkdown(artifact.content)
      assert.ok(blocks.filter(block =>
        block.type === 'heading' && block.level === 1).length <= 1)
      assert.ok(blocks.some(block =>
        block.type === 'heading' && block.level === 2))
      assert.ok(blocks.some(block => block.type === 'paragraph'))
      const visibleText = blocks.flatMap(block => {
        if (block.type === 'heading' || block.type === 'paragraph') {
          return walkInline(block.children)
        }
        if (block.type === 'list') {
          return block.items.flatMap(walkInline)
        }
        return [...block.header, ...block.rows.flat()].flatMap(walkInline)
      }).filter(node => node.type === 'text').map(node => node.value).join(' ')
      assert.doesNotMatch(visibleText, /(?:^|\s)#{1,3}\s|(?:^|\s)[-*]\s+\*\*/u)
    }
  }
  const historical = BUNDLED_LEGAL_RELEASE.artifacts.find(artifact =>
    artifact.document === 'terms' && artifact.locale === 'en' && artifact.version === '0.1')
  assert.ok(historical)
  assert.ok(parseLegalMarkdown(historical.content).length > 10)
})

test('page renderer uses Vue nodes, suppresses the corpus H1, and has responsive tables', async () => {
  const { readFile } = await import('node:fs/promises')
  const page = await readFile(
    new URL('../pages/legal-document.vue', import.meta.url),
    'utf8',
  )
  assert.doesNotMatch(page, /v-html/u)
  assert.doesNotMatch(page, /\{\{\s*published\.content\s*\}\}/u)
  assert.match(page, /block\.level === 1/u)
  assert.match(page, /overflow-x: auto/u)
  assert.match(page, /min-width: 42rem/u)
  assert.match(page, /scope: 'col'/u)
  assert.match(page, /noopener noreferrer/u)
})
