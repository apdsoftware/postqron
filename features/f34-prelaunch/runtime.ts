import {
  computed,
  defineNuxtPlugin,
  readonly,
  useHead,
  useRoute,
  useState,
} from '#imports'
import {
  usePostqronI18n,
} from '../f36-i18n/runtime.ts'
import {
  PRELAUNCH_MODE_STATE_KEY,
  resolvePrelaunchMode,
  type PrelaunchMode,
} from './src/config.ts'
import {
  PRELAUNCH_CATALOGS,
  type PrelaunchMessageKey,
} from './src/catalogs.ts'
import { shouldNoIndex } from './src/routing.ts'

const registeredRuntimes = new WeakSet<object>()

export function usePrelaunchMode() {
  return readonly(useState<PrelaunchMode>(
    PRELAUNCH_MODE_STATE_KEY,
    () => resolvePrelaunchMode(
      import.meta.server ? process.env.PRELAUNCH_MODE : undefined,
      process.env.NODE_ENV,
    ),
  ))
}

export function usePrelaunch() {
  const i18n = usePostqronI18n()
  if (!registeredRuntimes.has(i18n)) {
    i18n.registerCatalog('prelaunch', PRELAUNCH_CATALOGS)
    registeredRuntimes.add(i18n)
  }
  return {
    locale: i18n.locale,
    mode: usePrelaunchMode(),
    translate(key: PrelaunchMessageKey) {
      return i18n.translate(`prelaunch.${key}`)
    },
  }
}

export default defineNuxtPlugin(() => {
  const route = useRoute()
  const prelaunch = usePrelaunch()
  useHead(computed(() => ({
    meta: [{
      name: 'robots',
      content: shouldNoIndex(route.fullPath)
        ? 'noindex, nofollow'
        : 'index, follow',
    }],
    bodyAttrs: {
      'data-prelaunch-mode': prelaunch.mode.value.enabled ? 'on' : 'off',
    },
  })))
})
