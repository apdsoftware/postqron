import assert from 'node:assert/strict'
import test from 'node:test'
import {
  BUNDLED_LEGAL_RELEASE,
  LegalRepository,
  handleLegalApiRequest,
} from '../src/index.ts'
import { validReleaseInput } from './fixtures.ts'

const now = '2026-07-30T00:00:00.000Z'

test('blocked API returns 503 without legal content and cannot be cached', async () => {
  const repository = await LegalRepository.create({
    artifacts: [],
    evidence: [],
    releases: [],
    marketAllowlist: [],
  })
  const response = handleLegalApiRequest(repository, {
    method: 'GET',
    url: '/api/v1/legal-documents/terms/current?locale=it',
    now,
  })

  assert.equal(response.status, 503)
  assert.equal(response.headers['cache-control'], 'no-store')
  assert.deepEqual(response.body, {
    error: {
      code: 'legal_release_blocked',
      message: 'No complete legally approved release is installed.',
    },
  })
  assert.doesNotMatch(JSON.stringify(response.body), /content/iu)
})

test('the bundled release API exposes digest, locale, version, and history', async () => {
  const repository = await LegalRepository.create(BUNDLED_LEGAL_RELEASE)
  const current = handleLegalApiRequest(repository, {
    method: 'GET',
    url: '/api/v1/legal-documents/terms_it/current?locale=it',
    now,
  })
  assert.equal(current.status, 200)
  assert.equal(current.headers['x-legal-document-version'], '0.2')
  assert.match(current.headers.etag ?? '', /^"sha256-[a-f0-9]{64}"$/u)
  assert.equal((current.body as { locale: string }).locale, 'it')

  const historical = handleLegalApiRequest(repository, {
    method: 'GET',
    url: '/api/v1/legal-documents/privacy/versions/0.1?locale=it',
    now,
  })
  assert.equal(historical.status, 200)
  assert.equal((historical.body as { version: string }).version, '0.1')

  const historicalTerms = handleLegalApiRequest(repository, {
    method: 'GET',
    url: '/api/v1/legal-documents/terms/versions/0.1?locale=it',
    now,
  })
  assert.equal(historicalTerms.status, 200)
  assert.equal((historicalTerms.body as { version: string }).version, '0.1')
})

test('approved fixture API exposes digest, locale, version, and history', async () => {
  const repository = await LegalRepository.create(await validReleaseInput())
  const current = handleLegalApiRequest(repository, {
    method: 'GET',
    url: '/api/v1/legal-documents/privacy_it/current?locale=fr-CA',
    now,
  })
  assert.equal(current.status, 200)
  assert.equal(current.headers['x-legal-document-version'], '1.0')
  assert.match(current.headers.etag ?? '', /^"sha256-[a-f0-9]{64}"$/u)
  assert.equal((current.body as { locale: string }).locale, 'fr')

  const historical = handleLegalApiRequest(repository, {
    method: 'GET',
    url: '/api/v1/legal-documents/privacy/versions/0.9?locale=fr',
    now,
  })
  assert.equal(historical.status, 200)
  assert.equal((historical.body as { version: string }).version, '0.9')
})

test('the bundled release resolves the dpa and subprocessors aliases too', async () => {
  const repository = await LegalRepository.create(BUNDLED_LEGAL_RELEASE)
  for (const url of [
    '/api/v1/legal-documents/dpa_it/current?locale=it',
    '/api/v1/legal-documents/subprocessors/current?locale=en',
    '/api/v1/legal-documents/dpa/current?locale=en',
  ]) {
    const response = handleLegalApiRequest(repository, { method: 'GET', url, now })
    assert.equal(response.status, 200)
    assert.equal(response.headers['cache-control'], 'public, max-age=300, must-revalidate')
  }
})

test('an empty repository returns 503 for the dpa and subprocessors aliases too', async () => {
  const repository = await LegalRepository.create({
    artifacts: [],
    evidence: [],
    releases: [],
    marketAllowlist: [],
  })
  for (const url of [
    '/api/v1/legal-documents/dpa_it/current?locale=it',
    '/api/v1/legal-documents/subprocessors/current?locale=en',
    '/api/v1/legal-documents/dpa/current?locale=en',
  ]) {
    const response = handleLegalApiRequest(repository, { method: 'GET', url, now })
    assert.equal(response.status, 503)
    assert.equal(response.headers['cache-control'], 'no-store')
  }
})

test('API rejects writes and unknown versions', async () => {
  const repository = await LegalRepository.create(await validReleaseInput())
  const write = handleLegalApiRequest(repository, {
    method: 'POST',
    url: '/api/v1/legal-documents/terms/current',
    now,
  })
  assert.equal(write.status, 405)
  assert.equal(write.headers.allow, 'GET')

  const unknown = handleLegalApiRequest(repository, {
    method: 'GET',
    url: '/api/v1/legal-documents/terms/versions/8.4',
    now,
  })
  assert.equal(unknown.status, 404)
})
