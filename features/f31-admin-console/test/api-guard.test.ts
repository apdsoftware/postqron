import assert from 'node:assert/strict'
import test from 'node:test'
import {
  AdminApi,
  AdminApiError,
  normalizeAdminApiError,
  type AdminFetch,
} from '../core/api.ts'
import { adminGuardDecision } from '../core/guard.ts'

const sessionPayload = {
  account: {
    id: 'account-1',
    email: 'admin@example.test',
  },
  authenticated_at: '2026-07-25T12:00:00.000Z',
  csrf_token: 'opaque-csrf-token',
}

const dashboardPayload = {
  services: [{
    code: 'api',
    status: 'healthy',
    checked_at: '2026-07-25T12:00:00.000Z',
  }],
  entitlements: [{
    workspace_id: 'workspace-1',
    plan_code: 'pro',
    internal: false,
    social_token: 'must-not-survive-parser',
    complete_payment_data: 'must-not-survive-parser',
  }],
  recent_audit: [{
    id: 'audit-1',
    code: 'internal_plan.assign',
    actor_id: 'account-1',
    subject_id: 'workspace-1',
    reason: 'Approved for support',
    outcome: 'accepted',
    correlation_id: 'correlation-1',
    occurred_at: '2026-07-25T12:00:00.000Z',
  }],
}

test('admin client completes session, dashboard, assign and revoke flow securely', async () => {
  const calls: Array<{
    path: string
    options?: Readonly<Record<string, unknown>>
  }> = []
  const fetch: AdminFetch = async (path, options) => {
    calls.push({ path, options })
    if (path === '/api/v1/admin/session') {
      return sessionPayload
    }
    if (path === '/api/v1/admin/dashboard') {
      return dashboardPayload
    }
    return {
      code: 'ADMIN_MUTATION_ACCEPTED',
      correlation_id: `correlation-${calls.length}`,
    }
  }
  const api = new AdminApi('https://admin.postqron.test/', fetch)
  const session = await api.session({ cookie: '__Host-postqron_session=opaque' })
  const dashboard = await api.dashboard()
  await api.changeInternalPlan({
    action: 'assign',
    workspaceId: 'workspace-1',
    confirmed: true,
    reason: 'Approved for support work',
    csrfToken: session.csrf_token,
    idempotencyKey: 'aaaaaaaa',
  })
  await api.changeInternalPlan({
    action: 'revoke',
    workspaceId: 'workspace-1',
    confirmed: true,
    reason: 'Support work has completed',
    csrfToken: session.csrf_token,
    idempotencyKey: 'bbbbbbbb',
  })

  assert.equal(dashboard.entitlements[0]?.plan_code, 'pro')
  assert.equal(
    'social_token' in (dashboard.entitlements[0] as object),
    false,
  )
  assert.equal(
    'complete_payment_data' in (dashboard.entitlements[0] as object),
    false,
  )
  assert.deepEqual(calls[0], {
    path: '/api/v1/admin/session',
    options: {
      baseURL: 'https://admin.postqron.test',
      credentials: 'include',
      headers: { cookie: '__Host-postqron_session=opaque' },
    },
  })
  for (const call of calls.slice(2)) {
    const headers = call.options?.headers as Record<string, string>
    const body = call.options?.body as Record<string, unknown>
    assert.equal(headers['X-CSRF-Token'], session.csrf_token)
    assert.match(headers['Idempotency-Key']!, /^[ab]{8}$/u)
    assert.equal(body.confirmed, true)
    assert.ok(String(body.reason).length >= 8)
  }
  assert.equal(calls[2]?.options?.method, 'PUT')
  assert.equal(calls[3]?.options?.method, 'DELETE')
})

test('guard distinguishes expired and forbidden sessions without trusting route data', () => {
  const expired = adminGuardDecision({
    destination: '/de/admin?view=audit',
    error: new AdminApiError('ADMIN_UNAUTHENTICATED', 401),
  })
  assert.deepEqual(expired, {
    action: 'login',
    location: '/app?return_to=%2Fde%2Fadmin%3Fview%3Daudit',
  })
  assert.deepEqual(
    adminGuardDecision({
      destination: '/admin',
      error: new AdminApiError('ADMIN_FORBIDDEN', 403),
    }),
    { action: 'forbid' },
  )
  assert.deepEqual(
    adminGuardDecision({
      destination: '/admin',
      error: new AdminApiError('ADMIN_UNAVAILABLE', 503),
    }),
    { action: 'unavailable' },
  )
  assert.deepEqual(
    adminGuardDecision({
      destination: '//attacker.example',
      error: new AdminApiError('ADMIN_UNAUTHENTICATED', 401),
    }),
    {
      action: 'login',
      location: '/app?return_to=%2Fadmin',
    },
  )
})

test('remote errors expose only stable admin codes', () => {
  assert.deepEqual(
    {
      code: normalizeAdminApiError({
        statusCode: 403,
        data: {
          error: 'ADMIN_CSRF_INVALID',
          detail: 'database and allowlist internals',
        },
      }).code,
      status: normalizeAdminApiError({ statusCode: 403 }).status,
    },
    {
      code: 'ADMIN_CSRF_INVALID',
      status: 403,
    },
  )
  assert.equal(
    normalizeAdminApiError({
      statusCode: 500,
      data: { error: 'postgres connection string leaked' },
    }).code,
    'ADMIN_UNAVAILABLE',
  )
  assert.equal(
    normalizeAdminApiError({
      statusCode: 400,
      data: {
        error: {
          code: 'AUTH_CURRENT_PASSWORD_INVALID',
          message: 'safe remote message',
        },
      },
    }).code,
    'ADMIN_CURRENT_PASSWORD_INVALID',
  )
})

test('password change and logout send only secrets required by their authenticated contracts', async () => {
  const calls: Array<{
    path: string
    options?: Readonly<Record<string, unknown>>
  }> = []
  const api = new AdminApi(
    'https://admin.postqron.test',
    async (path, options) => {
      calls.push({ path, options })
      return path.endsWith('/change') ? { changed: true } : undefined
    },
  )

  await api.changePassword({
    currentPassword: 'current-password-value',
    newPassword: 'new-password-value',
    confirmation: 'new-password-value',
    csrfToken: 'opaque-csrf-token',
  })
  await api.logout('rotated-csrf-token')

  assert.deepEqual(calls, [
    {
      path: '/api/v1/auth/password/change',
      options: {
        baseURL: 'https://admin.postqron.test',
        credentials: 'include',
        method: 'POST',
        headers: {
          'Content-Type': 'application/json',
          'X-CSRF-Token': 'opaque-csrf-token',
        },
        body: {
          current_password: 'current-password-value',
          new_password: 'new-password-value',
          confirmation: 'new-password-value',
        },
      },
    },
    {
      path: '/api/v1/auth/logout',
      options: {
        baseURL: 'https://admin.postqron.test',
        credentials: 'include',
        method: 'POST',
        headers: {
          'X-CSRF-Token': 'rotated-csrf-token',
        },
      },
    },
  ])
})
