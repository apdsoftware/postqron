export const DOCUMENT_TYPES = ['terms', 'privacy', 'cookies', 'dpa', 'subprocessors'] as const
export type DocumentType = typeof DOCUMENT_TYPES[number]

export const LEGAL_LOCALES = ['en', 'it', 'es', 'fr', 'de'] as const
export type LegalLocale = typeof LEGAL_LOCALES[number]

export const DEFAULT_LEGAL_LOCALE: LegalLocale = 'en'

// Market/jurisdiction blocks defined by D08 (docs/decisions/d08-global-legal-governance.md
// §1.3): the six blocks with a dedicated matrix row, plus a catch-all for every
// other Paddle-supported market pending local legal review.
export const MARKET_CODES = ['IT', 'SEE', 'GB', 'US', 'CA', 'AU', 'OTHER'] as const
export type MarketCode = typeof MARKET_CODES[number]

export const DEFAULT_MARKET: MarketCode = 'IT'

export function isMarketCode(value: unknown): value is MarketCode {
  return typeof value === 'string'
    && (MARKET_CODES as readonly string[]).includes(value)
}

// Per-market activation gate described by D08 §3/§8/§12: a market row can
// exist in the matrix without being activatable — only `active` unlocks it.
export const MARKET_ALLOWLIST_STATUSES = [
  'not_reviewed',
  'pending_legal_approval',
  'active',
] as const
export type MarketAllowlistStatus = typeof MARKET_ALLOWLIST_STATUSES[number]

export function isMarketAllowlistStatus(value: unknown): value is MarketAllowlistStatus {
  return typeof value === 'string'
    && (MARKET_ALLOWLIST_STATUSES as readonly string[]).includes(value)
}

export interface MarketAllowlistEntry {
  market: MarketCode
  status: MarketAllowlistStatus
  approvalReference?: string
}

export const ARTIFACT_STATUSES = ['draft_pending_legal_review', 'approved'] as const
export type ArtifactStatus = typeof ARTIFACT_STATUSES[number]

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
  jurisdiction: MarketCode
  version: string
  status: ArtifactStatus
  title: string
  controllerName: string
  contactEmail: string
  content: string
  digestSha256: string
  approvalReference: string
  approvedAt: string
  publishedAt: string
  // Target effective date proposed during drafting/legal review, expressed as
  // a plain YYYY-MM-DD date. Distinct from `effectiveAt`, the immutable UTC
  // instant assigned once a release is actually approved.
  proposedEffectiveDate: string
  effectiveAt: string
  changeType: ChangeType
  revisionSummary: string
}

export function isArtifactStatus(value: unknown): value is ArtifactStatus {
  return typeof value === 'string'
    && (ARTIFACT_STATUSES as readonly string[]).includes(value)
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
  market: MarketCode
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
  marketAllowlist: readonly MarketAllowlistEntry[]
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
