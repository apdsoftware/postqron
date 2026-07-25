import {
  DEFAULT_LOCALE,
  normalizeLocale,
  type Locale,
} from './locales.ts'
import { localeFromUrl } from './routing.ts'

export const LOCALE_PRECEDENCE = [
  'url',
  'profile',
  'cookie',
  'browser',
  'fallback',
] as const

export type LocaleSource = typeof LOCALE_PRECEDENCE[number]

export interface LocaleResolutionInput {
  acceptLanguage?: string | readonly string[]
  cookie?: unknown
  profile?: unknown
  url: string
}

export interface LocaleResolution {
  locale: Locale
  source: LocaleSource
}

interface WeightedLanguage {
  index: number
  locale: Locale
  quality: number
}

function parseQuality(parameters: readonly string[]): number {
  const qualityParameter = parameters
    .map(parameter => parameter.trim())
    .find(parameter => parameter.toLowerCase().startsWith('q='))
  if (!qualityParameter) {
    return 1
  }
  const quality = Number(qualityParameter.slice(2))
  return Number.isFinite(quality) && quality >= 0 && quality <= 1 ? quality : 0
}

export function localeFromAcceptLanguage(
  header: string | readonly string[] | undefined,
): Locale | undefined {
  const source = typeof header === 'string' ? header : header?.join(',')
  if (!source) {
    return undefined
  }

  const weighted: WeightedLanguage[] = []
  for (const [index, rawEntry] of source.split(',').entries()) {
    const [range = '', ...parameters] = rawEntry.split(';')
    const quality = parseQuality(parameters)
    const locale = range.trim() === '*' ? undefined : normalizeLocale(range)
    if (locale && quality > 0) {
      weighted.push({ index, locale, quality })
    }
  }
  weighted.sort((left, right) =>
    right.quality - left.quality || left.index - right.index)
  return weighted[0]?.locale
}

export function resolveLocale(input: LocaleResolutionInput): LocaleResolution {
  const url = localeFromUrl(input.url)
  if (url) {
    return { locale: url, source: 'url' }
  }

  const profile = normalizeLocale(input.profile)
  if (profile) {
    return { locale: profile, source: 'profile' }
  }

  const cookie = normalizeLocale(input.cookie)
  if (cookie) {
    return { locale: cookie, source: 'cookie' }
  }

  const browser = localeFromAcceptLanguage(input.acceptLanguage)
  if (browser) {
    return { locale: browser, source: 'browser' }
  }

  return { locale: DEFAULT_LOCALE, source: 'fallback' }
}
