export const DOCUMENT_TYPES = ['terms', 'privacy', 'cookies'] as const
export type DocumentType = typeof DOCUMENT_TYPES[number]

export const LEGAL_LOCALES = ['en', 'it', 'es', 'fr', 'de'] as const
export type LegalLocale = typeof LEGAL_LOCALES[number]

export const DEFAULT_LEGAL_LOCALE: LegalLocale = 'en'

export const REQUIRED_EVIDENCE_KINDS = [
  'controller_identity',
  'cookie_inventory',
  'retention_schedule',
  'postqron_dpa',
  'mailronix_dpa',
  'mailronix_subprocessors',
  'mailronix_transfers',
  'paddle_terms',
] as const

export type EvidenceKind = typeof REQUIRED_EVIDENCE_KINDS[number]
export type ChangeType = 'material' | 'non_material'

export interface LegalArtifact {
  document: DocumentType
  locale: LegalLocale
  jurisdiction: 'IT'
  version: string
  title: string
  controllerName: string
  contactEmail: string
  content: string
  digestSha256: string
  approvalReference: string
  approvedAt: string
  publishedAt: string
  effectiveAt: string
  changeType: ChangeType
  revisionSummary: string
}

export interface LegalEvidence {
  id: string
  kind: EvidenceKind
  reference: string
  sourceUrl?: string
  verifiedAt: string
}

export interface LegalArtifactReference {
  document: DocumentType
  locale: LegalLocale
  version: string
  digestSha256: string
}

export interface LegalRelease {
  id: string
  market: 'IT'
  version: string
  fallbackLocale: 'en'
  approvalReference: string
  approvedAt: string
  effectiveAt: string
  artifacts: readonly LegalArtifactReference[]
  evidenceIds: readonly string[]
}

export interface LegalReleaseInput {
  artifacts: readonly LegalArtifact[]
  evidence: readonly LegalEvidence[]
  releases: readonly LegalRelease[]
}

export interface GateBlocker {
  code: string
  path: string
  message: string
}

export interface GateAudit {
  ready: boolean
  blockers: readonly GateBlocker[]
}

export interface PublishedLegalDocument extends Readonly<LegalArtifact> {
  requestedLocale: LegalLocale
  fallbackUsed: boolean
  permanentUrl: string
}

export function isDocumentType(value: unknown): value is DocumentType {
  return typeof value === 'string'
    && (DOCUMENT_TYPES as readonly string[]).includes(value)
}
