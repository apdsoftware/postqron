<script setup lang="ts">
import {
  computed,
  definePageMeta,
  navigateTo,
  ref,
  useAsyncData,
  useHead,
  useRoute,
  watch,
} from '#imports'
import type { AppShellMessageKey } from '../components/core/catalogs.ts'
import {
  filterEntriesForVisibleMonth,
  initialVisibleMonth,
  localCalendarDayKey,
  paddedMonthRange,
} from '../components/core/calendar-range.ts'
import {
  SCHEDULING_STATUSES,
  type CalendarEntry,
  type DraftView,
  type ScheduleInput,
  type SchedulingPostStatus,
} from '../components/core/editorial-contracts.ts'
import { normalizeEditorialApiError } from '../components/core/editorial-api.ts'
import { wallClockScheduleInput } from '../components/core/editorial-submit.ts'
import {
  appRoute,
  localeFromAppPath,
} from '../components/core/navigation.ts'
import type { SocialConnection } from '../components/core/social-connections.ts'
import {
  detectedTimeZone,
  resolveLocalDateTime,
  supportedTimeZones,
} from '../components/core/timezones.ts'
import {
  useAppSessionState,
  useAppShellApi,
  useAppShellI18n,
  useComposerApi,
  useSchedulingApi,
  useSocialConnectionsApi,
} from '../components/core/use-app-shell.ts'

definePageMeta({ layout: 'app-shell' })

const route = useRoute()
const session = useAppSessionState()
const accountApi = useAppShellApi()
const socialApi = useSocialConnectionsApi()
const composerApi = useComposerApi()
const schedulingApi = useSchedulingApi()
const { t, locale: uiLocale } = useAppShellI18n()
const locale = computed(() => localeFromAppPath(route.fullPath))
const workspaceId = computed(() => session.value?.current_workspace?.id ?? '')

const entries = ref<CalendarEntry[]>()
const connections = ref<SocialConnection[]>([])
const drafts = ref<DraftView[]>([])
const pageState = ref<'access-denied' | 'offline' | 'unavailable'>()
const view = ref<'calendar' | 'list'>('calendar')
const channelFilter = ref('')
const statusFilter = ref<SchedulingPostStatus | ''>('')
const browserTimezone = detectedTimeZone()
const timezoneOptions = supportedTimeZones(browserTimezone)
const timezone = ref(browserTimezone)
const visibleMonth = ref(initialVisibleMonth(new Date(), timezone.value))
const notice = ref<{ key: AppShellMessageKey, tone: 'error' | 'success' }>()
const busyPost = ref<string>()
const rescheduling = ref<CalendarEntry>()
const rescheduleDateTime = ref('')
const rescheduleTimeZone = ref('')
const rescheduleOffset = ref('')
const rescheduleResolution = computed(() => resolveLocalDateTime(
  rescheduleDateTime.value,
  rescheduleTimeZone.value,
))

useHead(computed(() => ({ title: t('documentTitle.calendar') })))

const range = computed(() => {
  return paddedMonthRange(visibleMonth.value)
})

function stateFromError(
  error: unknown,
): 'access-denied' | 'offline' | 'unavailable' {
  const kind = normalizeEditorialApiError(error).kind
  if (kind === 'access-denied' || kind === 'session') {
    return 'access-denied'
  }
  return kind === 'offline' ? 'offline' : 'unavailable'
}

function errorKey(error: unknown): AppShellMessageKey {
  switch (normalizeEditorialApiError(error).kind) {
    case 'conflict':
      return 'editorial.error.conflict'
    case 'dependency':
      return 'editorial.error.dependency'
    case 'offline':
      return 'editorial.error.offline'
    case 'access-denied':
    case 'session':
      return 'editorial.error.accessDenied'
    default:
      return 'editorial.error.unavailable'
  }
}

const { pending, refresh } = useAsyncData('postqron-calendar', async () => {
  try {
    if (!session.value) {
      session.value = await accountApi.session()
    }
    const [loadedEntries, loadedConnections, loadedDrafts] = await Promise.all([
      schedulingApi.calendar(workspaceId.value, {
        channelId: channelFilter.value || undefined,
        from: range.value.from,
        status: statusFilter.value || undefined,
        until: range.value.until,
      }),
      socialApi.list(workspaceId.value),
      composerApi.listDrafts(workspaceId.value),
    ])
    entries.value = loadedEntries
    connections.value = loadedConnections
    drafts.value = loadedDrafts
    pageState.value = undefined
    return true
  } catch (error) {
    pageState.value = stateFromError(error)
    return false
  }
}, { server: false })

watch([range, channelFilter, statusFilter, timezone, workspaceId], () => void refresh())
watch([rescheduleDateTime, rescheduleTimeZone], () => {
  const entry = rescheduling.value
  if (!entry) {
    return
  }
  const unchanged = rescheduleDateTime.value === entry.scheduled_local.slice(0, 16)
    && rescheduleTimeZone.value === entry.time_zone
  if (!unchanged) {
    rescheduleOffset.value = ''
  }
})

const monthLabel = computed(() => new Intl.DateTimeFormat(uiLocale.value, {
  month: 'long',
  timeZone: 'UTC',
  year: 'numeric',
}).format(visibleMonth.value))

const calendarCells = computed(() => {
  const first = new Date(range.value.from)
  const mondayOffset = (first.getUTCDay() + 6) % 7
  const start = new Date(first)
  start.setUTCDate(start.getUTCDate() - mondayOffset)
  const monthEntries = filterEntriesForVisibleMonth(
    entries.value ?? [],
    visibleMonth.value,
    timezone.value,
  )
  return Array.from({ length: 42 }, (_, index) => {
    const date = new Date(start)
    date.setUTCDate(start.getUTCDate() + index)
    const day = date.toISOString().slice(0, 10)
    return {
      date,
      day,
      currentMonth: date.getUTCMonth() === visibleMonth.value.getUTCMonth(),
      entries: monthEntries.filter(entry =>
        localCalendarDayKey(entry.scheduled_for_utc, timezone.value) === day,
      ),
    }
  })
})

const visibleEntries = computed(() =>
  filterEntriesForVisibleMonth(entries.value ?? [], visibleMonth.value, timezone.value))

const weekDays = computed(() => {
  const monday = new Date(Date.UTC(2026, 6, 27))
  return Array.from({ length: 7 }, (_, index) => {
    const date = new Date(monday)
    date.setUTCDate(monday.getUTCDate() + index)
    return new Intl.DateTimeFormat(uiLocale.value, {
      timeZone: 'UTC',
      weekday: 'short',
    }).format(date)
  })
})

function formatInstant(value: string): string {
  try {
    return new Intl.DateTimeFormat(uiLocale.value, {
      dateStyle: 'medium',
      timeStyle: 'short',
      timeZone: timezone.value,
    }).format(new Date(value))
  } catch {
    return value
  }
}

function formatTime(value: string): string {
  try {
    return new Intl.DateTimeFormat(uiLocale.value, {
      hour: '2-digit',
      minute: '2-digit',
      timeZone: timezone.value,
    }).format(new Date(value))
  } catch {
    return value
  }
}

function formatDayLabel(value: Date): string {
  return new Intl.DateTimeFormat(uiLocale.value, {
    dateStyle: 'full',
    timeZone: 'UTC',
  }).format(value)
}

function draftText(draftId: string): string {
  const text = drafts.value.find(item => item.draft.id === draftId)?.draft.content.text.trim()
  return text || t('calendar.untitled')
}

function channel(channelId: string): SocialConnection | undefined {
  return connections.value.find(item => item.id === channelId)
}

function channelMeta(channelId: string): string {
  const target = channel(channelId)
  if (!target) {
    return t('calendar.unknownChannel')
  }
  return target.handle
    ? `@${target.handle}`
    : t(`social.provider.${target.provider}`)
}

function destinationFailure(entry: CalendarEntry, channelId: string): string | undefined {
  if (entry.status !== 'failed') {
    return undefined
  }
  const target = channel(channelId)
  if (target?.status === 'reconnect_required' && target.reconnect_reason) {
    return t(`social.reason.${target.reconnect_reason}`)
  }
  return t('calendar.failureGeneric')
}

function shiftMonth(amount: number) {
  visibleMonth.value = new Date(Date.UTC(
    visibleMonth.value.getUTCFullYear(),
    visibleMonth.value.getUTCMonth() + amount,
    1,
  ))
}

function canMutate(entry: CalendarEntry): boolean {
  return entry.status === 'scheduled'
}

async function edit(entry: CalendarEntry) {
  await navigateTo({
    path: appRoute(locale.value, 'publish'),
    query: { post: entry.post_id },
  })
}

async function duplicate(entry: CalendarEntry) {
  busyPost.value = entry.post_id
  notice.value = undefined
  try {
    await schedulingApi.duplicate(workspaceId.value, entry.post_id, {
      expectedRevision: entry.revision,
    })
    notice.value = { tone: 'success', key: 'calendar.duplicated' }
    await refresh()
  } catch (error) {
    notice.value = { tone: 'error', key: errorKey(error) }
  } finally {
    busyPost.value = undefined
  }
}

function openReschedule(entry: CalendarEntry) {
  rescheduleDateTime.value = entry.scheduled_local.slice(0, 16)
  rescheduleTimeZone.value = entry.time_zone
  rescheduleOffset.value = String(entry.utc_offset_minutes)
  rescheduling.value = entry
}

function scheduleInput(): ScheduleInput | undefined {
  if (!rescheduling.value || !rescheduleDateTime.value || !rescheduleTimeZone.value.trim()) {
    notice.value = { tone: 'error', key: 'composer.scheduleRequired' }
    return undefined
  }
  const resolution = rescheduleResolution.value
  if (resolution.kind === 'invalid') {
    notice.value = { tone: 'error', key: 'composer.scheduleRequired' }
    return undefined
  }
  if (resolution.kind === 'nonexistent') {
    notice.value = { tone: 'error', key: 'composer.localTimeNonexistent' }
    return undefined
  }
  const offset = rescheduleOffset.value.trim()
  const selectedOffset = /^-?[0-9]+$/u.test(offset) ? Number(offset) : undefined
  const input = wallClockScheduleInput(
    rescheduleDateTime.value,
    rescheduleTimeZone.value.trim(),
    selectedOffset,
  )
  if (!input) {
    notice.value = { tone: 'error', key: 'composer.offsetRequired' }
  }
  return input
}

async function confirmReschedule() {
  const entry = rescheduling.value
  const input = scheduleInput()
  if (!entry || !input) {
    return
  }
  busyPost.value = entry.post_id
  try {
    await schedulingApi.reschedule(workspaceId.value, entry.post_id, {
      expectedRevision: entry.revision,
      scheduledAt: input,
    })
    rescheduling.value = undefined
    notice.value = { tone: 'success', key: 'calendar.rescheduled' }
    await refresh()
  } catch (error) {
    notice.value = { tone: 'error', key: errorKey(error) }
  } finally {
    busyPost.value = undefined
  }
}

async function cancel(entry: CalendarEntry) {
  if (!globalThis.confirm(t('calendar.cancelConfirm'))) {
    return
  }
  busyPost.value = entry.post_id
  try {
    await schedulingApi.cancel(workspaceId.value, entry.post_id, entry.revision)
    notice.value = { tone: 'success', key: 'calendar.cancelled' }
    await refresh()
  } catch (error) {
    notice.value = { tone: 'error', key: errorKey(error) }
  } finally {
    busyPost.value = undefined
  }
}
</script>

<template>
  <AppState
    v-if="pending && entries === undefined"
    kind="loading"
  />
  <AppState
    v-else-if="pageState"
    :kind="pageState"
    action
    @retry="refresh"
  />
  <section
    v-else
    class="app-page calendar-page"
  >
    <p class="app-eyebrow">
      {{ t('calendar.eyebrow') }}
    </p>
    <div class="app-page__title-row">
      <div>
        <h1>{{ t('calendar.title') }}</h1>
        <p class="app-page__lead">
          {{ t('calendar.description') }}
        </p>
      </div>
      <NuxtLink
        class="pq-button"
        :to="appRoute(locale, 'publish')"
      >
        <span aria-hidden="true">＋</span>
        {{ t('shell.newPost') }}
      </NuxtLink>
    </div>

    <p
      v-if="notice"
      class="app-inline-alert"
      :data-success="notice.tone === 'success'"
      :role="notice.tone === 'success' ? 'status' : 'alert'"
    >
      {{ t(notice.key) }}
    </p>

    <div
      class="calendar-toolbar"
      :aria-label="t('calendar.controlsLabel')"
    >
      <div class="calendar-view-toggle">
        <button
          type="button"
          :aria-pressed="view === 'calendar'"
          @click="view = 'calendar'"
        >
          {{ t('calendar.viewCalendar') }}
        </button>
        <button
          type="button"
          :aria-pressed="view === 'list'"
          @click="view = 'list'"
        >
          {{ t('calendar.viewList') }}
        </button>
      </div>
      <label>
        <span>{{ t('calendar.filterChannel') }}</span>
        <select v-model="channelFilter">
          <option value="">{{ t('calendar.allChannels') }}</option>
          <option
            v-for="item in connections"
            :key="item.id"
            :value="item.id"
          >
            {{ item.display_name }}
          </option>
        </select>
      </label>
      <label>
        <span>{{ t('calendar.filterStatus') }}</span>
        <select v-model="statusFilter">
          <option value="">{{ t('calendar.allStatuses') }}</option>
          <option
            v-for="status in SCHEDULING_STATUSES"
            :key="status"
            :value="status"
          >
            {{ t(`calendar.status.${status}`) }}
          </option>
        </select>
      </label>
      <label>
        <span>{{ t('calendar.timezone') }}</span>
        <select
          v-model="timezone"
        >
          <option
            v-for="zone in timezoneOptions"
            :key="zone"
            :value="zone"
          >
            {{ zone }}
          </option>
        </select>
      </label>
    </div>

    <div class="calendar-period">
      <button
        class="pq-button pq-button--secondary"
        type="button"
        :aria-label="t('calendar.previousMonth')"
        @click="shiftMonth(-1)"
      >
        ←
      </button>
      <h2 aria-live="polite">
        {{ monthLabel }}
      </h2>
      <button
        class="pq-button pq-button--secondary"
        type="button"
        :aria-label="t('calendar.nextMonth')"
        @click="shiftMonth(1)"
      >
        →
      </button>
    </div>

    <article
      v-if="rescheduling"
      class="app-card app-card--accent calendar-reschedule"
    >
      <div class="app-card__header">
        <span class="app-card__eyebrow">{{ t('calendar.rescheduleEyebrow') }}</span>
        <h2>{{ t('calendar.rescheduleTitle') }}</h2>
      </div>
      <label>
        <span>{{ t('composer.dateTimeLabel') }}</span>
        <input
          v-model="rescheduleDateTime"
          type="datetime-local"
        >
      </label>
      <label>
        <span>{{ t('composer.timezoneLabel') }}</span>
        <select v-model="rescheduleTimeZone">
          <option
            v-for="zone in timezoneOptions"
            :key="zone"
            :value="zone"
          >
            {{ zone }}
          </option>
        </select>
      </label>
      <label v-if="rescheduleResolution.kind === 'ambiguous'">
        <span>{{ t('composer.offsetLabel') }}</span>
        <select
          v-model="rescheduleOffset"
        >
          <option value="">{{ t('composer.chooseOffset') }}</option>
          <option
            v-for="offset in rescheduleResolution.offsets"
            :key="offset"
            :value="String(offset)"
          >
            UTC{{ offset >= 0 ? '+' : '' }}{{ offset / 60 }}
          </option>
        </select>
      </label>
      <p
        v-else-if="rescheduleResolution.kind === 'nonexistent'"
        class="app-inline-alert"
        role="alert"
      >
        {{ t('composer.localTimeNonexistent') }}
      </p>
      <div class="calendar-actions">
        <button
          class="pq-button"
          type="button"
          :disabled="busyPost === rescheduling.post_id"
          @click="confirmReschedule"
        >
          {{ t('calendar.confirmReschedule') }}
        </button>
        <button
          class="pq-button pq-button--secondary"
          type="button"
          @click="rescheduling = undefined"
        >
          {{ t('calendar.close') }}
        </button>
      </div>
    </article>

    <article
      v-if="visibleEntries.length === 0"
      class="app-card calendar-empty"
    >
      <span class="app-card__eyebrow">{{ t('calendar.emptyEyebrow') }}</span>
      <h2>{{ t('calendar.emptyTitle') }}</h2>
      <p>{{ t('calendar.emptyDescription') }}</p>
      <NuxtLink
        class="pq-button"
        :to="appRoute(locale, 'publish')"
      >
        {{ t('shell.newPost') }}
      </NuxtLink>
    </article>

    <div
      v-else-if="view === 'calendar'"
      class="calendar-grid"
    >
      <div
        v-for="day in weekDays"
        :key="day"
        class="calendar-grid__weekday"
      >
        {{ day }}
      </div>
      <section
        v-for="cell in calendarCells"
        :key="cell.day"
        class="calendar-grid__day"
        :data-outside="!cell.currentMonth"
        :aria-label="formatDayLabel(cell.date)"
      >
        <time :datetime="cell.day">{{ cell.date.getUTCDate() }}</time>
        <ul>
          <li
            v-for="entry in cell.entries"
            :key="entry.post_id"
          >
            <button
              v-if="canMutate(entry)"
              type="button"
              @click="edit(entry)"
            >
              <span>{{ formatTime(entry.scheduled_for_utc) }}</span>
              <strong>{{ draftText(entry.draft_id) }}</strong>
              <small>{{ t(`calendar.status.${entry.status}`) }}</small>
            </button>
            <div
              v-else
              class="calendar-grid__entry"
            >
              <span>{{ formatTime(entry.scheduled_for_utc) }}</span>
              <strong>{{ draftText(entry.draft_id) }}</strong>
              <small>{{ t(`calendar.status.${entry.status}`) }}</small>
            </div>
          </li>
        </ul>
      </section>
    </div>

    <ol
      v-else
      class="calendar-list"
    >
      <li
        v-for="entry in visibleEntries"
        :key="entry.post_id"
        class="app-card"
      >
        <div class="calendar-list__heading">
          <div>
            <time :datetime="entry.scheduled_for_utc">
              {{ formatInstant(entry.scheduled_for_utc) }}
            </time>
            <span>{{ entry.time_zone }} · UTC{{ entry.utc_offset_minutes >= 0 ? '+' : '' }}{{ entry.utc_offset_minutes / 60 }}</span>
          </div>
          <span :class="['app-badge', `calendar-status--${entry.status}`]">
            {{ t(`calendar.status.${entry.status}`) }}
          </span>
        </div>
        <h2>{{ draftText(entry.draft_id) }}</h2>
        <ul class="calendar-destinations">
          <li
            v-for="channelId in entry.channel_ids"
            :key="channelId"
          >
            <div>
              <strong>{{ channel(channelId)?.display_name ?? t('calendar.unknownChannel') }}</strong>
              <span>{{ channelMeta(channelId) }}</span>
            </div>
            <span class="app-badge">{{ t(`calendar.status.${entry.status}`) }}</span>
            <p
              v-if="destinationFailure(entry, channelId)"
              class="calendar-failure"
            >
              {{ destinationFailure(entry, channelId) }}
            </p>
          </li>
        </ul>
        <p
          v-if="!canMutate(entry)"
          class="app-inline-note"
        >
          {{ t('calendar.readOnly') }}
        </p>
        <div class="calendar-actions">
          <button
            v-if="canMutate(entry)"
            class="pq-button pq-button--secondary"
            type="button"
            @click="edit(entry)"
          >
            {{ t('calendar.edit') }}
          </button>
          <button
            v-if="canMutate(entry)"
            class="pq-button pq-button--secondary"
            type="button"
            :disabled="busyPost === entry.post_id"
            @click="duplicate(entry)"
          >
            {{ t('calendar.duplicate') }}
          </button>
          <button
            v-if="canMutate(entry)"
            class="pq-button pq-button--secondary"
            type="button"
            @click="openReschedule(entry)"
          >
            {{ t('calendar.reschedule') }}
          </button>
          <button
            v-if="entry.status === 'scheduled'"
            class="pq-button pq-button--secondary"
            type="button"
            :disabled="busyPost === entry.post_id"
            @click="cancel(entry)"
          >
            {{ t('calendar.cancel') }}
          </button>
        </div>
      </li>
    </ol>
  </section>
</template>
