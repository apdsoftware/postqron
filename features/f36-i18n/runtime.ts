import {
  addRouteMiddleware,
  computed,
  defineNuxtPlugin,
  inject,
  navigateTo,
  readonly,
  useCookie,
  useHead,
  useRequestHeaders,
  useRoute,
  useRouter,
  useState,
} from '#imports'
import LanguageSwitcher from './components/LanguageSwitcher.vue'
import {
  CatalogRegistry,
  FOUNDATION_CATALOGS,
  I18N_PAYLOAD_KEY,
  I18nError,
  LOCALE_COOKIE_CONTRACT,
  LocalePreferenceController,
  SUPPORTED_LOCALES,
  canonicalLocaleRedirect,
  createFormatters,
  createServerI18nState,
  hydrateI18nState,
  localeFromUrl,
  localizeUrl,
  resolveLocale,
  type Catalog,
  type CatalogSet,
  type FoundationMessageKey,
  type I18nFormatters,
  type Locale,
  type LocaleProfileStore,
  type MessageParameters,
  type SerializedI18nState,
} from './src/index.ts'

export * from './src/index.ts'

export const I18N_PROFILE_PAYLOAD_KEY = 'postqron.i18n.profileLocale'
export const POSTQRON_I18N_KEY = Symbol.for('postqron.i18n.runtime')

export interface ReadonlyLocaleState {
  readonly value: Locale
}

export interface PostqronI18nRuntime extends I18nFormatters {
  readonly locale: ReadonlyLocaleState
  registerCatalog<const Reference extends Catalog>(
    namespace: string,
    catalogs: CatalogSet<Reference>,
  ): void
  registerProfileStore(store: LocaleProfileStore): Promise<void>
  setLocale(locale: Locale, currentUrl?: string): Promise<void>
  t(key: FoundationMessageKey, parameters?: MessageParameters): string
  translate(namespacedKey: string, parameters?: MessageParameters): string
}

export function usePostqronI18n(): PostqronI18nRuntime {
  const runtime = inject<PostqronI18nRuntime | undefined>(
    POSTQRON_I18N_KEY,
    undefined,
  )
  if (!runtime) {
    throw new I18nError(
      'I18N_RUNTIME_UNAVAILABLE',
      'The Postqron i18n plugin is not installed',
    )
  }
  return runtime
}

function browserLanguages(): readonly string[] | undefined {
  return typeof navigator === 'undefined' ? undefined : navigator.languages
}

function updatePayload(
  payload: Record<string, unknown>,
  locale: Locale,
  source: SerializedI18nState['source'],
  currentUrl: string,
): void {
  payload[I18N_PAYLOAD_KEY] = Object.freeze({
    locale,
    source,
    htmlLang: locale,
    canonicalUrl: localizeUrl(locale, currentUrl),
  })
}

export default defineNuxtPlugin((nuxtApp) => {
  const router = useRouter()
  for (const [routeIndex, record] of router.getRoutes().entries()) {
    const component = record.components?.default
    if (!component || record.path.startsWith('/:')) {
      continue
    }
    for (const locale of SUPPORTED_LOCALES) {
      const path = `/${locale}${record.path === '/' ? '' : record.path}`
      const name = `postqron-i18n-${locale}-${String(record.name ?? routeIndex)}`
      if (!router.hasRoute(name)) {
        router.addRoute({
          name,
          path,
          component,
          meta: { ...record.meta, postqronLocaleAlias: locale },
          props: record.props.default,
        })
      }
    }
  }

  const route = useRoute()
  const headers = useRequestHeaders(['accept-language'])
  const cookie = useCookie<Locale | undefined>(LOCALE_COOKIE_CONTRACT.name, {
    path: LOCALE_COOKIE_CONTRACT.path,
    sameSite: LOCALE_COOKIE_CONTRACT.sameSite,
    secure: process.env.NODE_ENV === 'production',
    maxAge: LOCALE_COOKIE_CONTRACT.maxAgeSeconds,
  })
  const payloadData = nuxtApp.payload.data as Record<string, unknown>
  const hydratedPayload = payloadData[I18N_PAYLOAD_KEY]
  const initial = hydratedPayload
    ? hydrateI18nState(hydratedPayload)
    : createServerI18nState({
        url: route.fullPath,
        profile: payloadData[I18N_PROFILE_PAYLOAD_KEY],
        cookie: cookie.value,
        acceptLanguage: headers['accept-language'] || browserLanguages(),
      })
  const locale = useState<Locale>('postqron.i18n.locale', () => initial.locale)
  locale.value = initial.locale
  payloadData[I18N_PAYLOAD_KEY] = initial

  const registry = new CatalogRegistry()
  registry.register('foundation', FOUNDATION_CATALOGS)
  const preferences = new LocalePreferenceController({
    cookie: {
      read: () => cookie.value,
      write: (value) => {
        cookie.value = value
      },
    },
  })

  const runtime: PostqronI18nRuntime = {
    locale: readonly(locale),
    registerCatalog(namespace, catalogs) {
      registry.register(namespace, catalogs)
    },
    async registerProfileStore(store) {
      preferences.setProfileStore(store)
      let profile: unknown
      try {
        profile = await preferences.profileValue()
      } catch (error) {
        throw new I18nError(
          'I18N_PROFILE_PERSIST_FAILED',
          'The profile locale preference could not be loaded',
          error,
        )
      }
      const resolved = resolveLocale({
        url: route.fullPath,
        profile,
        cookie: preferences.cookieValue(),
        acceptLanguage: headers['accept-language'] || browserLanguages(),
      })
      locale.value = resolved.locale
      updatePayload(payloadData, resolved.locale, resolved.source, route.fullPath)
      const redirect = canonicalLocaleRedirect(resolved.locale, route.fullPath)
      if (redirect) {
        await navigateTo(redirect, { redirectCode: 302 })
      }
    },
    async setLocale(nextLocale, currentUrl = route.fullPath) {
      const target = await preferences.changeLocale(nextLocale, currentUrl)
      locale.value = nextLocale
      updatePayload(payloadData, nextLocale, 'cookie', target)
      if (target !== currentUrl) {
        await navigateTo(target, { redirectCode: 302 })
      }
    },
    t(key, parameters = {}) {
      return registry.translate(locale.value, `foundation.${key}`, parameters)
    },
    translate(namespacedKey, parameters = {}) {
      return registry.translate(locale.value, namespacedKey, parameters)
    },
    date(value, options = {}) {
      return createFormatters(locale.value).date(value, options)
    },
    number(value, options = {}) {
      return createFormatters(locale.value).number(value, options)
    },
    currency(value, currency, options = {}) {
      return createFormatters(locale.value).currency(value, currency, options)
    },
    timeZone(value, timeZone, options = {}) {
      return createFormatters(locale.value).timeZone(value, timeZone, options)
    },
  }

  nuxtApp.vueApp.provide(POSTQRON_I18N_KEY, runtime)
  nuxtApp.vueApp.component('PostqronLanguageSwitcher', LanguageSwitcher)
  nuxtApp.provide('postqronI18n', runtime)

  useHead(computed(() => ({
    htmlAttrs: {
      lang: locale.value,
    },
  })))

  addRouteMiddleware(
    'postqron-i18n',
    (to) => {
      const explicit = localeFromUrl(to.fullPath)
      if (explicit) {
        locale.value = explicit
        updatePayload(payloadData, explicit, 'url', to.fullPath)
      }
      const redirect = canonicalLocaleRedirect(
        explicit ?? locale.value,
        to.fullPath,
      )
      if (redirect) {
        return navigateTo(redirect, { redirectCode: 302 })
      }
    },
    { global: true },
  )
})
