import assert from 'node:assert/strict'
import test from 'node:test'
import type { AppFetch } from '../components/core/api.ts'
import {
  ComposerApi,
  normalizeEditorialApiError,
  SchedulingApi,
} from '../components/core/editorial-api.ts'
import type {
  DraftContent,
  ScheduledPost,
} from '../components/core/editorial-contracts.ts'

const content: DraftContent = {
  text: 'Launch day',
  link: '',
  media: [],
  thread: [],
  destinations: [{
    id: 'destination-youtube',
    channel_id: 'channel-youtube',
    channel_type: 'youtube_channel',
    capability_id: 'youtube:video',
    format: 'video',
    fields: {
      title: 'Launch day',
      visibility: 'public',
    },
  }],
}

function validation(valid = true) {
  return {
    capability_version: 'fixture-v1',
    valid,
    errors: [],
    destinations: [{
      destination_id: 'destination-youtube',
      channel_id: 'channel-youtube',
      channel_type: 'youtube_channel',
      capability_id: 'youtube:video',
      format: 'video',
      valid,
      errors: [],
    }],
  }
}

function draftView(revision = 1) {
  return {
    draft: {
      id: 'draft-1',
      workspace_id: 'workspace-1',
      created_by: 'account-1',
      content,
      revision,
      created_at: '2026-07-30T10:00:00.000Z',
      updated_at: '2026-07-30T10:00:00.000Z',
    },
    validation: validation(),
  }
}

function scheduledPost(
  status: ScheduledPost['status'] = 'scheduled',
  revision = 1,
): ScheduledPost {
  return {
    id: 'post-1',
    workspace_id: 'workspace-1',
    draft_id: 'draft-1',
    channel_ids: ['channel-youtube'],
    status,
    scheduled_for_utc: '2026-07-31T14:00:00.000Z',
    scheduled_local: '2026-07-31T10:00:00',
    time_zone: 'America/Santo_Domingo',
    utc_offset_minutes: -240,
    revision,
    created_at: '2026-07-30T10:00:00.000Z',
    updated_at: '2026-07-30T10:00:00.000Z',
    ...(status === 'cancelled'
      ? { cancelled_at: '2026-07-30T11:00:00.000Z' }
      : {}),
  }
}

test('fixture flow saves, validates, schedules, filters, reschedules, duplicates, and cancels through F6/F7', async () => {
  const calls: Array<{
    options?: Readonly<Record<string, unknown>>
    path: string
  }> = []
  let revision = 1
  const fetch: AppFetch = async (path, options) => {
    calls.push({ path, options })
    if (path.endsWith('/composer/capabilities')) {
      return {
        version: 'fixture-v1',
        status: 'ready',
        capabilities: [{
          id: 'youtube:video',
          provider: 'youtube',
          channel_type: 'youtube_channel',
          format: 'video',
          available: true,
          text: { allowed: true, required: false, max_characters: 5000 },
          link: { allowed: false, required: false },
          media: { allowed: true, minimum_items: 1 },
          thread: { allowed: false, required: false },
          fields: [
            {
              name: 'visibility',
              required: true,
              allowed_values: ['public', 'private'],
            },
            { name: 'title', required: true, max_length: 100 },
          ],
        }],
      }
    }
    if (path.endsWith('/drafts') && options?.method === 'POST') {
      return draftView()
    }
    if (path.endsWith('/drafts/draft-1') && options?.method === 'PATCH') {
      revision += 1
      return draftView(revision)
    }
    if (path.endsWith('/drafts/draft-1/validate')) {
      return { validation: validation() }
    }
    if (path.endsWith('/scheduled-posts') && options?.method === 'POST') {
      return scheduledPost()
    }
    if (path.includes('/calendar?')) {
      const post = scheduledPost()
      return {
        entries: [{
          post_id: post.id,
          draft_id: post.draft_id,
          channel_ids: post.channel_ids,
          status: post.status,
          scheduled_for_utc: post.scheduled_for_utc,
          scheduled_local: post.scheduled_local,
          time_zone: post.time_zone,
          utc_offset_minutes: post.utc_offset_minutes,
          revision: post.revision,
        }],
      }
    }
    if (path.endsWith('/reschedule')) {
      return scheduledPost('scheduled', 2)
    }
    if (path.endsWith('/duplicate')) {
      return { ...scheduledPost(), id: 'post-2', duplicated_from_post_id: 'post-1' }
    }
    if (path.endsWith('/cancel')) {
      return scheduledPost('cancelled', 2)
    }
    throw new Error(`Unexpected fixture request: ${path}`)
  }
  const composer = new ComposerApi('https://api.postqron.test/', fetch)
  const scheduling = new SchedulingApi('https://api.postqron.test/', fetch)

  const capabilities = await composer.capabilities('workspace-1')
  const draft = await composer.createDraft('workspace-1', content)
  const autosaved = await composer.saveDraft('workspace-1', draft.draft.id, {
    autosaveKey: 'autosave-fixture-1',
    content,
    expectedRevision: draft.draft.revision,
  })
  const report = await composer.validateDraft('workspace-1', draft.draft.id)
  const post = await scheduling.schedule('workspace-1', {
    channelIds: ['channel-youtube'],
    draftId: draft.draft.id,
    scheduledAt: {
      local_date_time: '2026-07-31T10:00',
      time_zone: 'America/Santo_Domingo',
    },
  })
  const calendar = await scheduling.calendar('workspace-1', {
    channelId: 'channel-youtube',
    from: '2026-07-01T00:00:00.000Z',
    status: 'scheduled',
    until: '2026-08-01T00:00:00.000Z',
  })
  const rescheduled = await scheduling.reschedule('workspace-1', post.id, {
    expectedRevision: post.revision,
    scheduledAt: {
      local_date_time: '2026-07-31T11:00',
      time_zone: 'America/Santo_Domingo',
    },
  })
  const duplicate = await scheduling.duplicate('workspace-1', post.id, {
    expectedRevision: post.revision,
  })
  const cancelled = await scheduling.cancel(
    'workspace-1',
    post.id,
    post.revision,
  )

  assert.equal(capabilities.capabilities[0]?.fields?.length, 2)
  assert.equal(autosaved.draft.revision, 2)
  assert.equal(report.valid, true)
  assert.equal(calendar[0]?.channel_ids[0], 'channel-youtube')
  assert.equal(rescheduled.revision, 2)
  assert.equal(duplicate.duplicated_from_post_id, 'post-1')
  assert.equal(cancelled.status, 'cancelled')
  assert.match(
    calls.find(call => call.path.includes('/calendar?'))?.path ?? '',
    /channel_id=channel-youtube&status=scheduled/u,
  )
  assert.deepEqual(calls.find(call =>
    call.path.endsWith('/drafts/draft-1')
    && call.options?.method === 'PATCH',
  )?.options?.body, {
    expected_revision: 1,
    autosave_key: 'autosave-fixture-1',
    content,
  })
})

test('dependency and offline errors remain distinct and retryable', () => {
  const dependency = normalizeEditorialApiError({
    status: 503,
    data: {
      error: {
        code: 'scheduling_dependency_unavailable',
        message: 'dependency unavailable',
        retryable: true,
      },
    },
  })
  assert.equal(dependency.kind, 'dependency')
  assert.equal(dependency.retryable, true)

  const offline = normalizeEditorialApiError({ status: 0 })
  assert.equal(offline.kind, 'offline')
  assert.equal(offline.retryable, true)
})
