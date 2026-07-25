import assert from 'node:assert/strict'
import test from 'node:test'
import {
  BUNDLED_LEGAL_RELEASE,
  LegalRepository,
  handleLegalApiRequest,
} from '../src/index.ts'
import { validReleaseInput } from './fixtures.ts'

const now = '2026-07-25T12:30:00.000Z'

test('blocked API returns 503 without legal content and cannot be cached', async () => {
  const repository = await LegalRepository.create(BUNDLED_LEGAL_RELEASE)
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
