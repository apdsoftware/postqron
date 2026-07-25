import { I18nError } from './errors.ts'
import { isLocale, type Locale } from './locales.ts'
import { localizeUrl } from './routing.ts'

export interface LocaleCookieStore {
  read(): unknown
  write(locale: Locale): void | Promise<void>
}

export interface LocaleProfileStore {
  isAuthenticated(): boolean | Promise<boolean>
  read(): unknown | Promise<unknown>
  write(locale: Locale): void | Promise<void>
}

export interface LocalePreferenceControllerOptions {
  cookie: LocaleCookieStore
  profile?: LocaleProfileStore
}

export class LocalePreferenceController {
  readonly #cookie: LocaleCookieStore
  #profile?: LocaleProfileStore

  constructor(options: LocalePreferenceControllerOptions) {
    this.#cookie = options.cookie
    this.#profile = options.profile
  }

  setProfileStore(profile: LocaleProfileStore | undefined): void {
    this.#profile = profile
  }

  cookieValue(): unknown {
    return this.#cookie.read()
  }

  async profileValue(): Promise<unknown> {
    if (!this.#profile || !await this.#profile.isAuthenticated()) {
      return undefined
    }
    return this.#profile.read()
  }

  async changeLocale(locale: Locale, currentUrl: string): Promise<string> {
    if (!isLocale(locale)) {
      throw new I18nError(
        'I18N_UNSUPPORTED_LOCALE',
        `Unsupported locale: ${String(locale)}`,
      )
    }

    try {
      if (this.#profile && await this.#profile.isAuthenticated()) {
        await this.#profile.write(locale)
      }
      await this.#cookie.write(locale)
    } catch (error) {
      throw new I18nError(
        'I18N_PROFILE_PERSIST_FAILED',
        'The locale preference could not be persisted',
        error,
      )
    }

    return localizeUrl(locale, currentUrl)
  }
}
