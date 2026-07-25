import {
  localizeUrl,
  type Locale,
  type MessageParameters,
} from '../../f36-i18n/src/index.ts'
import { usePostqronI18n } from '../../f36-i18n/runtime.ts'
import { MARKETING_FAQ_CATALOGS } from './faq.ts'
import { MARKETING_FEATURES_CATALOGS } from './features.ts'
import { MARKETING_HOME_CATALOGS } from './home.ts'
import { MARKETING_LEGAL_CATALOGS } from './legal.ts'
import { MARKETING_NAV_CATALOGS } from './nav.ts'
import { MARKETING_PLANNER_PREVIEW_CATALOGS } from './planner-preview.ts'

const registeredI18nRuntimes = new WeakSet<object>()

export interface ReadonlyLocaleState {
  readonly value: Locale
}

export interface MarketingSiteI18nRuntime {
  readonly locale: ReadonlyLocaleState
  date(value: Date | string, options?: Intl.DateTimeFormatOptions): string
  localize(path: string): string
  translate(namespacedKey: string, parameters?: MessageParameters): string
}

export function useMarketingSiteI18n(): MarketingSiteI18nRuntime {
  const i18n = usePostqronI18n()
  if (!registeredI18nRuntimes.has(i18n)) {
    i18n.registerCatalog('marketing-nav', MARKETING_NAV_CATALOGS)
    i18n.registerCatalog('marketing-home', MARKETING_HOME_CATALOGS)
    i18n.registerCatalog('marketing-features', MARKETING_FEATURES_CATALOGS)
    i18n.registerCatalog('marketing-faq', MARKETING_FAQ_CATALOGS)
    i18n.registerCatalog('marketing-legal', MARKETING_LEGAL_CATALOGS)
    i18n.registerCatalog(
      'marketing-planner-preview',
      MARKETING_PLANNER_PREVIEW_CATALOGS,
    )
    registeredI18nRuntimes.add(i18n)
  }

  return {
    locale: i18n.locale,
    date(value, options = {}) {
      return i18n.date(value, options)
    },
    localize(path) {
      return localizeUrl(i18n.locale.value, path)
    },
    translate(namespacedKey, parameters = {}) {
      return i18n.translate(namespacedKey, parameters)
    },
  }
}
