// Regenerates the F13 compliance-ledger bootstrap migration for the current
// Italian Terms and Privacy artifacts straight from the canonical F25 bundle
// (`src/bundle.ts`).
//
// This keeps content, digests, versions, dates, and approval references
// mechanically aligned with the approved corpus. Run from the repository root:
//   node --experimental-strip-types \
//     features/f25-legal-documents/scripts/build-legal-bootstrap-seed.ts
//
// Do not hand-edit the generated migration. Change the canonical bundle
// generator or this mechanical SQL renderer, then regenerate.

import { createHash } from 'node:crypto'
import { writeFile } from 'node:fs/promises'
import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'
import { BUNDLED_LEGAL_RELEASE } from '../src/bundle.ts'
import type {
  DocumentType,
  LegalArtifact,
  LegalRelease,
  LegalReleaseInput,
} from '../src/types.ts'

const featureRoot = resolve(dirname(fileURLToPath(import.meta.url)), '..')
export const LEGAL_BOOTSTRAP_SEED_MIGRATION_PATH = resolve(
  featureRoot,
  '../f13-compliance/migrations/000003_seed_current_terms_privacy_it.sql',
)

interface SeedDocument {
  document: Extract<DocumentType, 'terms' | 'privacy'>
  documentKey: 'terms_it' | 'privacy_it'
}

interface ResolvedSeedDocument extends SeedDocument {
  artifact: LegalArtifact
}

const SEED_DOCUMENTS: readonly SeedDocument[] = Object.freeze([
  { document: 'terms', documentKey: 'terms_it' },
  { document: 'privacy', documentKey: 'privacy_it' },
])

function parseVersion(version: string): readonly [number, number] {
  const match = /^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$/u.exec(version)
  if (!match) {
    throw new Error(`invalid legal release version: ${version}`)
  }
  return [Number(match[1]), Number(match[2])]
}

function compareVersions(left: string, right: string): number {
  const [leftMajor, leftMinor] = parseVersion(left)
  const [rightMajor, rightMinor] = parseVersion(right)
  return leftMajor - rightMajor || leftMinor - rightMinor
}

function currentItalianRelease(input: LegalReleaseInput): LegalRelease {
  const allowlistEntry = input.marketAllowlist.find(item => item.market === 'IT')
  if (allowlistEntry?.status !== 'active') {
    throw new Error('the canonical F25 bundle does not have an active IT market')
  }

  const releases = input.releases.filter(item => item.market === 'IT')
  if (releases.length === 0) {
    throw new Error('the canonical F25 bundle has no IT legal release')
  }

  const sorted = [...releases].sort((left, right) =>
    compareVersions(left.version, right.version),
  )
  const current = sorted.at(-1)!
  if (
    sorted.length > 1
    && compareVersions(sorted.at(-2)!.version, current.version) === 0
  ) {
    throw new Error(`the canonical F25 bundle has duplicate IT release ${current.version}`)
  }
  return current
}

function sha256(content: string): string {
  return createHash('sha256').update(content, 'utf8').digest('hex')
}

function resolveSeedDocuments(input: LegalReleaseInput): readonly ResolvedSeedDocument[] {
  const release = currentItalianRelease(input)

  return SEED_DOCUMENTS.map(seed => {
    const references = release.artifacts.filter(
      item => item.document === seed.document && item.locale === 'it',
    )
    if (references.length !== 1) {
      throw new Error(
        `current IT release ${release.version} must reference exactly one ${seed.document}/it artifact`,
      )
    }

    const reference = references[0]!
    const artifacts = input.artifacts.filter(item =>
      item.document === reference.document
      && item.locale === 'it'
      && item.version === reference.version,
    )
    if (artifacts.length !== 1) {
      throw new Error(
        `canonical F25 bundle must contain exactly one ${seed.document}/it/${reference.version} artifact`,
      )
    }

    const artifact = artifacts[0]!
    if (artifact.status !== 'approved') {
      throw new Error(`canonical ${seed.document}/it/${artifact.version} artifact is not approved`)
    }
    if (artifact.jurisdiction !== 'IT') {
      throw new Error(
        `canonical ${seed.document}/it/${artifact.version} artifact is not in jurisdiction IT`,
      )
    }
    if (artifact.digestSha256 !== reference.digestSha256) {
      throw new Error(
        `current IT release digest for ${seed.document}/it/${artifact.version} does not match its artifact`,
      )
    }
    if (sha256(artifact.content) !== artifact.digestSha256) {
      throw new Error(
        `canonical ${seed.document}/it/${artifact.version} content does not match its SHA-256 digest`,
      )
    }
    for (const [field, value] of [
      ['approvalReference', artifact.approvalReference],
      ['approvedAt', artifact.approvedAt],
      ['publishedAt', artifact.publishedAt],
      ['effectiveAt', artifact.effectiveAt],
    ] as const) {
      if (value.length === 0) {
        throw new Error(
          `canonical ${seed.document}/it/${artifact.version} artifact has no ${field}`,
        )
      }
    }

    return { ...seed, artifact }
  })
}

function sqlString(value: string): string {
  return `'${value.replaceAll("'", "''")}'`
}

function contentVariable(documentKey: string): string {
  return `expected_${documentKey}_content`
}

function contentDollarTag(item: ResolvedSeedDocument): string {
  return `$${item.documentKey}_v${item.artifact.version.replace('.', '_')}_content$`
}

function renderDeclaration(item: ResolvedSeedDocument): string {
  const dollarTag = contentDollarTag(item)
  if (item.artifact.content.includes(dollarTag)) {
    throw new Error(
      `dollar-quote tag collides with approved ${item.document}/it/${item.artifact.version} content`,
    )
  }
  return `  ${contentVariable(item.documentKey)} bytea := convert_to(${dollarTag}${item.artifact.content}${dollarTag}, 'UTF8');`
}

function renderInsertAndDriftCheck(item: ResolvedSeedDocument): string {
  const { artifact, documentKey } = item
  const permanentUrl = `/api/v1/legal-documents/${documentKey}/versions/${artifact.version}`
  const currentUrl = `/api/v1/legal-documents/${documentKey}/current`
  const expectedContent = contentVariable(documentKey)

  return `  INSERT INTO compliance_legal_documents (
      document_key,
      jurisdiction,
      locale,
      version,
      content_bytes,
      digest_sha256,
      content_status,
      legal_approval_id,
      approved_at,
      published_at,
      effective_at,
      permanent_url,
      current_url,
      change_type
  ) VALUES (
      ${sqlString(documentKey)},
      ${sqlString(artifact.jurisdiction)},
      'it-IT',
      ${sqlString(artifact.version)},
      ${expectedContent},
      ${sqlString(artifact.digestSha256)},
      'approved',
      ${sqlString(artifact.approvalReference)},
      ${sqlString(artifact.approvedAt)}::timestamptz,
      ${sqlString(artifact.publishedAt)}::timestamptz,
      ${sqlString(artifact.effectiveAt)}::timestamptz,
      ${sqlString(permanentUrl)},
      ${sqlString(currentUrl)},
      ${sqlString(artifact.changeType)}
  )
  ON CONFLICT (document_key, jurisdiction, locale, version) DO NOTHING;

  SELECT * INTO actual
  FROM compliance_legal_documents
  WHERE document_key = ${sqlString(documentKey)}
    AND jurisdiction = ${sqlString(artifact.jurisdiction)}
    AND locale = 'it-IT'
    AND version = ${sqlString(artifact.version)};

  IF NOT FOUND THEN
    RAISE EXCEPTION
      '${documentKey} % bootstrap migration: expected row is missing after insert',
      ${sqlString(artifact.version)};
  END IF;

  IF actual.content_bytes IS DISTINCT FROM ${expectedContent}
    OR actual.digest_sha256 IS DISTINCT FROM ${sqlString(artifact.digestSha256)}
    OR actual.content_status IS DISTINCT FROM 'approved'
    OR actual.legal_approval_id IS DISTINCT FROM ${sqlString(artifact.approvalReference)}
    OR actual.approved_at IS DISTINCT FROM ${sqlString(artifact.approvedAt)}::timestamptz
    OR actual.published_at IS DISTINCT FROM ${sqlString(artifact.publishedAt)}::timestamptz
    OR actual.effective_at IS DISTINCT FROM ${sqlString(artifact.effectiveAt)}::timestamptz
    OR actual.superseded_at IS NOT NULL
    OR actual.permanent_url IS DISTINCT FROM ${sqlString(permanentUrl)}
    OR actual.current_url IS DISTINCT FROM ${sqlString(currentUrl)}
    OR actual.change_type IS DISTINCT FROM ${sqlString(artifact.changeType)}
  THEN
    RAISE EXCEPTION
      '${documentKey} % bootstrap migration: an existing compliance_legal_documents row for ${documentKey}/${artifact.jurisdiction}/it-IT/% diverges from the F25-approved bundle (ref %); refusing to mask the conflict',
      ${sqlString(artifact.version)}, ${sqlString(artifact.version)}, ${sqlString(artifact.approvalReference)};
  END IF;`
}

export function renderLegalBootstrapSeedMigration(
  input: LegalReleaseInput = BUNDLED_LEGAL_RELEASE,
): string {
  const documents = resolveSeedDocuments(input)
  const references = documents
    .map(item => `${item.documentKey} ${item.artifact.version}: ${item.artifact.approvalReference}`)
    .join('; ')

  return `-- Materializes the current F25-approved Italian Terms and Privacy
-- artifacts into the F13 compliance ledger (\`compliance_legal_documents\`).
-- Generated by
-- \`node --experimental-strip-types features/f25-legal-documents/scripts/build-legal-bootstrap-seed.ts\`
-- from the canonical bundle in \`features/f25-legal-documents/src/bundle.ts\`
-- (refs. ${references}). Do not hand-edit this file.
--
-- \`ON CONFLICT DO NOTHING\` makes re-runs idempotent. The post-insert
-- comparisons remain fail-closed: every bundle-derived field is checked and
-- any pre-existing divergent row aborts this atomic block rather than masking
-- legal-ledger drift.
DO $$
DECLARE
${documents.map(renderDeclaration).join('\n')}
  actual compliance_legal_documents%ROWTYPE;
BEGIN
${documents.map(renderInsertAndDriftCheck).join('\n\n')}
END;
$$;
`
}

const isMainModule = process.argv[1] === fileURLToPath(import.meta.url)
if (isMainModule) {
  const output = renderLegalBootstrapSeedMigration()
  await writeFile(LEGAL_BOOTSTRAP_SEED_MIGRATION_PATH, output, 'utf8')
  console.warn(`wrote ${LEGAL_BOOTSTRAP_SEED_MIGRATION_PATH}`)
}
