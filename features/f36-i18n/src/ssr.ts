import { I18nError } from './errors.ts'
import { isLocale, type Locale } from './locales.ts'
import {
  LOCALE_PRECEDENCE,
  resolveLocale,
  type LocaleResolutionInput,
  type LocaleSource,
} from './resolver.ts'
import { localizeUrl } from './routing.ts'

export const I18N_PAYLOAD_KEY = 'postqron.i18n'

export interface SerializedI18nState {
  canonicalUrl: string
  htmlLang: Locale
  locale: Locale
  source: LocaleSource
}

export function createServerI18nState(
  input: LocaleResolutionInput,
): SerializedI18nState {
  const resolution = resolveLocale(input)
  return Object.freeze({
    locale: resolution.locale,
    source: resolution.source,
    htmlLang: resolution.locale,
    canonicalUrl: localizeUrl(resolution.locale, input.url),
  })
}

export function hydrateI18nState(value: unknown): SerializedI18nState {
  if (!value || typeof value !== 'object') {
    throw new I18nError(
      'I18N_INVALID_SSR_STATE',
      'The SSR locale payload is missing',
    )
  }
  const candidate = value as Partial<SerializedI18nState>
  const validSource = typeof candidate.source === 'string'
    && LOCALE_PRECEDENCE.includes(candidate.source as LocaleSource)
  if (
    !isLocale(candidate.locale)
    || candidate.htmlLang !== candidate.locale
    || typeof candidate.canonicalUrl !== 'string'
    || !candidate.canonicalUrl.startsWith('/')
    || !validSource
  ) {
    throw new I18nError(
      'I18N_INVALID_SSR_STATE',
      'The SSR locale payload is invalid',
    )
  }
  return Object.freeze({
    locale: candidate.locale,
    source: candidate.source as LocaleSource,
    htmlLang: candidate.locale,
    canonicalUrl: candidate.canonicalUrl,
  })
}
