import type {
  ScheduleInput,
  ScheduledPost,
} from './editorial-contracts.ts'
import {
  resolveLocalDateTime,
  utcOffsetMinutesAtInstant,
} from './timezones.ts'

export interface SchedulingSubmitClient {
  reschedule(
    workspaceId: string,
    postId: string,
    input: { expectedRevision: number, scheduledAt: ScheduleInput },
  ): Promise<ScheduledPost>
  schedule(
    workspaceId: string,
    input: {
      channelIds: string[]
      draftId: string
      idempotencyKey: string
      scheduledAt: ScheduleInput
    },
  ): Promise<ScheduledPost>
}

export function immediateScheduleInput(
  now: Date,
  timeZone: string,
): ScheduleInput {
  const target = new Date(now.getTime() + 2 * 60 * 1000)
  const parts = new Intl.DateTimeFormat('en-CA', {
    day: '2-digit',
    hour: '2-digit',
    hourCycle: 'h23',
    minute: '2-digit',
    month: '2-digit',
    timeZone,
    year: 'numeric',
  }).formatToParts(target)
  const value = Object.fromEntries(parts.map(part => [part.type, part.value]))
  return {
    local_date_time: `${value.year}-${value.month}-${value.day}T${value.hour}:${value.minute}`,
    time_zone: timeZone,
    utc_offset_minutes: utcOffsetMinutesAtInstant(target, timeZone),
  }
}

export function wallClockScheduleInput(
  localDateTime: string,
  timeZone: string,
  selectedOffset?: number,
): ScheduleInput | undefined {
  const resolution = resolveLocalDateTime(localDateTime, timeZone)
  const offset = resolution.kind === 'unique'
    ? resolution.offsets[0]
    : resolution.kind === 'ambiguous'
      && selectedOffset !== undefined
      && resolution.offsets.includes(selectedOffset)
      ? selectedOffset
      : undefined
  return offset === undefined
    ? undefined
    : {
        local_date_time: localDateTime,
        time_zone: timeZone,
        utc_offset_minutes: offset,
      }
}

export async function submitScheduledDraft(
  client: SchedulingSubmitClient,
  input: {
    channelIds: string[]
    draftId: string
    existingPost?: Pick<ScheduledPost, 'id' | 'revision'>
    idempotencyKey?: string
    scheduledAt: ScheduleInput
    workspaceId: string
  },
): Promise<ScheduledPost> {
  if (input.existingPost) {
    return client.reschedule(
      input.workspaceId,
      input.existingPost.id,
      {
        expectedRevision: input.existingPost.revision,
        scheduledAt: input.scheduledAt,
      },
    )
  }
  if (!input.idempotencyKey) {
    throw new Error('F30_IDEMPOTENCY_INTENT_REQUIRED')
  }
  return client.schedule(input.workspaceId, {
    channelIds: input.channelIds,
    draftId: input.draftId,
    idempotencyKey: input.idempotencyKey,
    scheduledAt: input.scheduledAt,
  })
}
