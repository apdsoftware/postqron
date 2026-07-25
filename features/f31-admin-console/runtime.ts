import { defineNuxtPlugin } from '#imports'
import './admin-console.css'
import { ADMIN_CATALOGS } from './core/catalogs.ts'

export default defineNuxtPlugin((nuxtApp) => {
  const i18n = (nuxtApp as typeof nuxtApp & {
    $postqronI18n?: {
      registerCatalog(namespace: string, catalogs: typeof ADMIN_CATALOGS): void
    }
  }).$postqronI18n
  if (!i18n) {
    throw new Error('ADMIN_I18N_RUNTIME_UNAVAILABLE')
  }
  i18n.registerCatalog('admin', ADMIN_CATALOGS)
})
