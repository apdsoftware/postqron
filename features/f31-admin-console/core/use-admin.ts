import {
  computed,
  useNuxtApp,
  useRuntimeConfig,
  useState,
} from '#imports'
import { AdminApi, type AdminFetch } from './api.ts'
import type {
  AdminDashboard,
  AdminSession,
  SearchResults,
} from './contracts.ts'
import type { AdminMessageKey } from './catalogs.ts'

interface I18nRuntime {
  locale: { readonly value: string }
  date(value: string | number | Date, options?: Intl.DateTimeFormatOptions): string
  translate(
    key: string,
    parameters?: Readonly<Record<string, string | number>>,
  ): string
}

export function useAdminApi(): AdminApi {
  const config = useRuntimeConfig()
  return new AdminApi(
    String(config.public.apiBase),
    globalThis.$fetch as unknown as AdminFetch,
  )
}

export function useAdminSessionState() {
  return useState<AdminSession | undefined>(
    'postqron.admin.session',
    () => undefined,
  )
}

export function useAdminDashboardState() {
  return useState<AdminDashboard | undefined>(
    'postqron.admin.dashboard',
    () => undefined,
  )
}

export function useAdminSearchState() {
  return useState<SearchResults | undefined>(
    'postqron.admin.search',
    () => undefined,
  )
}

export function useAdminI18n() {
  const nuxtApp = useNuxtApp() as ReturnType<typeof useNuxtApp> & {
    $postqronI18n: I18nRuntime
  }
  return {
    locale: computed(() => nuxtApp.$postqronI18n.locale.value),
    date: (value: string) => nuxtApp.$postqronI18n.date(value, {
      dateStyle: 'medium',
      timeStyle: 'short',
    }),
    t: (
      key: AdminMessageKey,
      parameters: Readonly<Record<string, string | number>> = {},
    ) => nuxtApp.$postqronI18n.translate(`admin.${key}`, parameters),
  }
}
