import type { AppFetch } from './api.ts'
import {
  parseCalendar,
  parseCapabilityCatalog,
  parseComposerMedia,
  parseDraftView,
  parseDraftViews,
  parseMediaUpload,
  parseScheduledPost,
  parseValidationResponse,
  type CalendarEntry,
  type CapabilityCatalog,
  type ComposerMedia,
  type DraftContent,
  type DraftView,
  type MediaUpload,
  type ScheduleInput,
  type ScheduledPost,
  type SchedulingPostStatus,
  type ValidationReport,
} from './editorial-contracts.ts'

export type EditorialApiErrorKind =
  | 'access-denied'
  | 'conflict'
  | 'dependency'
  | 'invalid'
  | 'not-found'
  | 'offline'
  | 'session'
  | 'unavailable'

export class EditorialApiError extends Error {
  readonly code: string
  readonly kind: EditorialApiErrorKind
  readonly retryable: boolean
  readonly status?: number
  readonly field?: string
  readonly rule?: string

  constructor(options: {
    cause?: unknown
    code: string
    field?: string
    kind: EditorialApiErrorKind
    message: string
    retryable: boolean
    rule?: string
    status?: number
  }) {
    super(options.message, { cause: options.cause })
    this.name = 'EditorialApiError'
    this.code = options.code
    this.kind = options.kind
    this.retryable = options.retryable
    this.status = options.status
    this.field = options.field
    this.rule = options.rule
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
  return [candidate.statusCode, candidate.status, candidate.response?.status]
    .find(value => typeof value === 'number') as number | undefined
}

function remoteError(error: unknown): {
  code?: string
  field?: string
  message?: string
  retryable?: boolean
  rule?: string
} {
  if (!error || typeof error !== 'object') {
    return {}
  }
  const data = (error as { data?: unknown }).data
  if (!data || typeof data !== 'object') {
    return {}
  }
  const nested = (data as Record<string, unknown>).error
  if (typeof nested === 'string') {
    return { code: nested, message: nested, retryable: false }
  }
  if (!nested || typeof nested !== 'object') {
    return {}
  }
  const value = nested as Record<string, unknown>
  return {
    code: typeof value.code === 'string' ? value.code : undefined,
    field: typeof value.field === 'string' ? value.field : undefined,
    message: typeof value.message === 'string' ? value.message : undefined,
    retryable: typeof value.retryable === 'boolean' ? value.retryable : undefined,
    rule: typeof value.rule === 'string' ? value.rule : undefined,
  }
}

export function normalizeEditorialApiError(error: unknown): EditorialApiError {
  if (error instanceof EditorialApiError) {
    return error
  }
  const status = statusOf(error)
  const remote = remoteError(error)
  const kind: EditorialApiErrorKind =
    remote.code === 'scheduling_dependency_unavailable'
    || remote.code === 'media_storage_unavailable'
      ? 'dependency'
      : status === 401
        ? 'session'
        : status === 403
          ? 'access-denied'
          : status === 404
            ? 'not-found'
            : status === 409
              ? 'conflict'
              : status === 400 || status === 422
                ? 'invalid'
                : status === 0 || status === undefined
                  ? 'offline'
                  : 'unavailable'
  return new EditorialApiError({
    cause: error,
    code: remote.code ?? 'editorial_unknown',
    field: remote.field,
    kind,
    message: remote.message ?? 'The editorial request failed',
    retryable: remote.retryable ?? ['offline', 'dependency', 'unavailable'].includes(kind),
    rule: remote.rule,
    status,
  })
}

function encodeIdentifier(value: string, name: string): string {
  if (value.trim() === '') {
    throw new EditorialApiError({
      code: `editorial_invalid_${name}`,
      kind: 'invalid',
      message: `A ${name} identifier is required`,
      retryable: false,
    })
  }
  return encodeURIComponent(value.trim())
}

class EditorialApi {
  readonly #baseURL: string
  readonly #fetch: AppFetch

  constructor(baseURL: string, fetch: AppFetch) {
    this.#baseURL = baseURL.replace(/\/+$/u, '')
    this.#fetch = fetch
  }

  async request(
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
      throw normalizeEditorialApiError(error)
    }
  }

  parse<T>(value: unknown, parser: (value: unknown) => T): T {
    try {
      return parser(value)
    } catch (error) {
      throw new EditorialApiError({
        cause: error,
        code: 'editorial_invalid_payload',
        kind: 'unavailable',
        message: 'The editorial response is invalid',
        retryable: true,
      })
    }
  }
}

export class ComposerApi extends EditorialApi {
  async capabilities(workspaceId: string): Promise<CapabilityCatalog> {
    const workspace = encodeIdentifier(workspaceId, 'workspace')
    return this.parse(
      await this.request(`/api/v1/workspaces/${workspace}/composer/capabilities`),
      parseCapabilityCatalog,
    )
  }

  async listDrafts(workspaceId: string): Promise<DraftView[]> {
    const workspace = encodeIdentifier(workspaceId, 'workspace')
    return this.parse(
      await this.request(`/api/v1/workspaces/${workspace}/drafts`),
      parseDraftViews,
    )
  }

  async getDraft(workspaceId: string, draftId: string): Promise<DraftView> {
    const workspace = encodeIdentifier(workspaceId, 'workspace')
    const draft = encodeIdentifier(draftId, 'draft')
    return this.parse(
      await this.request(`/api/v1/workspaces/${workspace}/drafts/${draft}`),
      parseDraftView,
    )
  }

  async createDraft(
    workspaceId: string,
    content: DraftContent,
  ): Promise<DraftView> {
    const workspace = encodeIdentifier(workspaceId, 'workspace')
    return this.parse(
      await this.request(`/api/v1/workspaces/${workspace}/drafts`, {
        method: 'POST',
        body: { content },
      }),
      parseDraftView,
    )
  }

  async saveDraft(
    workspaceId: string,
    draftId: string,
    input: {
      autosaveKey?: string
      content: DraftContent
      expectedRevision: number
    },
  ): Promise<DraftView> {
    const workspace = encodeIdentifier(workspaceId, 'workspace')
    const draft = encodeIdentifier(draftId, 'draft')
    return this.parse(
      await this.request(`/api/v1/workspaces/${workspace}/drafts/${draft}`, {
        method: input.autosaveKey ? 'PATCH' : 'PUT',
        body: {
          expected_revision: input.expectedRevision,
          ...(input.autosaveKey ? { autosave_key: input.autosaveKey } : {}),
          content: input.content,
        },
      }),
      parseDraftView,
    )
  }

  async validateDraft(
    workspaceId: string,
    draftId: string,
  ): Promise<ValidationReport> {
    const workspace = encodeIdentifier(workspaceId, 'workspace')
    const draft = encodeIdentifier(draftId, 'draft')
    return this.parse(
      await this.request(
        `/api/v1/workspaces/${workspace}/drafts/${draft}/validate`,
        { method: 'POST' },
      ),
      parseValidationResponse,
    )
  }

  async authorizeMedia(
    workspaceId: string,
    file: { name: string, size: number, type: string },
  ): Promise<MediaUpload> {
    const workspace = encodeIdentifier(workspaceId, 'workspace')
    return this.parse(
      await this.request(`/api/v1/workspaces/${workspace}/composer/media`, {
        method: 'POST',
        body: {
          file_name: file.name,
          content_type: file.type,
          size_bytes: file.size,
        },
      }),
      parseMediaUpload,
    )
  }

  async completeMedia(
    workspaceId: string,
    mediaId: string,
  ): Promise<ComposerMedia> {
    const workspace = encodeIdentifier(workspaceId, 'workspace')
    const media = encodeIdentifier(mediaId, 'media')
    return this.parse(
      await this.request(
        `/api/v1/workspaces/${workspace}/composer/media/${media}/complete`,
        { method: 'POST' },
      ),
      parseComposerMedia,
    )
  }

  async deleteMedia(workspaceId: string, mediaId: string): Promise<void> {
    const workspace = encodeIdentifier(workspaceId, 'workspace')
    const media = encodeIdentifier(mediaId, 'media')
    await this.request(
      `/api/v1/workspaces/${workspace}/composer/media/${media}`,
      { method: 'DELETE' },
    )
  }
}

export class SchedulingApi extends EditorialApi {
  async calendar(
    workspaceId: string,
    input: {
      channelId?: string
      from: string
      status?: SchedulingPostStatus
      until: string
    },
  ): Promise<CalendarEntry[]> {
    const workspace = encodeIdentifier(workspaceId, 'workspace')
    const query = new URLSearchParams({ from: input.from, until: input.until })
    if (input.channelId) {
      query.set('channel_id', input.channelId)
    }
    if (input.status) {
      query.set('status', input.status)
    }
    return this.parse(
      await this.request(`/api/v1/workspaces/${workspace}/calendar?${query}`),
      parseCalendar,
    )
  }

  async schedule(
    workspaceId: string,
    input: {
      channelIds: string[]
      draftId: string
      idempotencyKey: string
      scheduledAt: ScheduleInput
    },
  ): Promise<ScheduledPost> {
    const workspace = encodeIdentifier(workspaceId, 'workspace')
    return this.parse(
      await this.request(`/api/v1/workspaces/${workspace}/scheduled-posts`, {
        method: 'POST',
        headers: { 'Idempotency-Key': requireIdempotencyKey(input.idempotencyKey) },
        body: {
          draft_id: input.draftId,
          channel_ids: input.channelIds,
          scheduled_at: input.scheduledAt,
        },
      }),
      parseScheduledPost,
    )
  }

  async get(workspaceId: string, postId: string): Promise<ScheduledPost> {
    const workspace = encodeIdentifier(workspaceId, 'workspace')
    const post = encodeIdentifier(postId, 'post')
    return this.parse(
      await this.request(`/api/v1/workspaces/${workspace}/scheduled-posts/${post}`),
      parseScheduledPost,
    )
  }

  async edit(
    workspaceId: string,
    postId: string,
    input: {
      channelIds: string[]
      draftId: string
      expectedRevision: number
    },
  ): Promise<ScheduledPost> {
    return this.postMutation(workspaceId, postId, '', 'PUT', {
      expected_revision: input.expectedRevision,
      draft_id: input.draftId,
      channel_ids: input.channelIds,
    })
  }

  async reschedule(
    workspaceId: string,
    postId: string,
    input: { expectedRevision: number, scheduledAt: ScheduleInput },
  ): Promise<ScheduledPost> {
    return this.postMutation(workspaceId, postId, '/reschedule', 'POST', {
      expected_revision: input.expectedRevision,
      scheduled_at: input.scheduledAt,
    })
  }

  async duplicate(
    workspaceId: string,
    postId: string,
    input: {
      expectedRevision: number
      idempotencyKey: string
      scheduledAt?: ScheduleInput
    },
  ): Promise<ScheduledPost> {
    return this.postMutation(workspaceId, postId, '/duplicate', 'POST', {
      expected_revision: input.expectedRevision,
      ...(input.scheduledAt ? { scheduled_at: input.scheduledAt } : {}),
    }, input.idempotencyKey)
  }

  async cancel(
    workspaceId: string,
    postId: string,
    expectedRevision: number,
  ): Promise<ScheduledPost> {
    return this.postMutation(workspaceId, postId, '/cancel', 'POST', {
      expected_revision: expectedRevision,
    })
  }

  private async postMutation(
    workspaceId: string,
    postId: string,
    suffix: string,
    method: 'POST' | 'PUT',
    body: Readonly<Record<string, unknown>>,
    idempotencyKey?: string,
  ): Promise<ScheduledPost> {
    const workspace = encodeIdentifier(workspaceId, 'workspace')
    const post = encodeIdentifier(postId, 'post')
    return this.parse(
      await this.request(
        `/api/v1/workspaces/${workspace}/scheduled-posts/${post}${suffix}`,
        {
          method,
          body,
          ...(idempotencyKey
            ? { headers: { 'Idempotency-Key': requireIdempotencyKey(idempotencyKey) } }
            : {}),
        },
      ),
      parseScheduledPost,
    )
  }
}

function requireIdempotencyKey(value: string): string {
  if (typeof value !== 'string'
    || value.length < 1
    || value.length > 200
    || !/^[!-~]+$/u.test(value)) {
    throw new EditorialApiError({
      code: 'idempotency_key_invalid',
      field: 'Idempotency-Key',
      kind: 'invalid',
      message: 'A browser-safe Idempotency-Key is required',
      retryable: false,
    })
  }
  return value
}
