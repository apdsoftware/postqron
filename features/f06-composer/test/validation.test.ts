import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'

import type {
  CapabilityCatalog,
  ComposerFormat,
  Destination,
  DraftContent,
  Media,
} from '../client/contracts.ts'
import { validateDraft } from '../client/validation.ts'

const catalog = JSON.parse(
  readFileSync(new URL('./fixtures/capabilities.json', import.meta.url), 'utf8'),
) as CapabilityCatalog

test('browser validator covers every provider-agnostic capability family', () => {
  const imageOne = validImage('image-1')
  const imageTwo = validImage('image-2')
  const video = validVideo('video-1')
  const cases: DraftContent[] = [
    content({ text: 'text', destinations: [destination('text')] }),
    content({
      text: 'link',
      link: 'https://example.com/post',
      destinations: [destination('link')],
    }),
    content({
      text: 'image',
      media: [imageOne],
      destinations: [destination('image')],
    }),
    content({
      text: 'carousel',
      media: [imageOne, imageTwo],
      destinations: [destination('carousel')],
    }),
    content({
      text: 'video',
      media: [video],
      destinations: [
        { ...destination('video'), fields: { visibility: 'public' } },
      ],
    }),
    content({
      text: 'short',
      media: [video],
      destinations: [destination('short_video')],
    }),
    content({
      thread: [
        { text: 'one', media_ids: [] },
        { text: 'two', media_ids: [] },
      ],
      destinations: [destination('thread')],
    }),
  ]
  cases.forEach((candidate) => {
    const report = validateDraft(candidate, catalog)
    assert.equal(report.valid, true, JSON.stringify(report))
    assert.equal(report.capability_version, catalog.version)
  })
})

test('browser validation fails closed and returns actionable per-destination errors', () => {
  const report = validateDraft(
    content({
      text: 'content',
      destinations: [
        {
          id: 'unknown',
          channel_id: 'channel',
          channel_type: 'unknown',
          capability_id: 'missing',
          format: 'text',
        },
      ],
    }),
    catalog,
  )
  assert.equal(report.valid, false)
  const error = report.destinations[0]?.errors[0]
  assert.equal(error?.code, 'capability_unknown')
  assert.notEqual(error?.field, '')
  assert.notEqual(error?.rule, '')
  assert.notEqual(error?.remedy, '')
})

test('browser validation enforces safe links and D2-defined custom fields', () => {
  const unsafeLink = validateDraft(
    content({
      link: 'https://192.168.1.20/private',
      destinations: [destination('link')],
    }),
    catalog,
  )
  assert.ok(
    unsafeLink.destinations[0]?.errors.some(
      (error) => error.code === 'url_host_not_public',
    ),
  )

  const fields = validateDraft(
    content({
      media: [validVideo('video')],
      destinations: [
        {
          ...destination('video'),
          fields: { visibility: 'friends', undeclared: 'value' },
        },
      ],
    }),
    catalog,
  )
  const codes = fields.destinations[0]?.errors.map((error) => error.code) ?? []
  assert.ok(codes.includes('destination_field_invalid'))
  assert.ok(codes.includes('destination_field_unknown'))
})

test('browser validation preserves NFC code point parity with the server', () => {
  const report = validateDraft(
    content({
      text: 'e\u0301'.repeat(280),
      destinations: [destination('text')],
    }),
    catalog,
  )
  assert.equal(report.valid, true, JSON.stringify(report))
})

function content(overrides: Partial<DraftContent>): DraftContent {
  return {
    text: '',
    link: '',
    media: [],
    thread: [],
    destinations: [],
    ...overrides,
  }
}

function destination(family: ComposerFormat): Destination {
  const channelFamily = family === 'short_video' ? 'short' : family
  return {
    id: `destination-${family}`,
    channel_id: `channel-${family}`,
    channel_type: `fixture_${channelFamily}_channel`,
    capability_id: `fixture:${family}`,
    format: family,
  }
}

function validImage(id: string): Media {
  return {
    id,
    kind: 'image',
    content_type: 'image/jpeg',
    size_bytes: 2 * 1024 * 1024,
    width: 1080,
    height: 1080,
    inspection_status: 'ready',
    url: `/api/v1/media/${id}`,
  }
}

function validVideo(id: string): Media {
  return {
    id,
    kind: 'video',
    content_type: 'video/mp4',
    size_bytes: 20 * 1024 * 1024,
    width: 1080,
    height: 1920,
    video_codec: 'h264',
    audio_codec: 'aac',
    duration_seconds: 30,
    has_audio: true,
    inspection_status: 'ready',
    url: `/api/v1/media/${id}`,
  }
}
