import {
  readonly,
  useState,
} from '#imports'
import {
  localizeUrl,
  type MessageParameters,
} from '../f36-i18n/src/index.ts'
import {
  usePostqronI18n,
} from '../f36-i18n/runtime.ts'
import { SUPPORT_CONTACT_CATALOGS, type SupportContactMessageKey } from './src/catalogs.ts'
import {
  resolveSupportContactConfig,
  supportMailto,
  type SupportContactConfig,
} from './src/config.ts'

export const SUPPORT_CONTACT_STATE_KEY = 'postqron.support-contact.config'
const registeredI18nRuntimes = new WeakSet<object>()

export interface ReadonlySupportContactConfig {
  readonly value: Readonly<SupportContactConfig>
}

export interface SupportContactRuntime {
  readonly config: ReadonlySupportContactConfig
  localize(path: string): string
  mailto(): `mailto:${string}`
  translate(
    key: SupportContactMessageKey,
    parameters?: MessageParameters,
  ): string
}

export function useSupportContact(): SupportContactRuntime {
  const i18n = usePostqronI18n()
  if (!registeredI18nRuntimes.has(i18n)) {
    i18n.registerCatalog('support-contact', SUPPORT_CONTACT_CATALOGS)
    registeredI18nRuntimes.add(i18n)
  }

  const config = useState<Readonly<SupportContactConfig>>(
    SUPPORT_CONTACT_STATE_KEY,
    () => resolveSupportContactConfig(
      import.meta.server
        ? process.env.NUXT_PUBLIC_SUPPORT_EMAIL
        : undefined,
    ),
  )
  return {
    config: readonly(config),
    localize(path) {
      return localizeUrl(i18n.locale.value, path)
    },
    mailto() {
      return supportMailto(config.value.email)
    },
    translate(key, parameters = {}) {
      return i18n.translate(`support-contact.${key}`, parameters)
    },
  }
}
