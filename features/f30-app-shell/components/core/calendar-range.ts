import type { CalendarEntry } from './editorial-contracts.ts'

const RANGE_PADDING_DAYS = 2
const formatterCache = new Map<string, Intl.DateTimeFormat>()

function formatter(timeZone: string): Intl.DateTimeFormat {
  const cached = formatterCache.get(timeZone)
  if (cached) {
    return cached
  }
  const created = new Intl.DateTimeFormat('en-CA', {
    day: '2-digit',
    month: '2-digit',
    timeZone,
    year: 'numeric',
  })
  formatterCache.set(timeZone, created)
  return created
}

export function visibleMonthKey(month: Date): string {
  return `${month.getUTCFullYear()}-${String(month.getUTCMonth() + 1).padStart(2, '0')}`
}

export function paddedMonthRange(month: Date): {
  from: string
  until: string
} {
  const start = new Date(Date.UTC(
    month.getUTCFullYear(),
    month.getUTCMonth(),
    1,
  ))
  const end = new Date(Date.UTC(
    month.getUTCFullYear(),
    month.getUTCMonth() + 1,
    1,
  ))
  start.setUTCDate(start.getUTCDate() - RANGE_PADDING_DAYS)
  end.setUTCDate(end.getUTCDate() + RANGE_PADDING_DAYS)
  return {
    from: start.toISOString(),
    until: end.toISOString(),
  }
}

export function localCalendarDayKey(
  value: Date | string,
  timeZone: string,
): string {
  const date = typeof value === 'string' ? new Date(value) : value
  const parts = Object.fromEntries(
    formatter(timeZone).formatToParts(date).map(part => [part.type, part.value]),
  )
  return `${parts.year}-${parts.month}-${parts.day}`
}

export function filterEntriesForVisibleMonth(
  entries: readonly CalendarEntry[],
  month: Date,
  timeZone: string,
): CalendarEntry[] {
  const key = visibleMonthKey(month)
  return entries.filter(entry =>
    localCalendarDayKey(entry.scheduled_for_utc, timeZone).startsWith(`${key}-`))
}
