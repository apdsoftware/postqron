import assert from 'node:assert/strict'
import test from 'node:test'
import {
  BUNDLED_LEGAL_RELEASE,
  LegalRepository,
  auditLegalRelease,
} from '../src/index.ts'
import { validReleaseInput } from './fixtures.ts'

test('an empty input is fail-closed', async () => {
  const emptyInput = {
    artifacts: [],
    evidence: [],
    releases: [],
    marketAllowlist: [],
  }
  const audit = await auditLegalRelease(emptyInput)
  assert.equal(audit.ready, false)
  assert.equal(
    audit.blockers.filter(item => item.code === 'missing_locale_document').length,
    25,
  )
  assert.equal(
    audit.blockers.filter(item => item.code === 'missing_evidence_kind').length,
    8,
  )
  assert.equal(audit.blockers[0]?.code, 'missing_release')

  const repository = await LegalRepository.create(emptyInput)
  assert.equal(repository.ready, false)
  assert.equal(
    repository.current('terms', 'it', '2026-07-25T12:30:00.000Z'),
    undefined,
  )
})

test('the bundled release is complete, approved, and publication succeeds', async () => {
  const audit = await auditLegalRelease(BUNDLED_LEGAL_RELEASE)
  assert.equal(audit.ready, true)
  assert.deepEqual(audit.blockers, [])

  const repository = await LegalRepository.create(BUNDLED_LEGAL_RELEASE)
  assert.equal(repository.ready, true)
  const current = repository.current('terms', 'it', '2026-07-30T00:00:00.000Z')
  assert.equal(current?.version, '0.1')
  assert.equal(current?.locale, 'it')
  assert.equal(current?.approvalReference, 'LEGAL-APPROVAL-2026-07-25-F25')
})

test('a complete synthetic bundle exposes current and immutable history', async () => {
  const input = await validReleaseInput()
  const repository = await LegalRepository.create(input)

  assert.equal(repository.ready, true)
  const current = repository.current(
    'privacy',
    'it-IT',
    '2026-07-25T12:30:00.000Z',
  )
  assert.equal(current?.version, '1.0')
  assert.equal(current?.locale, 'it')
  assert.equal(current?.fallbackUsed, false)
  assert.equal(Object.isFrozen(current), true)

  const historical = repository.version(
    'privacy',
    '0.9',
    'it',
    '2026-07-25T12:30:00.000Z',
  )
  assert.equal(historical?.version, '0.9')
  assert.deepEqual(
    repository
      .history('privacy', 'it', '2026-07-25T12:30:00.000Z')
      .map(item => item.version),
    ['0.9', '1.0'],
  )
})

test('unknown locale explicitly falls back to English', async () => {
  const repository = await LegalRepository.create(await validReleaseInput())
  const selected = repository.current(
    'terms',
    'pt-BR',
    '2026-07-25T12:30:00.000Z',
  )

  assert.equal(selected?.requestedLocale, 'en')
  assert.equal(selected?.locale, 'en')
  assert.equal(selected?.fallbackUsed, true)
})

test('a missing translation or changed byte blocks the complete release', async () => {
  const completeMissing = await validReleaseInput()
  const currentRelease = completeMissing.releases[1]
  assert.ok(currentRelease)
  const missing = {
    ...completeMissing,
    releases: [
      completeMissing.releases[0]!,
      { ...currentRelease, artifacts: currentRelease.artifacts.slice(1) },
    ],
  }
  const missingAudit = await auditLegalRelease(missing)
  assert.equal(missingAudit.ready, false)
  assert.ok(missingAudit.blockers.some(item =>
    item.code === 'missing_locale_document'
    && item.message === 'terms:en'))

  const completeTampered = await validReleaseInput()
  const firstArtifact = completeTampered.artifacts[0]
  assert.ok(firstArtifact)
  const tampered = {
    ...completeTampered,
    artifacts: [
      { ...firstArtifact, content: `${firstArtifact.content}changed` },
      ...completeTampered.artifacts.slice(1),
    ],
  }
  const tamperedAudit = await auditLegalRelease(tampered)
  assert.equal(tamperedAudit.ready, false)
  assert.ok(tamperedAudit.blockers.some(item => item.code === 'digest_mismatch'))
})

test('a draft artifact referenced by a release blocks that release', async () => {
  const input = await validReleaseInput()
  const currentRelease = input.releases[1]
  assert.ok(currentRelease)
  const draftKey = currentRelease.artifacts[0]
  assert.ok(draftKey)
  const draftedArtifacts = input.artifacts.map(item =>
    item.document === draftKey.document
      && item.locale === draftKey.locale
      && item.version === draftKey.version
      ? { ...item, status: 'draft_pending_legal_review' as const }
      : item)
  const audit = await auditLegalRelease({ ...input, artifacts: draftedArtifacts })
  assert.equal(audit.ready, false)
  assert.ok(audit.blockers.some(item => item.code === 'draft_status_blocks_release'))
})
