const REQUIRED_TIMEZONES = ['UTC', 'Europe/Rome'] as const

function validTimeZone(value: string): boolean {
  try {
    new Intl.DateTimeFormat('en', { timeZone: value }).format()
    return true
  } catch {
    return false
  }
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
