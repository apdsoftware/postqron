import assert from 'node:assert/strict'
import test from 'node:test'

import type { PublicPlan } from '../../f02-marketing-site/src/catalog.ts'
import { planChannelLimit, pricingChannels } from '../src/plan-display.ts'

function plan(overrides: Partial<PublicPlan>): PublicPlan {
  return {
    code: 'pro',
    name: 'Pro',
    purchasable: true,
    prices: {
      monthly: { amount_cents: 450, currency: 'EUR' },
      annual: { amount_cents: 4500, currency: 'EUR' },
    },
    price_tiers: [],
    limits: {
      members: 3,
      channels: 6,
      scheduled_publications: 250,
      scheduled_publications_per_channel: 250,
    },
    ...overrides,
  }
}

test('pricing channel quantity stays separate from displayed plan limit', () => {
  const pro = plan({})
  const team = plan({
    code: 'team',
    name: 'Team',
    limits: {
      members: 6,
      channels: 9,
      scheduled_publications: 500,
      scheduled_publications_per_channel: 500,
    },
  })

  assert.equal(pricingChannels(pro), 3)
  assert.equal(planChannelLimit(pro), 6)
  assert.equal(pricingChannels(team), 3)
  assert.equal(planChannelLimit(team), 9)
})

test('start displays the authoritative catalog limit without paid pricing logic', () => {
  const start = plan({
    code: 'start',
    name: 'Start',
    purchasable: false,
    prices: {
      monthly: { amount_cents: 0, currency: 'EUR' },
      annual: { amount_cents: 0, currency: 'EUR' },
    },
    limits: {
      members: 1,
      channels: 3,
      scheduled_publications: 10,
      scheduled_publications_per_channel: 10,
    },
  })

  assert.equal(pricingChannels(start), 3)
  assert.equal(planChannelLimit(start), 3)
})

test('unlimited keeps null quota representation for both price and feature rendering', () => {
  const unlimited = plan({
    code: 'unlimited',
    name: 'Unlimited',
    limits: {
      members: null,
      channels: null,
      scheduled_publications: null,
      scheduled_publications_per_channel: null,
    },
  })

  assert.equal(pricingChannels(unlimited), null)
  assert.equal(planChannelLimit(unlimited), null)
})
