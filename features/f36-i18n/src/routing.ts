import { I18nError } from './errors.ts'
import {
  isLocale,
  type Locale,
} from './locales.ts'

const LOCAL_ORIGIN = 'https://postqron.local'

function hasUnsafeLocalUrlCharacter(value: string): boolean {
  return [...value].some((character) => {
    const codePoint = character.codePointAt(0) ?? 0
    return character === '\\' || codePoint <= 31 || codePoint === 127
  })
}

export interface LocalUrl {
  hash: string
  pathname: string
  search: string
}

export interface LocalePath {
  locale?: Locale
  pathname: string
}

export function parseLocalUrl(value: string): LocalUrl {
  if (
    !value.startsWith('/')
    || value.startsWith('//')
    || hasUnsafeLocalUrlCharacter(value)
  ) {
    throw new I18nError(
      'I18N_INVALID_LOCAL_URL',
      'Only an origin-relative URL is accepted',
    )
  }

  let parsed: URL
  try {
    parsed = new URL(value, LOCAL_ORIGIN)
  } catch (error) {
    throw new I18nError(
      'I18N_INVALID_LOCAL_URL',
      'The local URL is malformed',
      error,
    )
  }
  if (parsed.origin !== LOCAL_ORIGIN) {
    throw new I18nError(
      'I18N_INVALID_LOCAL_URL',
      'The local URL resolves outside the application origin',
    )
  }
  return {
    pathname: parsed.pathname,
    search: parsed.search,
    hash: parsed.hash,
  }
}

export function splitLocalePath(pathname: string): LocalePath {
  const segments = pathname.split('/')
  const candidate = segments[1]?.toLowerCase()
  if (!isLocale(candidate)) {
    return { pathname }
  }

  const remainder = `/${segments.slice(2).join('/')}`
  return {
    locale: candidate,
    pathname: remainder === '/' ? '/' : remainder.replace(/\/+$/u, '') || '/',
  }
}

export function localeFromUrl(value: string): Locale | undefined {
  return splitLocalePath(parseLocalUrl(value).pathname).locale
}

export function localizeUrl(locale: Locale, value: string): string {
  if (!isLocale(locale)) {
    throw new I18nError(
      'I18N_UNSUPPORTED_LOCALE',
      `Unsupported locale: ${String(locale)}`,
    )
  }
  const parsed = parseLocalUrl(value)
  const basePath = splitLocalePath(parsed.pathname).pathname
  const localizedPath = `/${locale}${basePath === '/' ? '' : basePath}`
  return `${localizedPath}${parsed.search}${parsed.hash}`
}

export function canonicalLocaleRedirect(
  locale: Locale,
  value: string,
): string | undefined {
  const parsed = parseLocalUrl(value)
  const current = `${parsed.pathname}${parsed.search}${parsed.hash}`
  const canonical = localizeUrl(locale, current)
  return current === canonical ? undefined : canonical
}

export function safeLocalizeUrl(locale: Locale, value: string): string {
  try {
    return localizeUrl(locale, value)
  } catch (error) {
    if (
      error instanceof I18nError
      && error.code === 'I18N_INVALID_LOCAL_URL'
    ) {
      return localizeUrl(locale, '/')
    }
    throw error
  }
}
