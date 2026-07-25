import {
  addRouteMiddleware,
  defineNuxtPlugin,
  navigateTo,
  useState,
} from '#imports'
import './components/billing-ui.css'
import { localeFromPath } from '../f02-marketing-site/src/catalog.ts'
import {
  checkoutPath,
  parsePurchaseIntent,
  safePaddleClientToken,
} from './src/billing.ts'
import { BILLING_UI_CATALOGS } from './src/catalogs.ts'

export default defineNuxtPlugin((nuxtApp) => {
  const i18n = (nuxtApp as typeof nuxtApp & {
    $postqronI18n?: {
      registerCatalog(namespace: string, catalogs: typeof BILLING_UI_CATALOGS): void
    }
  }).$postqronI18n
  if (!i18n) {
    throw new Error('BILLING_UI_I18N_RUNTIME_UNAVAILABLE')
  }
  i18n.registerCatalog('billingUI', BILLING_UI_CATALOGS)

  useState<string | undefined>(
    'postqron.billing-ui.paddle-client-token',
    () => import.meta.server
      ? safePaddleClientToken(process.env.NUXT_PUBLIC_PADDLE_CLIENT_TOKEN)
      : undefined,
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
