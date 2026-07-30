import assert from 'node:assert/strict'
import test from 'node:test'
import type { AppSession } from '../components/core/contracts.ts'
import { routeGuardDecision } from '../components/core/guard.ts'
import {
  AppNavigationError,
  accountDeletionCancellationRoute,
  authenticatedDestination,
  isPublicAccountDeletionCancellationDestination,
  legacyProviderManagementRedirect,
  sanitizeAppDestination,
} from '../components/core/navigation.ts'

const session: AppSession = {
  account: {
    id: 'account-1',
    display_name: 'Ada',
    email: 'ada@example.test',
    locale: 'en',
  },
  onboarding_required: false,
  current_workspace: {
    id: 'workspace-1',
    name: 'Ada Studio',
    role: 'owner',
  },
  workspaces: [{
    id: 'workspace-1',
    name: 'Ada Studio',
    role: 'owner',
  }],
}

const localePrefixes = ['/en', '/it', '/es', '/fr', '/de']

for (const prefix of localePrefixes) {
  test(`preserves a validated purchase intent through ${prefix || 'en'} authentication`, () => {
    const target = `${prefix}/app?plan=pro&interval=annual&quantity=12`
    assert.equal(sanitizeAppDestination(target), target)
    assert.equal(
      authenticatedDestination(target, false),
      `${prefix}/app/home?plan=pro&interval=annual&quantity=12`,
    )
  })

  test(`guards anonymous and authenticated ${prefix || 'en'} routes`, () => {
    const target = `${prefix}/app/home?view=week`
    assert.deepEqual(
      routeGuardDecision({ destination: target, failure: 'session' }),
      {
        action: 'redirect',
        location: `${prefix}/app?return_to=${encodeURIComponent(target)}`,
      },
    )
    assert.deepEqual(
      routeGuardDecision({ destination: target, session }),
      { action: 'allow' },
    )
  })

  test(`keeps account deletion cancellation public in ${prefix || 'en'}`, () => {
    const locale = prefix.slice(1) as 'en' | 'it' | 'es' | 'fr' | 'de'
    const target = accountDeletionCancellationRoute(locale, 'deletion-219')
    assert.equal(
      target,
      `${prefix}/app/account-deletions/deletion-219/cancel`,
    )
    assert.equal(
      isPublicAccountDeletionCancellationDestination(target),
      true,
    )
    assert.deepEqual(
      routeGuardDecision({ destination: target, failure: 'session' }),
      { action: 'allow' },
    )
  })

  test(`redirects the retired provider-management destination in ${prefix || 'en'}`, () => {
    assert.equal(
      legacyProviderManagementRedirect(`${prefix}/app/providers?from=bookmark`),
      `${prefix}/app/security`,
    )
    assert.equal(
      legacyProviderManagementRedirect(`${prefix}/app/security`),
      undefined,
    )
  })
}

test('legacy provider redirect defaults an unlocalized request to English security', () => {
  assert.equal(
    legacyProviderManagementRedirect('/app/providers'),
    '/en/app/security',
  )
})

test('preserves the public unlimited purchase intent without quantity', () => {
  const target = '/en/app?plan=unlimited&interval=annual'
  assert.equal(sanitizeAppDestination(target), target)
  assert.equal(
    authenticatedDestination(target, false),
    '/en/app/home?plan=unlimited&interval=annual',
  )
})

test('new accounts are sent to onboarding with the exact safe destination', () => {
  const target = '/fr/app/home?view=week'
  const onboardingSession = { ...session, onboarding_required: true }
  assert.deepEqual(
    routeGuardDecision({ destination: target, session: onboardingSession }),
    {
      action: 'redirect',
      location: `/fr/app/onboarding?return_to=${encodeURIComponent(target)}`,
    },
  )
})

test('offline, unavailable, and denied guards expose only stable application state', () => {
  assert.deepEqual(
    routeGuardDecision({
      destination: '/de/app/home',
      failure: 'configuration',
    }),
    {
      action: 'redirect',
      location: '/de/app?return_to=%2Fde%2Fapp%2Fhome&app_state=unavailable',
    },
  )
  assert.deepEqual(
    routeGuardDecision({
      destination: '/app/home',
      failure: 'access-denied',
    }),
    {
      action: 'redirect',
      location: '/en/app?return_to=%2Fapp%2Fhome&app_state=access-denied',
    },
  )
})

test('transport and service failures preserve a previously validated session', () => {
  assert.deepEqual(
    routeGuardDecision({
      destination: '/it/app/profile',
      failure: 'offline',
      session,
    }),
    { action: 'allow' },
  )
  assert.deepEqual(
    routeGuardDecision({
      destination: '/it/app/profile',
      failure: 'unknown',
      session,
    }),
    { action: 'allow' },
  )
  assert.deepEqual(
    routeGuardDecision({
      destination: '/it/app/profile',
      failure: 'session',
      session,
    }),
    {
      action: 'redirect',
      location: '/it/app?return_to=%2Fit%2Fapp%2Fprofile',
    },
  )
})

for (const target of [
  'https://evil.example/app',
  '//evil.example/app',
  '/pricing',
  '/app?plan=enterprise&interval=monthly',
  '/app?plan=pro&interval=weekly',
  '/app?plan=unlimited&interval=monthly&quantity=1',
  '/app?plan=team&interval=monthly&quantity=9007199254740992',
  '/app?plan=team&interval=monthly&quantity=1.5',
  '/app?plan=team&interval=monthly&admin=true',
]) {
  test(`rejects manipulated destination ${target}`, () => {
    assert.throws(
      () => sanitizeAppDestination(target),
      (error: unknown) =>
        error instanceof AppNavigationError
        && (
          error.code === 'APP_INVALID_DESTINATION'
          || error.code === 'APP_INVALID_PURCHASE_INTENT'
        ),
    )
  })
}
