import assert from 'node:assert/strict'
import test from 'node:test'
import {
  BUNDLED_LEGAL_RELEASE,
  DOCUMENT_TYPES,
  LEGAL_LOCALES,
  LegalRepository,
  handleLegalApiRequest,
} from '../src/index.ts'
import { validReleaseInput } from './fixtures.ts'

const now = '2026-07-30T00:00:00.000Z'

test('an empty repository returns 503 for all 25 document/locale combinations', async () => {
  const repository = await LegalRepository.create({
    artifacts: [],
    evidence: [],
    releases: [],
    marketAllowlist: [],
  })
  for (const document of DOCUMENT_TYPES) {
    for (const locale of LEGAL_LOCALES) {
      const response = handleLegalApiRequest(repository, {
        method: 'GET',
        url: `/api/v1/legal-documents/${document}/current?locale=${locale}`,
        now,
      })
      assert.equal(
        response.status,
        503,
        `${document}:${locale} current should be blocked`,
      )
      assert.equal(response.headers['cache-control'], 'no-store')
      assert.doesNotMatch(
        JSON.stringify(response.body),
        /content/iu,
        `${document}:${locale} response leaked content`,
      )
    }
  }
})

test('the bundled release resolves all 25 document/locale combinations', async () => {
  const repository = await LegalRepository.create(BUNDLED_LEGAL_RELEASE)
  for (const document of DOCUMENT_TYPES) {
    for (const locale of LEGAL_LOCALES) {
      const response = handleLegalApiRequest(repository, {
        method: 'GET',
        url: `/api/v1/legal-documents/${document}/current?locale=${locale}&market=IT`,
        now,
      })
      assert.equal(response.status, 200, `${document}:${locale} should resolve`)
      const body = response.body as { document: string, locale: string }
      assert.equal(body.document, document)
      assert.equal(body.locale, locale)
    }
  }
})

test('the bundled release also resolves the legacy _it aliases', async () => {
  const repository = await LegalRepository.create(BUNDLED_LEGAL_RELEASE)
  for (const alias of ['terms_it', 'privacy_it', 'cookies_it', 'dpa_it']) {
    const response = handleLegalApiRequest(repository, {
      method: 'GET',
      url: `/api/v1/legal-documents/${alias}/current?locale=en&market=IT`,
      now,
    })
    assert.equal(response.status, 200, `${alias} should resolve`)
  }
})

test('an active-market synthetic release exposes all 25 document/locale combinations', async () => {
  const repository = await LegalRepository.create(await validReleaseInput())
  for (const document of DOCUMENT_TYPES) {
    for (const locale of LEGAL_LOCALES) {
      const response = handleLegalApiRequest(repository, {
        method: 'GET',
        url: `/api/v1/legal-documents/${document}/current?locale=${locale}&market=IT`,
        now,
      })
      assert.equal(response.status, 200, `${document}:${locale} should resolve`)
      const body = response.body as { document: string, locale: string }
      assert.equal(body.document, document)
      assert.equal(body.locale, locale)
    }
  }
})

test('a market with no active release never resolves, even with a complete fixture bundle', async () => {
  const repository = await LegalRepository.create(await validReleaseInput())
  const response = handleLegalApiRequest(repository, {
    method: 'GET',
    url: '/api/v1/legal-documents/terms/current?locale=en&market=SEE',
    now,
  })
  assert.equal(response.status, 404)
})
