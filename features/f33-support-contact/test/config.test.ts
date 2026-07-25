import assert from 'node:assert/strict'
import test from 'node:test'
import {
  DEFAULT_SUPPORT_EMAIL,
  SUPPORT_RESPONSE_BUSINESS_DAYS,
  SupportContactConfigError,
  resolveSupportContactConfig,
  supportMailto,
} from '../src/config.ts'

test('support contact configuration has a validated default', () => {
  assert.deepEqual(resolveSupportContactConfig(), {
    email: DEFAULT_SUPPORT_EMAIL,
    responseBusinessDays: SUPPORT_RESPONSE_BUSINESS_DAYS,
  })
  assert.deepEqual(resolveSupportContactConfig('  '), {
    email: DEFAULT_SUPPORT_EMAIL,
    responseBusinessDays: SUPPORT_RESPONSE_BUSINESS_DAYS,
  })
  assert.equal(supportMailto(DEFAULT_SUPPORT_EMAIL), 'mailto:help@postqron.com')
})

test('a valid configured address replaces the default without changing operations data', () => {
  const configured = resolveSupportContactConfig('support@example.org')
  assert.equal(configured.email, 'support@example.org')
  assert.equal(
    configured.responseBusinessDays,
    SUPPORT_RESPONSE_BUSINESS_DAYS,
  )
  assert.equal(supportMailto(configured.email), 'mailto:support@example.org')
  assert.equal(Object.isFrozen(configured), true)
})

test('malformed or header-injection values fail with a stable configuration error', () => {
  for (const value of [
    'not-an-email',
    'support@localhost',
    'support@example.org\nBcc: attacker@example.org',
    'support..contact@example.org',
    42,
  ]) {
    assert.throws(
      () => resolveSupportContactConfig(value),
      (error: unknown) =>
        error instanceof SupportContactConfigError
        && error.code === 'SUPPORT_CONTACT_INVALID_EMAIL',
    )
  }
})
