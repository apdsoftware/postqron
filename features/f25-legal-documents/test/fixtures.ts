import {
  DOCUMENT_TYPES,
  LEGAL_LOCALES,
  REQUIRED_EVIDENCE_KINDS,
  sha256,
  type LegalArtifact,
  type LegalEvidence,
  type LegalRelease,
  type LegalReleaseInput,
} from '../src/index.ts'

const approvedAt = '2026-07-25T10:00:00.000Z'

async function artifact(
  document: LegalArtifact['document'],
  locale: LegalArtifact['locale'],
  version: string,
  publishedAt: string,
  effectiveAt: string,
): Promise<LegalArtifact> {
  const content = [
    `Synthetic ${document} test fixture for ${locale}, version ${version}.`,
    'This material exists only to exercise parsing, digest verification, immutable history, locale selection, and the fail-closed release gate.',
    'It is not bundled by the application and does not represent user-facing legal copy or a real review decision.',
  ].join(' ').repeat(4)
  return {
    document,
    locale,
    jurisdiction: 'IT',
    version,
    status: 'approved',
    title: `Synthetic ${document} fixture`,
    controllerName: 'Fixture controller',
    contactEmail: 'legal@example.test',
    content,
    digestSha256: await sha256(content),
    approvalReference: `fixture-artifact-review:${document}:${locale}:${version}`,
    approvedAt,
    publishedAt,
    proposedEffectiveDate: '2026-07-01',
    effectiveAt,
    changeType: 'material',
    revisionSummary: `Synthetic revision ${version}`,
  }
}

function evidence(version: string): LegalEvidence[] {
  return REQUIRED_EVIDENCE_KINDS.map(kind => ({
    id: `fixture:${version}:${kind}`,
    kind,
    reference: `fixture-review:${version}:${kind}`,
    sourceUrl: `https://example.test/evidence/${version}/${kind}`,
    verifiedAt: approvedAt,
  }))
}

export async function validReleaseInput(): Promise<LegalReleaseInput> {
  const definitions = [
    {
      version: '0.9',
      publishedAt: '2026-07-25T10:05:00.000Z',
      effectiveAt: '2026-07-25T11:00:00.000Z',
    },
    {
      version: '1.0',
      publishedAt: '2026-07-25T11:05:00.000Z',
      effectiveAt: '2026-07-25T12:00:00.000Z',
    },
  ]
  const artifacts: LegalArtifact[] = []
  const releases: LegalRelease[] = []
  const allEvidence: LegalEvidence[] = []

  for (const definition of definitions) {
    const releaseArtifacts: LegalArtifact[] = []
    for (const document of DOCUMENT_TYPES) {
      for (const locale of LEGAL_LOCALES) {
        releaseArtifacts.push(await artifact(
          document,
          locale,
          definition.version,
          definition.publishedAt,
          definition.effectiveAt,
        ))
      }
    }
    const releaseEvidence = evidence(definition.version)
    artifacts.push(...releaseArtifacts)
    allEvidence.push(...releaseEvidence)
    releases.push({
      id: `fixture-release:${definition.version}`,
      market: 'IT',
      version: definition.version,
      fallbackLocale: 'en',
      approvalReference: `fixture-release-review:${definition.version}`,
      approvedAt,
      effectiveAt: definition.effectiveAt,
      artifacts: releaseArtifacts.map(item => ({
        document: item.document,
        locale: item.locale,
        version: item.version,
        digestSha256: item.digestSha256,
      })),
      evidenceIds: releaseEvidence.map(item => item.id),
    })
  }
  return {
    artifacts,
    evidence: allEvidence,
    releases,
    marketAllowlist: [
      { market: 'IT', status: 'active', approvalReference: 'fixture-market-review:IT' },
    ],
  }
}
