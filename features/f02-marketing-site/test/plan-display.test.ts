import assert from 'node:assert/strict'
import test from 'node:test'

import type { PublicPlan } from '../src/catalog.ts'
import { displayedChannelLimit, quantityForPlan } from '../src/plan-display.ts'

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

test('displayed channel limits stay authoritative while price quantity follows the slider', () => {
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

  assert.equal(quantityForPlan(pro, 3), 3)
  assert.equal(displayedChannelLimit(pro), 6)
  assert.equal(quantityForPlan(team, 3), 3)
  assert.equal(displayedChannelLimit(team), 9)
  assert.equal(quantityForPlan(team, 9), 9)
  assert.equal(displayedChannelLimit(team), 9)
})

test('start and unlimited keep their authoritative catalog shapes', () => {
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

  assert.equal(quantityForPlan(start, 9), 3)
  assert.equal(displayedChannelLimit(start), 3)
  assert.equal(quantityForPlan(unlimited, 9), null)
  assert.equal(displayedChannelLimit(unlimited), null)
})
