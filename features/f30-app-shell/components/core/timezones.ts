const REQUIRED_TIMEZONES = ['UTC', 'Europe/Rome'] as const

export function validTimeZone(value: string): boolean {
  try {
    new Intl.DateTimeFormat('en', { timeZone: value }).format()
    return true
  } catch {
    return false
  }
}

interface WallClockParts {
  day: number
  hour: number
  minute: number
  month: number
  second: number
  year: number
}

export interface LocalDateTimeResolution {
  kind: 'ambiguous' | 'invalid' | 'nonexistent' | 'unique'
  offsets: number[]
}

function instantParts(date: Date, timeZone: string): WallClockParts {
  const values = Object.fromEntries(new Intl.DateTimeFormat('en-CA', {
    day: '2-digit',
    hour: '2-digit',
    hourCycle: 'h23',
    minute: '2-digit',
    month: '2-digit',
    second: '2-digit',
    timeZone,
    year: 'numeric',
  }).formatToParts(date).map(part => [part.type, part.value]))
  return {
    day: Number(values.day),
    hour: Number(values.hour),
    minute: Number(values.minute),
    month: Number(values.month),
    second: Number(values.second),
    year: Number(values.year),
  }
}

export function utcOffsetMinutesAtInstant(date: Date, timeZone: string): number {
  if (Number.isNaN(date.getTime()) || !validTimeZone(timeZone)) {
    throw new Error('TIMEZONE_INVALID_INSTANT')
  }
  const parts = instantParts(date, timeZone)
  const instantWithoutMilliseconds = Math.floor(date.getTime() / 1000) * 1000
  return (Date.UTC(
    parts.year,
    parts.month - 1,
    parts.day,
    parts.hour,
    parts.minute,
    parts.second,
  ) - instantWithoutMilliseconds) / 60_000
}

function parseLocalDateTime(value: string): WallClockParts | undefined {
  const match = /^(\d{4})-(\d{2})-(\d{2})T(\d{2}):(\d{2})(?::(\d{2}))?$/u.exec(value.trim())
  if (!match) {
    return undefined
  }
  const parts = {
    year: Number(match[1]),
    month: Number(match[2]),
    day: Number(match[3]),
    hour: Number(match[4]),
    minute: Number(match[5]),
    second: Number(match[6] ?? 0),
  }
  const roundTrip = new Date(Date.UTC(
    parts.year,
    parts.month - 1,
    parts.day,
    parts.hour,
    parts.minute,
    parts.second,
  ))
  return roundTrip.getUTCFullYear() === parts.year
    && roundTrip.getUTCMonth() === parts.month - 1
    && roundTrip.getUTCDate() === parts.day
    && roundTrip.getUTCHours() === parts.hour
    && roundTrip.getUTCMinutes() === parts.minute
    && roundTrip.getUTCSeconds() === parts.second
    ? parts
    : undefined
}

function sameParts(left: WallClockParts, right: WallClockParts): boolean {
  return left.year === right.year
    && left.month === right.month
    && left.day === right.day
    && left.hour === right.hour
    && left.minute === right.minute
    && left.second === right.second
}

export function resolveLocalDateTime(
  localDateTime: string,
  timeZone: string,
): LocalDateTimeResolution {
  const wall = parseLocalDateTime(localDateTime)
  if (!wall || !validTimeZone(timeZone)) {
    return { kind: 'invalid', offsets: [] }
  }
  const wallMilliseconds = Date.UTC(
    wall.year,
    wall.month - 1,
    wall.day,
    wall.hour,
    wall.minute,
    wall.second,
  )
  const nearbyOffsets = new Set<number>()
  for (let delta = -36 * 60; delta <= 36 * 60; delta += 30) {
    nearbyOffsets.add(utcOffsetMinutesAtInstant(
      new Date(wallMilliseconds + delta * 60_000),
      timeZone,
    ))
  }
  const offsets = [...nearbyOffsets]
    .filter(offset => {
      const candidate = new Date(wallMilliseconds - offset * 60_000)
      return utcOffsetMinutesAtInstant(candidate, timeZone) === offset
        && sameParts(instantParts(candidate, timeZone), wall)
    })
    .sort((left, right) => right - left)
  return offsets.length === 0
    ? { kind: 'nonexistent', offsets }
    : offsets.length === 1
      ? { kind: 'unique', offsets }
      : { kind: 'ambiguous', offsets }
}

export function detectedTimeZone(): string {
  const detected = Intl.DateTimeFormat().resolvedOptions().timeZone
  return typeof detected === 'string' && validTimeZone(detected)
    ? detected
    : 'UTC'
}

export function supportedTimeZones(
  detected = detectedTimeZone(),
): string[] {
  const supportedValuesOf = (
    Intl as typeof Intl & {
      supportedValuesOf?: (key: 'timeZone') => string[]
    }
  ).supportedValuesOf
  const runtimeValues: string[] = (() => {
    try {
      return supportedValuesOf?.('timeZone') ?? []
    } catch {
      return []
    }
  })()
  return [...new Set([
    ...REQUIRED_TIMEZONES,
    detected,
    ...runtimeValues,
  ])]
    .filter(value => typeof value === 'string' && validTimeZone(value))
    .sort((left, right) => left.localeCompare(right, 'en'))
}

export function safeTimeZone(
  value: unknown,
  detected = detectedTimeZone(),
): string {
  const zones = supportedTimeZones(detected)
  return typeof value === 'string' && zones.includes(value)
    ? value
    : zones.includes(detected)
      ? detected
      : 'UTC'
}
