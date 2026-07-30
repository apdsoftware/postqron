import type { AppApiErrorKind } from './api.ts'
import type { AppSession } from './contracts.ts'
import {
  appRoot,
  isPublicAccountDeletionCancellationDestination,
  localeFromAppPath,
  onboardingDestination,
  safeAppDestination,
} from './navigation.ts'

export type RouteGuardDecision =
  | { action: 'allow' }
  | { action: 'redirect', location: string }

export function routeGuardDecision(input: {
  destination: string
  failure?: AppApiErrorKind
  session?: AppSession
}): RouteGuardDecision {
  if (isPublicAccountDeletionCancellationDestination(input.destination)) {
    return { action: 'allow' }
  }
  const locale = localeFromAppPath(input.destination)
  const destination = safeAppDestination(input.destination, locale)
  if (
    input.session
    && (
      input.failure === 'offline'
      || input.failure === 'configuration'
      || input.failure === 'unknown'
    )
  ) {
    return { action: 'allow' }
  }
  const session = input.failure === 'session' || input.failure === 'access-denied'
    ? undefined
    : input.session
  if (!session) {
    const login = new URL(appRoot(locale), 'https://postqron.local')
    login.searchParams.set('return_to', destination)
    if (input.failure === 'offline') {
      login.searchParams.set('app_state', 'offline')
    } else if (input.failure === 'configuration' || input.failure === 'unknown') {
      login.searchParams.set('app_state', 'unavailable')
    } else if (input.failure === 'access-denied') {
      login.searchParams.set('app_state', 'access-denied')
    }
    return {
      action: 'redirect',
      location: `${login.pathname}${login.search}`,
    }
  }

  const onboarding = `${appRoot(locale)}/onboarding`
  const destinationPath = new URL(
    destination,
    'https://postqron.local',
  ).pathname
  if (session.onboarding_required && destinationPath !== onboarding) {
    return {
      action: 'redirect',
      location: onboardingDestination(destination),
    }
  }
  if (!session.onboarding_required && destinationPath === onboarding) {
    return {
      action: 'redirect',
      location: `${appRoot(locale)}/home`,
    }
  }
  return { action: 'allow' }
}
