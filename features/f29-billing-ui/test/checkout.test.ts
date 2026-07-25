import assert from 'node:assert/strict'
import test from 'node:test'
import {
  BillingApi,
  checkoutPath,
  checkoutTransition,
  createIdempotencyKey,
  entitlementConfirmed,
  parseCheckoutSession,
  parsePurchaseIntent,
  safePaddleClientToken,
  type BillingOverview,
} from '../src/billing.ts'
import { checkoutActionForPaddleEvent } from '../src/paddle.ts'

test('login return intent preserves plan, interval, and quantity in every locale', () => {
  const intent = parsePurchaseIntent({ plan: 'pro', interval: 'annual', quantity: '25' })
  assert.deepEqual(intent, { plan: 'pro', interval: 'annual', quantity: 25 })
  assert.equal(checkoutPath('en', intent), '/app/billing/checkout?plan=pro&interval=annual&quantity=25')
  assert.equal(checkoutPath('fr', intent), '/fr/app/billing/checkout?plan=pro&interval=annual&quantity=25')
  assert.throws(() => parsePurchaseIntent({ plan: 'internal', interval: 'annual', quantity: '1' }))
  assert.throws(() => parsePurchaseIntent({ plan: 'pro', interval: 'annual', quantity: '51' }))
})

test('checkout uses a stable server intent and never sends client price IDs', async () => {
  const calls: Array<{ path: string, options?: Readonly<Record<string, unknown>> }> = []
  const api = new BillingApi('https://api.postqron.test', async (path, options) => {
    calls.push({ path, options })
    return {
      id: 'txn_01abc', url: 'https://pay.paddle.io/checkout/test',
      expires_at: '2026-07-26T00:00:00Z',
    }
  })
  const key = createIdempotencyKey(() => 'fixed')
  await api.checkout('workspace-1', { plan: 'team', interval: 'monthly', quantity: 12 }, key)
  assert.deepEqual(calls[0]!.options?.body, {
    plan: 'team', interval: 'monthly', channels: 12,
    idempotency_key: 'billing-ui:fixed',
  })
  assert.equal(JSON.stringify(calls[0]).includes('price_id'), false)
})

test('Paddle success stays processing until the webhook entitlement is visible', () => {
  let state = checkoutTransition('idle', 'create')
  state = checkoutTransition(state, 'opened')
  state = checkoutTransition(state, checkoutActionForPaddleEvent({ name: 'checkout.completed' })!)
  assert.equal(state, 'processing')
  state = checkoutTransition(state, 'closed')
  assert.equal(state, 'processing')
  state = checkoutTransition(state, 'entitlement-confirmed')
  assert.equal(state, 'confirmed')
})

test('close, payment failure, retry, invalid sessions, and tokens are safe', () => {
  assert.equal(checkoutActionForPaddleEvent({ name: 'checkout.closed' }), 'closed')
  assert.equal(checkoutActionForPaddleEvent({ name: 'checkout.payment.failed' }), 'payment-failed')
  assert.equal(checkoutTransition('closed', 'retry'), 'idle')
  assert.equal(safePaddleClientToken('test_abc123'), 'test_abc123')
  assert.equal(safePaddleClientToken('live_abc123'), 'live_abc123')
  assert.equal(safePaddleClientToken('pdl_live_apikey'), undefined)
  assert.throws(() => parseCheckoutSession({
    id: 'txn_ok', url: 'https://evil.example/checkout', expires_at: '2026-07-26T00:00:00Z',
  }))
})

test('quantity upgrades require the verified channel entitlement limit', () => {
  const overview = {
    plan: { code: 'pro' }, interval: 'monthly', state: 'active',
    usage: [{ resource: 'channels', limit: 5 }],
  } as BillingOverview
  assert.equal(entitlementConfirmed(overview, { plan: 'pro', interval: 'monthly', quantity: 10 }), false)
  overview.usage[0]!.limit = 10
  assert.equal(entitlementConfirmed(overview, { plan: 'pro', interval: 'monthly', quantity: 10 }), true)
})
