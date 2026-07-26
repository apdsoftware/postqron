import { FOUNDATION_CATALOGS } from './foundation-catalogs.ts'
import {
  translateCatalog,
} from './catalog.ts'
import { SUPPORTED_LOCALES, type Locale } from './locales.ts'
import { localizeUrl } from './routing.ts'

export interface LanguageSwitcherItem {
  active: boolean
  href: string
  label: string
  locale: Locale
}

export interface LanguageSwitcherModel {
  accessibility: {
    activation: 'native-select'
    currentAttribute: 'value'
    statusLive: 'polite'
  }
  items: readonly LanguageSwitcherItem[]
  label: string
}

export function createLanguageSwitcherModel(
  activeLocale: Locale,
  currentUrl: string,
): LanguageSwitcherModel {
  return {
    label: translateCatalog(
      FOUNDATION_CATALOGS[activeLocale],
      activeLocale,
      'languageSwitcher.label',
    ),
    items: SUPPORTED_LOCALES.map(locale => ({
      locale,
      active: locale === activeLocale,
      href: localizeUrl(locale, currentUrl),
      label: translateCatalog(
        FOUNDATION_CATALOGS[activeLocale],
        activeLocale,
        `language.${locale}`,
      ),
    })),
    accessibility: {
      activation: 'native-select',
      currentAttribute: 'value',
      statusLive: 'polite',
    },
  }
}
