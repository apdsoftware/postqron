import assert from 'node:assert/strict'
import test from 'node:test'
import { LegalRepository } from '../../f25-legal-documents/src/index.ts'
import { validReleaseInput } from '../../f25-legal-documents/test/fixtures.ts'
import { handleLegalProxyRequest } from '../src/legal.ts'

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

test('with an empty BUNDLED_LEGAL_RELEASE the proxy stays fail-closed: 503, no-store, no draft content', async () => {
  const response = await handleLegalProxyRequest({
    method: 'GET',
    slug: 'termini',
    locale: 'it',
    now,
  })

  assert.equal(response.status, 503)
  assert.equal(response.headers['cache-control'], 'no-store')
  assert.equal(
    (response.body as { error: { code: string } }).error.code,
    'legal_release_blocked',
  )
  assert.doesNotMatch(JSON.stringify(response.body), /content/iu)
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
