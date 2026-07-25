import {
  parseBootstrap,
  parseSession,
  type AppBootstrap,
  type AppSession,
  type ConsentReceipt,
  type OAuthProvider,
} from './contracts.ts'
import { sanitizeAppDestination } from './navigation.ts'

export type AppFetch = (
  path: string,
  options?: Readonly<Record<string, unknown>>,
) => Promise<unknown>

export function resolveAppShellApiBase(
  config: {
    apiBase?: unknown
    public: { apiBase?: unknown }
  },
  server: boolean,
): string {
  return String(server ? config.apiBase : config.public.apiBase)
}

export type AppApiErrorKind =
  | 'access-denied'
  | 'configuration'
  | 'offline'
  | 'session'
  | 'unknown'

export class AppApiError extends Error {
  readonly code: string
  readonly kind: AppApiErrorKind
  readonly retryable: boolean
  readonly status?: number

  constructor(options: {
    cause?: unknown
    code: string
    kind: AppApiErrorKind
    message: string
    retryable: boolean
    status?: number
  }) {
    super(options.message, { cause: options.cause })
    this.name = 'AppApiError'
    this.code = options.code
    this.kind = options.kind
    this.retryable = options.retryable
    this.status = options.status
  }
}

function statusOf(error: unknown): number | undefined {
  if (!error || typeof error !== 'object') {
    return undefined
  }
  const candidate = error as {
    response?: { status?: unknown }
    status?: unknown
    statusCode?: unknown
  }
  for (const value of [
    candidate.statusCode,
    candidate.status,
    candidate.response?.status,
  ]) {
    if (typeof value === 'number') {
      return value
    }
  }
  return undefined
}

function safeRemoteError(error: unknown): {
  code?: string
  message?: string
  retryable?: boolean
} {
  if (!error || typeof error !== 'object') {
    return {}
  }
  const candidate = error as {
    data?: {
      error?: {
        code?: unknown
        message?: unknown
        retryable?: unknown
      }
    }
  }
  const payload = candidate.data?.error
  return {
    code: typeof payload?.code === 'string' ? payload.code : undefined,
    message: typeof payload?.message === 'string' ? payload.message : undefined,
    retryable: typeof payload?.retryable === 'boolean'
      ? payload.retryable
      : undefined,
  }
}

export function normalizeAppApiError(error: unknown): AppApiError {
  if (error instanceof AppApiError) {
    return error
  }
  const status = statusOf(error)
  const remote = safeRemoteError(error)
  const kind: AppApiErrorKind = status === 401
    ? 'session'
    : status === 403
      ? 'access-denied'
      : status === 0 || status === undefined
        ? 'offline'
        : status >= 500
          ? 'configuration'
          : 'unknown'
  return new AppApiError({
    cause: error,
    status,
    kind,
    code: remote.code ?? `APP_${kind.replace('-', '_').toUpperCase()}`,
    message: remote.message ?? 'The app service request failed',
    retryable: remote.retryable ?? (kind === 'offline' || kind === 'configuration'),
  })
}

export class AppShellApi {
  readonly #baseURL: string
  readonly #fetch: AppFetch

  constructor(baseURL: string, fetch: AppFetch) {
    this.#baseURL = baseURL.replace(/\/+$/u, '')
    this.#fetch = fetch
  }

  async #request(
    path: string,
    options: Readonly<Record<string, unknown>> = {},
  ): Promise<unknown> {
    try {
      return await this.#fetch(path, {
        baseURL: this.#baseURL,
        credentials: 'include',
        ...options,
      })
    } catch (error) {
      throw normalizeAppApiError(error)
    }
  }

  async bootstrap(headers?: Readonly<Record<string, string>>): Promise<AppBootstrap> {
    const value = await this.#request('/api/v1/app/bootstrap', { headers })
    try {
      return parseBootstrap(value)
    } catch (error) {
      throw new AppApiError({
        cause: error,
        code: 'APP_INVALID_BOOTSTRAP_PAYLOAD',
        kind: 'configuration',
        message: 'The app bootstrap response is invalid',
        retryable: true,
      })
    }
  }

  async session(headers?: Readonly<Record<string, string>>): Promise<AppSession> {
    const value = await this.#request('/api/v1/app/session', { headers })
    try {
      return parseSession(value)
    } catch (error) {
      throw new AppApiError({
        cause: error,
        code: 'APP_INVALID_SESSION_PAYLOAD',
        kind: 'configuration',
        message: 'The app session response is invalid',
        retryable: true,
      })
    }
  }

  async authorize(input: {
    consents: ConsentReceipt[]
    contractCountry: string
    provider: OAuthProvider
    returnTo: string
  }): Promise<string> {
    const value = await this.#request('/api/v1/auth/authorize', {
      method: 'POST',
      body: {
        provider: input.provider,
        return_to: sanitizeAppDestination(input.returnTo),
        contract_country: input.contractCountry,
        consents: input.consents,
      },
    })
    const authorizationURL = value && typeof value === 'object'
      ? (value as Record<string, unknown>).authorization_url
      : undefined
    if (typeof authorizationURL !== 'string') {
      throw new AppApiError({
        code: 'APP_INVALID_AUTHORIZATION_PAYLOAD',
        kind: 'configuration',
        message: 'The authorization response is invalid',
        retryable: true,
      })
    }
    let parsed: URL
    try {
      parsed = new URL(authorizationURL)
    } catch {
      throw new AppApiError({
        code: 'APP_INVALID_AUTHORIZATION_URL',
        kind: 'configuration',
        message: 'The authorization URL is malformed',
        retryable: true,
      })
    }
    if (parsed.protocol !== 'https:') {
      throw new AppApiError({
        code: 'APP_INSECURE_AUTHORIZATION_URL',
        kind: 'configuration',
        message: 'The authorization URL is not secure',
        retryable: false,
      })
    }
    return parsed.href
  }

  async callback(
    parameters: Readonly<Record<'code' | 'error' | 'state', string>>,
  ): Promise<{
    onboarding: boolean
    returnTo: string
  }> {
    const query = new URLSearchParams()
    for (const [key, value] of Object.entries(parameters)) {
      if (value) {
        query.set(key, value)
      }
    }
    const value = await this.#request(`/api/v1/auth/callback?${query}`, {
      method: 'POST',
    })
    if (!value || typeof value !== 'object') {
      throw new AppApiError({
        code: 'APP_INVALID_CALLBACK_PAYLOAD',
        kind: 'configuration',
        message: 'The callback response is invalid',
        retryable: true,
      })
    }
    const payload = value as Record<string, unknown>
    if (typeof payload.onboarding !== 'boolean' || typeof payload.return_to !== 'string') {
      throw new AppApiError({
        code: 'APP_INVALID_CALLBACK_PAYLOAD',
        kind: 'configuration',
        message: 'The callback response is invalid',
        retryable: true,
      })
    }
    return {
      onboarding: payload.onboarding,
      returnTo: sanitizeAppDestination(payload.return_to),
    }
  }

  async completeOnboarding(input: {
    consents: ConsentReceipt[]
    workspace:
      | { mode: 'create', name: string }
      | { mode: 'select', id: string }
  }): Promise<AppSession> {
    const value = await this.#request('/api/v1/app/onboarding', {
      method: 'POST',
      body: input,
    })
    return parseSession(value)
  }

  async selectWorkspace(workspaceId: string): Promise<void> {
    if (!workspaceId.trim()) {
      throw new AppApiError({
        code: 'APP_INVALID_WORKSPACE',
        kind: 'unknown',
        message: 'A workspace identifier is required',
        retryable: false,
      })
    }
    await this.#request('/api/v1/app/workspaces/select', {
      method: 'POST',
      body: { workspace_id: workspaceId },
    })
  }

  async logout(): Promise<void> {
    await this.#request('/api/v1/auth/logout', { method: 'POST' })
  }
}
