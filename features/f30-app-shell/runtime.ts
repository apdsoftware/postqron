import { defineNuxtPlugin } from '#imports'
import './components/app-shell.css'
import { APP_SHELL_CATALOGS } from './components/core/catalogs.ts'

export const APP_SHELL_SLOTS = Object.freeze([
  'primary-navigation',
  'workspace-actions',
  'home-summary',
  'home-primary',
  'home-secondary',
  'feature-content',
] as const)

export default defineNuxtPlugin((nuxtApp) => {
  const i18n = (nuxtApp as typeof nuxtApp & {
    $postqronI18n?: {
      registerCatalog(namespace: string, catalogs: typeof APP_SHELL_CATALOGS): void
    }
  }).$postqronI18n
  if (!i18n) {
    throw new Error('APP_SHELL_I18N_RUNTIME_UNAVAILABLE')
  }
  i18n.registerCatalog('appShell', APP_SHELL_CATALOGS)

  nuxtApp.provide('postqronAppShell', Object.freeze({
    slots: APP_SHELL_SLOTS,
    version: '0.2.0',
  }))
})
