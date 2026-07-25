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
    locale: 'de',
  },
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
