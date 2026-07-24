import assert from 'node:assert/strict'
import test from 'node:test'
import {
  CookieConsentManager,
  COOKIE_BANNER_FIRST_LEVEL_ACTIONS,
  InMemoryConsentLedger,
  LegalDocumentRegistry,
  sha256,
  type ConsentEvidence,
  type CookieEvidenceContext,
  type CookiePreferenceStore,
  type CookiePreferences,
  type OptionalTracker,
} from '../src/index.ts'

const instant = '2026-07-24T12:00:00.000Z'

test('legal artifacts are versioned, immutable, and verified against exact bytes', async () => {
  const registry = new LegalDocumentRegistry()
  const content = '# Termini approvati\n'
  const digestSha256 = await sha256(content)
  const stored = await registry.add({
    documentKey: 'terms_it',
    jurisdiction: 'IT',
    locale: 'it-IT',
    version: '1.0',
    content,
    digestSha256,
    contentStatus: 'approved',
    legalApprovalId: 'LEGAL-2026-001',
    approvedAt: instant,
    publishedAt: instant,
    effectiveAt: instant,
    permanentUrl: 'https://postqron.example/legal/terms_it/1.0',
    currentUrl: 'https://postqron.example/legal/terms',
    changeType: 'material',
  })

  assert.equal(registry.current('terms_it', instant), stored)
  assert.equal(Object.isFrozen(stored), true)
  assert.deepEqual(registry.list('terms_it').map(item => item.version), ['1.0'])

  await assert.rejects(() => registry.add({ ...stored }), /immutable/)
  await assert.rejects(() => registry.add({
    ...stored,
    version: '1.1',
    content: `${content}altered`,
  }), /does not match/)
})

test('placeholder copy can be stored for drafting but never approved or published', async () => {
  const registry = new LegalDocumentRegistry()
  const content = '# BOZZA NON PUBBLICABILE\n'
  const digestSha256 = await sha256(content)

  await registry.add({
    documentKey: 'privacy_it',
    jurisdiction: 'IT',
    locale: 'it-IT',
    version: '0.0',
    content,
    digestSha256,
    contentStatus: 'placeholder',
    changeType: 'material',
  })
  assert.equal(registry.current('privacy_it', instant), undefined)

  await assert.rejects(() => registry.add({
    documentKey: 'cookies_it',
    jurisdiction: 'IT',
    locale: 'it-IT',
    version: '0.0',
    content,
    digestSha256,
    contentStatus: 'placeholder',
    legalApprovalId: 'NOT-ALLOWED',
    approvedAt: instant,
    publishedAt: instant,
    effectiveAt: instant,
    permanentUrl: 'https://postqron.example/legal/cookies_it/0.0',
    currentUrl: 'https://postqron.example/legal/cookies',
    changeType: 'material',
  }), /placeholder content cannot/)
})

function evidence(overrides: Partial<ConsentEvidence> = {}): ConsentEvidence {
  return {
    eventId: 'event-1',
    subject: { kind: 'authenticated_user', id: 'user_opaque_1' },
    workspaceId: 'workspace_opaque_1',
    documentKey: 'terms_it',
    documentVersion: '1.0',
    documentDigestSha256: 'a'.repeat(64),
    purpose: 'terms:contract',
    action: 'accepted',
    occurredAt: instant,
    locale: 'it-IT',
    contractualCountry: 'IT',
    surface: 'signup',
    correlationId: 'correlation-1',
    idempotencyKey: 'idempotency-1',
    controlTextVersion: 'signup-legal-v1',
    ...overrides,
  }
}

test('consent evidence is append-only and safely idempotent', () => {
  const ledger = new InMemoryConsentLedger()
  const first = ledger.append(evidence())
  const replay = ledger.append(evidence())

  assert.equal(replay, first)
  assert.equal(Object.isFrozen(first), true)
  assert.equal(Object.isFrozen(first.subject), true)
  assert.equal(ledger.events().length, 1)
  assert.throws(
    () => ledger.append(evidence({ action: 'withdrawn' })),
    /different evidence/,
  )

  ledger.append(evidence({
    eventId: 'event-2',
    action: 'withdrawn',
    idempotencyKey: 'idempotency-2',
  }))
  assert.deepEqual(ledger.events().map(item => item.action), ['accepted', 'withdrawn'])
})

class MemoryPreferenceStore implements CookiePreferenceStore {
  value?: CookiePreferences

  read(): CookiePreferences | undefined {
    return this.value ? { ...this.value } : undefined
  }

  write(preferences: CookiePreferences): void {
    this.value = { ...preferences }
  }
}

class TrackerSpy implements OptionalTracker {
  activations = 0
  revocations = 0
  readonly id: string
  readonly category: 'preferences' | 'analytics' | 'marketing'

  constructor(
    id: string,
    category: 'preferences' | 'analytics' | 'marketing',
  ) {
    this.id = id
    this.category = category
  }

  activate(): void {
    this.activations += 1
  }

  revoke(): void {
    this.revocations += 1
  }
}

function cookieContext(
  idempotencyPrefix: string,
  now = instant,
): CookieEvidenceContext {
  return {
    subject: { kind: 'pseudonymous_browser', id: 'browser_opaque_1' },
    cookiePolicyVersion: '1.0',
    cookiePolicyDigestSha256: 'b'.repeat(64),
    uiDigestSha256: 'c'.repeat(64),
    surface: 'cookie_banner',
    correlationId: `correlation-${idempotencyPrefix}`,
    controlTextVersion: 'cookie-banner-v1',
    now,
    idempotencyPrefix,
  }
}

test('non-essential trackers are blocked until an explicit opt-in', async () => {
  const store = new MemoryPreferenceStore()
  const ledger = new InMemoryConsentLedger()
  const manager = new CookieConsentManager(store, ledger, instant)
  const analytics = new TrackerSpy('analytics-script', 'analytics')
  const marketing = new TrackerSpy('marketing-pixel', 'marketing')

  await manager.registerTracker(analytics)
  await manager.registerTracker(marketing)
  assert.equal(manager.hasRecordedChoice(), false)
  assert.equal(analytics.activations, 0)
  assert.equal(marketing.activations, 0)

  const rejected = await manager.rejectAll(cookieContext('reject'))
  assert.equal(rejected.necessary, true)
  assert.equal(rejected.analytics, false)
  assert.equal(rejected.marketing, false)
  assert.equal(analytics.activations, 0)
  assert.equal(marketing.activations, 0)
  assert.deepEqual(
    ledger.events().map(item => item.action),
    ['rejected', 'rejected', 'rejected'],
  )
  assert.deepEqual(
    COOKIE_BANNER_FIRST_LEVEL_ACTIONS.map(action => action.label),
    ['Accetta tutte', 'Rifiuta tutte', 'Personalizza'],
  )
  assert.equal(
    new Set(COOKIE_BANNER_FIRST_LEVEL_ACTIONS.map(action => action.prominence)).size,
    1,
  )
})

test('accept, reject, and granular revisions synchronise trackers immediately', async () => {
  const store = new MemoryPreferenceStore()
  const ledger = new InMemoryConsentLedger()
  const manager = new CookieConsentManager(store, ledger, instant)
  const preference = new TrackerSpy('preference-store', 'preferences')
  const analytics = new TrackerSpy('analytics-script', 'analytics')
  const marketing = new TrackerSpy('marketing-pixel', 'marketing')
  await manager.registerTracker(preference)
  await manager.registerTracker(analytics)
  await manager.registerTracker(marketing)

  await manager.acceptAll(cookieContext('accept'))
  assert.deepEqual(
    [preference.activations, analytics.activations, marketing.activations],
    [1, 1, 1],
  )

  const revised = await manager.saveCustom({
    preferences: true,
    analytics: false,
    marketing: true,
  }, cookieContext('custom', '2026-07-24T12:01:00.000Z'))
  assert.equal(revised.preferences, true)
  assert.equal(revised.analytics, false)
  assert.equal(revised.marketing, true)
  assert.equal(analytics.revocations, 1)
  assert.equal(preference.revocations, 0)
  assert.equal(marketing.revocations, 0)
  assert.equal(store.value?.expiresAt, '2027-01-24T12:01:00.000Z')
  assert.deepEqual(
    ledger.events().slice(-3).map(item => item.action),
    ['granted', 'withdrawn', 'granted'],
  )

  await manager.rejectAll(cookieContext('reject', '2026-07-24T12:02:00.000Z'))
  assert.deepEqual(
    [preference.revocations, analytics.revocations, marketing.revocations],
    [1, 1, 1],
  )
})

test('expired or overlong stored choices revert to privacy-safe defaults', async () => {
  const store = new MemoryPreferenceStore()
  store.value = {
    necessary: true,
    preferences: true,
    analytics: true,
    marketing: true,
    selectedAt: '2026-01-01T00:00:00.000Z',
    expiresAt: '2027-01-01T00:00:00.000Z',
  }
  const manager = new CookieConsentManager(
    store,
    new InMemoryConsentLedger(),
    instant,
  )
  const analytics = new TrackerSpy('analytics-script', 'analytics')
  await manager.registerTracker(analytics)

  assert.equal(manager.hasRecordedChoice(), false)
  assert.equal(manager.preferences().analytics, false)
  assert.equal(analytics.activations, 0)
})
