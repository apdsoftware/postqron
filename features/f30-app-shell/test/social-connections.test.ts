import assert from 'node:assert/strict'
import test from 'node:test'
import {
  parseSocialBootstrap,
  parseSocialAuthorization,
  parseSocialConnection,
  parseSocialConnections,
  parseSocialRevocation,
  parseSocialSelection,
} from '../components/core/social-connections.ts'

const connectionPayload = {
  id: 'conn-1',
  workspace_id: 'workspace-1',
  provider: 'facebook_pages',
  remote_id: 'page-1',
  resource_type: 'facebook_page',
  account_type: 'page',
  display_name: 'Studio Page',
  handle: 'studio',
  scopes: ['pages_show_list', 'pages_manage_posts'],
  status: 'connected',
  last_verified_at: '2026-07-29T10:00:00.000Z',
  created_at: '2026-07-20T10:00:00.000Z',
  updated_at: '2026-07-29T10:00:00.000Z',
}

test('bootstrap availability parses fail-closed provider states', () => {
  const bootstrap = parseSocialBootstrap({
    providers: [
      { provider: 'facebook_pages', status: 'available', retryable: false },
      { provider: 'instagram_professional', status: 'unavailable', retryable: true },
    ],
  })
  assert.equal(bootstrap.providers.length, 2)
  assert.equal(bootstrap.providers[0]?.status, 'available')
  assert.equal(bootstrap.providers[1]?.status, 'unavailable')
})

test('bootstrap rejects an unknown provider or status', () => {
  assert.throws(() => parseSocialBootstrap({
    providers: [{ provider: 'tiktok', status: 'available', retryable: false }],
  }))
  assert.throws(() => parseSocialBootstrap({
    providers: [{ provider: 'facebook_pages', status: 'maybe', retryable: false }],
  }))
})

test('authorization requires an https URL and rejects insecure hand-offs', () => {
  const authorization = parseSocialAuthorization({
    authorization_url: 'https://www.facebook.com/dialog/oauth?state=abc',
    expires_at: '2026-07-30T10:10:00.000Z',
  })
  assert.match(authorization.authorization_url, /^https:\/\//u)
  assert.throws(() => parseSocialAuthorization({
    authorization_url: 'http://insecure.example/oauth',
    expires_at: '2026-07-30T10:10:00.000Z',
  }))
})

test('selection keeps only publishable candidates and safe picture URLs', () => {
  const selection = parseSocialSelection({
    selection_id: 'sel-1',
    provider: 'instagram_professional',
    expires_at: '2026-07-30T10:10:00.000Z',
    resources: [{
      remote_id: 'ig-1',
      resource_type: 'instagram_professional',
      account_type: 'creator',
      display_name: 'Creator',
      handle: 'creator',
      picture_url: 'javascript:alert(1)',
      scopes: ['instagram_business_basic'],
    }],
  })
  assert.equal(selection.resources.length, 1)
  assert.equal(selection.resources[0]?.account_type, 'creator')
  // An unsafe picture URL is dropped, never surfaced to the browser.
  assert.equal(selection.resources[0]?.picture_url, undefined)
})

test('selection requires at least one resource', () => {
  assert.throws(() => parseSocialSelection({
    selection_id: 'sel-1',
    provider: 'facebook_pages',
    expires_at: '2026-07-30T10:10:00.000Z',
    resources: [],
  }))
})

test('connection parsing never surfaces token material even if present', () => {
  const connection = parseSocialConnection({
    ...connectionPayload,
    access_token: 'super-secret-token',
    refresh_token: 'another-secret',
    page_access_token: 'do-not-leak',
  })
  const serialized = JSON.stringify(connection)
  assert.doesNotMatch(serialized, /token/u)
  assert.doesNotMatch(serialized, /secret/u)
  assert.equal(connection.status, 'connected')
  assert.equal(connection.last_verified_at, '2026-07-29T10:00:00.000Z')
})

test('connection surfaces reconnect status and known reason only', () => {
  const connection = parseSocialConnection({
    ...connectionPayload,
    status: 'reconnect_required',
    reconnect_reason: 'authentication_revoked',
    last_verified_at: null,
  })
  assert.equal(connection.status, 'reconnect_required')
  assert.equal(connection.reconnect_reason, 'authentication_revoked')
  assert.equal(connection.last_verified_at, undefined)

  const unknownReason = parseSocialConnection({
    ...connectionPayload,
    status: 'reconnect_required',
    reconnect_reason: 'mystery',
  })
  assert.equal(unknownReason.reconnect_reason, undefined)
})

test('connection list and revocation parse the safe envelope', () => {
  const connections = parseSocialConnections({ connections: [connectionPayload] })
  assert.equal(connections.length, 1)

  const revocation = parseSocialRevocation({
    connection: { ...connectionPayload, status: 'revoked', revoked_at: '2026-07-30T09:00:00.000Z' },
    provider_revoked: true,
  })
  assert.equal(revocation.provider_revoked, true)
  assert.equal(revocation.connection.status, 'revoked')
})

test('connection rejects an unknown status fail-closed', () => {
  assert.throws(() => parseSocialConnection({ ...connectionPayload, status: 'pending' }))
})
