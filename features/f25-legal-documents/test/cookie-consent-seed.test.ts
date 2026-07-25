import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import test from 'node:test'
import { BUNDLED_LEGAL_RELEASE } from '../src/bundle.ts'
import {
  COOKIE_CONSENT_SEED_MIGRATION_PATH,
  renderCookieConsentSeedMigration,
} from '../scripts/build-cookie-consent-seed.ts'

test('the checked-in F13 cookies_it seed migration is mechanically derived from the canonical F25 bundle', async () => {
  const checkedIn = await readFile(COOKIE_CONSENT_SEED_MIGRATION_PATH, 'utf8')
  const regenerated = renderCookieConsentSeedMigration()
  assert.equal(
    checkedIn,
    regenerated,
    'the migration file has drifted from the canonical bundle — regenerate it with ' +
      'features/f25-legal-documents/scripts/build-cookie-consent-seed.ts instead of hand-editing it',
  )
})

test('the seed migration carries exactly the approved cookies_it artifact metadata', () => {
  const artifact = BUNDLED_LEGAL_RELEASE.artifacts.find(
    item => item.document === 'cookies' && item.locale === 'it',
  )
  assert.ok(artifact, 'canonical bundle must contain a cookies/it artifact')
  assert.equal(artifact?.status, 'approved')
  assert.equal(artifact?.jurisdiction, 'IT')
  assert.equal(artifact?.version, '0.1')
  assert.match(artifact?.digestSha256 ?? '', /^[a-f0-9]{64}$/u)
  assert.equal(artifact?.approvalReference, 'LEGAL-APPROVAL-2026-07-25-F25')

  const migration = renderCookieConsentSeedMigration()
  for (const expected of [
    "'cookies_it'",
    "'IT'",
    "'it-IT'",
    `'${artifact?.version}'`,
    `'${artifact?.digestSha256}'`,
    `'${artifact?.approvalReference}'`,
    "'approved'",
    'ON CONFLICT (document_key, jurisdiction, locale, version) DO NOTHING',
  ]) {
    assert.ok(migration.includes(expected), `migration is missing ${expected}`)
  }
  assert.ok(
    artifact && migration.includes(artifact.content),
    'migration must embed the exact approved content bytes, not a paraphrase or fixture',
  )
})

test('no second editorial source of cookies_it content exists outside the canonical F25 corpus', async () => {
  const artifact = BUNDLED_LEGAL_RELEASE.artifacts.find(
    item => item.document === 'cookies' && item.locale === 'it',
  )
  assert.ok(artifact)
  const migration = await readFile(COOKIE_CONSENT_SEED_MIGRATION_PATH, 'utf8')
  // The migration must be a mechanical wrapper around the bundle content: once
  // the generated SQL scaffolding is stripped away, only the bundle's own
  // content string should remain as the document body.
  const contentStart = migration.indexOf(artifact!.content)
  assert.ok(contentStart >= 0, 'migration does not embed the bundle content verbatim')
  const occurrences = migration.split(artifact!.content).length - 1
  assert.equal(occurrences, 1, 'the approved content must appear exactly once, not be duplicated or forked')
})
