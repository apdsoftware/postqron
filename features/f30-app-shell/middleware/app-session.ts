import {
  navigateTo,
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
  // The API owns the host-only __Host- session cookie. On cross-origin
  // deployments the web SSR host cannot receive or forward that cookie.
  if (import.meta.server) {
    return
  }
  const sessionState = useAppSessionState()
  try {
    const session = await useAppShellApi().session()
    sessionState.value = session
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
      session: sessionState.value,
    })
    if (failure.kind === 'session') {
      sessionState.value = undefined
    }
    if (decision.action === 'allow') {
      return
    }
    return navigateTo(
      decision.location,
      {
      redirectCode: 302,
      },
    )
  }
})
