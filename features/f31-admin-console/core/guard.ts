import type { AdminApiError } from './api.ts'
import type { AdminSession } from './contracts.ts'

export type AdminGuardDecision =
  | { action: 'allow' }
  | { action: 'login', location: string }
  | { action: 'forbid' }
  | { action: 'unavailable' }

export function adminGuardDecision(input: {
  destination: string
  error?: AdminApiError
  session?: AdminSession
}): AdminGuardDecision {
  if (input.session) {
    return { action: 'allow' }
  }
  if (input.error?.status === 403 || input.error?.code === 'ADMIN_FORBIDDEN') {
    return { action: 'forbid' }
  }
  if (input.error?.status !== 401) {
    return { action: 'unavailable' }
  }
  const target = input.destination.startsWith('/')
    && !input.destination.startsWith('//')
    ? input.destination
    : '/admin'
  const parameters = new URLSearchParams({ return_to: target })
  return { action: 'login', location: `/app?${parameters.toString()}` }
}
