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

const directive = (policy, name) => policy
  .split('; ')
  .find(candidate => candidate.startsWith(`${name} `))

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
  ], 'https://api.postqron.com')

  assert.match(policy, /script-src 'self'/u)
  assert.equal(policy.includes(hash(bootstrap)), true)
  assert.equal(policy.includes(hash(payload)), true)
  assert.doesNotMatch(policy, /script-src[^;]*'unsafe-inline'/u)
  assert.equal(
    directive(policy, 'connect-src'),
    "connect-src 'self' https://api.postqron.com",
  )
})

test('inline hashes are stable, deduplicated, and ignore external scripts', () => {
  const inline = 'window.__NUXT__.config={public:{siteUrl:"https://example.test"}}'

  assert.deepEqual(inlineScriptHashes([
    `<script>${inline}</script><script>${inline}</script>`,
    '<script src="https://cdn.example.test/library.js"></script>',
  ]), [hash(inline)])
})

test('the public API origin is normalized with standard and custom ports', () => {
  const httpsDefault = contentSecurityPolicyForHtml(
    [],
    'https://API.EXAMPLE.test:443/api/v1?source=web',
  )
  const httpDefault = contentSecurityPolicyForHtml(
    [],
    'http://api.example.test:80/api/v1',
  )
  const customPort = contentSecurityPolicyForHtml(
    [],
    'https://api.example.test:8443/api/v1',
  )

  assert.equal(
    directive(httpsDefault, 'connect-src'),
    "connect-src 'self' https://api.example.test",
  )
  assert.equal(
    directive(httpDefault, 'connect-src'),
    "connect-src 'self' http://api.example.test",
  )
  assert.equal(
    directive(customPort, 'connect-src'),
    "connect-src 'self' https://api.example.test:8443",
  )
})

test('a relative API base stays covered by self', () => {
  assert.equal(
    contentSecurityPolicyForHtml([], '/api/v1'),
    STATIC_CONTENT_SECURITY_POLICY,
  )
})

test('invalid or non-HTTP API bases fail closed', () => {
  for (const apiBase of [
    'ftp://api.example.test/api/v1',
    'javascript:alert(1)',
    'https://*.example.test/api/v1',
    'https://operator:secret@api.example.test/api/v1',
    'https://api.example.test; script-src *',
    'not a URL',
    { origin: 'https://api.example.test' },
  ]) {
    assert.equal(
      contentSecurityPolicyForHtml([], apiBase),
      STATIC_CONTENT_SECURITY_POLICY,
    )
  }
})

test('adding an API origin preserves all other directives and hashes', () => {
  const inline = 'window.__NUXT__={payload:{serverRendered:true}}'
  const policy = contentSecurityPolicyForHtml(
    [`<script>${inline}</script>`],
    'https://api.postqron.com/v1',
  )

  assert.equal(directive(policy, 'default-src'), "default-src 'self'")
  assert.equal(directive(policy, 'img-src'), "img-src 'self' data:")
  assert.equal(
    directive(policy, 'style-src'),
    "style-src 'self' 'unsafe-inline'",
  )
  assert.equal(
    directive(policy, 'script-src'),
    `script-src 'self' ${hash(inline)}`,
  )
})
