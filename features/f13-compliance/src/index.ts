export const LEGAL_DOCUMENT_KEYS = [
  'terms_it',
  'privacy_it',
  'cookies_it',
  'dpa_it',
  'subprocessors',
] as const

export type LegalDocumentKey = typeof LEGAL_DOCUMENT_KEYS[number]
export type ContentStatus = 'placeholder' | 'approved'
export type ChangeType = 'material' | 'non_material'

export interface LegalDocumentArtifact {
  documentKey: LegalDocumentKey
  jurisdiction: 'IT'
  locale: 'it-IT'
  version: string
  content: string
  digestSha256: string
  contentStatus: ContentStatus
  legalApprovalId?: string
  approvedAt?: string
  publishedAt?: string
  effectiveAt?: string
  supersededAt?: string
  permanentUrl?: string
  currentUrl?: string
  changeType: ChangeType
}

export interface PublishedLegalDocument extends LegalDocumentArtifact {
  contentStatus: 'approved'
  legalApprovalId: string
  approvedAt: string
  publishedAt: string
  effectiveAt: string
  permanentUrl: string
  currentUrl: string
}

const VERSION_PATTERN = /^(0|[1-9]\d*)\.(0|[1-9]\d*)$/
const SHA256_PATTERN = /^[a-f0-9]{64}$/

function parseVersion(version: string): [number, number] {
  const match = VERSION_PATTERN.exec(version)
  if (!match) {
    throw new Error('legal document version must use monotonic major.minor format')
  }
  return [Number(match[1]), Number(match[2])]
}

function compareVersions(left: string, right: string): number {
  const [leftMajor, leftMinor] = parseVersion(left)
  const [rightMajor, rightMinor] = parseVersion(right)
  return leftMajor - rightMajor || leftMinor - rightMinor
}

function assertUtcInstant(value: string, field: string): void {
  const parsed = new Date(value)
  if (
    Number.isNaN(parsed.valueOf())
    || !value.endsWith('Z')
    || parsed.toISOString() !== value
  ) {
    throw new Error(`${field} must be a canonical UTC instant`)
  }
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

async function validateArtifact(artifact: LegalDocumentArtifact): Promise<void> {
  parseVersion(artifact.version)
  if (!SHA256_PATTERN.test(artifact.digestSha256)) {
    throw new Error('digestSha256 must be a lowercase SHA-256 digest')
  }
  if (await sha256(artifact.content) !== artifact.digestSha256) {
    throw new Error('digestSha256 does not match the exact document content')
  }

  if (artifact.contentStatus === 'placeholder') {
    if (
      artifact.legalApprovalId
      || artifact.approvedAt
      || artifact.publishedAt
      || artifact.effectiveAt
      || artifact.supersededAt
      || artifact.permanentUrl
      || artifact.currentUrl
    ) {
      throw new Error('placeholder content cannot carry approval or publication data')
    }
    return
  }

  if (!artifact.legalApprovalId || !artifact.approvedAt) {
    throw new Error('approved content requires a legal approval reference and timestamp')
  }
  assertUtcInstant(artifact.approvedAt, 'approvedAt')

  const publicationFields = [
    artifact.publishedAt,
    artifact.effectiveAt,
    artifact.permanentUrl,
    artifact.currentUrl,
  ]
  const populatedPublicationFields = publicationFields.filter(Boolean).length
  if (populatedPublicationFields !== 0 && populatedPublicationFields !== 4) {
    throw new Error('publication metadata must be supplied as a complete set')
  }
  if (artifact.publishedAt) {
    assertUtcInstant(artifact.publishedAt, 'publishedAt')
    assertUtcInstant(artifact.effectiveAt!, 'effectiveAt')
    if (artifact.supersededAt) {
      assertUtcInstant(artifact.supersededAt, 'supersededAt')
      if (new Date(artifact.supersededAt) <= new Date(artifact.publishedAt)) {
        throw new Error('supersededAt must be later than publishedAt')
      }
    }
    if (!artifact.permanentUrl!.includes(`/${artifact.version}`)) {
      throw new Error('permanentUrl must identify the exact document version')
    }
  }
}

function immutableArtifact(
  artifact: LegalDocumentArtifact,
): Readonly<LegalDocumentArtifact> {
  return Object.freeze({ ...artifact })
}

export class LegalDocumentRegistry {
  readonly #artifacts = new Map<string, Readonly<LegalDocumentArtifact>>()

  async add(
    artifact: LegalDocumentArtifact,
  ): Promise<Readonly<LegalDocumentArtifact>> {
    await validateArtifact(artifact)
    const key = this.#key(artifact.documentKey, artifact.version)
    if (this.#artifacts.has(key)) {
      throw new Error('published legal artifacts are immutable and cannot be replaced')
    }

    const current = this.list(artifact.documentKey).at(-1)
    if (current && compareVersions(artifact.version, current.version) <= 0) {
      throw new Error('legal document versions must increase monotonically')
    }

    const stored = immutableArtifact(artifact)
    this.#artifacts.set(key, stored)
    return stored
  }

  get(
    documentKey: LegalDocumentKey,
    version: string,
  ): Readonly<LegalDocumentArtifact> | undefined {
    return this.#artifacts.get(this.#key(documentKey, version))
  }

  list(documentKey: LegalDocumentKey): ReadonlyArray<Readonly<LegalDocumentArtifact>> {
    return [...this.#artifacts.values()]
      .filter(artifact => artifact.documentKey === documentKey)
      .sort((left, right) => compareVersions(left.version, right.version))
  }

  current(
    documentKey: LegalDocumentKey,
    at = new Date().toISOString(),
  ): Readonly<PublishedLegalDocument> | undefined {
    assertUtcInstant(at, 'at')
    return this.list(documentKey)
      .filter((artifact): artifact is Readonly<PublishedLegalDocument> =>
        artifact.contentStatus === 'approved'
        && Boolean(artifact.publishedAt)
        && new Date(artifact.effectiveAt!) <= new Date(at)
        && (!artifact.supersededAt || new Date(artifact.supersededAt) > new Date(at)))
      .at(-1)
  }

  #key(documentKey: LegalDocumentKey, version: string): string {
    return `${documentKey}:${version}`
  }
}

export type EvidenceAction =
  | 'accepted'
  | 'acknowledged'
  | 'granted'
  | 'rejected'
  | 'withdrawn'

export interface ConsentSubject {
  kind: 'authenticated_user' | 'pseudonymous_browser'
  id: string
}

export interface ConsentEvidence {
  eventId: string
  subject: ConsentSubject
  workspaceId?: string
  documentKey: LegalDocumentKey
  documentVersion: string
  documentDigestSha256: string
  uiDigestSha256?: string
  purpose: string
  action: EvidenceAction
  occurredAt: string
  locale: 'it-IT'
  contractualCountry: 'IT'
  surface: string
  correlationId: string
  idempotencyKey: string
  controlTextVersion: string
}

function canonicalEvidence(evidence: ConsentEvidence): string {
  return JSON.stringify([
    evidence.eventId,
    evidence.subject.kind,
    evidence.subject.id,
    evidence.workspaceId,
    evidence.documentKey,
    evidence.documentVersion,
    evidence.documentDigestSha256,
    evidence.uiDigestSha256,
    evidence.purpose,
    evidence.action,
    evidence.occurredAt,
    evidence.locale,
    evidence.contractualCountry,
    evidence.surface,
    evidence.correlationId,
    evidence.idempotencyKey,
    evidence.controlTextVersion,
  ])
}

function validateEvidence(evidence: ConsentEvidence): void {
  if (
    !evidence.eventId
    || !evidence.subject.id
    || !evidence.purpose
    || !evidence.surface
    || !evidence.correlationId
    || !evidence.idempotencyKey
    || !evidence.controlTextVersion
  ) {
    throw new Error('consent evidence requires stable proof identifiers')
  }
  parseVersion(evidence.documentVersion)
  if (!SHA256_PATTERN.test(evidence.documentDigestSha256)) {
    throw new Error('consent evidence requires the exact document or UI digest')
  }
  if (evidence.uiDigestSha256 && !SHA256_PATTERN.test(evidence.uiDigestSha256)) {
    throw new Error('uiDigestSha256 must be a lowercase SHA-256 digest')
  }
  assertUtcInstant(evidence.occurredAt, 'occurredAt')
}

export interface ConsentEvidenceWriter {
  append(evidence: ConsentEvidence): Readonly<ConsentEvidence>
}

export class InMemoryConsentLedger implements ConsentEvidenceWriter {
  readonly #events: Readonly<ConsentEvidence>[] = []
  readonly #idempotency = new Map<string, Readonly<ConsentEvidence>>()

  append(evidence: ConsentEvidence): Readonly<ConsentEvidence> {
    validateEvidence(evidence)
    const previous = this.#idempotency.get(evidence.idempotencyKey)
    if (previous) {
      if (canonicalEvidence(previous) !== canonicalEvidence(evidence)) {
        throw new Error('idempotency key was already used for different evidence')
      }
      return previous
    }

    const stored = Object.freeze({
      ...evidence,
      subject: Object.freeze({ ...evidence.subject }),
    })
    this.#events.push(stored)
    this.#idempotency.set(stored.idempotencyKey, stored)
    return stored
  }

  events(): ReadonlyArray<Readonly<ConsentEvidence>> {
    return [...this.#events]
  }
}

export const COOKIE_CATEGORIES = [
  'necessary',
  'preferences',
  'analytics',
  'marketing',
] as const

export type CookieCategory = typeof COOKIE_CATEGORIES[number]
export type OptionalCookieCategory = Exclude<CookieCategory, 'necessary'>

export const COOKIE_BANNER_FIRST_LEVEL_ACTIONS = Object.freeze([
  Object.freeze({ id: 'accept_all', label: 'Accetta tutte', prominence: 'equal' }),
  Object.freeze({ id: 'reject_all', label: 'Rifiuta tutte', prominence: 'equal' }),
  Object.freeze({ id: 'customize', label: 'Personalizza', prominence: 'equal' }),
] as const)

export interface CookiePreferences {
  necessary: true
  preferences: boolean
  analytics: boolean
  marketing: boolean
  selectedAt: string
  expiresAt: string
}

export interface CookiePreferenceStore {
  read(): CookiePreferences | undefined
  write(preferences: CookiePreferences): void
}

export interface OptionalTracker {
  id: string
  category: OptionalCookieCategory
  activate(): void | Promise<void>
  revoke(): void | Promise<void>
}

export interface CookieEvidenceContext {
  subject: ConsentSubject
  cookiePolicyVersion: string
  cookiePolicyDigestSha256: string
  uiDigestSha256: string
  surface: string
  correlationId: string
  controlTextVersion: string
  now: string
  idempotencyPrefix: string
}

function defaultPreferences(now: string): CookiePreferences {
  return {
    necessary: true,
    preferences: false,
    analytics: false,
    marketing: false,
    selectedAt: now,
    expiresAt: now,
  }
}

function clonePreferences(preferences: CookiePreferences): CookiePreferences {
  return { ...preferences }
}

function assertCookieContext(context: CookieEvidenceContext): void {
  parseVersion(context.cookiePolicyVersion)
  assertUtcInstant(context.now, 'now')
  if (
    !SHA256_PATTERN.test(context.cookiePolicyDigestSha256)
    || !SHA256_PATTERN.test(context.uiDigestSha256)
  ) {
    throw new Error('cookie evidence must reference exact policy and UI digests')
  }
}

function addSixCalendarMonths(value: string): string {
  const instant = new Date(value)
  const originalDay = instant.getUTCDate()
  instant.setUTCDate(1)
  instant.setUTCMonth(instant.getUTCMonth() + 6)
  const lastDay = new Date(instant)
  lastDay.setUTCMonth(lastDay.getUTCMonth() + 1)
  lastDay.setUTCDate(0)
  instant.setUTCDate(Math.min(originalDay, lastDay.getUTCDate()))
  return instant.toISOString()
}

function isValidStoredPreference(
  stored: CookiePreferences,
  now: string,
): boolean {
  try {
    assertUtcInstant(stored.selectedAt, 'selectedAt')
    assertUtcInstant(stored.expiresAt, 'expiresAt')
  } catch {
    return false
  }
  return stored.necessary === true
    && new Date(stored.expiresAt) > new Date(now)
    && new Date(stored.expiresAt) <= new Date(addSixCalendarMonths(stored.selectedAt))
}

export class CookieConsentManager {
  readonly #activeTrackers = new Set<string>()
  readonly #trackers = new Map<string, OptionalTracker>()
  readonly #store: CookiePreferenceStore
  readonly #evidenceWriter: ConsentEvidenceWriter
  #preferences: CookiePreferences
  #hasChoice = false

  constructor(
    store: CookiePreferenceStore,
    evidenceWriter: ConsentEvidenceWriter,
    now = new Date().toISOString(),
  ) {
    this.#store = store
    this.#evidenceWriter = evidenceWriter
    assertUtcInstant(now, 'now')
    const stored = store.read()
    if (stored && isValidStoredPreference(stored, now)) {
      this.#preferences = clonePreferences(stored)
      this.#hasChoice = true
    } else {
      this.#preferences = defaultPreferences(now)
    }
  }

  preferences(): Readonly<CookiePreferences> {
    return Object.freeze(clonePreferences(this.#preferences))
  }

  hasRecordedChoice(): boolean {
    return this.#hasChoice
  }

  async registerTracker(tracker: OptionalTracker): Promise<void> {
    if (this.#trackers.has(tracker.id)) {
      throw new Error(`tracker ${tracker.id} is already registered`)
    }
    this.#trackers.set(tracker.id, tracker)
    if (this.#hasChoice && this.#preferences[tracker.category]) {
      await tracker.activate()
      this.#activeTrackers.add(tracker.id)
    }
  }

  async acceptAll(context: CookieEvidenceContext): Promise<Readonly<CookiePreferences>> {
    return this.#apply({
      preferences: true,
      analytics: true,
      marketing: true,
    }, context)
  }

  async rejectAll(context: CookieEvidenceContext): Promise<Readonly<CookiePreferences>> {
    return this.#apply({
      preferences: false,
      analytics: false,
      marketing: false,
    }, context)
  }

  async saveCustom(
    selection: Record<OptionalCookieCategory, boolean>,
    context: CookieEvidenceContext,
  ): Promise<Readonly<CookiePreferences>> {
    return this.#apply(selection, context)
  }

  async #apply(
    selection: Record<OptionalCookieCategory, boolean>,
    context: CookieEvidenceContext,
  ): Promise<Readonly<CookiePreferences>> {
    assertCookieContext(context)
    const previous = this.#preferences
    const selectedAt = context.now
    const expiresAt = addSixCalendarMonths(selectedAt)
    const next: CookiePreferences = {
      necessary: true,
      ...selection,
      selectedAt,
      expiresAt,
    }

    await this.#revokeDeniedTrackers(next)

    for (const category of COOKIE_CATEGORIES.slice(1)) {
      const optionalCategory = category as OptionalCookieCategory
      const wasGranted = this.#hasChoice && previous[optionalCategory]
      const isGranted = next[optionalCategory]
      const action: EvidenceAction = isGranted
        ? 'granted'
        : wasGranted
          ? 'withdrawn'
          : 'rejected'
      this.#evidenceWriter.append({
        eventId: `${context.idempotencyPrefix}:${optionalCategory}`,
        subject: context.subject,
        documentKey: 'cookies_it',
        documentVersion: context.cookiePolicyVersion,
        documentDigestSha256: context.cookiePolicyDigestSha256,
        uiDigestSha256: context.uiDigestSha256,
        purpose: `cookies:${optionalCategory}`,
        action,
        occurredAt: context.now,
        locale: 'it-IT',
        contractualCountry: 'IT',
        surface: context.surface,
        correlationId: context.correlationId,
        idempotencyKey: `${context.idempotencyPrefix}:${optionalCategory}`,
        controlTextVersion: context.controlTextVersion,
      })
    }

    this.#preferences = next
    this.#hasChoice = true
    this.#store.write(clonePreferences(next))
    await this.#activateAllowedTrackers()
    return this.preferences()
  }

  async #revokeDeniedTrackers(next: CookiePreferences): Promise<void> {
    for (const tracker of this.#trackers.values()) {
      if (!next[tracker.category] && this.#activeTrackers.has(tracker.id)) {
        await tracker.revoke()
        this.#activeTrackers.delete(tracker.id)
      }
    }
  }

  async #activateAllowedTrackers(): Promise<void> {
    for (const tracker of this.#trackers.values()) {
      if (
        this.#preferences[tracker.category]
        && !this.#activeTrackers.has(tracker.id)
      ) {
        await tracker.activate()
        this.#activeTrackers.add(tracker.id)
      }
    }
  }
}
