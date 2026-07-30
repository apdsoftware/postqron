import assert from 'node:assert/strict'
import test from 'node:test'
import type { PublicPlan } from '../../f02-marketing-site/src/catalog.ts'
import {
  BillingApi,
  PlanChangeApiError,
  type BillingOverview,
  type PlanChangeIntent,
} from '../src/billing.ts'
import { overviewMatchesTarget } from '../src/plan-change.ts'

const money = (amount_cents: number) => ({
  amount_cents,
  currency: 'EUR' as const,
})

const pro: PublicPlan = {
  code: 'pro',
  name: 'Pro',
  purchasable: true,
  prices: { monthly: money(450), annual: money(4_500) },
  price_tiers: [{
    from_channel: 1,
    to_channel: 10,
    monthly: money(450),
    annual: money(4_500),
  }, {
    from_channel: 11,
    to_channel: 25,
    monthly: money(400),
    annual: money(4_000),
  }, {
    from_channel: 26,
    to_channel: 50,
    monthly: money(300),
    annual: money(3_000),
  }],
  limits: {
    members: 3,
    channels: 6,
    scheduled_publications: 250,
    scheduled_publications_per_channel: 250,
  },
}

const team: PublicPlan = {
  code: 'team',
  name: 'Team',
  purchasable: true,
  prices: { monthly: money(900), annual: money(9_000) },
  price_tiers: [{
    from_channel: 1,
    to_channel: 10,
    monthly: money(900),
    annual: money(9_000),
  }, {
    from_channel: 11,
    to_channel: 25,
    monthly: money(400),
    annual: money(4_000),
  }, {
    from_channel: 26,
    to_channel: 50,
    monthly: money(300),
    annual: money(3_000),
  }],
  limits: {
    members: 6,
    channels: 9,
    scheduled_publications: 500,
    scheduled_publications_per_channel: 500,
  },
}

function billingOverview(
  plan: PublicPlan,
  interval: 'monthly' | 'annual',
  channelLimit: number,
  usage = { members: 2, channels: 4, posts: 120 },
): BillingOverview {
  return {
    plan,
    interval,
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
        remaining: plan.limits.members! - usage.members,
        over_limit: false,
      },
      {
        resource: 'channels',
        used: usage.channels,
        limit: channelLimit,
        remaining: channelLimit - usage.channels,
        over_limit: false,
      },
      {
        resource: 'scheduled_publications',
        used: usage.posts,
        limit: plan.limits.scheduled_publications,
        remaining: plan.limits.scheduled_publications! - usage.posts,
        over_limit: false,
      },
    ],
  }
}

function fixtureApi(initial: BillingOverview) {
  let active = structuredClone(initial)
  let pending: PlanChangeIntent | undefined
  const methods: string[] = []
  const api = new BillingApi('https://api.postqron.test', async (path, options) => {
    methods.push(String(options?.method ?? 'GET'))
    if (path.endsWith('/billing') && !options?.method) {
      return active
    }
    const body = options?.body as Record<string, unknown>
    const target = {
      plan: body.plan,
      interval: body.plan === 'start' ? 'monthly' : body.interval,
      channels: body.plan === 'start' ? 3 : body.channels ?? null,
    }
    const used = Object.fromEntries(active.usage.map(item => [
      item.resource,
      item.used,
    ]))
    const targetPlan = target.plan === 'pro' ? pro : team
    const limits = {
      members: targetPlan.limits.members,
      channels: target.channels,
      scheduled_publications: targetPlan.limits.scheduled_publications,
    }
    const overages = Object.entries(used).flatMap(([resource, value]) => {
      const limit = limits[resource as keyof typeof limits]
      return typeof limit === 'number' && value > limit
        ? [{ resource, used: value, limit, excess: value - limit }]
        : []
    })
    if (overages.length > 0) {
      throw {
        statusCode: 409,
        data: {
          error: 'downgrade_limit_exceeded',
          retryable: false,
          overages,
        },
      }
    }
    const upgrade = active.plan.code === 'pro' && target.plan === 'team'
    if (path.endsWith('/preview')) {
      return {
        direction: upgrade ? 'upgrade' : 'downgrade',
        action: 'update_subscription',
        immediate: upgrade,
        target,
        provider_preview: {
          data: {
            currency_code: 'EUR',
            immediate_transaction: upgrade
              ? { details: { totals: { total: '5400' } } }
              : null,
          },
        },
      }
    }
    pending = {
      plan: target.plan as PlanChangeIntent['plan'],
      interval: target.interval as PlanChangeIntent['interval'],
      channels: typeof target.channels === 'number' ? target.channels : undefined,
    }
    return {
      status: 'pending',
      direction: upgrade ? 'upgrade' : 'downgrade',
      action: 'update_subscription',
      target,
      idempotency_key: body.idempotency_key,
    }
  })
  return {
    api,
    methods,
    pending: () => pending,
    applyWebhook() {
      if (!pending || pending.plan === 'start' || !pending.interval) {
        return
      }
      const plan = pending.plan === 'pro' ? pro : team
      active = billingOverview(
        plan,
        pending.interval,
        pending.channels!,
      )
    },
  }
}

test('fixture E2E: upgrade previews, requests pending, and activates only after webhook', async () => {
  const fixture = fixtureApi(billingOverview(pro, 'monthly', 6))
  const intent = { plan: 'team', interval: 'annual', channels: 9 } as const
  const preview = await fixture.api.previewSubscriptionChange(
    'workspace',
    intent,
    'billing-ui:upgrade',
  )
  assert.equal(preview.immediate, true)
  const result = await fixture.api.applySubscriptionChange(
    'workspace',
    intent,
    'billing-ui:upgrade',
  )
  assert.equal(result.status, 'pending')
  assert.equal(
    overviewMatchesTarget(await fixture.api.overview('workspace'), result.target),
    false,
  )
  fixture.applyWebhook()
  assert.equal(
    overviewMatchesTarget(await fixture.api.overview('workspace'), result.target),
    true,
  )
  assert.deepEqual(fixture.methods.slice(0, 2), ['POST', 'PATCH'])
})

test('fixture E2E: allowed downgrade stays pending and never mutates resources locally', async () => {
  const original = billingOverview(team, 'annual', 9)
  const fixture = fixtureApi(original)
  const intent = { plan: 'pro', interval: 'annual', channels: 6 } as const
  const preview = await fixture.api.previewSubscriptionChange(
    'workspace',
    intent,
    'billing-ui:downgrade',
  )
  assert.equal(preview.immediate, false)
  const result = await fixture.api.applySubscriptionChange(
    'workspace',
    intent,
    'billing-ui:downgrade',
  )
  assert.equal(result.status, 'pending')
  assert.deepEqual(
    (await fixture.api.overview('workspace')).usage.map(item => item.used),
    original.usage.map(item => item.used),
  )
  assert.equal(fixture.methods.includes('DELETE'), false)
})

test('fixture E2E: blocked downgrade returns resource, used, limit, and excess without a request', async () => {
  const original = billingOverview(
    team,
    'annual',
    9,
    { members: 5, channels: 8, posts: 300 },
  )
  const fixture = fixtureApi(original)
  await assert.rejects(
    () => fixture.api.previewSubscriptionChange(
      'workspace',
      { plan: 'pro', interval: 'annual', channels: 6 },
      'billing-ui:blocked',
    ),
    (error: unknown) => {
      assert.ok(error instanceof PlanChangeApiError)
      assert.deepEqual(error.overages, [
        { resource: 'members', used: 5, limit: 3, excess: 2 },
        { resource: 'channels', used: 8, limit: 6, excess: 2 },
        {
          resource: 'scheduled_publications',
          used: 300,
          limit: 250,
          excess: 50,
        },
      ])
      return true
    },
  )
  assert.deepEqual(
    (await fixture.api.overview('workspace')).usage.map(item => item.used),
    original.usage.map(item => item.used),
  )
  assert.deepEqual(fixture.methods, ['POST', 'GET'])
})
