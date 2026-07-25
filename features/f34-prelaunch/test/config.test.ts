import assert from 'node:assert/strict'
import test from 'node:test'
import { resolvePrelaunchMode } from '../src/config.ts'

test('production mode is fail-closed for absent and invalid values', () => {
  for (const value of [undefined, '', 'TRUE', ' false ', '0']) {
    assert.deepEqual(resolvePrelaunchMode(value, 'production'), {
      enabled: true,
      source: 'fail_closed',
    })
  }
})

test('only exact explicit values toggle the mode', () => {
  assert.deepEqual(resolvePrelaunchMode('true', 'production'), {
    enabled: true,
    source: 'explicit_true',
  })
  assert.deepEqual(resolvePrelaunchMode('false', 'production'), {
    enabled: false,
    source: 'explicit_false',
  })
  assert.deepEqual(resolvePrelaunchMode(undefined, 'development'), {
    enabled: false,
    source: 'non_production_default',
  })
})
