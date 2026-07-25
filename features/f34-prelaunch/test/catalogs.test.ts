import assert from 'node:assert/strict'
import test from 'node:test'
import { PRELAUNCH_CATALOGS } from '../src/catalogs.ts'

test('all five catalogs have identical complete keys', () => {
  const locales = ['en', 'it', 'es', 'fr', 'de'] as const
  const reference = Object.keys(PRELAUNCH_CATALOGS.en).sort()
  for (const locale of locales) {
    assert.deepEqual(Object.keys(PRELAUNCH_CATALOGS[locale]).sort(), reference)
    for (const [key, value] of Object.entries(PRELAUNCH_CATALOGS[locale])) {
      assert.equal(typeof value, 'string', `${locale}.${key}`)
      assert.notEqual(value.trim(), '', `${locale}.${key}`)
    }
  }
})

test('localized access copy explicitly separates the request from marketing', () => {
  for (const locale of ['en', 'it', 'es', 'fr', 'de'] as const) {
    assert.match(
      PRELAUNCH_CATALOGS[locale]['access.emailHelp'],
      /marketing/iu,
    )
  }
})
