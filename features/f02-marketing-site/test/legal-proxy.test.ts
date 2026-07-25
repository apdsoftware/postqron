import assert from 'node:assert/strict'
import test from 'node:test'
import { LegalRepository } from '../../f25-legal-documents/src/index.ts'
import { validReleaseInput } from '../../f25-legal-documents/test/fixtures.ts'
import { handleLegalProxyRequest, parsePublishedLegalDocument } from '../src/legal.ts'

const now = '2026-07-25T12:30:00.000Z'

test('the F25 adapter is used directly: an installed active-market release resolves a supported locale', async () => {
  const repository = await LegalRepository.create(await validReleaseInput())
  const response = await handleLegalProxyRequest({
    method: 'GET',
    slug: 'privacy',
    locale: 'fr',
    market: 'IT',
    now,
    repository,
  })

  assert.equal(response.status, 200)
  const body = response.body as { document: string, locale: string, fallbackUsed: boolean }
  assert.equal(body.document, 'privacy')
  assert.equal(body.locale, 'fr')
  assert.equal(body.fallbackUsed, false)
  assert.equal(response.headers['x-legal-document-version'], '1.0')
  assert.match(response.headers.etag ?? '', /^"sha256-[a-f0-9]{64}"$/u)
})

test('legacy public slugs (termini/privacy/cookie) keep working and map to the F25 document types', async () => {
  const repository = await LegalRepository.create(await validReleaseInput())
  const cases: Array<[string, string]> = [
    ['termini', 'terms'],
    ['privacy', 'privacy'],
    ['cookie', 'cookies'],
  ]
  for (const [slug, document] of cases) {
    const response = await handleLegalProxyRequest({
      method: 'GET',
      slug,
      locale: 'it',
      market: 'IT',
      now,
      repository,
    })
    assert.equal(response.status, 200, `${slug} should resolve`)
    assert.equal((response.body as { document: string }).document, document)
  }
})

test('an unsupported locale falls back to the release fallback locale (en)', async () => {
  const repository = await LegalRepository.create(await validReleaseInput())
  const response = await handleLegalProxyRequest({
    method: 'GET',
    slug: 'termini',
    locale: 'zz',
    market: 'IT',
    now,
    repository,
  })

  assert.equal(response.status, 200)
  // The F25 HTTP adapter normalizes the locale before it reaches the
  // repository (see f25-legal-documents/src/api.ts), so an unrecognized
  // locale is served as the release's English fallback content.
  const body = response.body as { locale: string, requestedLocale: string }
  assert.equal(body.locale, 'en')
  assert.equal(body.requestedLocale, 'en')
})

test('an invalid document slug is rejected without exposing internal routing', async () => {
  const repository = await LegalRepository.create(await validReleaseInput())
  const response = await handleLegalProxyRequest({
    method: 'GET',
    slug: 'not-a-real-document',
    locale: 'it',
    market: 'IT',
    now,
    repository,
  })

  assert.equal(response.status, 404)
  assert.equal(
    (response.body as { error: { code: string } }).error.code,
    'legal_document_not_found',
  )
})

test('non-GET requests are rejected fail-closed regardless of release state', async () => {
  const repository = await LegalRepository.create(await validReleaseInput())
  const response = await handleLegalProxyRequest({
    method: 'POST',
    slug: 'privacy',
    locale: 'it',
    market: 'IT',
    now,
    repository,
  })

  assert.equal(response.status, 405)
  assert.equal(response.headers.allow, 'GET')
})

test('with an empty repository the proxy stays fail-closed: 503, no-store, no draft content', async () => {
  const repository = await LegalRepository.create({
    artifacts: [],
    evidence: [],
    releases: [],
    marketAllowlist: [],
  })
  const response = await handleLegalProxyRequest({
    method: 'GET',
    slug: 'termini',
    locale: 'it',
    now,
    repository,
  })

  assert.equal(response.status, 503)
  assert.equal(response.headers['cache-control'], 'no-store')
  assert.equal(
    (response.body as { error: { code: string } }).error.code,
    'legal_release_blocked',
  )
  assert.doesNotMatch(JSON.stringify(response.body), /content/iu)
})

test('the real BUNDLED_LEGAL_RELEASE resolves the approved F25 release without a fixture repository', async () => {
  const response = await handleLegalProxyRequest({
    method: 'GET',
    slug: 'termini',
    locale: 'it',
    market: 'IT',
    now: '2026-07-30T00:00:00.000Z',
  })

  assert.equal(response.status, 200)
  const body = response.body as { document: string, locale: string, version: string }
  assert.equal(body.document, 'terms')
  assert.equal(body.locale, 'it')
  assert.equal(body.version, '0.1')
})

test('an F25 approved release body survives the client-side parser with content, version, and fallback metadata intact', async () => {
  const repository = await LegalRepository.create(await validReleaseInput())
  const response = await handleLegalProxyRequest({
    method: 'GET',
    slug: 'termini',
    locale: 'zz',
    market: 'IT',
    now,
    repository,
  })
  assert.equal(response.status, 200)

  const parsed = parsePublishedLegalDocument(response.body)
  const withGateMetadata = parsed as unknown as {
    content: string
    version: string
    effectiveAt: string
    digestSha256: string
    requestedLocale: string
    fallbackUsed: boolean
    permanentUrl: string
  }
  assert.equal(withGateMetadata.version, '1.0')
  assert.ok(withGateMetadata.content.length > 0)
  assert.equal(withGateMetadata.effectiveAt, '2026-07-25T12:00:00.000Z')
  assert.match(withGateMetadata.digestSha256, /^[a-f0-9]{64}$/u)
  // F25-only metadata (absent from the legacy F13 shape) must not be
  // stripped by the parser: the fallback locale used to serve this
  // unsupported request stays visible to callers that want it.
  assert.equal(withGateMetadata.requestedLocale, 'en')
  assert.equal(withGateMetadata.fallbackUsed, false)
  assert.match(withGateMetadata.permanentUrl, /^\/legal\/terms\/1\.0\?locale=en$/u)
})

test('a market with no active release never resolves, even against a complete fixture bundle', async () => {
  const repository = await LegalRepository.create(await validReleaseInput())
  const response = await handleLegalProxyRequest({
    method: 'GET',
    slug: 'termini',
    locale: 'en',
    market: 'SEE',
    now,
    repository,
  })

  assert.equal(response.status, 404)
})
