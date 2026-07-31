import assert from 'node:assert/strict'
import test from 'node:test'
import type { ContentCapability } from '../components/core/editorial-contracts.ts'
import { aggregateThreadConstraints } from '../components/core/editorial-thread.ts'
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

  assert.equal(summary, undefined)
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
