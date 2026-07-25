import type { AppApiErrorKind } from './api.ts'
import type { AppSession } from './contracts.ts'
import {
  appRoot,
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
  const locale = localeFromAppPath(input.destination)
  const destination = safeAppDestination(input.destination, locale)
  if (!input.session) {
    const login = new URL(appRoot(locale), 'https://postqron.local')
    login.searchParams.set('return_to', destination)
    if (input.failure === 'offline' || input.failure === 'configuration') {
      login.searchParams.set('app_state', 'offline')
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
  if (input.session.onboarding_required && destinationPath !== onboarding) {
    return {
      action: 'redirect',
      location: onboardingDestination(destination),
    }
  }
  if (!input.session.onboarding_required && destinationPath === onboarding) {
    return {
      action: 'redirect',
      location: `${appRoot(locale)}/home`,
    }
  }
  return { action: 'allow' }
}
