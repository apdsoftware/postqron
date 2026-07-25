import assert from 'node:assert/strict'
import test from 'node:test'
import {
  I18N_PAYLOAD_KEY,
  LOCALE_COOKIE_CONTRACT,
  LocalePreferenceController,
  createServerI18nState,
  hydrateI18nState,
  type Locale,
} from '../src/index.ts'

class CookieStore {
  value?: Locale

  read(): Locale | undefined {
    return this.value
  }

  write(locale: Locale): void {
    this.value = locale
  }
}

class ProfileStore {
  readonly authenticated: boolean
  value?: Locale
  writes = 0

  constructor(authenticated: boolean) {
    this.authenticated = authenticated
  }

  isAuthenticated(): boolean {
    return this.authenticated
  }

  read(): Locale | undefined {
    return this.value
  }

  write(locale: Locale): void {
    this.writes += 1
    this.value = locale
  }
}

test('manual choice persists in functional cookie and authenticated profile', async () => {
  const cookie = new CookieStore()
  const profile = new ProfileStore(true)
  const preferences = new LocalePreferenceController({ cookie, profile })

  const target = await preferences.changeLocale(
    'fr',
    '/it/calendar?view=week',
  )

  assert.equal(target, '/fr/calendar?view=week')
  assert.equal(cookie.value, 'fr')
  assert.equal(profile.value, 'fr')
  assert.equal(profile.writes, 1)
})

test('anonymous manual choice persists only in the functional cookie', async () => {
  const cookie = new CookieStore()
  const profile = new ProfileStore(false)
  const preferences = new LocalePreferenceController({ cookie, profile })

  assert.equal(await preferences.changeLocale('de', '/pricing?q=team'), '/de/pricing?q=team')
  assert.equal(cookie.value, 'de')
  assert.equal(profile.value, undefined)
  assert.equal(profile.writes, 0)
  assert.deepEqual(LOCALE_COOKIE_CONTRACT, {
    name: 'postqron_locale',
    classification: 'necessary_functional',
    purpose: 'Remember the language explicitly selected by the user',
    consentRequired: false,
    containsPersonalData: false,
    path: '/',
    sameSite: 'lax',
    secureInProduction: true,
    httpOnly: false,
    maxAgeSeconds: 31_536_000,
  })
})

test('SSR serializes one locale and hydration reuses it without browser re-resolution', () => {
  const server = createServerI18nState({
    url: '/dashboard?range=month',
    acceptLanguage: 'es-MX, en;q=0.8',
  })
  const payload = { [I18N_PAYLOAD_KEY]: server }
  const hydrated = hydrateI18nState(payload[I18N_PAYLOAD_KEY])

  assert.deepEqual(server, {
    locale: 'es',
    source: 'browser',
    htmlLang: 'es',
    canonicalUrl: '/es/dashboard?range=month',
  })
  assert.deepEqual(hydrated, server)
  assert.equal(hydrated.htmlLang, hydrated.locale)
})

test('invalid hydration payload fails with a stable technical error code', () => {
  assert.throws(
    () => hydrateI18nState({
      locale: 'pt',
      source: 'browser',
      htmlLang: 'pt',
      canonicalUrl: '/pt',
    }),
    (error: unknown) =>
      error instanceof Error
      && 'code' in error
      && error.code === 'I18N_INVALID_SSR_STATE',
  )
})
