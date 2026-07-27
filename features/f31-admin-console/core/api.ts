import {
  assertSafeIdentifier,
  parseAdminSession,
  parseAuditEvent,
  parseAuditList,
  parseDashboard,
  parseMutationResult,
  parsePlanList,
  parseSearchResults,
  parseUserDirectoryDetail,
  parseUserDirectoryPage,
  parseWorkspaceDirectoryPage,
  type AdminDashboard,
  type AdminSession,
  type AuditEvent,
  type AuditList,
  type ExportFormat,
  type MutationResult,
  type PlanList,
  type SearchResults,
  type UserDirectoryItem,
  type UserDirectoryPage,
  type UserDirectoryParams,
  type WorkspaceDirectoryPage,
  type WorkspaceDirectoryParams,
} from './contracts.ts'
import { directorySearchParams } from './directory-query.ts'

export interface AdminPlanQuery {
  q?: string
  plan?: string
  status?: string
  type?: string
  from?: string
  to?: string
  sort?: string
  direction?: 'asc' | 'desc'
  page?: number
  page_size?: number
}

export interface AdminAuditQuery {
  action?: string
  actor?: string
  subject?: string
  outcome?: string
  from?: string
  to?: string
  sort?: string
  direction?: 'asc' | 'desc'
  page?: number
  page_size?: number
}

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

export interface AdminDownload {
  body: Blob
  filename: string
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

function queryString(
  input: Readonly<Record<string, string | number | undefined>>,
): string {
  const parameters = new URLSearchParams()
  for (const [key, value] of Object.entries(input)) {
    if (value !== undefined && value !== '') {
      parameters.set(key, String(value))
    }
  }
  return parameters.toString()
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

  async dashboard(options?: Readonly<{
    headers?: Readonly<Record<string, string>>
    signal?: AbortSignal
  }>): Promise<AdminDashboard> {
    try {
      return parseDashboard(await this.#request('/api/v1/admin/dashboard', {
        headers: options?.headers,
        signal: options?.signal,
      }))
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

  async plans(query: AdminPlanQuery): Promise<PlanList> {
    try {
      return parsePlanList(await this.#request(
        `/api/v1/admin/plans?${queryString({ ...query })}`,
      ))
    } catch (error) {
      if (error instanceof AdminApiError) {
        throw error
      }
      throw new AdminApiError('ADMIN_UNAVAILABLE', undefined, error)
    }
  }

  async users(parameters: UserDirectoryParams): Promise<UserDirectoryPage> {
    const query = directorySearchParams(parameters)
    try {
      return parseUserDirectoryPage(
        await this.#request(`/api/v1/admin/users?${query.toString()}`),
      )
    } catch (error) {
      if (error instanceof AdminApiError) {
        throw error
      }
      throw new AdminApiError('ADMIN_UNAVAILABLE', undefined, error)
    }
  }

  async user(accountId: string): Promise<UserDirectoryItem> {
    const id = assertSafeIdentifier(accountId)
    try {
      return parseUserDirectoryDetail(await this.#request(
        `/api/v1/admin/users/${encodeURIComponent(id)}`,
      ))
    } catch (error) {
      if (error instanceof AdminApiError) {
        throw error
      }
      throw new AdminApiError('ADMIN_UNAVAILABLE', undefined, error)
    }
  }

  async audit(query: AdminAuditQuery): Promise<AuditList> {
    try {
      return parseAuditList(await this.#request(
        `/api/v1/admin/audit?${queryString({ ...query })}`,
      ))
    } catch (error) {
      if (error instanceof AdminApiError) {
        throw error
      }
      throw new AdminApiError('ADMIN_UNAVAILABLE', undefined, error)
    }
  }

  async workspaces(
    parameters: WorkspaceDirectoryParams,
  ): Promise<WorkspaceDirectoryPage> {
    const query = directorySearchParams(parameters)
    try {
      return parseWorkspaceDirectoryPage(
        await this.#request(`/api/v1/admin/workspaces?${query.toString()}`),
      )
    } catch (error) {
      if (error instanceof AdminApiError) {
        throw error
      }
      throw new AdminApiError('ADMIN_UNAVAILABLE', undefined, error)
    }
  }

  async auditEvent(eventId: string): Promise<AuditEvent> {
    const safeEventId = assertSafeIdentifier(eventId)
    try {
      return parseAuditEvent(await this.#request(
        `/api/v1/admin/audit/${encodeURIComponent(safeEventId)}`,
      ))
    } catch (error) {
      if (error instanceof AdminApiError) {
        throw error
      }
      throw new AdminApiError('ADMIN_UNAVAILABLE', undefined, error)
    }
  }

  async exportUsers(
    parameters: UserDirectoryParams,
    format: ExportFormat,
  ): Promise<AdminDownload> {
    return this.#exportDirectory('users', parameters, format)
  }

  async exportWorkspaces(
    parameters: WorkspaceDirectoryParams,
    format: ExportFormat,
  ): Promise<AdminDownload> {
    return this.#exportDirectory('workspaces', parameters, format)
  }

  async #exportDirectory(
    subject: 'users' | 'workspaces',
    parameters: UserDirectoryParams | WorkspaceDirectoryParams,
    format: ExportFormat,
  ): Promise<AdminDownload> {
    const query = directorySearchParams(parameters, false)
    query.set('format', format)
    try {
      const body = await this.#request(
        `/api/v1/admin/${subject}/export?${query.toString()}`,
        { responseType: 'blob' },
      )
      if (!(body instanceof Blob)) {
        throw new Error('ADMIN_INVALID_EXPORT')
      }
      return {
        body,
        filename: `postqron-admin-${subject}.${format}`,
      }
    } catch (error) {
      if (error instanceof AdminApiError) {
        throw error
      }
      throw new AdminApiError('ADMIN_UNAVAILABLE', undefined, error)
    }
  }

  plansExportURL(
    query: AdminPlanQuery,
    format: 'csv' | 'xlsx',
  ): string {
    return `${this.#baseURL}/api/v1/admin/plans/export?${queryString({
      ...query,
      page: undefined,
      page_size: undefined,
      format,
    })}`
  }

  auditExportURL(
    query: AdminAuditQuery,
    format: 'csv' | 'xlsx',
  ): string {
    return `${this.#baseURL}/api/v1/admin/audit/export?${queryString({
      ...query,
      page: undefined,
      page_size: undefined,
      format,
    })}`
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
