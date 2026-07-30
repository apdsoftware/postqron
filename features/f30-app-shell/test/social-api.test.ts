import assert from 'node:assert/strict'
import test from 'node:test'
import type { AppFetch } from '../components/core/api.ts'
import {
  normalizeSocialApiError,
  SocialApiError,
  SocialConnectionsApi,
} from '../components/core/social-api.ts'

const connection = {
  id: 'conn-1',
  workspace_id: 'workspace-1',
  provider: 'facebook_pages',
  remote_id: 'page-1',
  resource_type: 'facebook_page',
  account_type: 'page',
  display_name: 'Studio Page',
  scopes: ['pages_manage_posts'],
  status: 'connected',
  last_verified_at: '2026-07-29T10:00:00.000Z',
  created_at: '2026-07-20T10:00:00.000Z',
  updated_at: '2026-07-29T10:00:00.000Z',
}

test('bootstrap and list hit the workspace-scoped F5 routes with credentials, no CSRF', async () => {
  const calls: Array<{ path: string, options?: Readonly<Record<string, unknown>> }> = []
  const fetch: AppFetch = async (path, options) => {
    calls.push({ path, options })
    if (path.endsWith('/bootstrap')) {
      return { providers: [
        { provider: 'facebook_pages', status: 'available', retryable: false },
        { provider: 'instagram_professional', status: 'unavailable', retryable: false },
      ] }
    }
    return { connections: [connection] }
  }
  const api = new SocialConnectionsApi('https://api.postqron.test/', fetch)

  const bootstrap = await api.bootstrap('workspace-1')
  const connections = await api.list('workspace-1')

  assert.equal(bootstrap.providers[1]?.status, 'unavailable')
  assert.equal(connections.length, 1)
  assert.deepEqual(calls, [
    {
      path: '/api/v1/workspaces/workspace-1/social-connections/bootstrap',
      options: { baseURL: 'https://api.postqron.test', credentials: 'include' },
    },
    {
      path: '/api/v1/workspaces/workspace-1/social-connections',
      options: { baseURL: 'https://api.postqron.test', credentials: 'include' },
    },
  ])
  assert.equal(
    calls.some(call => String(call.path).includes('csrf')),
    false,
  )
})

test('begin, select, reconnect, and revoke use the exact contract paths and bodies', async () => {
  const calls: Array<{ path: string, options?: Readonly<Record<string, unknown>> }> = []
  const fetch: AppFetch = async (path, options) => {
    calls.push({ path, options })
    if (path.endsWith('/social-authorizations') || path.endsWith('/reconnect')) {
      return {
        authorization_url: 'https://www.facebook.com/dialog/oauth?state=abc',
        expires_at: '2026-07-30T10:10:00.000Z',
      }
    }
    if (path.endsWith('/social-connections')) {
      return connection
    }
    return { connection: { ...connection, status: 'revoked' }, provider_revoked: true }
  }
  const api = new SocialConnectionsApi('https://api.postqron.test', fetch)

  await api.begin('workspace-1', 'facebook_pages')
  await api.selectResource('workspace-1', { selectionId: 'sel-1', remoteId: 'page-1' })
  await api.reconnect('workspace-1', 'conn-1')
  const revocation = await api.revoke('workspace-1', 'conn-1')

  assert.equal(revocation.provider_revoked, true)
  assert.deepEqual(calls.map(call => `${call.options?.method ?? 'GET'} ${call.path}`), [
    'POST /api/v1/workspaces/workspace-1/social-authorizations',
    'POST /api/v1/workspaces/workspace-1/social-connections',
    'POST /api/v1/workspaces/workspace-1/social-connections/conn-1/reconnect',
    'DELETE /api/v1/workspaces/workspace-1/social-connections/conn-1',
  ])
  assert.deepEqual(calls[0]?.options?.body, { provider: 'facebook_pages' })
  assert.deepEqual(calls[1]?.options?.body, {
    selection_id: 'sel-1',
    remote_id: 'page-1',
  })
})

test('callback exchange forwards state, code, and error to the shared endpoint', async () => {
  const calls: string[] = []
  const fetch: AppFetch = async (path) => {
    calls.push(path)
    return {
      selection_id: 'sel-1',
      provider: 'facebook_pages',
      expires_at: '2026-07-30T10:10:00.000Z',
      resources: [{
        remote_id: 'page-1',
        resource_type: 'facebook_page',
        account_type: 'page',
        display_name: 'Studio Page',
        scopes: ['pages_manage_posts'],
      }],
    }
  }
  const api = new SocialConnectionsApi('https://api.postqron.test', fetch)

  const selection = await api.completeAuthorization({
    state: 'state-1',
    code: 'code-1',
    error: '',
  })

  assert.equal(selection.resources.length, 1)
  assert.equal(calls[0], '/api/v1/social-authorizations/callback?state=state-1&code=code-1')
})

test('flat F5 error envelope maps to stable, fail-closed kinds', () => {
  const quota = normalizeSocialApiError({
    status: 409,
    data: { code: 'channel_quota_exceeded', message: 'x', retryable: false },
  })
  assert.equal(quota.kind, 'quota-exceeded')
  assert.equal(quota.retryable, false)

  const unavailable = normalizeSocialApiError({
    status: 503,
    data: { code: 'provider_unavailable', message: 'x', retryable: false },
  })
  assert.equal(unavailable.kind, 'provider-unavailable')
  assert.equal(unavailable.retryable, false)

  const denied = normalizeSocialApiError({
    status: 400,
    data: { code: 'provider_denied', message: 'x', retryable: true },
  })
  assert.equal(denied.kind, 'provider-denied')

  const providerAccessDenied = normalizeSocialApiError({
    status: 422,
    data: { code: 'provider_access_denied', message: 'x', retryable: false },
  })
  assert.equal(providerAccessDenied.kind, 'provider-access-denied')
  assert.equal(providerAccessDenied.retryable, false)

  const temporary = normalizeSocialApiError({
    status: 502,
    data: { code: 'provider_temporary', message: 'x', retryable: true },
  })
  assert.equal(temporary.kind, 'provider-temporary')
  assert.equal(temporary.retryable, true)

  const forbidden = normalizeSocialApiError({ status: 403, data: { code: 'forbidden' } })
  assert.equal(forbidden.kind, 'access-denied')

  // An undeclared code fails closed to unavailable, never to success.
  const unknown = normalizeSocialApiError({ status: 500, data: { code: 'surprise' } })
  assert.equal(unknown.kind, 'unavailable')

  const offline = normalizeSocialApiError({ status: 0 })
  assert.equal(offline.kind, 'offline')
})

test('request failures are normalized to SocialApiError', async () => {
  const api = new SocialConnectionsApi('https://api.postqron.test', async () => {
    throw { status: 403, data: { code: 'forbidden', message: 'owner only', retryable: false } }
  })
  await assert.rejects(
    () => api.begin('workspace-1', 'facebook_pages'),
    (error: unknown) =>
      error instanceof SocialApiError && error.kind === 'access-denied',
  )
})

test('a missing workspace id fails closed before any request', async () => {
  const api = new SocialConnectionsApi('https://api.postqron.test', async () => {
    throw new Error('should not be called')
  })
  await assert.rejects(
    () => api.bootstrap('  '),
    (error: unknown) =>
      error instanceof SocialApiError && error.kind === 'invalid',
  )
})
