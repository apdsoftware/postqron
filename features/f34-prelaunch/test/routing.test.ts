import assert from 'node:assert/strict'
import test from 'node:test'
import {
  isExplicitlyAllowedPath,
  prelaunchRouteDecision,
  shouldNoIndex,
  unlocalizedPath,
} from '../src/routing.ts'

test('pre-launch redirects incomplete routes to the selected locale', () => {
  for (const [locale, url] of [
    ['en', '/'],
    ['it', '/it/prezzi?interval=annual'],
    ['es', '/es/app/home'],
    ['fr', '/fr/funzionalita'],
    ['de', '/de/faq'],
  ] as const) {
    const prefix = `/${locale}`
    assert.deepEqual(
      prelaunchRouteDecision({ enabled: true, locale, url }),
      { action: 'redirect', location: `${prefix}/prelaunch` },
    )
  }
})

test('login, callback, landing, support, infrastructure and admin remain allowed', () => {
  for (const path of [
    '/app',
    '/app?return_to=%2Fadmin',
    '/it/app',
    '/de/app/oauth/callback',
    '/fr/app/oauth/callback?code=opaque&state=opaque',
    '/en/prelaunch',
    '/it/prelaunch/access',
    '/es/legal/privacy',
    '/fr/contatti',
    '/de/admin',
    '/api/v1/prelaunch/status',
    '/api/v1/prelaunch/access-requests',
    '/api/v1/admin/session',
    '/api/v1/admin/dashboard',
    '/api/legal/terms',
    '/api/cookie-preferences',
    '/api/features',
    '/api/health',
    '/healthz',
    '/_nuxt/app.js',
    '/brand/logo-primary.svg',
    '/manifest.webmanifest',
    '/robots.txt',
  ]) {
    assert.equal(isExplicitlyAllowedPath(path), true, path)
    assert.deepEqual(
      prelaunchRouteDecision({ enabled: true, locale: 'en', url: path }),
      { action: 'allow' },
      path,
    )
  }
})

test('pre-launch allows only the exact app login and OAuth callback routes', () => {
  for (const path of [
    '/app/',
    '/app/home',
    '/app/onboarding',
    '/app/oauth',
    '/app/oauth/callback/extra',
    '/it/app/home',
    '/de/app/onboarding',
    '/fr/app/oauth/callback/extra',
  ]) {
    assert.equal(isExplicitlyAllowedPath(path), false, path)
    assert.deepEqual(
      prelaunchRouteDecision({ enabled: true, locale: 'it', url: path }),
      { action: 'redirect', location: '/it/prelaunch' },
      path,
    )
  }
})

test('the API allowlist is explicit, not a wildcard: unrelated product APIs stay gated', () => {
  for (const path of [
    '/api/plans',
    '/api/v1/publishing',
    '/api/v1/social-connections',
    '/api/v1/entitlements',
    '/api/v1/workspaces',
    '/api/v1/scheduling',
    '/api/v1/analytics',
    '/it/api/v1/publishing',
  ]) {
    assert.equal(isExplicitlyAllowedPath(path), false, path)
    assert.deepEqual(
      prelaunchRouteDecision({ enabled: true, locale: 'it', url: path }),
      { action: 'redirect', location: '/it/prelaunch' },
      path,
    )
  }
})

test('redirect decisions cannot loop', () => {
  const destination = prelaunchRouteDecision({
    enabled: true,
    locale: 'it',
    url: '/it/prezzi',
  })
  assert.deepEqual(destination, {
    action: 'redirect',
    location: '/it/prelaunch',
  })
  assert.deepEqual(prelaunchRouteDecision({
    enabled: true,
    locale: 'it',
    url: destination.location,
  }), { action: 'allow' })
})

test('go-live retires pre-launch pages without touching the normal site', () => {
  assert.deepEqual(prelaunchRouteDecision({
    enabled: false,
    locale: 'en',
    url: '/en/prelaunch/access',
  }), { action: 'redirect', location: '/en/app' })
  assert.deepEqual(prelaunchRouteDecision({
    enabled: false,
    locale: 'en',
    url: '/prezzi',
  }), { action: 'allow' })
})

test('locale stripping and noindex classification are deterministic', () => {
  assert.equal(unlocalizedPath('/fr/prelaunch/access?done=1'), '/prelaunch/access')
  assert.equal(shouldNoIndex('/de/prelaunch/access'), true)
  assert.equal(shouldNoIndex('/it/app/home'), true)
  assert.equal(shouldNoIndex('/en/prelaunch'), false)
})
