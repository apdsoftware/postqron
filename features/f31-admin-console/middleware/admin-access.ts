import {
  abortNavigation,
  createError,
  navigateTo,
  useRequestHeaders,
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
  try {
    const headers = import.meta.server
      ? useRequestHeaders(['cookie'])
      : undefined
    const session = await useAdminApi().session(headers)
    useAdminSessionState().value = session
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
      return navigateTo(decision.location, { redirectCode: 302 })
    }
  }
})
