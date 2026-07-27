import {
  assertSafeIdentifier,
  parseAdminSession,
  parseDashboard,
  parseMutationResult,
  parseSearchResults,
  type AdminDashboard,
  type AdminSession,
  type MutationResult,
  type SearchResults,
} from './contracts.ts'

export type AdminFetch = (
  path: string,
  options?: Readonly<Record<string, unknown>>,
) => Promise<unknown>

export class AdminApiError extends Error {
  readonly code: string
  readonly status?: number

  constructor(code: string, status?: number, cause?: unknown) {
    super(code, { cause })
    this.name = 'AdminApiError'
    this.code = code
    this.status = status
  }
}

function statusOf(error: unknown): number | undefined {
  if (!error || typeof error !== 'object') {
    return undefined
  }
  const candidate = error as {
    status?: unknown
    statusCode?: unknown
    response?: { status?: unknown }
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

function remoteCode(error: unknown): string | undefined {
  if (!error || typeof error !== 'object') {
    return undefined
  }
  const value = (error as {
    data?: { error?: unknown }
  }).data?.error
  const code = typeof value === 'string'
    ? value
    : value && typeof value === 'object'
      ? (value as { code?: unknown }).code
      : undefined
  return typeof code === 'string'
    && /^(?:ADMIN|AUTH)_[A-Z_]+$/u.test(code)
    ? code
    : undefined
}

function adminCode(code: string | undefined): string | undefined {
  const passwordCodes: Readonly<Record<string, string>> = {
    AUTH_UNAUTHENTICATED: 'ADMIN_UNAUTHENTICATED',
    AUTH_CSRF_INVALID: 'ADMIN_CSRF_INVALID',
    AUTH_REAUTHENTICATION_REQUIRED: 'ADMIN_REAUTH_REQUIRED',
    AUTH_CURRENT_PASSWORD_INVALID: 'ADMIN_CURRENT_PASSWORD_INVALID',
    AUTH_PASSWORD_CONFIRMATION_MISMATCH: 'ADMIN_PASSWORD_CONFIRMATION_MISMATCH',
    AUTH_PASSWORD_WEAK: 'ADMIN_PASSWORD_WEAK',
    AUTH_PASSWORD_CHANGE_RATE_LIMITED: 'ADMIN_PASSWORD_CHANGE_RATE_LIMITED',
    AUTH_PASSWORD_CHANGE_CONFLICT: 'ADMIN_REAUTH_REQUIRED',
    AUTH_PASSWORD_UNAVAILABLE: 'ADMIN_UNAVAILABLE',
  }
  if (!code) {
    return undefined
  }
  return code.startsWith('ADMIN_') ? code : passwordCodes[code]
}

export function normalizeAdminApiError(error: unknown): AdminApiError {
  if (error instanceof AdminApiError) {
    return error
  }
  const status = statusOf(error)
  const fallback = status === 401
    ? 'ADMIN_UNAUTHENTICATED'
    : status === 403
      ? 'ADMIN_FORBIDDEN'
      : 'ADMIN_UNAVAILABLE'
  return new AdminApiError(adminCode(remoteCode(error)) ?? fallback, status, error)
}

export class AdminApi {
  readonly #baseURL: string
  readonly #fetch: AdminFetch

  constructor(baseURL: string, fetch: AdminFetch) {
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
      throw normalizeAdminApiError(error)
    }
  }

  async session(headers?: Readonly<Record<string, string>>): Promise<AdminSession> {
    try {
      return parseAdminSession(await this.#request('/api/v1/admin/session', { headers }))
    } catch (error) {
      if (error instanceof AdminApiError) {
        throw error
      }
      throw new AdminApiError('ADMIN_UNAVAILABLE', undefined, error)
    }
  }

  async passwordLogin(input: {
    email: string
    password: string
  }): Promise<void> {
    try {
      await this.#request('/api/v1/auth/password/login', {
        method: 'POST',
        body: {
          email: input.email.trim(),
          password: input.password,
        },
      })
    } catch (error) {
      const status = statusOf(error)
      throw new AdminApiError(
        status === 401
          ? 'ADMIN_INVALID_CREDENTIALS'
          : 'ADMIN_UNAVAILABLE',
        status,
        error,
      )
    }
  }

  async logout(csrfToken: string): Promise<void> {
    await this.#request('/api/v1/auth/logout', {
      method: 'POST',
      headers: {
        'X-CSRF-Token': csrfToken,
      },
    })
  }

  async changePassword(input: {
    confirmation: string
    csrfToken: string
    currentPassword: string
    newPassword: string
  }): Promise<void> {
    await this.#request('/api/v1/auth/password/change', {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
        'X-CSRF-Token': input.csrfToken,
      },
      body: {
        current_password: input.currentPassword,
        new_password: input.newPassword,
        confirmation: input.confirmation,
      },
    })
  }

  async dashboard(headers?: Readonly<Record<string, string>>): Promise<AdminDashboard> {
    try {
      return parseDashboard(await this.#request('/api/v1/admin/dashboard', { headers }))
    } catch (error) {
      if (error instanceof AdminApiError) {
        throw error
      }
      throw new AdminApiError('ADMIN_UNAVAILABLE', undefined, error)
    }
  }

  async search(query: string): Promise<SearchResults> {
    const normalized = query.trim()
    if (normalized.length < 2 || normalized.length > 120) {
      throw new AdminApiError('ADMIN_INVALID_SEARCH', 400)
    }
    const parameters = new URLSearchParams({ q: normalized })
    try {
      return parseSearchResults(
        await this.#request(`/api/v1/admin/search?${parameters.toString()}`),
      )
    } catch (error) {
      if (error instanceof AdminApiError) {
        throw error
      }
      throw new AdminApiError('ADMIN_UNAVAILABLE', undefined, error)
    }
  }

  async changeInternalPlan(input: {
    action: 'assign' | 'revoke'
    confirmed: boolean
    csrfToken: string
    idempotencyKey: string
    reason: string
    workspaceId: string
  }): Promise<MutationResult> {
    const workspaceId = assertSafeIdentifier(input.workspaceId)
    const method = input.action === 'assign' ? 'PUT' : 'DELETE'
    try {
      return parseMutationResult(await this.#request(
        `/api/v1/admin/workspaces/${encodeURIComponent(workspaceId)}/internal-plan`,
        {
          method,
          headers: {
            'Content-Type': 'application/json',
            'X-CSRF-Token': input.csrfToken,
            'Idempotency-Key': input.idempotencyKey,
          },
          body: {
            confirmed: input.confirmed,
            reason: input.reason,
          },
        },
      ))
    } catch (error) {
      if (error instanceof AdminApiError) {
        throw error
      }
      throw new AdminApiError('ADMIN_UNAVAILABLE', undefined, error)
    }
  }
}
