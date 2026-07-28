import assert from 'node:assert/strict'
import test from 'node:test'
import {
  AppApiError,
  AppShellApi,
  normalizeAppApiError,
  resolveAppShellApiBase,
  type AppFetch,
} from '../components/core/api.ts'

const validSession = {
  account: {
    id: 'account-1',
    display_name: 'Ada',
    email: 'ada@example.test',
    email_verified: true,
    locale: 'de',
  },
  authenticated_at: '2026-07-28T12:00:00.000Z',
  onboarding_required: false,
  current_workspace: {
    id: 'workspace-1',
    name: 'Studio',
    role: 'owner',
  },
  workspaces: [{
    id: 'workspace-1',
    name: 'Studio',
    role: 'owner',
  }],
}

test('API base resolution keeps private SSR and public browser origins separate', () => {
  const config = {
    apiBase: 'http://fixture-api.internal:8080',
    public: {
      apiBase: 'https://preview.postqron.test',
    },
  }

  assert.equal(
    resolveAppShellApiBase(config, true),
    'http://fixture-api.internal:8080',
  )
  assert.equal(
    resolveAppShellApiBase(config, false),
    'https://preview.postqron.test',
  )
})

test('session resolution forwards SSR cookie only to the configured API boundary', async () => {
  const calls: Array<{ path: string, options?: Readonly<Record<string, unknown>> }> = []
  const fetch: AppFetch = async (path, options) => {
    calls.push({ path, options })
    return validSession
  }
  const api = new AppShellApi('https://api.postqron.test/', fetch)
  const resolved = await api.session({ cookie: '__Host-postqron_session=opaque' })

  assert.equal(resolved.account.id, 'account-1')
  assert.deepEqual(calls, [{
    path: '/api/v1/app/session',
    options: {
      baseURL: 'https://api.postqron.test',
      credentials: 'include',
      headers: { cookie: '__Host-postqron_session=opaque' },
    },
  }])
})

test('invalid session payload fails closed as retryable configuration error', async () => {
  const api = new AppShellApi(
    'https://api.postqron.test',
    async () => ({ account: { id: 'forged' } }),
  )
  await assert.rejects(
    () => api.session(),
    (error: unknown) =>
      error instanceof AppApiError
      && error.kind === 'configuration'
      && error.code === 'APP_INVALID_SESSION_PAYLOAD',
  )
})

test('HTTP status maps to safe route-guard categories without provider detail', () => {
  assert.equal(
    normalizeAppApiError({ statusCode: 401 }).kind,
    'session',
  )
  assert.equal(
    normalizeAppApiError({ response: { status: 403 } }).kind,
    'access-denied',
  )
  assert.equal(
    normalizeAppApiError({ status: 503 }).kind,
    'configuration',
  )
})

test('authorization rejects insecure provider URLs', async () => {
  const api = new AppShellApi(
    'https://api.postqron.test',
    async () => ({ authorization_url: 'http://provider.example/authorize' }),
  )
  await assert.rejects(
    () => api.authorize({
      provider: 'google',
      returnTo: '/app?plan=team&interval=annual&quantity=5',
      contractCountry: 'IT',
      consents: [],
    }),
    (error: unknown) =>
      error instanceof AppApiError
      && error.code === 'APP_INSECURE_AUTHORIZATION_URL',
  )
})

test('link provider rejects malformed authorization URLs', async () => {
  const api = new AppShellApi(
    'https://api.postqron.test',
    async (path) => {
      if (path === '/api/v1/auth/csrf') {
        return { csrf_token: 'csrf-token-1' }
      }
      return { authorization_url: 'not a url' }
    },
  )
  await assert.rejects(
    () => api.linkProvider({
      provider: 'google',
      returnTo: '/it/app/providers',
    }),
    (error: unknown) =>
      error instanceof AppApiError
      && error.code === 'APP_INVALID_AUTHORIZATION_URL',
  )
})

test('link provider rejects insecure authorization URLs', async () => {
  const api = new AppShellApi(
    'https://api.postqron.test',
    async (path) => {
      if (path === '/api/v1/auth/csrf') {
        return { csrf_token: 'csrf-token-1' }
      }
      return { authorization_url: 'http://provider.example/link' }
    },
  )
  await assert.rejects(
    () => api.linkProvider({
      provider: 'google',
      returnTo: '/it/app/providers',
    }),
    (error: unknown) =>
      error instanceof AppApiError
      && error.code === 'APP_INSECURE_AUTHORIZATION_URL',
  )
})

test('OAuth registration authorize forwards F3 signup receipts, not onboarding receipts', async () => {
  const calls: Array<{ path: string, options?: Readonly<Record<string, unknown>> }> = []
  const fetch: AppFetch = async (path, options) => {
    calls.push({ path, options })
    return { authorization_url: 'https://provider.example/authorize' }
  }
  const api = new AppShellApi('https://api.postqron.test', fetch)

  await api.authorize({
    provider: 'google',
    returnTo: '/it/app',
    contractCountry: 'IT',
    consents: [
      {
        document_key: 'terms_it',
        version: '2026-07-25',
        digest_sha256: 'a'.repeat(64),
        action: 'accepted',
        purpose: 'contract',
        locale: 'it-IT',
        surface: 'signup',
        control_text_id: 'signup-terms-v1',
      },
      {
        document_key: 'privacy_it',
        version: '2026-07-25',
        digest_sha256: 'b'.repeat(64),
        action: 'acknowledged',
        purpose: 'privacy_notice',
        locale: 'it-IT',
        surface: 'signup',
        control_text_id: 'signup-privacy-v1',
      },
    ],
  })

  assert.deepEqual(calls, [{
    path: '/api/v1/auth/authorize',
    options: {
      baseURL: 'https://api.postqron.test',
      credentials: 'include',
      method: 'POST',
      body: {
        provider: 'google',
        return_to: '/it/app',
        contract_country: 'IT',
        consents: [
          {
            document_key: 'terms_it',
            version: '2026-07-25',
            digest_sha256: 'a'.repeat(64),
            action: 'accepted',
            purpose: 'contract',
            locale: 'it-IT',
            surface: 'signup',
            control_text_id: 'signup-terms-v1',
          },
          {
            document_key: 'privacy_it',
            version: '2026-07-25',
            digest_sha256: 'b'.repeat(64),
            action: 'acknowledged',
            purpose: 'privacy_notice',
            locale: 'it-IT',
            surface: 'signup',
            control_text_id: 'signup-privacy-v1',
          },
        ],
      },
    },
  }])
})

test('authenticated mutations fetch a fresh CSRF token immediately before each request', async () => {
  const calls: Array<{ path: string, options?: Readonly<Record<string, unknown>> }> = []
  let csrfSequence = 0
  const fetch: AppFetch = async (path, options) => {
    calls.push({ path, options })
    if (path === '/api/v1/auth/csrf') {
      csrfSequence += 1
      return { csrf_token: `csrf-token-${csrfSequence}` }
    }
    switch (path) {
      case '/api/v1/auth/password/change':
      case '/api/v1/app/workspaces/select':
      case '/api/v1/account/providers/provider-1':
      case '/api/v1/account/deletions/deletion-1':
      case '/api/v1/auth/sessions/revoke':
        return {}
      case '/api/v1/auth/link':
        return { authorization_url: 'https://provider.example/link' }
      case '/api/v1/app/onboarding':
        return validSession
      case '/api/v1/account/profile':
        return {
          account_id: 'account-1',
          display_name: 'Ada Lovelace',
          locale: 'it-IT',
          timezone: 'Europe/Rome',
          updated_at: '2026-07-28T12:00:00.000Z',
        }
      case '/api/v1/account/exports':
        return {
          id: 'export-1',
          account_id: 'account-1',
          scope: 'account',
          status: 'queued',
          requested_at: '2026-07-28T12:00:00.000Z',
          expires_at: '2026-07-29T12:00:00.000Z',
        }
      case '/api/v1/account/deletions':
        return {
          id: 'deletion-1',
          account_id: 'account-1',
          scope: 'workspace',
          workspace_id: 'workspace-1',
          status: 'grace_period',
          requested_at: '2026-07-28T12:00:00.000Z',
          grace_ends_at: '2026-08-04T12:00:00.000Z',
          ownership: {
            actions: [],
          },
        }
      default:
        throw new Error(`Unexpected path: ${path}`)
    }
  }
  const api = new AppShellApi('https://api.postqron.test', fetch)

  await api.changePassword({
    currentPassword: 'current-password-123',
    newPassword: 'new-password-123',
    confirmation: 'new-password-123',
  })
  await api.linkProvider({
    provider: 'google',
    returnTo: '/it/app/providers',
  })
  await api.completeOnboarding({
    consents: [{
      document_key: 'terms',
      version: '2026-07-25',
      digest_sha256: 'a'.repeat(64),
      action: 'accepted',
      purpose: 'contract',
      locale: 'it',
      surface: 'app_onboarding',
      control_text_id: 'app.consent.terms.v1',
    }],
    workspace: {
      mode: 'create',
      name: 'Studio',
    },
  })
  await api.selectWorkspace('workspace-1')
  await api.updateProfile({
    displayName: 'Ada Lovelace',
    locale: 'it-IT',
    timezone: 'Europe/Rome',
  })
  await api.disconnectProvider('provider-1')
  await api.requestExport({ scope: 'account' })
  await api.requestDeletion({
    scope: 'workspace',
    workspaceId: 'workspace-1',
  })
  await api.cancelDeletion('deletion-1')
  await api.revokeSessions()

  const expectedMutationPaths = [
    '/api/v1/auth/password/change',
    '/api/v1/auth/link',
    '/api/v1/app/onboarding',
    '/api/v1/app/workspaces/select',
    '/api/v1/account/profile',
    '/api/v1/account/providers/provider-1',
    '/api/v1/account/exports',
    '/api/v1/account/deletions',
    '/api/v1/account/deletions/deletion-1',
    '/api/v1/auth/sessions/revoke',
  ]

  assert.equal(calls.length, expectedMutationPaths.length * 2)
  for (const [index, path] of expectedMutationPaths.entries()) {
    const csrf = calls[index * 2]
    const mutation = calls[index * 2 + 1]
    assert.equal(csrf.path, '/api/v1/auth/csrf')
    assert.equal((csrf.options?.method as string | undefined), 'GET')
    assert.equal(mutation.path, path)
    assert.equal(
      (mutation.options?.headers as Record<string, string> | undefined)?.['X-CSRF-Token'],
      `csrf-token-${index + 1}`,
    )
  }
})

test('logout fetches the authenticated CSRF token immediately before the mutation', async () => {
  const calls: Array<{ path: string, options?: Readonly<Record<string, unknown>> }> = []
  const fetch: AppFetch = async (path, options) => {
    calls.push({ path, options })
    if (path === '/api/v1/auth/csrf') {
      return { csrf_token: 'csrf-token-1' }
    }
    return {}
  }
  const api = new AppShellApi('https://api.postqron.test', fetch)

  await api.logout()

  assert.deepEqual(calls, [
    {
      path: '/api/v1/auth/csrf',
      options: {
        baseURL: 'https://api.postqron.test',
        credentials: 'include',
        method: 'GET',
        cache: 'no-store',
        headers: {
          'Cache-Control': 'no-store',
        },
      },
    },
    {
      path: '/api/v1/auth/logout',
      options: {
        baseURL: 'https://api.postqron.test',
        credentials: 'include',
        method: 'POST',
        headers: {
          'X-CSRF-Token': 'csrf-token-1',
        },
      },
    },
  ])
})
