import assert from 'node:assert/strict'
import test from 'node:test'
import {
  buildConsentReceipts,
  parseBootstrap,
  parseSession,
} from '../components/core/contracts.ts'
import {
  securityEmailCommand,
  welcomeEmailCommand,
  workspaceInvitationEmailCommand,
} from '../components/core/email-events.ts'

const digest = 'a'.repeat(64)

test('bootstrap accepts only two versioned required documents and known providers', () => {
  const bootstrap = parseBootstrap({
    providers: ['google', 'apple', 'facebook', 'linkedin'],
    legal_documents: [
      {
        key: 'terms',
        version: '2026-07-25',
        digest_sha256: digest,
        href: '/legal/termini',
      },
      {
        key: 'privacy',
        version: '2026-07-25',
        digest_sha256: digest,
        href: '/it/legal/privacy',
      },
    ],
  })
  assert.equal(bootstrap.providers.length, 4)
  assert.deepEqual(
    buildConsentReceipts(bootstrap.legal_documents, 'it'),
    [
      {
        document_key: 'terms',
        version: '2026-07-25',
        digest_sha256: digest,
        action: 'accepted',
        purpose: 'contract',
        locale: 'it',
        surface: 'app_onboarding',
        control_text_id: 'app.consent.terms.v1',
      },
      {
        document_key: 'privacy',
        version: '2026-07-25',
        digest_sha256: digest,
        action: 'accepted',
        purpose: 'privacy_acknowledgement',
        locale: 'it',
        surface: 'app_onboarding',
        control_text_id: 'app.consent.privacy.v1',
      },
    ],
  )
})

test('session parsing rejects a current workspace outside memberships', () => {
  assert.throws(
    () => parseSession({
      account: { id: 'a1', display_name: 'Ada', locale: 'en' },
      onboarding_required: false,
      workspaces: [],
      current_workspace: { id: 'w1', name: 'Secret', role: 'owner' },
    }),
    /APP_INVALID_SESSION_PAYLOAD/u,
  )
})

test('welcome/onboarding, invitation, and security use exact F14 idempotency keys', () => {
  const recipient = {
    id: 'account-1',
    email: 'ada@example.test',
    name: 'Ada',
    locale: 'fr' as const,
  }
  const occurredAt = '2026-07-25T12:00:00.000Z'
  const welcome = welcomeEmailCommand(
    recipient,
    occurredAt,
    'https://app.postqron.com/fr/app/home',
  )
  const invitation = workspaceInvitationEmailCommand(
    'invitation-7',
    recipient,
    occurredAt,
    'https://app.postqron.com/fr/app/invitations/accept',
  )
  const security = securityEmailCommand(
    'event-9',
    recipient,
    occurredAt,
    'A new provider was linked',
  )

  assert.equal(welcome.event, 'f14.welcome.v1')
  assert.equal(welcome.idempotency_key, 'welcome:account-1')
  assert.equal(welcome.channel, 'transactional')
  assert.equal(invitation.event, 'f14.workspace_invitation.v1')
  assert.equal(invitation.idempotency_key, 'workspace-invite:invitation-7')
  assert.equal(security.event, 'f14.account_security.v1')
  assert.equal(security.idempotency_key, 'security:event-9')
  assert.deepEqual(
    new Set([welcome.template_version, invitation.template_version, security.template_version]),
    new Set(['1.0.0']),
  )
})
