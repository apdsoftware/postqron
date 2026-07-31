import assert from 'node:assert/strict'
import test from 'node:test'
import type { CalendarEntry } from '../components/core/editorial-contracts.ts'
import {
  filterEntriesForVisibleMonth,
  localCalendarDayKey,
  paddedMonthRange,
  visibleMonthKey,
} from '../components/core/calendar-range.ts'

function entry(
  scheduled_for_utc: string,
  scheduled_local: string,
): CalendarEntry {
  return {
    post_id: scheduled_for_utc,
    draft_id: 'draft-1',
    channel_ids: ['channel-1'],
    status: 'scheduled',
    scheduled_for_utc,
    scheduled_local,
    time_zone: 'Europe/Rome',
    utc_offset_minutes: 120,
    revision: 1,
  }
}

test('calendar ranges pad the visible month to survive local DST boundaries', () => {
  const range = paddedMonthRange(new Date(Date.UTC(2026, 2, 1)))
  assert.equal(range.from, '2026-02-27T00:00:00.000Z')
  assert.equal(range.until, '2026-04-03T00:00:00.000Z')
  assert.equal(visibleMonthKey(new Date(Date.UTC(2026, 2, 1))), '2026-03')
})

test('calendar filtering keeps entries whose local day remains inside the visible month', () => {
  const month = new Date(Date.UTC(2026, 2, 1))
  const entries = [
    entry('2026-02-28T23:30:00.000Z', '2026-03-01T00:30:00'),
    entry('2026-03-31T21:30:00.000Z', '2026-03-31T23:30:00'),
    entry('2026-04-01T00:30:00.000Z', '2026-04-01T02:30:00'),
  ]

  assert.equal(localCalendarDayKey(entries[0].scheduled_for_utc, 'Europe/Rome'), '2026-03-01')
  assert.deepEqual(
    filterEntriesForVisibleMonth(entries, month, 'Europe/Rome').map(item => item.post_id),
    [
      '2026-02-28T23:30:00.000Z',
      '2026-03-31T21:30:00.000Z',
    ],
  )
})
