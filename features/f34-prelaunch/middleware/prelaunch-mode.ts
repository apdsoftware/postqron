import {
  defineNuxtRouteMiddleware,
  navigateTo,
} from '#imports'
import { usePostqronI18n } from '../../f36-i18n/runtime.ts'
import { usePrelaunchMode } from '../runtime.ts'
import { prelaunchRouteDecision } from '../src/routing.ts'

export default defineNuxtRouteMiddleware((to) => {
  const mode = usePrelaunchMode()
  const locale = usePostqronI18n().locale.value
  const decision = prelaunchRouteDecision({
    enabled: mode.value.enabled,
    locale,
    url: to.fullPath,
  })
  if (decision.action === 'redirect' && decision.location !== to.path) {
    return navigateTo(decision.location, { redirectCode: 302 })
  }
})
