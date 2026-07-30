import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import test from 'node:test'
import type {
  PublicCatalog,
  PublicPlan,
} from '../../f02-marketing-site/src/catalog.ts'
import {
  BillingApi,
  PlanChangeApiError,
  createIdempotencyKey,
  parseSubscriptionChangePreview,
  type BillingOverview,
} from '../src/billing.ts'
import { BILLING_PLAN_CATALOGS } from '../src/plan-catalogs.ts'
import {
  compatibilityForPlan,
  currentPlanPrice,
  intentForPlan,
  minimumCompatiblePlan,
  overviewMatchesTarget,
  providerPreviewAmounts,
  recommendedChannelQuantity,
  usagePercentage,
} from '../src/plan-change.ts'

const money = (amount_cents: number) => ({
  amount_cents,
  currency: 'EUR' as const,
})

const plans: PublicPlan[] = [
  {
    code: 'start',
    name: 'Start',
    purchasable: false,
    prices: { monthly: money(0), annual: money(0) },
    price_tiers: [],
    limits: {
      members: 1,
      channels: 3,
      scheduled_publications: 10,
      scheduled_publications_per_channel: 10,
    },
  },
  {
    code: 'pro',
    name: 'Pro',
    purchasable: true,
    prices: { monthly: money(450), annual: money(4_500) },
    price_tiers: [{
      from_channel: 1,
      to_channel: 6,
      monthly: money(450),
      annual: money(4_500),
    }],
    limits: {
      members: 3,
      channels: 6,
      scheduled_publications: 250,
      scheduled_publications_per_channel: 250,
    },
  },
  {
    code: 'team',
    name: 'Team',
    purchasable: true,
    prices: { monthly: money(900), annual: money(9_000) },
    price_tiers: [{
      from_channel: 1,
      to_channel: 9,
      monthly: money(900),
      annual: money(9_000),
    }],
    limits: {
      members: 6,
      channels: 9,
      scheduled_publications: 500,
      scheduled_publications_per_channel: 500,
    },
  },
  {
    code: 'unlimited',
    name: 'Unlimited',
    purchasable: true,
    prices: { monthly: money(12_900), annual: money(129_000) },
    price_tiers: [],
    limits: {
      members: null,
      channels: null,
      scheduled_publications: null,
      scheduled_publications_per_channel: null,
    },
  },
]

const catalog: PublicCatalog = {
  provider: 'paddle',
  catalog_version: 'd09-v2',
  currency: 'EUR',
  plans,
}

function overview(
  plan: PublicPlan = plans[2]!,
  usage = { members: 2, channels: 4, scheduled: 11 },
): BillingOverview {
  return {
    plan,
    interval: 'annual',
    state: 'active',
    period: {
      start: '2026-07-01T00:00:00Z',
      end: '2027-07-01T00:00:00Z',
    },
    usage: [
      {
        resource: 'members',
        used: usage.members,
        limit: plan.limits.members,
        remaining: plan.limits.members === null
          ? null
          : Math.max(0, plan.limits.members - usage.members),
        over_limit: false,
      },
      {
        resource: 'channels',
        used: usage.channels,
        limit: plan.code === 'team' ? 9 : plan.limits.channels,
        remaining: plan.limits.channels === null
          ? null
          : Math.max(0, plan.limits.channels - usage.channels),
        over_limit: false,
      },
      {
        resource: 'scheduled_publications',
        used: usage.scheduled,
        limit: plan.limits.scheduled_publications,
        remaining: plan.limits.scheduled_publications === null
          ? null
          : Math.max(0, plan.limits.scheduled_publications - usage.scheduled),
        over_limit: false,
      },
    ],
  }
}

test('minimum compatible plan and disabled lower plans derive only from F10 catalog limits', () => {
  const current = overview()
  assert.equal(minimumCompatiblePlan(catalog, current).code, 'pro')
  assert.deepEqual(compatibilityForPlan(plans[0]!, current), {
    compatible: false,
    overages: [
      { resource: 'members', used: 2, limit: 1, excess: 1 },
      { resource: 'channels', used: 4, limit: 3, excess: 1 },
      { resource: 'scheduled_publications', used: 11, limit: 10, excess: 1 },
    ],
  })
  assert.equal(compatibilityForPlan(plans[1]!, current).compatible, true)
})

test('channel quantity preserves current capacity when possible and clamps to target catalog maximum', () => {
  const current = overview()
  assert.equal(recommendedChannelQuantity(plans[1]!, current), 6)
  assert.equal(recommendedChannelQuantity(plans[2]!, current), 9)
  assert.equal(recommendedChannelQuantity(plans[3]!, current), undefined)
  assert.deepEqual(intentForPlan(plans[1]!, 'monthly', current), {
    plan: 'pro',
    interval: 'monthly',
    channels: 6,
  })
  assert.deepEqual(intentForPlan(plans[0]!, 'annual', current), {
    plan: 'start',
  })
})

test('current price and usage percentages use overview capacity without duplicated values', () => {
  const current = overview()
  assert.deepEqual(currentPlanPrice(current), money(81_000))
  assert.equal(usagePercentage(current.usage[0]!), 33)
  assert.equal(usagePercentage({
    ...current.usage[0]!,
    limit: null,
    remaining: null,
  }), null)
})

test('F10 preview parser exposes direction, timing, target, and safe Paddle totals', () => {
  const preview = parseSubscriptionChangePreview({
    direction: 'upgrade',
    action: 'update_subscription',
    immediate: true,
    target: {
      plan: 'unlimited',
      interval: 'annual',
      channels: null,
    },
    provider_preview: {
      data: {
        currency_code: 'EUR',
        immediate_transaction: {
          details: { totals: { total: '4800' } },
        },
        recurring_transaction_details: {
          details: { totals: { total: '129000' } },
        },
      },
    },
  })
  assert.deepEqual(providerPreviewAmounts(preview), {
    currency: 'EUR',
    immediate: money(4_800),
    recurring: money(129_000),
  })
  assert.equal(overviewMatchesTarget(overview(plans[3]!), preview.target), true)
})

test('preview and request use the F10 payload and preserve one idempotency key', async () => {
  const calls: Array<{
    options?: Readonly<Record<string, unknown>>
    path: string
  }> = []
  const key = createIdempotencyKey(() => 'plan-change')
  const api = new BillingApi('https://api.postqron.test', async (path, options) => {
    calls.push({ path, options })
    if (path.endsWith('/preview')) {
      return {
        direction: 'downgrade',
        action: 'update_subscription',
        immediate: false,
        target: { plan: 'pro', interval: 'annual', channels: 6 },
        provider_preview: { data: { currency_code: 'EUR' } },
      }
    }
    return {
      status: 'pending',
      direction: 'downgrade',
      action: 'update_subscription',
      target: { plan: 'pro', interval: 'annual', channels: 6 },
      idempotency_key: key,
    }
  })

  const intent = { plan: 'pro', interval: 'annual', channels: 6 } as const
  await api.previewSubscriptionChange('workspace-1', intent, key)
  const result = await api.applySubscriptionChange('workspace-1', intent, key)

  assert.equal(result.status, 'pending')
  assert.equal(calls[0]!.options?.method, 'POST')
  assert.equal(calls[1]!.options?.method, 'PATCH')
  assert.deepEqual(calls.map(call => call.options?.body), [
    {
      plan: 'pro',
      interval: 'annual',
      channels: 6,
      idempotency_key: key,
    },
    {
      plan: 'pro',
      interval: 'annual',
      channels: 6,
      idempotency_key: key,
    },
  ])
})

test('blocked downgrade keeps all authoritative resource details and is non-retryable', async () => {
  const api = new BillingApi('https://api.postqron.test', async () => {
    throw {
      statusCode: 409,
      data: {
        error: 'downgrade_limit_exceeded',
        retryable: false,
        overages: [
          { resource: 'members', used: 4, limit: 1, excess: 3 },
          { resource: 'channels', used: 8, limit: 3, excess: 5 },
          {
            resource: 'scheduled_publications',
            used: 12,
            limit: 10,
            excess: 2,
          },
        ],
      },
    }
  })

  await assert.rejects(
    () => api.previewSubscriptionChange(
      'workspace-1',
      { plan: 'start' },
      'billing-ui:blocked',
    ),
    (error: unknown) => {
      assert.ok(error instanceof PlanChangeApiError)
      assert.equal(error.code, 'downgrade_limit_exceeded')
      assert.equal(error.retryable, false)
      assert.deepEqual(error.overages.map(item => item.excess), [3, 5, 2])
      return true
    },
  )
})

test('plan copy is complete for EN/IT/ES/FR/DE and explains blocked cleanup without claiming deletion', () => {
  const keys = Object.keys(BILLING_PLAN_CATALOGS.en).sort()
  assert.deepEqual(Object.keys(BILLING_PLAN_CATALOGS), ['en', 'it', 'es', 'fr', 'de'])
  for (const copy of Object.values(BILLING_PLAN_CATALOGS)) {
    assert.deepEqual(Object.keys(copy).sort(), keys)
    assert.ok(Object.values(copy).every(Boolean))
    assert.match(copy['overview.manage'], /portal|portale|portail/iu)
    assert.match(copy['blocked.values'], /\{used\}/u)
    assert.match(copy['blocked.values'], /\{limit\}/u)
    assert.match(copy['blocked.values'], /\{excess\}/u)
    assert.ok(copy['blocked.noDeletion'])
  }
})

test('plan component contract includes modal focus, navigation state, progress semantics, and no destructive API', async () => {
  const [dialog, page, billing, runtime] = await Promise.all([
    readFile(new URL('../components/PlanChangeDialog.vue', import.meta.url), 'utf8'),
    readFile(new URL('../pages/billing.vue', import.meta.url), 'utf8'),
    readFile(new URL('../src/billing.ts', import.meta.url), 'utf8'),
    readFile(new URL('../runtime.ts', import.meta.url), 'utf8'),
  ])
  assert.match(dialog, /role="dialog"/u)
  assert.match(dialog, /aria-modal="true"/u)
  assert.match(dialog, /event\.key !== 'Tab'/u)
  assert.match(dialog, /event\.key === 'Escape'/u)
  assert.match(page, /'progressbar'/u)
  assert.match(page, /aria-valuenow/u)
  assert.match(dialog, /compatibility\(plan\)\.overages/u)
  assert.match(runtime, /setAttribute\('aria-current', 'page'\)/u)
  assert.doesNotMatch(`${dialog}\n${billing}`, /method:\s*['"]DELETE['"]/u)
})
