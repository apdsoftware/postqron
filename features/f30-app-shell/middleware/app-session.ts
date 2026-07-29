import {
  navigateTo,
  useRequestHeaders,
} from '#imports'
import { normalizeAppApiError } from '../components/core/api.ts'
import {
  routeGuardDecision,
} from '../components/core/guard.ts'
import {
  isPublicAccountDeletionCancellationDestination,
} from '../components/core/navigation.ts'
import {
  useAppSessionState,
  useAppShellApi,
} from '../components/core/use-app-shell.ts'

export default defineNuxtRouteMiddleware(async (to) => {
  if (isPublicAccountDeletionCancellationDestination(to.fullPath)) {
    return
  }
  try {
    const headers = import.meta.server
      ? useRequestHeaders(['cookie'])
      : undefined
    const session = await useAppShellApi().session(headers)
    useAppSessionState().value = session
    const decision = routeGuardDecision({
      destination: to.fullPath,
      session,
    })
    if (decision.action === 'redirect') {
      return navigateTo(decision.location, { redirectCode: 302 })
    }
  } catch (error) {
    const failure = normalizeAppApiError(error)
    const decision = routeGuardDecision({
      destination: to.fullPath,
      failure: failure.kind,
    })
    return navigateTo(
      decision.action === 'redirect' ? decision.location : '/app',
      {
      redirectCode: 302,
      },
    )
  }
})
