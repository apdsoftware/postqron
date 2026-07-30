import {
  computed,
  useNuxtApp,
  useRequestFetch,
  useRuntimeConfig,
  useState,
} from '#imports'
import {
  appServiceStateFromError,
  AppShellApi,
  resolveAppShellApiBase,
  type AppFetch,
} from './api.ts'
import { SocialConnectionsApi } from './social-api.ts'
import {
  ComposerApi,
  SchedulingApi,
} from './editorial-api.ts'
import type {
  AccountArea,
  AppBootstrap,
  AppSession,
  DeletionStatus,
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

export function useAppAccountAreaState() {
  return useState<AccountArea | undefined>(
    'postqron.app-shell.account-area',
    () => undefined,
  )
}

export interface AccountDeletionCancellationState {
  graceEndsAt: string
  requestId: string
  status: DeletionStatus
}

export function useAccountDeletionCancellationState() {
  return useState<AccountDeletionCancellationState | undefined>(
    'postqron.app-shell.account-deletion-cancellation',
    () => undefined,
  )
}

export function useAppShellApi(): AppShellApi {
  const config = useRuntimeConfig()
  const requestFetch = useRequestFetch()
  return new AppShellApi(
    resolveAppShellApiBase(config, import.meta.server),
    requestFetch as unknown as AppFetch,
  )
}

export function useSocialConnectionsApi(): SocialConnectionsApi {
  const config = useRuntimeConfig()
  const requestFetch = useRequestFetch()
  return new SocialConnectionsApi(
    resolveAppShellApiBase(config, import.meta.server),
    requestFetch as unknown as AppFetch,
  )
}

export function useComposerApi(): ComposerApi {
  const config = useRuntimeConfig()
  const requestFetch = useRequestFetch()
  return new ComposerApi(
    resolveAppShellApiBase(config, import.meta.server),
    requestFetch as unknown as AppFetch,
  )
}

export function useSchedulingApi(): SchedulingApi {
  const config = useRuntimeConfig()
  const requestFetch = useRequestFetch()
  return new SchedulingApi(
    resolveAppShellApiBase(config, import.meta.server),
    requestFetch as unknown as AppFetch,
  )
}

export function appStateKindFromError(
  error: unknown,
): 'access-denied' | 'offline' | 'unavailable' {
  return appServiceStateFromError(error)
}
