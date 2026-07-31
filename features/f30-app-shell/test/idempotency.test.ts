import assert from 'node:assert/strict'
import test from 'node:test'
import type { AppFetch } from '../components/core/api.ts'
import {
  EditorialApiError,
  SchedulingApi,
} from '../components/core/editorial-api.ts'
import {
  createBrowserSafeIdempotencyKey,
  mutationIntent,
} from '../components/core/idempotency.ts'

const scheduledPost = {
  id: 'post-1',
  workspace_id: 'workspace-1',
  draft_id: 'draft-1',
  channel_ids: ['channel-1'],
  status: 'scheduled',
  scheduled_for_utc: '2026-08-01T14:00:00.000Z',
  scheduled_local: '2026-08-01T10:00',
  time_zone: 'America/Santo_Domingo',
  utc_offset_minutes: -240,
  revision: 1,
  created_at: '2026-07-31T12:00:00.000Z',
  updated_at: '2026-07-31T12:00:00.000Z',
}

function idempotentF7Fixture() {
  const operations = new Map<string, { fingerprint: string, response: unknown }>()
  const headers: string[] = []
  const fetch: AppFetch = async (path, options) => {
    const requestHeaders = options?.headers as Record<string, string> | undefined
    const header = requestHeaders?.['Idempotency-Key'] ?? ''
    headers.push(header)
    if (!header) {
      throw {
        status: 400,
        data: { error: { code: 'idempotency_key_invalid', retryable: false } },
      }
    }
    const operation = path.endsWith('/duplicate') ? 'duplicate' : 'schedule'
    const scopedKey = `${operation}:${header}`
    const fingerprint = JSON.stringify(options?.body)
    const existing = operations.get(scopedKey)
    if (existing && existing.fingerprint !== fingerprint) {
      throw {
        status: 409,
        data: { error: { code: 'idempotency_payload_mismatch', retryable: false } },
      }
    }
    if (existing) {
      return existing.response
    }
    const response = operation === 'duplicate'
      ? { ...scheduledPost, id: 'post-2', duplicated_from_post_id: 'post-1' }
      : scheduledPost
    operations.set(scopedKey, { fingerprint, response })
    return response
  }
  return { fetch, headers, operations }
}

test('browser-safe keys are visible ASCII and mutation intents reuse only matching payloads', () => {
  const key = createBrowserSafeIdempotencyKey()
  assert.match(key, /^[!-~]{1,200}$/u)

  const first = mutationIntent(undefined, { draft: 'draft-1', channels: ['one'] })
  const retry = mutationIntent(first, { channels: ['one'], draft: 'draft-1' })
  const changed = mutationIntent(retry, { channels: ['two'], draft: 'draft-1' })
  assert.equal(retry.key, first.key)
  assert.notEqual(changed.key, first.key)
})

test('F7 schedule fails closed without a key, replays a retry, and rejects key mismatch', async () => {
  const fixture = idempotentF7Fixture()
  const api = new SchedulingApi('https://api.postqron.test', fixture.fetch)
  const input = {
    channelIds: ['channel-1'],
    draftId: 'draft-1',
    idempotencyKey: 'schedule-user-intent-1',
    scheduledAt: {
      local_date_time: '2026-08-01T10:00',
      time_zone: 'America/Santo_Domingo',
      utc_offset_minutes: -240,
    },
  }

  await assert.rejects(
    () => api.schedule('workspace-1', { ...input, idempotencyKey: '' }),
    (error: unknown) => error instanceof EditorialApiError
      && error.code === 'idempotency_key_invalid',
  )
  const first = await api.schedule('workspace-1', input)
  const replay = await api.schedule('workspace-1', input)
  assert.deepEqual(replay, first)
  await assert.rejects(
    () => api.schedule('workspace-1', {
      ...input,
      scheduledAt: { ...input.scheduledAt, local_date_time: '2026-08-01T11:00' },
    }),
    (error: unknown) => error instanceof EditorialApiError
      && error.code === 'idempotency_payload_mismatch'
      && error.retryable === false,
  )
  assert.deepEqual(fixture.headers, [
    'schedule-user-intent-1',
    'schedule-user-intent-1',
    'schedule-user-intent-1',
  ])
})

test('F7 duplicate sends its key, replays exactly, and rejects changed payload', async () => {
  const fixture = idempotentF7Fixture()
  const api = new SchedulingApi('https://api.postqron.test', fixture.fetch)
  const input = { expectedRevision: 1, idempotencyKey: 'duplicate-user-intent-1' }

  const first = await api.duplicate('workspace-1', 'post-1', input)
  const replay = await api.duplicate('workspace-1', 'post-1', input)
  assert.deepEqual(replay, first)
  await assert.rejects(
    () => api.duplicate('workspace-1', 'post-1', {
      ...input,
      scheduledAt: {
        local_date_time: '2026-08-02T10:00',
        time_zone: 'America/Santo_Domingo',
        utc_offset_minutes: -240,
      },
    }),
    (error: unknown) => error instanceof EditorialApiError
      && error.code === 'idempotency_payload_mismatch',
  )
  assert.equal(fixture.operations.size, 1)
  assert.deepEqual(fixture.headers, Array(3).fill('duplicate-user-intent-1'))
})
