// Regional preference helpers for the account profile: the locale select is
// limited to the languages Postqron ships, the timezone select is grouped by
// IANA region, and dates are rendered in the caller's interface locale. All
// helpers preserve whatever value the account currently holds, so a stored
// value that predates this list stays selectable instead of silently changing.

export interface PreferenceOption {
  label: string
  value: string
}

export interface TimezoneGroup {
  region: string
  zones: string[]
}

// Language names are shown in their own language, the convention for language
// pickers. Both bare language tags and common regional variants are offered so
// values such as `it` and `it-IT` both map to a first-class option.
const SUPPORTED_LOCALES: readonly PreferenceOption[] = Object.freeze([
  { value: 'en', label: 'English' },
  { value: 'en-US', label: 'English (United States)' },
  { value: 'en-GB', label: 'English (United Kingdom)' },
  { value: 'it', label: 'Italiano' },
  { value: 'it-IT', label: 'Italiano (Italia)' },
  { value: 'es', label: 'Español' },
  { value: 'es-ES', label: 'Español (España)' },
  { value: 'fr', label: 'Français' },
  { value: 'fr-FR', label: 'Français (France)' },
  { value: 'de', label: 'Deutsch' },
  { value: 'de-DE', label: 'Deutsch (Deutschland)' },
])

// A resilient default in the rare runtime without Intl enumeration support.
const FALLBACK_TIMEZONES: readonly string[] = Object.freeze([
  'UTC',
  'Africa/Cairo',
  'Africa/Johannesburg',
  'Africa/Lagos',
  'America/Chicago',
  'America/Los_Angeles',
  'America/New_York',
  'America/Sao_Paulo',
  'Asia/Dubai',
  'Asia/Kolkata',
  'Asia/Shanghai',
  'Asia/Singapore',
  'Asia/Tokyo',
  'Australia/Sydney',
  'Europe/Berlin',
  'Europe/London',
  'Europe/Madrid',
  'Europe/Paris',
  'Europe/Rome',
  'Pacific/Auckland',
])

function withValue(
  options: readonly PreferenceOption[],
  current: string,
): PreferenceOption[] {
  const normalized = current.trim()
  if (normalized === '' || options.some(option => option.value === normalized)) {
    return [...options]
  }
  return [{ value: normalized, label: normalized }, ...options]
}

export function localeOptions(current: string): PreferenceOption[] {
  return withValue(SUPPORTED_LOCALES, current)
}

export function timezoneValues(): string[] {
  const enumerable = Intl as unknown as {
    supportedValuesOf?: (key: string) => string[]
  }
  if (typeof enumerable.supportedValuesOf === 'function') {
    try {
      const zones = enumerable.supportedValuesOf('timeZone')
      if (Array.isArray(zones) && zones.length > 0) {
        return zones
      }
    } catch {
      // Fall through to the curated list below.
    }
  }
  return [...FALLBACK_TIMEZONES]
}

// Group IANA zones by their leading region (`Europe/Rome` -> `Europe`) so the
// select can render accessible <optgroup> sections instead of one long list.
// The current value is guaranteed to appear even if it is outside the catalog.
export function timezoneGroups(current: string): TimezoneGroup[] {
  const zones = new Set(timezoneValues())
  const normalized = current.trim()
  if (normalized !== '') {
    zones.add(normalized)
  }
  const byRegion = new Map<string, string[]>()
  for (const zone of zones) {
    const region = zone.includes('/') ? zone.slice(0, zone.indexOf('/')) : 'Other'
    const bucket = byRegion.get(region)
    if (bucket) {
      bucket.push(zone)
    } else {
      byRegion.set(region, [zone])
    }
  }
  return [...byRegion.entries()]
    .map(([region, list]) => ({
      region,
      zones: list.sort((first, second) => first.localeCompare(second)),
    }))
    .sort((first, second) => first.region.localeCompare(second.region))
}

// Render an ISO instant in the interface locale, falling back to the raw string
// when the value or locale cannot be formatted so the UI never shows `Invalid
// Date`.
export function formatDateTime(value: string, locale: string): string {
  const parsed = new Date(value)
  if (Number.isNaN(parsed.getTime())) {
    return value
  }
  try {
    return new Intl.DateTimeFormat(locale, {
      dateStyle: 'medium',
      timeStyle: 'short',
    }).format(parsed)
  } catch {
    return parsed.toISOString()
  }
}
