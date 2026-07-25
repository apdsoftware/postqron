import assert from 'node:assert/strict'
import test from 'node:test'
import {
  translateCatalog,
  validateCatalogs,
} from '../../f36-i18n/src/catalog.ts'
import { SUPPORTED_LOCALES } from '../../f36-i18n/src/locales.ts'
import { localizeUrl } from '../../f36-i18n/src/routing.ts'
import { SUPPORT_CONTACT_CATALOGS } from '../src/catalogs.ts'
import {
  DEFAULT_SUPPORT_EMAIL,
  SUPPORT_RESPONSE_BUSINESS_DAYS,
} from '../src/config.ts'

test('contact page and complete footer catalogs exist for all five locales', () => {
  validateCatalogs(SUPPORT_CONTACT_CATALOGS)
  assert.deepEqual(Object.keys(SUPPORT_CONTACT_CATALOGS).sort(), [
    'de',
    'en',
    'es',
    'fr',
    'it',
  ])

  const referenceKeys = Object.keys(SUPPORT_CONTACT_CATALOGS.en).sort()
  for (const locale of SUPPORTED_LOCALES) {
    assert.deepEqual(
      Object.keys(SUPPORT_CONTACT_CATALOGS[locale]).sort(),
      referenceKeys,
    )
    assert.ok(referenceKeys.some(key => key.startsWith('footer.')))
    assert.ok(referenceKeys.some(key => key.startsWith('page.')))
  }
})

test('email and response objective stay invariant while user-facing copy is translated', () => {
  const timings = SUPPORTED_LOCALES.map(locale =>
    translateCatalog(
      SUPPORT_CONTACT_CATALOGS[locale],
      locale,
      'page.responseTiming',
      { count: SUPPORT_RESPONSE_BUSINESS_DAYS },
    ))
  assert.equal(new Set(timings).size, SUPPORTED_LOCALES.length)
  assert.equal(DEFAULT_SUPPORT_EMAIL, 'help@postqron.com')
  assert.equal(SUPPORT_RESPONSE_BUSINESS_DAYS, 1)
})

test('the contact route is available at the canonical path for every locale', () => {
  assert.deepEqual(
    SUPPORTED_LOCALES.map(locale => localizeUrl(locale, '/contatti')),
    [
      '/contatti',
      '/it/contatti',
      '/es/contatti',
      '/fr/contatti',
      '/de/contatti',
    ],
  )
})
