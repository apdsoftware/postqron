import assert from 'node:assert/strict'
import test from 'node:test'
import {
  DEFAULT_LOCALE,
  LOCALE_PRECEDENCE,
  SUPPORTED_LOCALES,
  canonicalLocaleRedirect,
  localeFromAcceptLanguage,
  localizeUrl,
  resolveLocale,
  safeLocalizeUrl,
} from '../src/index.ts'

test('the supported locale set is exact and English is the default', () => {
  assert.deepEqual(SUPPORTED_LOCALES, ['en', 'it', 'es', 'fr', 'de'])
  assert.equal(DEFAULT_LOCALE, 'en')
  assert.deepEqual(LOCALE_PRECEDENCE, [
    'url',
    'profile',
    'cookie',
    'browser',
    'fallback',
  ])
})

for (const locale of SUPPORTED_LOCALES) {
  test(`resolves exact and regional browser tags for ${locale}`, () => {
    assert.equal(localeFromAcceptLanguage(locale), locale)
    assert.equal(localeFromAcceptLanguage(`${locale}-CH`), locale)
  })
}

test('Accept-Language respects quality weights, order, exclusions, and fallback', () => {
  assert.equal(
    localeFromAcceptLanguage('it-CH;q=0.4, fr-CA;q=0.9, de;q=0.8'),
    'fr',
  )
  assert.equal(localeFromAcceptLanguage('es;q=0, de;q=0.7'), 'de')
  assert.equal(localeFromAcceptLanguage('pt-BR, nl;q=0.9, *;q=0.8'), undefined)
  assert.equal(localeFromAcceptLanguage('fr;q=0.8, de;q=0.8'), 'fr')
  assert.equal(localeFromAcceptLanguage(['it-IT;q=0.5', 'es;q=0.9']), 'es')
})

test('resolver enforces URL, profile, cookie, browser, English precedence', () => {
  assert.deepEqual(resolveLocale({
    url: '/de/account?tab=profile',
    profile: 'fr',
    cookie: 'es',
    acceptLanguage: 'it',
  }), { locale: 'de', source: 'url' })

  assert.deepEqual(resolveLocale({
    url: '/account',
    profile: 'fr-CA',
    cookie: 'es',
    acceptLanguage: 'it',
  }), { locale: 'fr', source: 'profile' })

  assert.deepEqual(resolveLocale({
    url: '/account',
    profile: 'pt',
    cookie: 'es-MX',
    acceptLanguage: 'it',
  }), { locale: 'es', source: 'cookie' })

  assert.deepEqual(resolveLocale({
    url: '/account',
    cookie: 'pt',
    acceptLanguage: 'it-CH',
  }), { locale: 'it', source: 'browser' })

  assert.deepEqual(resolveLocale({
    url: '/account',
    acceptLanguage: 'pt-BR',
  }), { locale: 'en', source: 'fallback' })
})

test('canonical routing keeps English unprefixed and preserves path and query', () => {
  assert.equal(
    localizeUrl('it', '/account/posts?state=draft&sort=-date'),
    '/it/account/posts?state=draft&sort=-date',
  )
  assert.equal(
    localizeUrl('fr', '/it/account/posts?state=draft#post-2'),
    '/fr/account/posts?state=draft#post-2',
  )
  assert.equal(localizeUrl('en', '/de/account'), '/account')
  assert.equal(canonicalLocaleRedirect('en', '/en/account?q=one'), '/account?q=one')
  assert.equal(canonicalLocaleRedirect('de', '/de/account?q=one'), undefined)
})

test('routing rejects open redirects and degrades safely without a loop', () => {
  assert.throws(
    () => localizeUrl('fr', 'https://evil.example/login'),
    (error: unknown) =>
      error instanceof Error
      && 'code' in error
      && error.code === 'I18N_INVALID_LOCAL_URL',
  )
  assert.throws(() => localizeUrl('fr', '//evil.example/login'))
  assert.throws(() => localizeUrl('fr', '/\\evil.example/login'))
  assert.equal(safeLocalizeUrl('fr', '//evil.example/login'), '/fr')
  assert.equal(canonicalLocaleRedirect('fr', '/fr'), undefined)
})
