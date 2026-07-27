import assert from 'node:assert/strict'
import test from 'node:test'
import {
  annualCheckoutSummary,
  BillingApi,
  checkoutPath,
  checkoutTransition,
  createIdempotencyKey,
  entitlementConfirmed,
  intentCompatibleWithPlan,
  parseCheckoutSession,
  parsePurchaseIntent,
  safePaddleClientToken,
  type BillingOverview,
} from '../src/billing.ts'
import { checkoutActionForPaddleEvent } from '../src/paddle.ts'
import type { PublicPlan } from '../../f02-marketing-site/src/catalog.ts'

const money = (amount_cents: number) => ({ amount_cents, currency: 'EUR' as const })
const proPlan: PublicPlan = {
  code: 'pro', name: 'Pro', purchasable: true,
  prices: { monthly: money(450), annual: money(4500) },
  price_tiers: [{ from_channel: 1, to_channel: 10, monthly: money(450), annual: money(4500) }],
  limits: { members: 3, channels: 6, scheduled_publications: 250, scheduled_publications_per_channel: 250 },
}
const startPlan: PublicPlan = {
  code: 'start', name: 'Start', purchasable: false,
  prices: { monthly: money(0), annual: money(0) }, price_tiers: [],
  limits: { members: 1, channels: 3, scheduled_publications: 10, scheduled_publications_per_channel: 10 },
}
const unlimitedPlan: PublicPlan = {
  code: 'unlimited', name: 'Unlimited', purchasable: true,
  prices: { monthly: money(12_900), annual: money(129_000) }, price_tiers: [],
  limits: { members: null, channels: null, scheduled_publications: null, scheduled_publications_per_channel: null },
}

test('login return intent preserves plan, interval, and quantity in every locale', () => {
  const intent = parsePurchaseIntent({ plan: 'pro', interval: 'annual', quantity: '6' })
  assert.deepEqual(intent, { plan: 'pro', interval: 'annual', quantity: 6 })
  assert.equal(checkoutPath('en', intent), '/app/billing/checkout?plan=pro&interval=annual&quantity=6')
  assert.equal(checkoutPath('fr', intent), '/fr/app/billing/checkout?plan=pro&interval=annual&quantity=6')
  assert.throws(() => parsePurchaseIntent({ plan: 'internal', interval: 'annual', quantity: '1' }))
  assert.throws(() => parsePurchaseIntent({ plan: 'pro', interval: 'annual', quantity: '0' }))
  assert.throws(() => parsePurchaseIntent({ plan: 'pro', interval: 'annual', quantity: 'abc' }))
})

test('parsing a purchase intent never hardcodes a per-plan quantity ceiling: the catalog decides', () => {
  // Format-only parsing accepts any positive integer; only the fetched
  // catalog's plan.limits.channels can reject it as incompatible.
  const overQuota = parsePurchaseIntent({ plan: 'pro', interval: 'annual', quantity: '7' })
  assert.deepEqual(overQuota, { plan: 'pro', interval: 'annual', quantity: 7 })
  assert.equal(intentCompatibleWithPlan(proPlan, overQuota), false)
  const withinQuota = parsePurchaseIntent({ plan: 'pro', interval: 'annual', quantity: '6' })
  assert.equal(intentCompatibleWithPlan(proPlan, withinQuota), true)
})

test('intentCompatibleWithPlan rejects mismatched plan codes and Unlimited quantities', () => {
  const proIntent = parsePurchaseIntent({ plan: 'pro', interval: 'monthly', quantity: '3' })
  assert.equal(intentCompatibleWithPlan(unlimitedPlan, proIntent), false)
  const unlimitedIntent = parsePurchaseIntent({ plan: 'unlimited', interval: 'monthly' })
  assert.equal(intentCompatibleWithPlan(unlimitedPlan, unlimitedIntent), true)
  assert.equal(intentCompatibleWithPlan(proPlan, unlimitedIntent), false)
})

test('Unlimited purchase intent carries no channel quantity', () => {
  const intent = parsePurchaseIntent({ plan: 'unlimited', interval: 'monthly' })
  assert.deepEqual(intent, { plan: 'unlimited', interval: 'monthly' })
  assert.equal(checkoutPath('en', intent), '/app/billing/checkout?plan=unlimited&interval=monthly')
  assert.throws(() => parsePurchaseIntent({ plan: 'unlimited', interval: 'monthly', quantity: '1' }))
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
  await api.checkout('workspace-1', { plan: 'team', interval: 'monthly', quantity: 9 }, key)
  assert.deepEqual(calls[0]!.options?.body, {
    plan: 'team', interval: 'monthly', channels: 9,
    idempotency_key: 'billing-ui:fixed',
  })
  assert.equal(JSON.stringify(calls[0]).includes('price_id'), false)
})

test('Unlimited checkout omits channels entirely, matching the flat-rate contract', async () => {
  const calls: Array<{ path: string, options?: Readonly<Record<string, unknown>> }> = []
  const api = new BillingApi('https://api.postqron.test', async (path, options) => {
    calls.push({ path, options })
    return {
      id: 'txn_01abc', url: 'https://pay.paddle.io/checkout/test',
      expires_at: '2026-07-26T00:00:00Z',
    }
  })
  const key = createIdempotencyKey(() => 'fixed')
  await api.checkout('workspace-1', { plan: 'unlimited', interval: 'annual' }, key)
  assert.deepEqual(calls[0]!.options?.body, {
    plan: 'unlimited', interval: 'annual',
    idempotency_key: 'billing-ui:fixed',
  })
  assert.equal('channels' in (calls[0]!.options?.body as object), false)
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
  assert.equal(entitlementConfirmed(overview, { plan: 'pro', interval: 'monthly', quantity: 6 }), false)
  overview.usage[0]!.limit = 6
  assert.equal(entitlementConfirmed(overview, { plan: 'pro', interval: 'monthly', quantity: 6 }), true)
})

test('annual checkout summary derives every figure from the catalog, with no hardcoded percentage', () => {
  const summary = annualCheckoutSummary(proPlan, { plan: 'pro', interval: 'annual', quantity: 4 })
  assert.ok(summary)
  assert.deepEqual(summary!.total, money(18_000))
  assert.deepEqual(summary!.monthlyPrice, money(1_800))
  assert.deepEqual(summary!.monthlyEquivalent, money(1_500))
  assert.deepEqual(summary!.savings, money(3_600))
})

test('annual checkout summary is withheld for monthly billing, non-purchasable plans, and incompatible quantities', () => {
  assert.equal(
    annualCheckoutSummary(proPlan, { plan: 'pro', interval: 'monthly', quantity: 4 }),
    undefined,
  )
  assert.equal(
    annualCheckoutSummary(proPlan, { plan: 'pro', interval: 'annual', quantity: 7 }),
    undefined,
  )
  assert.equal(
    annualCheckoutSummary(startPlan, { plan: 'start', interval: 'annual', quantity: 2 }),
    undefined,
  )
})

test('Unlimited annual checkout summary needs no channel quantity', () => {
  const summary = annualCheckoutSummary(unlimitedPlan, { plan: 'unlimited', interval: 'annual' })
  assert.ok(summary)
  assert.deepEqual(summary!.total, money(129_000))
  assert.deepEqual(summary!.monthlyPrice, money(12_900))
  assert.deepEqual(summary!.monthlyEquivalent, money(10_750))
  assert.deepEqual(summary!.savings, money(25_800))
})

test('Unlimited entitlement does not require a channel quantity to be confirmed', () => {
  const overview = {
    plan: { code: 'unlimited' }, interval: 'annual', state: 'active',
    usage: [{ resource: 'channels', limit: null }],
  } as BillingOverview
  assert.equal(entitlementConfirmed(overview, { plan: 'unlimited', interval: 'annual' }), true)
  assert.equal(entitlementConfirmed(overview, { plan: 'pro', interval: 'annual' }), false)
})
