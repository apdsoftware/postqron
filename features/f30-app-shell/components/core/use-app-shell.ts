import {
  computed,
  useNuxtApp,
  useRuntimeConfig,
  useState,
} from '#imports'
import { AppShellApi, type AppFetch } from './api.ts'
import type {
  AppBootstrap,
  AppSession,
} from './contracts.ts'
import type {
  AppShellMessageKey,
} from './catalogs.ts'

interface I18nRuntime {
  locale: { readonly value: string }
  translate(
    key: string,
    parameters?: Readonly<Record<string, string | number>>,
  ): string
}

export function useAppShellI18n(): {
  locale: Readonly<{ value: string }>
  t(
    key: AppShellMessageKey,
    parameters?: Readonly<Record<string, string | number>>,
  ): string
} {
  const nuxtApp = useNuxtApp() as ReturnType<typeof useNuxtApp> & {
    $postqronI18n: I18nRuntime
  }
  return {
    locale: computed(() => nuxtApp.$postqronI18n.locale.value),
    t: (key, parameters = {}) =>
      nuxtApp.$postqronI18n.translate(`appShell.${key}`, parameters),
  }
}

export function useAppSessionState() {
  return useState<AppSession | undefined>(
    'postqron.app-shell.session',
    () => undefined,
  )
}

export function useAppBootstrapState() {
  return useState<AppBootstrap | undefined>(
    'postqron.app-shell.bootstrap',
    () => undefined,
  )
}

export function useAppShellApi(): AppShellApi {
  const config = useRuntimeConfig()
  const nuxtApp = useNuxtApp()
  return new AppShellApi(
    String(config.public.apiBase),
    nuxtApp.$fetch as unknown as AppFetch,
  )
}
