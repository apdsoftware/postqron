import assert from 'node:assert/strict'
import { createHash } from 'node:crypto'
import test from 'node:test'
import {
  contentSecurityPolicyForHtml,
  inlineScriptHashes,
  STATIC_CONTENT_SECURITY_POLICY,
} from '../server/utils/content-security-policy.ts'

const hash = content => `'sha256-${
  createHash('sha256').update(content, 'utf8').digest('base64')
}'`

test('the static policy keeps inline scripts blocked', () => {
  assert.match(STATIC_CONTENT_SECURITY_POLICY, /script-src 'self'(?:;|$)/u)
  assert.doesNotMatch(
    STATIC_CONTENT_SECURITY_POLICY,
    /script-src[^;]*'unsafe-inline'/u,
  )
})

test('SSR policy authorizes only the exact inline Nuxt scripts', () => {
  const bootstrap = 'window.__NUXT__={config:{public:{apiBase:"/api"}}}'
  const payload = '[{"serverRendered":true}]'
  const policy = contentSecurityPolicyForHtml([
    `<script>${bootstrap}</script>`,
    `<script src="/_nuxt/entry.js"></script>
     <script type="application/json" id="__NUXT_DATA__">${payload}</script>`,
  ])

  assert.match(policy, /script-src 'self'/u)
  assert.equal(policy.includes(hash(bootstrap)), true)
  assert.equal(policy.includes(hash(payload)), true)
  assert.doesNotMatch(policy, /script-src[^;]*'unsafe-inline'/u)
})

test('inline hashes are stable, deduplicated, and ignore external scripts', () => {
  const inline = 'window.__NUXT__.config={public:{siteUrl:"https://example.test"}}'

  assert.deepEqual(inlineScriptHashes([
    `<script>${inline}</script><script>${inline}</script>`,
    '<script src="https://cdn.example.test/library.js"></script>',
  ]), [hash(inline)])
})
