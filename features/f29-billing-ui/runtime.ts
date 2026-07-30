import {
  addRouteMiddleware,
  defineNuxtPlugin,
  navigateTo,
  useRoute,
  useState,
} from '#imports'
/* global HTMLAnchorElement */
import './components/billing-ui.css'
import { localeFromPath } from '../f02-marketing-site/src/catalog.ts'
import {
  checkoutPath,
  parsePurchaseIntent,
  safePaddleClientToken,
} from './src/billing.ts'
import { BILLING_UI_CATALOGS } from './src/catalogs.ts'
import { BILLING_PLAN_CATALOGS } from './src/plan-catalogs.ts'

export default defineNuxtPlugin((nuxtApp) => {
  const i18n = (nuxtApp as typeof nuxtApp & {
    $postqronI18n?: {
      registerCatalog(
        namespace: string,
        catalogs: typeof BILLING_UI_CATALOGS | typeof BILLING_PLAN_CATALOGS,
      ): void
    }
  }).$postqronI18n
  if (!i18n) {
    throw new Error('BILLING_UI_I18N_RUNTIME_UNAVAILABLE')
  }
  i18n.registerCatalog('billingUI', BILLING_UI_CATALOGS)
  i18n.registerCatalog('billingPlans', BILLING_PLAN_CATALOGS)

  if (import.meta.client) {
    const route = useRoute()
    nuxtApp.hook('page:finish', () => {
      const billingRoute = /(?:^|\/)app\/billing(?:\/|$)/u.test(
        route.path,
      )
      for (const link of globalThis.document.querySelectorAll<HTMLAnchorElement>(
        '.product-sidebar nav a[href]',
      )) {
        const planLink = /(?:^|\/)app\/plan\/?$/u.test(
          link.getAttribute('href') ?? '',
        )
        if (billingRoute && planLink) {
          link.setAttribute('aria-current', 'page')
          link.dataset.postqronBillingCurrent = 'true'
        } else if (link.dataset.postqronBillingCurrent === 'true') {
          link.removeAttribute('aria-current')
          delete link.dataset.postqronBillingCurrent
        }
      }
    })
  }

  useState<string | undefined>(
    'postqron.billing-ui.paddle-client-token',
    () => import.meta.server
      ? safePaddleClientToken(process.env.NUXT_PUBLIC_PADDLE_CLIENT_TOKEN)
      : undefined,
  )

  addRouteMiddleware(
    'billing-plan-entry',
    (to) => {
      if (!/(?:^|\/)app\/plan\/?$/u.test(to.path)) {
        return
      }
      const locale = localeFromPath(to.fullPath)
      const prefix = locale === 'en' ? '/en' : `/${locale}`
      return navigateTo({
        path: `${prefix}/app/billing`,
        query: to.query,
        hash: to.hash,
      }, { replace: true })
    },
    { global: true },
  )

  addRouteMiddleware(
    'billing-purchase-intent',
    (to) => {
      if (!/(?:^|\/)app\/home$/u.test(to.path) || !('plan' in to.query)) {
        return
      }
      try {
        return navigateTo(
          checkoutPath(localeFromPath(to.fullPath), parsePurchaseIntent(to.query)),
          { replace: true },
        )
      } catch {
        return navigateTo(
          `${localeFromPath(to.fullPath) === 'en' ? '' : `/${localeFromPath(to.fullPath)}`}/app/billing/checkout?invalid=1`,
          { replace: true },
        )
      }
    },
    { global: true },
  )
})
