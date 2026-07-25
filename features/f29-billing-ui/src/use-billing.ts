import {
  computed,
  useNuxtApp,
  useRequestFetch,
  useRuntimeConfig,
  useState,
} from '#imports'
import type { PricingLocale } from '../../f02-marketing-site/src/catalog.ts'
import { BillingApi, type BillingFetch } from './billing.ts'
import type { BillingUIMessageKey } from './catalogs.ts'

interface I18nRuntime {
  locale: { readonly value: PricingLocale }
  date(value: Date | number | string, options?: Intl.DateTimeFormatOptions): string
  number(value: number, options?: Intl.NumberFormatOptions): string
  translate(
    key: string,
    parameters?: Readonly<Record<string, string | number>>,
  ): string
}

export function useBillingI18n(): {
  locale: Readonly<{ value: PricingLocale }>
  date(value: Date | number | string): string
  number(value: number): string
  t(
    key: BillingUIMessageKey,
    parameters?: Readonly<Record<string, string | number>>,
  ): string
} {
  const nuxtApp = useNuxtApp() as ReturnType<typeof useNuxtApp> & {
    $postqronI18n: I18nRuntime
  }
  const runtime = nuxtApp.$postqronI18n
  return {
    locale: computed(() => runtime.locale.value),
    date: value => runtime.date(value, { dateStyle: 'medium' }),
    number: value => runtime.number(value),
    t: (key, parameters = {}) =>
      runtime.translate(`billingUI.${key}`, parameters),
  }
}

export function useBillingApi(): BillingApi {
  const config = useRuntimeConfig()
  const requestFetch = useRequestFetch()
  return new BillingApi(
    String(config.public.apiBase),
    requestFetch as unknown as BillingFetch,
  )
}

export function usePaddleClientToken() {
  return useState<string | undefined>(
    'postqron.billing-ui.paddle-client-token',
    () => undefined,
  )
}
