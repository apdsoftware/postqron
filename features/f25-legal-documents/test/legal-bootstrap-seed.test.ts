import assert from 'node:assert/strict'
import { createHash } from 'node:crypto'
import { readFile } from 'node:fs/promises'
import test from 'node:test'
import {
  LEGAL_BOOTSTRAP_SEED_MIGRATION_PATH,
  renderLegalBootstrapSeedMigration,
} from '../scripts/build-legal-bootstrap-seed.ts'
import { BUNDLED_LEGAL_RELEASE } from '../src/bundle.ts'
import type { LegalReleaseInput } from '../src/types.ts'

const EXPECTED_CURRENT_ARTIFACTS = Object.freeze([
  {
    document: 'terms',
    documentKey: 'terms_it',
    version: '0.2',
    digest: '2630d35d50853a781453dcad5b067725df2bcd0469e8bf37e9e109c660533f9b',
    approvalReference:
      'https://github.com/apdsoftware/postqron/issues/179#issuecomment-5090088911',
  },
  {
    document: 'privacy',
    documentKey: 'privacy_it',
    version: '0.1',
    digest: 'e9bd3260ec45259f92e84592f988be9886423d69f3c8ddb84ab8f03a39b1a660',
    approvalReference: 'LEGAL-APPROVAL-2026-07-25-F25',
  },
] as const)

test('the checked-in F13 legal bootstrap migration is deterministic and has no bundle drift', async () => {
  const checkedIn = await readFile(LEGAL_BOOTSTRAP_SEED_MIGRATION_PATH, 'utf8')
  const firstRender = renderLegalBootstrapSeedMigration()
  const secondRender = renderLegalBootstrapSeedMigration()

  assert.equal(firstRender, secondRender, 'rendering the same canonical bundle must be deterministic')
  assert.equal(
    checkedIn,
    firstRender,
    'the migration has drifted from the canonical F25 bundle — regenerate it with ' +
      'features/f25-legal-documents/scripts/build-legal-bootstrap-seed.ts',
  )
})

test('the migration carries only the approved current Italian Terms and Privacy artifacts', () => {
  const migration = renderLegalBootstrapSeedMigration()

  for (const expected of EXPECTED_CURRENT_ARTIFACTS) {
    const artifacts = BUNDLED_LEGAL_RELEASE.artifacts.filter(
      item => item.document === expected.document
        && item.locale === 'it'
        && item.version === expected.version,
    )
    assert.equal(artifacts.length, 1)
    const artifact = artifacts[0]!

    assert.equal(artifact.status, 'approved')
    assert.equal(artifact.jurisdiction, 'IT')
    assert.equal(artifact.digestSha256, expected.digest)
    assert.equal(artifact.approvalReference, expected.approvalReference)
    assert.equal(
      createHash('sha256').update(artifact.content, 'utf8').digest('hex'),
      expected.digest,
    )
    assert.equal(
      migration.split(artifact.content).length - 1,
      1,
      `${expected.documentKey} content must be embedded exactly once and verbatim`,
    )

    for (const value of [
      expected.documentKey,
      expected.version,
      expected.digest,
      expected.approvalReference,
      artifact.approvedAt,
      artifact.publishedAt,
      artifact.effectiveAt,
      `/api/v1/legal-documents/${expected.documentKey}/versions/${expected.version}`,
      `/api/v1/legal-documents/${expected.documentKey}/current`,
    ]) {
      assert.ok(migration.includes(value), `migration is missing bundle-derived value ${value}`)
    }
  }

  assert.doesNotMatch(migration, /'terms_it'[\s\S]*?'0\.1'[\s\S]*?expected_terms_it_content/u)
  assert.equal(
    (migration.match(/ON CONFLICT \(document_key, jurisdiction, locale, version\) DO NOTHING/gu)
      ?? []).length,
    EXPECTED_CURRENT_ARTIFACTS.length,
  )
})

test('every inserted artifact is guarded by a complete fail-closed drift comparison', () => {
  const migration = renderLegalBootstrapSeedMigration()
  const requiredComparisons = [
    'content_bytes',
    'digest_sha256',
    'content_status',
    'legal_approval_id',
    'approved_at',
    'published_at',
    'effective_at',
    'superseded_at',
    'permanent_url',
    'current_url',
    'change_type',
  ]

  for (const expected of EXPECTED_CURRENT_ARTIFACTS) {
    const rowLookup = migration.indexOf(`WHERE document_key = '${expected.documentKey}'`)
    assert.ok(rowLookup >= 0)
    const start = migration.indexOf('  IF actual.content_bytes', rowLookup)
    assert.ok(start >= 0)
    const end = migration.indexOf('  END IF;', start)
    assert.ok(end > start)
    const guard = migration.slice(start, end)

    for (const field of requiredComparisons) {
      assert.match(guard, new RegExp(`actual\\.${field}`, 'u'))
    }
    assert.match(guard, /RAISE EXCEPTION/u)
    assert.match(guard, /refusing to mask the conflict/u)
  }
})

test('the generator rejects content and release-reference drift before emitting SQL', () => {
  const contentDrift = structuredClone(BUNDLED_LEGAL_RELEASE) as LegalReleaseInput
  const currentTerms = contentDrift.artifacts.find(
    item => item.document === 'terms' && item.locale === 'it' && item.version === '0.2',
  )
  assert.ok(currentTerms)
  currentTerms.content = `${currentTerms.content}\nunauthorized drift`
  assert.throws(
    () => renderLegalBootstrapSeedMigration(contentDrift),
    /content does not match its SHA-256 digest/u,
  )

  const referenceDrift = structuredClone(BUNDLED_LEGAL_RELEASE) as LegalReleaseInput
  const currentRelease = referenceDrift.releases.find(item => item.version === '0.2')
  const privacyReference = currentRelease?.artifacts.find(
    item => item.document === 'privacy' && item.locale === 'it',
  )
  assert.ok(privacyReference)
  privacyReference.digestSha256 = '0'.repeat(64)
  assert.throws(
    () => renderLegalBootstrapSeedMigration(referenceDrift),
    /current IT release digest .* does not match its artifact/u,
  )
})
