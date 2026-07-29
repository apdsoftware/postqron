import {
  parseAccountArea,
  parseBootstrap,
  parseDeletionCancelCapability,
  parseDeletionRequest,
  parseExportDownload,
  parseExportRequest,
  parseSession,
  parseWorkspaceMembers,
  type AccountArea,
  type AppBootstrap,
  type AppSession,
  type ConsentReceipt,
  type DeletionCancelCapability,
  type DeletionRequest,
  type ExportDownload,
  type ExportRequest,
  type OAuthProvider,
  type RegistrationConsentReceipt,
  type WorkspaceMember,
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
  | 'reauthentication'
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
      } | string
    }
  }
  const payload = candidate.data?.error
  if (typeof payload === 'string') {
    return { code: payload, message: payload, retryable: false }
  }
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
  const code = remote.code ?? 'APP_UNKNOWN'
  const kind: AppApiErrorKind = code === 'reauthentication_required'
    || code === 'AUTH_REAUTHENTICATION_REQUIRED'
    ? 'reauthentication'
    : status === 401
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

  async #csrfToken(): Promise<string> {
    const value = await this.#request('/api/v1/auth/csrf', {
      method: 'GET',
      cache: 'no-store',
      headers: {
        'Cache-Control': 'no-store',
      },
    })
    const token = value && typeof value === 'object'
      ? (value as Record<string, unknown>).csrf_token
      : undefined
    if (typeof token !== 'string' || token.trim() === '') {
      throw new AppApiError({
        code: 'APP_CSRF_TOKEN_MISSING',
        kind: 'configuration',
        message: 'The session security token is unavailable',
        retryable: true,
      })
    }
    return token
  }

  async #csrfMutation(
    path: string,
    options: Readonly<Record<string, unknown>> = {},
  ): Promise<unknown> {
    const csrfToken = await this.#csrfToken()
    const headers = {
      ...((options.headers as Readonly<Record<string, string>> | undefined) ?? {}),
      'X-CSRF-Token': csrfToken,
    }
    return this.#request(path, {
      ...options,
      headers,
    })
  }

  #authorizationURL(value: unknown): string {
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

  async passwordRegister(input: {
    confirmation: string
    consents: RegistrationConsentReceipt[]
    email: string
    password: string
  }): Promise<void> {
    await this.#request('/api/v1/auth/password/register', {
      method: 'POST',
      body: {
        email: input.email.trim(),
        password: input.password,
        confirmation: input.confirmation,
        contract_country: 'IT',
        consents: input.consents,
      },
    })
  }

  async verifyEmail(token: string): Promise<void> {
    await this.#request('/api/v1/auth/password/verify', {
      method: 'POST',
      body: { token: token.trim() },
    })
  }

  async resendVerification(email: string): Promise<void> {
    await this.#request('/api/v1/auth/password/verify/resend', {
      method: 'POST',
      body: { email: email.trim() },
    })
  }

  async passwordLogin(input: {
    email: string
    password: string
  }): Promise<void> {
    await this.#request('/api/v1/auth/password/login', {
      method: 'POST',
      body: {
        email: input.email.trim(),
        password: input.password,
      },
    })
  }

  async changePassword(input: {
    confirmation: string
    currentPassword: string
    newPassword: string
  }): Promise<void> {
    await this.#csrfMutation('/api/v1/auth/password/change', {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
      },
      body: {
        current_password: input.currentPassword,
        new_password: input.newPassword,
        confirmation: input.confirmation,
      },
    })
  }

  async authorize(input: {
    consents?: RegistrationConsentReceipt[]
    contractCountry?: string
    provider: OAuthProvider
    returnTo: string
  }): Promise<string> {
    return this.#authorizationURL(await this.#request('/api/v1/auth/authorize', {
      method: 'POST',
      body: {
        provider: input.provider,
        return_to: sanitizeAppDestination(input.returnTo),
        contract_country: input.contractCountry,
        consents: input.consents,
      },
    }))
  }

  async linkProvider(input: {
    provider: OAuthProvider
    returnTo: string
  }): Promise<string> {
    return this.#authorizationURL(await this.#csrfMutation('/api/v1/auth/link', {
      method: 'POST',
      body: {
        provider: input.provider,
        return_to: sanitizeAppDestination(input.returnTo),
      },
    }))
  }

  async callback(
    parameters: Readonly<Record<'code' | 'error' | 'state', string>>,
  ): Promise<{
    linked: boolean
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
      method: 'GET',
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
    if (
      typeof payload.onboarding !== 'boolean'
      || typeof payload.linked !== 'boolean'
      || typeof payload.return_to !== 'string'
    ) {
      throw new AppApiError({
        code: 'APP_INVALID_CALLBACK_PAYLOAD',
        kind: 'configuration',
        message: 'The callback response is invalid',
        retryable: true,
      })
    }
    return {
      linked: payload.linked,
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
    const value = await this.#csrfMutation('/api/v1/app/onboarding', {
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
    await this.#csrfMutation('/api/v1/app/workspaces/select', {
      method: 'POST',
      body: { workspace_id: workspaceId },
    })
  }

  async currentWorkspaceMembers(): Promise<WorkspaceMember[]> {
    return parseWorkspaceMembers(
      await this.#request('/api/v1/app/workspaces/current/members'),
    )
  }

  async accountArea(headers?: Readonly<Record<string, string>>): Promise<AccountArea> {
    return parseAccountArea(
      await this.#request('/api/v1/account', { headers }),
    )
  }

  async updateProfile(input: {
    displayName: string
    locale: string
    timezone: string
  }) {
    const value = await this.#csrfMutation('/api/v1/account/profile', {
      method: 'PATCH',
      body: {
        display_name: input.displayName.trim(),
        locale: input.locale,
        timezone: input.timezone,
      },
    })
    return parseAccountArea({
      profile: value,
      providers: [],
      workspaces: [],
    }).profile
  }

  async disconnectProvider(providerId: string): Promise<void> {
    await this.#csrfMutation(`/api/v1/account/providers/${encodeURIComponent(providerId)}`, {
      method: 'DELETE',
      body: {
        confirmation: providerId,
      },
    })
  }

  async requestExport(input: {
    scope: ExportRequest['scope']
    workspaceId?: string
  }): Promise<ExportRequest> {
    return parseExportRequest(await this.#csrfMutation('/api/v1/account/exports', {
      method: 'POST',
      body: {
        scope: input.scope,
        workspace_id: input.workspaceId,
        confirmation: 'EXPORT',
      },
    }))
  }

  async downloadExport(exportId: string): Promise<ExportDownload> {
    return parseExportDownload(
      await this.#request(`/api/v1/account/exports/${encodeURIComponent(exportId)}/download`),
    )
  }

  async requestDeletion(input: {
    ownershipActions?: DeletionRequest['ownership']['actions']
    scope: DeletionRequest['scope']
    workspaceId?: string
  }): Promise<DeletionRequest> {
    return parseDeletionRequest(await this.#csrfMutation('/api/v1/account/deletions', {
      method: 'POST',
      body: {
        scope: input.scope,
        workspace_id: input.workspaceId,
        ownership_actions: input.ownershipActions ?? [],
        confirmation: 'DELETE',
      },
    }))
  }

  async issueAccountDeletionCancelCapability(): Promise<DeletionCancelCapability> {
    const value = await this.#csrfMutation(
      '/api/v1/account/deletion-cancel-capabilities',
      {
        method: 'POST',
        cache: 'no-store',
        headers: {
          'Cache-Control': 'no-store',
        },
      },
    )
    try {
      return parseDeletionCancelCapability(value)
    } catch (error) {
      throw new AppApiError({
        cause: error,
        code: 'APP_INVALID_DELETION_CANCEL_CAPABILITY_PAYLOAD',
        kind: 'configuration',
        message: 'The account deletion cancellation response is invalid',
        retryable: true,
      })
    }
  }

  async cancelWorkspaceDeletion(requestId: string): Promise<void> {
    await this.#csrfMutation(`/api/v1/account/deletions/${encodeURIComponent(requestId)}`, {
      method: 'DELETE',
    })
  }

  async cancelAccountDeletion(requestId: string): Promise<void> {
    await this.#request(
      `/api/v1/account/deletions/${encodeURIComponent(requestId)}/cancel`,
      {
        method: 'POST',
        cache: 'no-store',
        headers: {
          'Cache-Control': 'no-store',
        },
      },
    )
  }

  async revokeSessions(): Promise<void> {
    await this.#csrfMutation('/api/v1/auth/sessions/revoke', {
      method: 'POST',
    })
  }

  async logout(): Promise<void> {
    await this.#csrfMutation('/api/v1/auth/logout', {
      method: 'POST',
    })
  }
}
