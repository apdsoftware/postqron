// Browser client for the authenticated F5 social connections runtime.
//
// The F5 runtime (PR #287) returns a FLAT `{code, message, retryable}` error
// envelope — different from the account API `{error:{...}}` shape — and guards
// mutations with a `Sec-Fetch-Site` same-origin check rather than a CSRF token.
// Requests are credentialed (session cookie) and never carry token material.

import type { AppFetch } from './api.ts'
import {
  parseSocialAuthorization,
  parseSocialBootstrap,
  parseSocialConnection,
  parseSocialConnections,
  parseSocialRevocation,
  parseSocialSelection,
  type SocialAuthorization,
  type SocialBootstrap,
  type SocialConnection,
  type SocialProvider,
  type SocialRevocation,
  type SocialSelection,
} from './social-connections.ts'

// Stable failure kinds the UI maps to fail-closed, retry-aware copy. A code the
// runtime has not declared collapses to `unavailable`, never to success.
export type SocialApiErrorKind =
  | 'access-denied'
  | 'quota-exceeded'
  | 'quota-unavailable'
  | 'provider-unavailable'
  | 'already-connected'
  | 'flow-expired'
  | 'invalid-state'
  | 'provider-denied'
  | 'no-resources'
  | 'not-found'
  | 'session'
  | 'offline'
  | 'invalid'
  | 'unavailable'

export class SocialApiError extends Error {
  readonly code: string
  readonly kind: SocialApiErrorKind
  readonly retryable: boolean
  readonly status?: number

  constructor(options: {
    cause?: unknown
    code: string
    kind: SocialApiErrorKind
    message: string
    retryable: boolean
    status?: number
  }) {
    super(options.message, { cause: options.cause })
    this.name = 'SocialApiError'
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

// F5 errors are flat: `error.data === { code, message, retryable }`.
function flatRemoteError(error: unknown): {
  code?: string
  message?: string
  retryable?: boolean
} {
  if (!error || typeof error !== 'object') {
    return {}
  }
  const payload = (error as { data?: unknown }).data
  if (!payload || typeof payload !== 'object') {
    return {}
  }
  const record = payload as Record<string, unknown>
  return {
    code: typeof record.code === 'string' ? record.code : undefined,
    message: typeof record.message === 'string' ? record.message : undefined,
    retryable: typeof record.retryable === 'boolean' ? record.retryable : undefined,
  }
}

const kindByCode: Readonly<Record<string, SocialApiErrorKind>> = {
  forbidden: 'access-denied',
  origin_forbidden: 'access-denied',
  channel_quota_exceeded: 'quota-exceeded',
  channel_quota_unavailable: 'quota-unavailable',
  provider_unavailable: 'provider-unavailable',
  resource_already_connected: 'already-connected',
  flow_expired: 'flow-expired',
  invalid_oauth_state: 'invalid-state',
  provider_denied: 'provider-denied',
  no_publishable_resources: 'no-resources',
  resource_not_found: 'not-found',
  unauthenticated: 'session',
  invalid_request: 'invalid',
}

export function normalizeSocialApiError(error: unknown): SocialApiError {
  if (error instanceof SocialApiError) {
    return error
  }
  const status = statusOf(error)
  const remote = flatRemoteError(error)
  const code = remote.code ?? 'social_unknown'
  const kind: SocialApiErrorKind = kindByCode[code]
    ?? (status === 401
      ? 'session'
      : status === 403
        ? 'access-denied'
        : status === 0 || status === undefined
          ? 'offline'
          : 'unavailable')
  const retryable = remote.retryable
    ?? (kind === 'offline' || kind === 'quota-unavailable'
      || kind === 'provider-unavailable' || kind === 'flow-expired')
  return new SocialApiError({
    cause: error,
    status,
    kind,
    code,
    message: remote.message ?? 'The social connection request failed',
    retryable,
  })
}

function requireWorkspace(workspaceId: string): string {
  const trimmed = workspaceId.trim()
  if (trimmed === '') {
    throw new SocialApiError({
      code: 'social_invalid_workspace',
      kind: 'invalid',
      message: 'A workspace identifier is required',
      retryable: false,
    })
  }
  return encodeURIComponent(trimmed)
}

export class SocialConnectionsApi {
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
      throw normalizeSocialApiError(error)
    }
  }

  #parse<T>(value: unknown, parse: (value: unknown) => T): T {
    try {
      return parse(value)
    } catch (error) {
      throw new SocialApiError({
        cause: error,
        code: 'social_invalid_payload',
        kind: 'unavailable',
        message: 'The social connection response is invalid',
        retryable: true,
      })
    }
  }

  async bootstrap(workspaceId: string): Promise<SocialBootstrap> {
    const workspace = requireWorkspace(workspaceId)
    return this.#parse(
      await this.#request(
        `/api/v1/workspaces/${workspace}/social-connections/bootstrap`,
      ),
      parseSocialBootstrap,
    )
  }

  async list(workspaceId: string): Promise<SocialConnection[]> {
    const workspace = requireWorkspace(workspaceId)
    return this.#parse(
      await this.#request(`/api/v1/workspaces/${workspace}/social-connections`),
      parseSocialConnections,
    )
  }

  async begin(
    workspaceId: string,
    provider: SocialProvider,
  ): Promise<SocialAuthorization> {
    const workspace = requireWorkspace(workspaceId)
    return this.#parse(
      await this.#request(
        `/api/v1/workspaces/${workspace}/social-authorizations`,
        { method: 'POST', body: { provider } },
      ),
      parseSocialAuthorization,
    )
  }

  async completeAuthorization(parameters: {
    state: string
    code: string
    error: string
  }): Promise<SocialSelection> {
    const query = new URLSearchParams()
    for (const [key, value] of Object.entries(parameters)) {
      if (value) {
        query.set(key, value)
      }
    }
    return this.#parse(
      await this.#request(`/api/v1/social-authorizations/callback?${query}`),
      parseSocialSelection,
    )
  }

  async selectResource(
    workspaceId: string,
    input: { selectionId: string, remoteId: string },
  ): Promise<SocialConnection> {
    const workspace = requireWorkspace(workspaceId)
    return this.#parse(
      await this.#request(
        `/api/v1/workspaces/${workspace}/social-connections`,
        {
          method: 'POST',
          body: {
            selection_id: input.selectionId,
            remote_id: input.remoteId,
          },
        },
      ),
      parseSocialConnection,
    )
  }

  async reconnect(
    workspaceId: string,
    connectionId: string,
  ): Promise<SocialAuthorization> {
    const workspace = requireWorkspace(workspaceId)
    const connection = encodeURIComponent(connectionId.trim())
    return this.#parse(
      await this.#request(
        `/api/v1/workspaces/${workspace}/social-connections/${connection}/reconnect`,
        { method: 'POST' },
      ),
      parseSocialAuthorization,
    )
  }

  async revoke(
    workspaceId: string,
    connectionId: string,
  ): Promise<SocialRevocation> {
    const workspace = requireWorkspace(workspaceId)
    const connection = encodeURIComponent(connectionId.trim())
    return this.#parse(
      await this.#request(
        `/api/v1/workspaces/${workspace}/social-connections/${connection}`,
        { method: 'DELETE' },
      ),
      parseSocialRevocation,
    )
  }
}
