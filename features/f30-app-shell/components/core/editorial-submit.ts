import type {
  ScheduleInput,
  ScheduledPost,
} from './editorial-contracts.ts'

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
    hour12: false,
    minute: '2-digit',
    month: '2-digit',
    timeZone,
    year: 'numeric',
  }).formatToParts(target)
  const value = Object.fromEntries(parts.map(part => [part.type, part.value]))
  return {
    local_date_time: `${value.year}-${value.month}-${value.day}T${value.hour}:${value.minute}`,
    time_zone: timeZone,
  }
}

export async function submitScheduledDraft(
  client: SchedulingSubmitClient,
  input: {
    channelIds: string[]
    draftId: string
    existingPost?: Pick<ScheduledPost, 'id' | 'revision'>
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
  return client.schedule(input.workspaceId, {
    channelIds: input.channelIds,
    draftId: input.draftId,
    scheduledAt: input.scheduledAt,
  })
}
