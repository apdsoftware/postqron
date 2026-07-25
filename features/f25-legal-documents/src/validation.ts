import {
  DEFAULT_LEGAL_LOCALE,
  DOCUMENT_TYPES,
  LEGAL_LOCALES,
  REQUIRED_EVIDENCE_KINDS,
  isArtifactStatus,
  type GateAudit,
  type GateBlocker,
  type LegalArtifact,
  type LegalArtifactReference,
  type LegalLocale,
  type LegalRelease,
  type LegalReleaseInput,
} from './types.ts'

const VERSION_PATTERN = /^(0|[1-9]\d*)\.(0|[1-9]\d*)$/u
const DIGEST_PATTERN = /^[a-f0-9]{64}$/u
const EMAIL_PATTERN = /^[^\s@]+@[^\s@]+\.[^\s@]+$/u
// "TODO" is checked case-sensitively and separately from the rest of this
// pattern because its lowercase form ("todo") is an ordinary Spanish and
// Italian word ("all"/"every") that appears constantly in real legal prose;
// a case-insensitive match would flag legitimate multilingual content.
const DRAFTING_MARKER_PATTERN =
  /\b(?:tbd|lorem ipsum|placeholder|insert here|da definire|à définir|por definir|zu definieren)\b/iu
const UPPERCASE_TODO_PATTERN = /\bTODO\b/u

export function hasDraftingMarker(value: string): boolean {
  return DRAFTING_MARKER_PATTERN.test(value) || UPPERCASE_TODO_PATTERN.test(value)
}

export function artifactKey(
  reference: Pick<LegalArtifactReference, 'document' | 'locale' | 'version'>,
): string {
  return `${reference.document}:${reference.locale}:${reference.version}`
}

export function releaseTupleKey(
  reference: Pick<LegalArtifactReference, 'document' | 'locale'>,
): string {
  return `${reference.document}:${reference.locale}`
}

export function normalizeLegalLocale(value: unknown): LegalLocale {
  if (typeof value !== 'string') {
    return DEFAULT_LEGAL_LOCALE
  }
  const base = value.trim().toLowerCase().split(/[-_]/u, 1)[0] ?? ''
  return (LEGAL_LOCALES as readonly string[]).includes(base)
    ? base as LegalLocale
    : DEFAULT_LEGAL_LOCALE
}

export async function sha256(content: string): Promise<string> {
  const digest = await globalThis.crypto.subtle.digest(
    'SHA-256',
    new TextEncoder().encode(content),
  )
  return Array.from(new Uint8Array(digest))
    .map(byte => byte.toString(16).padStart(2, '0'))
    .join('')
}

export function compareVersions(left: string, right: string): number {
  const leftMatch = VERSION_PATTERN.exec(left)
  const rightMatch = VERSION_PATTERN.exec(right)
  if (!leftMatch || !rightMatch) {
    throw new Error('legal versions must use major.minor')
  }
  return Number(leftMatch[1]) - Number(rightMatch[1])
    || Number(leftMatch[2]) - Number(rightMatch[2])
}

function isUtcInstant(value: string): boolean {
  const instant = new Date(value)
  return value.endsWith('Z')
    && !Number.isNaN(instant.valueOf())
    && instant.toISOString() === value
}

function blocker(
  blockers: GateBlocker[],
  code: string,
  path: string,
  message: string,
): void {
  blockers.push({ code, path, message })
}

async function auditArtifact(
  artifact: LegalArtifact,
  path: string,
  blockers: GateBlocker[],
): Promise<void> {
  if (!(DOCUMENT_TYPES as readonly string[]).includes(artifact.document)) {
    blocker(blockers, 'invalid_document', `${path}.document`, 'unsupported document')
  }
  if (!(LEGAL_LOCALES as readonly string[]).includes(artifact.locale)) {
    blocker(blockers, 'invalid_locale', `${path}.locale`, 'unsupported locale')
  }
  if (!VERSION_PATTERN.test(artifact.version)) {
    blocker(blockers, 'invalid_version', `${path}.version`, 'major.minor is required')
  }
  if (artifact.jurisdiction !== 'IT') {
    blocker(blockers, 'invalid_jurisdiction', `${path}.jurisdiction`, 'IT is required')
  }
  if (!isArtifactStatus(artifact.status)) {
    blocker(blockers, 'invalid_status', `${path}.status`, 'a recognized artifact status is required')
  }
  if (!EMAIL_PATTERN.test(artifact.contactEmail)) {
    blocker(blockers, 'invalid_contact', `${path}.contactEmail`, 'a public email is required')
  }
  for (const [field, value] of [
    ['title', artifact.title],
    ['controllerName', artifact.controllerName],
    ['approvalReference', artifact.approvalReference],
    ['revisionSummary', artifact.revisionSummary],
  ] as const) {
    if (!value.trim()) {
      blocker(blockers, 'missing_metadata', `${path}.${field}`, `${field} is required`)
    } else if (hasDraftingMarker(value)) {
      blocker(blockers, 'drafting_marker', `${path}.${field}`, `${field} is not final`)
    }
  }
  if (artifact.content.trim().length < 500) {
    blocker(
      blockers,
      'incomplete_content',
      `${path}.content`,
      'approved content must contain at least 500 characters',
    )
  } else if (hasDraftingMarker(artifact.content)) {
    blocker(blockers, 'drafting_marker', `${path}.content`, 'content is not final')
  }
  if (!DIGEST_PATTERN.test(artifact.digestSha256)) {
    blocker(blockers, 'invalid_digest', `${path}.digestSha256`, 'lowercase SHA-256 is required')
  } else if (await sha256(artifact.content) !== artifact.digestSha256) {
    blocker(blockers, 'digest_mismatch', `${path}.digestSha256`, 'digest does not match content')
  }
  for (const field of ['approvedAt', 'publishedAt', 'effectiveAt'] as const) {
    if (!isUtcInstant(artifact[field])) {
      blocker(blockers, 'invalid_timestamp', `${path}.${field}`, 'canonical UTC is required')
    }
  }
  if (
    isUtcInstant(artifact.approvedAt)
    && isUtcInstant(artifact.publishedAt)
    && new Date(artifact.publishedAt) < new Date(artifact.approvedAt)
  ) {
    blocker(
      blockers,
      'invalid_publication_order',
      path,
      'publication cannot precede approval',
    )
  }
}

function requiredTuples(): Set<string> {
  return new Set(
    DOCUMENT_TYPES.flatMap(document =>
      LEGAL_LOCALES.map(locale => `${document}:${locale}`)),
  )
}

function auditRelease(
  release: LegalRelease,
  index: number,
  artifacts: Map<string, LegalArtifact>,
  evidence: Map<string, LegalReleaseInput['evidence'][number]>,
  blockers: GateBlocker[],
): void {
  const path = `releases[${index}]`
  if (release.market !== 'IT') {
    blocker(blockers, 'invalid_market', `${path}.market`, 'IT is required')
  }
  if (release.fallbackLocale !== DEFAULT_LEGAL_LOCALE) {
    blocker(blockers, 'invalid_fallback', `${path}.fallbackLocale`, 'en is required')
  }
  if (!VERSION_PATTERN.test(release.version)) {
    blocker(blockers, 'invalid_version', `${path}.version`, 'major.minor is required')
  }
  if (!release.id.trim() || !release.approvalReference.trim()) {
    blocker(
      blockers,
      'missing_release_approval',
      path,
      'release id and legal approval reference are required',
    )
  }
  for (const field of ['approvedAt', 'effectiveAt'] as const) {
    if (!isUtcInstant(release[field])) {
      blocker(blockers, 'invalid_timestamp', `${path}.${field}`, 'canonical UTC is required')
    }
  }
  if (
    isUtcInstant(release.approvedAt)
    && isUtcInstant(release.effectiveAt)
    && new Date(release.effectiveAt) < new Date(release.approvedAt)
  ) {
    blocker(blockers, 'invalid_release_order', path, 'effective date cannot precede approval')
  }

  const expected = requiredTuples()
  const seen = new Set<string>()
  for (const [referenceIndex, reference] of release.artifacts.entries()) {
    const referencePath = `${path}.artifacts[${referenceIndex}]`
    const tuple = releaseTupleKey(reference)
    if (seen.has(tuple)) {
      blocker(blockers, 'duplicate_release_artifact', referencePath, `${tuple} is duplicated`)
    }
    seen.add(tuple)
    expected.delete(tuple)
    const artifact = artifacts.get(artifactKey(reference))
    if (!artifact) {
      blocker(blockers, 'missing_artifact', referencePath, 'referenced artifact is absent')
    } else if (artifact.digestSha256 !== reference.digestSha256) {
      blocker(blockers, 'release_digest_mismatch', referencePath, 'release digest is not exact')
    } else {
      if (artifact.status !== 'approved') {
        blocker(
          blockers,
          'draft_status_blocks_release',
          referencePath,
          'a draft artifact cannot be served as approved or current',
        )
      }
      if (new Date(artifact.approvedAt) > new Date(release.approvedAt)) {
        blocker(
          blockers,
          'artifact_approved_after_release',
          referencePath,
          'artifact approval must precede release approval',
        )
      }
      if (new Date(artifact.publishedAt) > new Date(release.effectiveAt)) {
        blocker(
          blockers,
          'artifact_published_after_release',
          referencePath,
          'artifact publication must not follow release effectiveness',
        )
      }
      if (new Date(artifact.effectiveAt) > new Date(release.effectiveAt)) {
        blocker(
          blockers,
          'artifact_not_effective',
          referencePath,
          'artifact must be effective with its release',
        )
      }
    }
  }
  for (const tuple of [...expected].sort()) {
    blocker(blockers, 'missing_locale_document', `${path}.artifacts`, tuple)
  }

  const evidenceKinds = new Set<string>()
  for (const id of release.evidenceIds) {
    const item = evidence.get(id)
    if (!item) {
      blocker(blockers, 'missing_evidence', `${path}.evidenceIds`, id)
      continue
    }
    evidenceKinds.add(item.kind)
    if (new Date(item.verifiedAt) > new Date(release.approvedAt)) {
      blocker(
        blockers,
        'evidence_verified_after_release',
        `${path}.evidenceIds`,
        `${id} was verified after release approval`,
      )
    }
  }
  for (const kind of REQUIRED_EVIDENCE_KINDS) {
    if (!evidenceKinds.has(kind)) {
      blocker(blockers, 'missing_evidence_kind', `${path}.evidenceIds`, kind)
    }
  }
}

export async function auditLegalRelease(
  input: LegalReleaseInput,
): Promise<GateAudit> {
  const blockers: GateBlocker[] = []
  if (input.releases.length === 0) {
    blocker(
      blockers,
      'missing_release',
      'releases',
      'a complete legally approved release is required',
    )
    for (const tuple of [...requiredTuples()].sort()) {
      blocker(blockers, 'missing_locale_document', 'artifacts', tuple)
    }
    for (const kind of REQUIRED_EVIDENCE_KINDS) {
      blocker(blockers, 'missing_evidence_kind', 'evidence', kind)
    }
  }

  const artifacts = new Map<string, LegalArtifact>()
  for (const [index, artifact] of input.artifacts.entries()) {
    const path = `artifacts[${index}]`
    const key = artifactKey(artifact)
    if (artifacts.has(key)) {
      blocker(blockers, 'duplicate_artifact', path, key)
    } else {
      artifacts.set(key, artifact)
    }
    await auditArtifact(artifact, path, blockers)
  }

  const evidence = new Map<string, LegalReleaseInput['evidence'][number]>()
  for (const [index, item] of input.evidence.entries()) {
    const path = `evidence[${index}]`
    if (!item.id.trim() || !item.reference.trim()) {
      blocker(blockers, 'missing_evidence_metadata', path, 'id and reference are required')
    }
    if (!(REQUIRED_EVIDENCE_KINDS as readonly string[]).includes(item.kind)) {
      blocker(blockers, 'invalid_evidence_kind', `${path}.kind`, 'unsupported evidence kind')
    }
    if (!isUtcInstant(item.verifiedAt)) {
      blocker(blockers, 'invalid_timestamp', `${path}.verifiedAt`, 'canonical UTC is required')
    }
    if (item.sourceUrl) {
      try {
        const url = new URL(item.sourceUrl)
        if (url.protocol !== 'https:') {
          throw new Error('HTTPS is required')
        }
      } catch {
        blocker(blockers, 'invalid_evidence_url', `${path}.sourceUrl`, 'HTTPS URL is required')
      }
    }
    if (evidence.has(item.id)) {
      blocker(blockers, 'duplicate_evidence', path, item.id)
    } else {
      evidence.set(item.id, item)
    }
  }

  for (const [index, release] of input.releases.entries()) {
    auditRelease(release, index, artifacts, evidence, blockers)
    if (index > 0) {
      const previous = input.releases[index - 1]
      if (!previous) {
        continue
      }
      try {
        if (compareVersions(release.version, previous.version) <= 0) {
          blocker(
            blockers,
            'non_monotonic_release',
            `releases[${index}].version`,
            'release versions must increase',
          )
        }
      } catch {
        // Individual invalid versions already have a precise blocker.
      }
      if (new Date(release.effectiveAt) <= new Date(previous.effectiveAt)) {
        blocker(
          blockers,
          'non_monotonic_effective_date',
          `releases[${index}].effectiveAt`,
          'release effective dates must increase',
        )
      }
    }
  }

  return Object.freeze({
    ready: blockers.length === 0,
    blockers: Object.freeze(blockers.map(item => Object.freeze(item))),
  })
}
