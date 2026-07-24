import assert from 'node:assert/strict'
import test from 'node:test'

import type { DraftContent, Media } from '../client/contracts.ts'
import { validateDraft } from '../client/validation.ts'

function image(id: string, contentType = 'image/jpeg'): Media {
  return {
    id,
    storage_key: `workspace/media/${id}`,
    kind: 'image',
    content_type: contentType,
    size_bytes: 2 * 1024 * 1024,
    width: 1080,
    height: 1080,
    color_space: 'sRGB',
  }
}

test('returns a validation outcome for every destination', () => {
  const content: DraftContent = {
    text: 'Shared image',
    media: [image('shared', 'image/png')],
    destinations: [
      {
        id: 'facebook',
        channel_id: 'page-1',
        channel_type: 'facebook_page',
        format: 'image',
      },
      {
        id: 'instagram',
        channel_id: 'ig-1',
        channel_type: 'instagram_professional',
        format: 'image',
      },
    ],
  }

  const report = validateDraft(content)

  assert.equal(report.valid, false)
  assert.equal(report.destinations.length, 2)
  assert.equal(report.destinations[0]?.valid, true)
  assert.equal(report.destinations[1]?.valid, false)
  assert.ok(
    report.destinations[1]?.errors.some(
      (error) =>
        error.field === 'media[0].content_type' &&
        error.rule === 'allowed_image_type' &&
        error.code === 'image_type_invalid',
    ),
  )
})

test('supports per-destination text and media overrides', () => {
  const content: DraftContent = {
    text: 'Facebook',
    media: [image('facebook', 'image/png'), image('instagram')],
    destinations: [
      {
        id: 'facebook',
        channel_id: 'page-1',
        channel_type: 'facebook_page',
        format: 'image',
        media_ids: ['facebook'],
      },
      {
        id: 'instagram',
        channel_id: 'ig-1',
        channel_type: 'instagram_professional',
        format: 'image',
        text_override: 'Instagram',
        media_ids: ['instagram'],
      },
    ],
  }

  assert.equal(validateDraft(content).valid, true)
})

test('counts normalized Unicode code points and exposes field/rule errors', () => {
  const content: DraftContent = {
    text: 'e\u0301'.repeat(2201),
    media: [image('instagram')],
    destinations: [
      {
        id: 'instagram',
        channel_id: 'ig-1',
        channel_type: 'instagram_professional',
        format: 'image',
      },
    ],
  }

  const report = validateDraft(content)

  assert.equal(report.valid, false)
  assert.deepEqual(
    report.destinations[0]?.errors.map(({ field, rule, code }) => ({
      field,
      rule,
      code,
    })),
    [
      {
        field: 'text',
        rule: 'maximum_code_points',
        code: 'text_too_long',
      },
    ],
  )
})

test('blocks unsupported Instagram text posts and private links', () => {
  const instagram = validateDraft({
    text: 'Text only',
    media: [],
    destinations: [
      {
        id: 'instagram',
        channel_id: 'ig-1',
        channel_type: 'instagram_professional',
        format: 'text',
      },
    ],
  })
  assert.ok(
    instagram.destinations[0]?.errors.some(
      (error) => error.code === 'format_unsupported',
    ),
  )

  const facebook = validateDraft({
    text: 'https://127.0.0.1/private',
    media: [],
    destinations: [
      {
        id: 'facebook',
        channel_id: 'page-1',
        channel_type: 'facebook_page',
        format: 'text',
      },
    ],
  })
  assert.ok(
    facebook.destinations[0]?.errors.some(
      (error) => error.code === 'url_host_not_public',
    ),
  )
})

test('reports structural fields and rules before the server request', () => {
  const duplicate = image('same')
  const report = validateDraft({
    text: '',
    media: [duplicate, { ...duplicate, storage_key: '' }],
    destinations: [],
  })

  assert.ok(
    report.errors.some(
      (error) =>
        error.field === 'media[1].id' &&
        error.rule === 'unique' &&
        error.code === 'media_id_duplicate',
    ),
  )
  assert.ok(
    report.errors.some(
      (error) =>
        error.field === 'media[1].storage_key' &&
        error.rule === 'required' &&
        error.code === 'media_storage_key_required',
    ),
  )
})
