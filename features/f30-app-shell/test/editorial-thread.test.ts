import assert from 'node:assert/strict'
import test from 'node:test'
import type { ContentCapability } from '../components/core/editorial-contracts.ts'
import {
  aggregateThreadConstraints,
  canRemoveThreadItem,
  threadItemsForSubmission,
} from '../components/core/editorial-thread.ts'
import {
  localizedValidationField,
  localizedValidationMessage,
} from '../components/core/editorial-validation.ts'

function capability(overrides: Partial<ContentCapability['thread']>): ContentCapability {
  return {
    id: 'capability-1',
    provider: 'facebook',
    channel_type: 'facebook_page',
    format: 'thread',
    available: true,
    text: { allowed: true, required: false },
    link: { allowed: false, required: false },
    media: { allowed: true },
    thread: {
      allowed: true,
      required: false,
      ...overrides,
    },
  }
}

test('thread constraints aggregate fail-closed across selected capabilities', () => {
  const summary = aggregateThreadConstraints([
    capability({
      required: true,
      minimum_items: 2,
      maximum_items: 5,
      max_item_characters: 300,
      max_media_per_item: 3,
    }),
    capability({
      minimum_items: 1,
      maximum_items: 4,
      max_item_characters: 280,
      max_media_per_item: 1,
    }),
  ])

  assert.deepEqual(summary, {
    allowed: true,
    compatible: true,
    required: true,
    minimumItems: 2,
    maximumItems: 4,
    maxItemCharacters: 280,
    maxMediaPerItem: 1,
  })
})

test('thread constraints disappear when no selected capability allows threads', () => {
  const summary = aggregateThreadConstraints([{
    id: 'capability-1',
    provider: 'linkedin',
    channel_type: 'linkedin_profile',
    format: 'text',
    available: true,
    text: { allowed: true, required: true },
    link: { allowed: true, required: false },
    media: { allowed: false },
    thread: { allowed: false, required: false },
  }])

  assert.deepEqual(summary, {
    allowed: false,
    compatible: true,
    required: false,
    minimumItems: 0,
    maximumItems: 0,
    maxItemCharacters: 0,
    maxMediaPerItem: 0,
  })
})

test('one selected destination that forbids threads makes the aggregate fail closed', () => {
  const summary = aggregateThreadConstraints([
    capability({ allowed: true, maximum_items: 5 }),
    capability({ allowed: false }),
  ])

  assert.equal(summary?.allowed, false)
  assert.deepEqual(threadItemsForSubmission([
    { text: 'stale', media_ids: ['media-1'] },
  ], summary), [])
  assert.equal(canRemoveThreadItem(summary, 1), true)
})

test('an impossible min/max intersection is explicit and stale items remain removable', () => {
  const summary = aggregateThreadConstraints([
    capability({ minimum_items: 4, maximum_items: 8 }),
    capability({ minimum_items: 2, maximum_items: 3 }),
  ])

  assert.equal(summary?.allowed, true)
  assert.equal(summary?.compatible, false)
  assert.equal(summary?.minimumItems, 4)
  assert.equal(summary?.maximumItems, 3)
  assert.equal(canRemoveThreadItem(summary, 1), true)
  assert.deepEqual(threadItemsForSubmission([{ text: 'stale', media_ids: [] }], summary), [])
})

test('validation field and message mapping localize known backend codes and rules', () => {
  const t = (key: string) => key

  assert.equal(localizedValidationField('thread[0].text', t as never), 'composer.field.thread')
  assert.equal(localizedValidationField('media[0].id', t as never), 'composer.field.media')
  assert.equal(localizedValidationMessage({
    field: 'thread[0].text',
    rule: 'minimum_items',
    code: 'text_required',
    message: 'fallback',
  }, t as never), 'composer.validation.code.text_required')
  assert.equal(localizedValidationMessage({
    field: 'thread[0].text',
    rule: 'minimum_items',
    code: 'unknown_code',
    message: 'fallback',
  }, t as never), 'composer.validation.rule.minimum_items')
  assert.equal(localizedValidationMessage({
    field: 'thread[0].text',
    rule: 'unknown_rule',
    code: 'unknown_code',
    message: 'fallback',
  }, t as never), 'fallback')
})
