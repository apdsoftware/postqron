export const SUPPORTED_LOCALES = ['en', 'it', 'es', 'fr', 'de'] as const

export type Locale = typeof SUPPORTED_LOCALES[number]

export const DEFAULT_LOCALE: Locale = 'en'
export const FALLBACK_LOCALE: Locale = 'en'

const supportedLocaleSet = new Set<string>(SUPPORTED_LOCALES)

export function isLocale(value: unknown): value is Locale {
  return typeof value === 'string' && supportedLocaleSet.has(value)
}

export function normalizeLocale(value: unknown): Locale | undefined {
  if (typeof value !== 'string') {
    return undefined
  }
  const base = value.trim().toLowerCase().split(/[-_]/u, 1)[0]
  return isLocale(base) ? base : undefined
}
