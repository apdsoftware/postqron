import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import test from 'node:test'
import type {
  ComposerDestination,
  ContentCapability,
  ScheduledPost,
} from '../components/core/editorial-contracts.ts'
import {
  applyDestinationCapability,
  setDestinationField,
} from '../components/core/editorial-form.ts'
import {
  immediateScheduleInput,
  submitScheduledDraft,
  wallClockScheduleInput,
  type SchedulingSubmitClient,
} from '../components/core/editorial-submit.ts'
import {
  safeTimeZone,
  supportedTimeZones,
} from '../components/core/timezones.ts'

function source(path: string): Promise<string> {
  return readFile(new URL(path, import.meta.url), 'utf8')
}

function post(revision = 4): ScheduledPost {
  return {
    id: 'post-1',
    workspace_id: 'workspace-1',
    draft_id: 'draft-1',
    channel_ids: ['channel-1'],
    status: 'scheduled',
    scheduled_for_utc: '2026-07-30T18:00:00.000Z',
    scheduled_local: '2026-07-30T20:00:00',
    time_zone: 'Europe/Rome',
    utc_offset_minutes: 120,
    revision,
    created_at: '2026-07-30T10:00:00.000Z',
    updated_at: '2026-07-30T10:00:00.000Z',
  }
}

test('publish now on an existing post reschedules that post and never creates a duplicate', async () => {
  const calls: string[] = []
  const client: SchedulingSubmitClient = {
    async reschedule(_workspaceId, postId, input) {
      calls.push(`reschedule:${postId}:${input.expectedRevision}`)
      return post(input.expectedRevision + 1)
    },
    async schedule() {
      calls.push('schedule')
      throw new Error('must not create a second post')
    },
  }

  const result = await submitScheduledDraft(client, {
    channelIds: ['channel-1'],
    draftId: 'draft-1',
    existingPost: post(),
    scheduledAt: {
      local_date_time: '2026-07-30T20:02',
      time_zone: 'Europe/Rome',
    },
    workspaceId: 'workspace-1',
  })

  assert.equal(result.id, 'post-1')
  assert.deepEqual(calls, ['reschedule:post-1:4'])
})

test('publish now schedules exactly two minutes after the current instant', () => {
  assert.deepEqual(
    immediateScheduleInput(
      new Date('2026-07-30T18:00:00.000Z'),
      'Europe/Rome',
    ),
    {
      local_date_time: '2026-07-30T20:02',
      time_zone: 'Europe/Rome',
      utc_offset_minutes: 120,
    },
  )
})

test('publish now keeps the exact occurrence during the repeated DST hour', () => {
  assert.deepEqual(immediateScheduleInput(
    new Date('2026-10-25T00:28:00.000Z'),
    'Europe/Rome',
  ), {
    local_date_time: '2026-10-25T02:30',
    time_zone: 'Europe/Rome',
    utc_offset_minutes: 120,
  })
  assert.deepEqual(immediateScheduleInput(
    new Date('2026-10-25T01:28:00.000Z'),
    'Europe/Rome',
  ), {
    local_date_time: '2026-10-25T02:30',
    time_zone: 'Europe/Rome',
    utc_offset_minutes: 60,
  })
})

test('publish now formats midnight as hour zero', () => {
  assert.deepEqual(immediateScheduleInput(
    new Date('2026-07-30T23:58:00.000Z'),
    'UTC',
  ), {
    local_date_time: '2026-07-31T00:00',
    time_zone: 'UTC',
    utc_offset_minutes: 0,
  })
})

test('wall-clock scheduling recalculates seasonal and timezone offsets', () => {
  assert.deepEqual(wallClockScheduleInput(
    '2027-07-15T10:00',
    'Europe/Rome',
  ), {
    local_date_time: '2027-07-15T10:00',
    time_zone: 'Europe/Rome',
    utc_offset_minutes: 120,
  })
  assert.deepEqual(wallClockScheduleInput(
    '2027-07-15T10:00',
    'America/New_York',
  ), {
    local_date_time: '2027-07-15T10:00',
    time_zone: 'America/New_York',
    utc_offset_minutes: -240,
  })
  assert.equal(wallClockScheduleInput(
    '2026-10-25T02:30',
    'Europe/Rome',
  ), undefined)
  assert.equal(wallClockScheduleInput(
    '2026-10-25T02:30',
    'Europe/Rome',
    60,
  )?.utc_offset_minutes, 60)
})

test('a new post still uses the F7 schedule operation', async () => {
  const calls: string[] = []
  const client: SchedulingSubmitClient = {
    async reschedule() {
      throw new Error('unexpected reschedule')
    },
    async schedule(_workspaceId, input) {
      calls.push(`schedule:${input.draftId}`)
      return post()
    },
  }
  await submitScheduledDraft(client, {
    channelIds: ['channel-1'],
    draftId: 'draft-1',
    idempotencyKey: 'schedule-intent-1',
    scheduledAt: {
      local_date_time: '2026-07-30T20:00',
      time_zone: 'Europe/Rome',
    },
    workspaceId: 'workspace-1',
  })
  assert.deepEqual(calls, ['schedule:draft-1'])
})

test('capability-driven provider fields stay attached to destination.fields', () => {
  const destination: ComposerDestination = {
    id: 'destination-1',
    channel_id: 'youtube-1',
    channel_type: 'youtube_channel',
    capability_id: 'old',
    format: 'text',
    fields: { visibility: 'private', stale: 'remove-me' },
  }
  const capability: ContentCapability = {
    id: 'youtube:video',
    provider: 'youtube',
    channel_type: 'youtube_channel',
    format: 'video',
    available: true,
    text: { allowed: true, required: false },
    link: { allowed: false, required: false },
    media: { allowed: true },
    thread: { allowed: false, required: false },
    fields: [
      { name: 'visibility', required: true, allowed_values: ['public', 'private'] },
      { name: 'title', required: true, max_length: 100 },
    ],
  }

  applyDestinationCapability(destination, capability)
  setDestinationField(destination, 'title', 'Launch')

  assert.equal(destination.capability_id, 'youtube:video')
  assert.equal(destination.format, 'video')
  assert.deepEqual(destination.fields, {
    visibility: 'private',
    title: 'Launch',
  })
})

test('timezone options use runtime values with safe required fallbacks', () => {
  const zones = supportedTimeZones('Europe/Rome')
  assert.ok(zones.includes('UTC'))
  assert.ok(zones.includes('Europe/Rome'))
  assert.equal(safeTimeZone('not/a-zone', 'Europe/Rome'), 'Europe/Rome')
})

test('composer renders capability fields and timezone selects, calendar localizes controls', async () => {
  const [composer, calendar] = await Promise.all([
    source('../pages/publish.vue'),
    source('../pages/calendar.vue'),
  ])
  assert.match(composer, /selectedCapability\(connection\)\?\.fields/u)
  assert.match(composer, /field\.allowed_values\?\.length/u)
  assert.match(composer, /:required="field\.required"/u)
  assert.match(composer, /:maxlength="field\.max_length"/u)
  assert.match(composer, /updateProviderField\(/u)
  assert.match(composer, /aggregateThreadConstraints/u)
  assert.match(composer, /localizedValidation(Field|Message)/u)
  assert.match(composer, /<fieldset[\s\S]*composer-thread/u)
  assert.match(composer, /role="group"/u)
  assert.match(composer, /v-model="scheduleTimezone"[\s\S]*v-for="zone in timezoneOptions"/u)
  assert.doesNotMatch(composer, /v-model="scheduleTimezone"\s+type="text"/u)
  assert.match(calendar, /:aria-label="t\('calendar\.controlsLabel'\)"/u)
  assert.match(calendar, /v-model="timezone"[\s\S]*v-for="zone in timezoneOptions"/u)
  assert.match(calendar, /v-model="rescheduleTimeZone"/u)
  assert.match(calendar, /canMutate\(entry\)/u)
  assert.match(calendar, /calendar\.readOnly/u)
  assert.doesNotMatch(calendar, /aria-label="Calendar controls"/u)
})

test('Mastodon and Bluesky use delivered discovery inputs and authoritative availability', async () => {
  const [page, catalogs] = await Promise.all([
    source('../pages/social-channels.vue'),
    source('../components/core/editorial-catalogs.ts'),
  ])
  assert.match(page, /provider\.capabilities\.dynamic_discovery/u)
  assert.doesNotMatch(page, /decentralized_blocked|adapter.*not integrated/iu)
  assert.doesNotMatch(page, /instance_url|app_password/iu)
  assert.doesNotMatch(catalogs, /adapter.*not integrated/iu)
})

test('social channels rely on the JSON callback handoff and authoritative workspace permissions', async () => {
  const page = await source('../pages/social-channels.vue')
  assert.match(page, /accountApi\.currentWorkspace\(\)/u)
  assert.match(page, /role === 'owner'\s*&& workspace\.value\?\.status === 'active'/u)
  assert.match(page, /parseSocialCallbackDocument/u)
  assert.match(page, /callbackPathnames\.has\(windowHandle\.location\.pathname\)/u)
  assert.match(page, /social\.callbackRelayURL\(globalThis\.location\.origin\)/u)
  assert.match(page, /data-postqron-social-callback-handoff/u)
  assert.match(page, /windowHandle\.opener = null/u)
  assert.match(page, /dynamic_discovery/u)
  assert.match(page, /history\.replaceState\([\s\S]*withoutSocialOAuthCallbackParameters\(route\.query\)/u)
  assert.match(page, /if \(!contextIsCurrent\(context, true\)\) \{[\s\S]*social\.completeAuthorization/u)
})
