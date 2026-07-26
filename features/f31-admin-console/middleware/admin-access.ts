import {
  abortNavigation,
  createError,
} from '#imports'
import {
  normalizeAdminApiError,
} from '../core/api.ts'
import { adminGuardDecision } from '../core/guard.ts'
import {
  useAdminApi,
  useAdminSessionState,
} from '../core/use-admin.ts'

export default defineNuxtRouteMiddleware(async (to) => {
  if (import.meta.server) {
    return
  }

  const sessionState = useAdminSessionState()
  sessionState.value = undefined
  try {
    const session = await useAdminApi().session()
    sessionState.value = session
    return
  } catch (error) {
    const decision = adminGuardDecision({
      destination: to.fullPath,
      error: normalizeAdminApiError(error),
    })
    if (decision.action === 'forbid') {
      return abortNavigation(createError({
        statusCode: 403,
        statusMessage: 'ADMIN_FORBIDDEN',
      }))
    }
    if (decision.action === 'unavailable') {
      return abortNavigation(createError({
        statusCode: 503,
        statusMessage: 'ADMIN_UNAVAILABLE',
      }))
    }
    if (decision.action === 'login') {
      return
    }
  }
})
