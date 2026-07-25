import assert from 'node:assert/strict'
import test from 'node:test'
import { BILLING_NOTIFICATION_BOUNDARY } from '../src/billing.ts'
import { BILLING_UI_CATALOGS } from '../src/catalogs.ts'

test('billing and checkout copy is complete in en/it/es/fr/de with English fallback contract', () => {
  const keys = Object.keys(BILLING_UI_CATALOGS.en).sort()
  assert.deepEqual(Object.keys(BILLING_UI_CATALOGS), ['en', 'it', 'es', 'fr', 'de'])
  for (const catalog of Object.values(BILLING_UI_CATALOGS)) {
    assert.deepEqual(Object.keys(catalog).sort(), keys)
    assert.ok(Object.values(catalog).every(Boolean))
  }
})

test('Mailronix lifecycle notifications and Paddle receipts have a non-duplicating boundary', () => {
  assert.equal(BILLING_NOTIFICATION_BOUNDARY.uiEmitsEmail, false)
  assert.deepEqual(BILLING_NOTIFICATION_BOUNDARY.mailronixEvents, [
    'f14.payment_failed.v1',
    'f14.plan_changed.v1',
    'f14.plan_cancelled.v1',
    'f14.grace_period.v1',
  ])
  assert.deepEqual(BILLING_NOTIFICATION_BOUNDARY.paddleOwns, [
    'fiscal_receipt',
    'mandatory_payment_notice',
  ])
})
