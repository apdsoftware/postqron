import type {
  ChannelType,
  ComposerFormat,
  Destination,
  DestinationValidation,
  DraftContent,
  Media,
  ValidationError,
  ValidationReport,
} from './contracts.ts'

const MIB = 1024 * 1024
const MIN_IMAGE_RATIO = 4 / 5
const MAX_IMAGE_RATIO = 1.91
const REEL_RATIO = 9 / 16
const RATIO_TOLERANCE = 0.001
const URL_PATTERN = /\b[a-z][a-z0-9+.-]*:\/\/[^\s]+/giu

type AddError = (
  field: string,
  rule: string,
  code: string,
  message: string,
  details?: Record<string, unknown>,
) => void

// This is the browser-side implementation of the server contract. The server
// remains authoritative and returns the same destination/field/rule shape.
export function validateDraft(content: DraftContent): ValidationReport {
  const errors: ValidationError[] = []
  const mediaIDs = new Set<string>()
  content.media.forEach((item, index) => {
    if (item.id.trim() === '') {
      errors.push({
        field: `media[${index}].id`,
        rule: 'required',
        code: 'media_id_required',
        message: 'Media id is required.',
      })
    } else if (mediaIDs.has(item.id.trim())) {
      errors.push({
        field: `media[${index}].id`,
        rule: 'unique',
        code: 'media_id_duplicate',
        message: 'Media ids must be unique within a draft.',
      })
    }
    mediaIDs.add(item.id.trim())
    if (item.storage_key.trim() === '') {
      errors.push({
        field: `media[${index}].storage_key`,
        rule: 'required',
        code: 'media_storage_key_required',
        message: 'Media storage key is required.',
      })
    }
  })
  const destinationIDs = new Set<string>()
  content.destinations.forEach((destination, index) => {
    if (destination.id.trim() === '') {
      errors.push({
        field: `destinations[${index}].id`,
        rule: 'required',
        code: 'destination_id_required',
        message: 'Destination id is required.',
      })
    } else if (destinationIDs.has(destination.id.trim())) {
      errors.push({
        field: `destinations[${index}].id`,
        rule: 'unique',
        code: 'destination_id_duplicate',
        message: 'Destination ids must be unique within a draft.',
      })
    }
    destinationIDs.add(destination.id.trim())
    if (destination.channel_id.trim() === '') {
      errors.push({
        field: `destinations[${index}].channel_id`,
        rule: 'required',
        code: 'channel_id_required',
        message: 'Destination channel id is required.',
      })
    }
  })
  if (content.destinations.length === 0) {
    errors.push({
      field: 'destinations',
      rule: 'required',
      code: 'destinations_required',
      message: 'Select at least one destination.',
    })
  }
  const mediaByID = new Map(content.media.map((item) => [item.id, item]))
  const destinations = content.destinations.map((destination) =>
    validateDestination(content, destination, mediaByID),
  )
  return {
    valid: errors.length === 0 && destinations.every((result) => result.valid),
    errors,
    destinations,
  }
}

function validateDestination(
  content: DraftContent,
  destination: Destination,
  mediaByID: ReadonlyMap<string, Media>,
): DestinationValidation {
  const errors: ValidationError[] = []
  const add: AddError = (field, rule, code, message, details) => {
    errors.push({
      destination_id: destination.id,
      field,
      rule,
      code,
      message,
      ...(details === undefined ? {} : { details }),
    })
  }

  if (!supportedChannel(destination.channel_type)) {
    add(
      'channel_type',
      'supported',
      'channel_unsupported',
      'The selected channel is not supported.',
      { actual: destination.channel_type },
    )
  }
  if (!supportedFormat(destination.channel_type, destination.format)) {
    add(
      'format',
      'supported_for_channel',
      'format_unsupported',
      'The selected format is not supported by this channel.',
      { channel_type: destination.channel_type, actual: destination.format },
    )
  }

  const text = (
    destination.text_override == null ? content.text : destination.text_override
  ).normalize('NFC')
  let media = content.media
  if (destination.media_ids != null) {
    media = []
    destination.media_ids.forEach((mediaID, index) => {
      const item = mediaByID.get(mediaID)
      if (item === undefined) {
        add(
          `media_ids[${index}]`,
          'references_draft_media',
          'media_not_found',
          'The selected media does not belong to this draft.',
          { media_id: mediaID },
        )
      } else {
        media.push(item)
      }
    })
  }

  switch (destination.format) {
    case 'text':
      validateTextPost(text, media, add)
      break
    case 'image':
      validateImagePost(destination.channel_type, text, media, add)
      break
    case 'carousel':
      validateCarousel(text, media, add)
      break
    case 'reel':
      validateReel(destination.channel_type, text, media, add)
      break
  }

  return {
    destination_id: destination.id,
    channel_id: destination.channel_id,
    channel_type: destination.channel_type,
    format: destination.format,
    valid: errors.length === 0,
    errors,
  }
}

function validateTextPost(text: string, media: Media[], add: AddError): void {
  validateTextLength(text, 1, 5000, add)
  if (media.length !== 0) {
    add(
      'media',
      'none',
      'media_not_allowed',
      'A Facebook text/link post cannot include media.',
      { actual_count: media.length },
    )
  }
  const links = Array.from(text.matchAll(URL_PATTERN), (match) => match[0])
  if (links.length > 1) {
    add(
      'text',
      'maximum_one_url',
      'too_many_urls',
      'Text/link posts can contain at most one absolute URL.',
      { actual_count: links.length, maximum: 1 },
    )
  }
  links.forEach((link) => validatePublicHTTPSURL(link, add))
}

function validatePublicHTTPSURL(rawURL: string, add: AddError): void {
  let parsed: URL
  try {
    parsed = new URL(rawURL)
  } catch {
    add(
      'text',
      'absolute_https_url',
      'url_must_be_https',
      'Links must be absolute HTTPS URLs.',
      { url: rawURL },
    )
    return
  }
  if (parsed.protocol !== 'https:') {
    add(
      'text',
      'absolute_https_url',
      'url_must_be_https',
      'Links must be absolute HTTPS URLs.',
      { url: rawURL },
    )
    return
  }
  if (parsed.username !== '' || parsed.password !== '') {
    add(
      'text',
      'no_url_credentials',
      'url_credentials_not_allowed',
      'Links cannot contain credentials.',
      { url: rawURL },
    )
  }
  if (isNonPublicLiteralHost(parsed.hostname)) {
    add(
      'text',
      'public_url_host',
      'url_host_not_public',
      'Links must not target private, loopback, or link-local addresses.',
      { host: parsed.hostname },
    )
  }
}

function isNonPublicLiteralHost(host: string): boolean {
  const normalized = host.replace(/^\[|\]$/gu, '').toLowerCase()
  if (normalized === 'localhost' || normalized === '::1' || normalized === '::') {
    return true
  }
  if (
    normalized.startsWith('fc') ||
    normalized.startsWith('fd') ||
    normalized.startsWith('fe8') ||
    normalized.startsWith('fe9') ||
    normalized.startsWith('fea') ||
    normalized.startsWith('feb')
  ) {
    return true
  }
  const octets = normalized.split('.').map(Number)
  if (
    octets.length !== 4 ||
    octets.some((octet) => !Number.isInteger(octet) || octet < 0 || octet > 255)
  ) {
    return false
  }
  const first = octets[0] ?? -1
  const second = octets[1] ?? -1
  return (
    first === 0 ||
    first === 10 ||
    first === 127 ||
    (first === 169 && second === 254) ||
    (first === 172 && second >= 16 && second <= 31) ||
    (first === 192 && second === 168)
  )
}

function validateImagePost(
  channel: ChannelType,
  text: string,
  media: Media[],
  add: AddError,
): void {
  validateTextLength(text, 0, channel === 'instagram_professional' ? 2200 : 5000, add)
  if (!validateMediaCount(media, 1, 1, add)) return
  const first = media[0]
  if (first === undefined) return
  validateImage(
    first,
    'media[0]',
    channel === 'facebook_page'
      ? new Set(['image/jpeg', 'image/png'])
      : new Set(['image/jpeg']),
    add,
  )
}

function validateCarousel(text: string, media: Media[], add: AddError): void {
  validateTextLength(text, 0, 2200, add)
  if (!validateMediaCount(media, 2, 10, add)) return
  media.forEach((item, index) => {
    validateImage(item, `media[${index}]`, new Set(['image/jpeg']), add)
  })
  const totalSize = media.reduce((total, item) => total + item.size_bytes, 0)
  if (totalSize > 80 * MIB) {
    add(
      'media',
      'maximum_total_size_bytes',
      'carousel_too_large',
      'The carousel exceeds the total size limit.',
      { actual: totalSize, maximum: 80 * MIB },
    )
  }
  const first = media[0]
  if (first === undefined) return
  media.slice(1).forEach((item, offset) => {
    if (
      first.width > 0 &&
      first.height > 0 &&
      item.width > 0 &&
      item.height > 0 &&
      first.width * item.height !== item.width * first.height
    ) {
      add(
        `media[${offset + 1}].aspect_ratio`,
        'same_carousel_ratio',
        'carousel_ratio_mismatch',
        'All carousel images must have the same aspect ratio.',
        {
          expected_width: first.width,
          expected_height: first.height,
          actual_width: item.width,
          actual_height: item.height,
        },
      )
    }
  })
}

function validateImage(
  media: Media,
  field: string,
  allowedTypes: ReadonlySet<string>,
  add: AddError,
): void {
  if (media.kind !== 'image') {
    add(`${field}.kind`, 'image', 'media_kind_invalid', 'An image is required.', {
      actual: media.kind,
    })
  }
  const contentType = media.content_type.toLowerCase()
  if (!allowedTypes.has(contentType)) {
    add(
      `${field}.content_type`,
      'allowed_image_type',
      'image_type_invalid',
      'The image type is not supported for this destination.',
      { actual: contentType, allowed: Array.from(allowedTypes) },
    )
  }
  if (media.size_bytes <= 0 || media.size_bytes > 8 * MIB) {
    add(
      `${field}.size_bytes`,
      'range',
      'image_size_invalid',
      'Images must be non-empty and no larger than 8 MiB.',
      { actual: media.size_bytes, maximum: 8 * MIB },
    )
  }
  if (media.color_space?.toLowerCase() !== 'srgb') {
    add(
      `${field}.color_space`,
      'srgb',
      'image_color_space_invalid',
      'Images must use the sRGB color space.',
      { actual: media.color_space ?? '' },
    )
  }
  if (media.width < 320 || media.width > 1440) {
    add(
      `${field}.width`,
      'range',
      'image_width_invalid',
      'Image width must be between 320 and 1,440 pixels.',
      { actual: media.width, minimum: 320, maximum: 1440 },
    )
  }
  if (media.height <= 0) {
    add(
      `${field}.height`,
      'positive',
      'image_height_invalid',
      'Image height must be positive.',
      { actual: media.height },
    )
    return
  }
  const ratio = media.width / media.height
  if (
    ratio < MIN_IMAGE_RATIO - RATIO_TOLERANCE ||
    ratio > MAX_IMAGE_RATIO + RATIO_TOLERANCE
  ) {
    add(
      `${field}.aspect_ratio`,
      'range',
      'image_ratio_invalid',
      'Image aspect ratio must be between 4:5 and 1.91:1.',
      { actual: ratio, minimum: MIN_IMAGE_RATIO, maximum: MAX_IMAGE_RATIO },
    )
  }
}

function validateReel(
  channel: ChannelType,
  text: string,
  media: Media[],
  add: AddError,
): void {
  validateTextLength(text, 0, channel === 'instagram_professional' ? 2200 : 5000, add)
  if (!validateMediaCount(media, 1, 1, add)) return
  const item = media[0]
  if (item === undefined) return
  const field = 'media[0]'
  checkEqual(add, `${field}.kind`, 'video', 'media_kind_invalid', item.kind, 'video')
  checkEqual(
    add,
    `${field}.content_type`,
    'video/mp4',
    'video_type_invalid',
    item.content_type.toLowerCase(),
    'video/mp4',
  )
  checkEqual(
    add,
    `${field}.video_codec`,
    'h264',
    'video_codec_invalid',
    item.video_codec?.toLowerCase() ?? '',
    'h264',
  )
  if (item.size_bytes <= 0 || item.size_bytes > 100 * MIB) {
    add(
      `${field}.size_bytes`,
      'range',
      'video_size_invalid',
      'Reels must be non-empty and no larger than 100 MiB.',
      { actual: item.size_bytes, maximum: 100 * MIB },
    )
  }
  if (item.has_audio === true) {
    checkEqual(
      add,
      `${field}.audio_codec`,
      'aac',
      'audio_codec_invalid',
      item.audio_codec?.toLowerCase() ?? '',
      'aac',
    )
    if (item.audio_sample_rate !== 48000) {
      add(
        `${field}.audio_sample_rate`,
        'equals',
        'audio_sample_rate_invalid',
        'Reel audio must use a 48 kHz sample rate.',
        { actual: item.audio_sample_rate ?? 0, expected: 48000 },
      )
    }
    if (
      (item.audio_bitrate ?? 0) <= 0 ||
      (item.audio_bitrate ?? 0) > 128_000
    ) {
      add(
        `${field}.audio_bitrate`,
        'range',
        'audio_bitrate_invalid',
        'Reel audio bitrate must be at most 128 kbps.',
        { actual: item.audio_bitrate ?? 0, maximum: 128_000 },
      )
    }
  }
  if (
    (item.frames_per_second ?? 0) < 23 ||
    (item.frames_per_second ?? 0) > 60
  ) {
    add(
      `${field}.frames_per_second`,
      'range',
      'frame_rate_invalid',
      'Reel frame rate must be between 23 and 60 fps.',
      { actual: item.frames_per_second ?? 0, minimum: 23, maximum: 60 },
    )
  }
  if ((item.video_bitrate ?? 0) <= 0 || (item.video_bitrate ?? 0) > 25_000_000) {
    add(
      `${field}.video_bitrate`,
      'range',
      'video_bitrate_invalid',
      'Reel video bitrate must be at most 25 Mbps.',
      { actual: item.video_bitrate ?? 0, maximum: 25_000_000 },
    )
  }
  if (
    item.width < 720 ||
    item.width > 1080 ||
    item.height < 1280 ||
    item.height > 1920
  ) {
    add(
      `${field}.resolution`,
      'range',
      'video_resolution_invalid',
      'Reel resolution must be between 720×1280 and 1080×1920.',
      {
        width: item.width,
        height: item.height,
        minimum_width: 720,
        maximum_width: 1080,
        minimum_height: 1280,
        maximum_height: 1920,
      },
    )
  }
  if (
    item.height > 0 &&
    Math.abs(item.width / item.height - REEL_RATIO) > RATIO_TOLERANCE
  ) {
    add(
      `${field}.aspect_ratio`,
      'equals',
      'video_ratio_invalid',
      'Reels must use a 9:16 aspect ratio.',
      { actual: item.width / item.height, expected: REEL_RATIO },
    )
  }
  if (
    (item.duration_seconds ?? 0) < 4 ||
    (item.duration_seconds ?? 0) > 60
  ) {
    add(
      `${field}.duration_seconds`,
      'range',
      'video_duration_invalid',
      'Reel duration must be between 4 and 60 seconds.',
      { actual: item.duration_seconds ?? 0, minimum: 4, maximum: 60 },
    )
  }
  if (item.has_edit_list === true) {
    add(
      `${field}.has_edit_list`,
      'false',
      'video_edit_list_not_allowed',
      'Reels cannot contain an edit list.',
    )
  }
  if (item.moov_before_media_data !== true) {
    add(
      `${field}.moov_before_media_data`,
      'true',
      'video_fast_start_required',
      'The MP4 moov atom must precede media data.',
    )
  }
}

function validateTextLength(
  text: string,
  minimum: number,
  maximum: number,
  add: AddError,
): void {
  const length = Array.from(text.normalize('NFC')).length
  if (length < minimum) {
    add(
      'text',
      'minimum_code_points',
      'text_too_short',
      'Text is required for this destination.',
      { actual: length, minimum },
    )
  }
  if (length > maximum) {
    add(
      'text',
      'maximum_code_points',
      'text_too_long',
      'Text exceeds the limit for this destination.',
      { actual: length, maximum },
    )
  }
}

function validateMediaCount(
  media: Media[],
  minimum: number,
  maximum: number,
  add: AddError,
): boolean {
  if (media.length < minimum || media.length > maximum) {
    add(
      'media',
      'count_range',
      'media_count_invalid',
      'The number of media items is invalid for this format.',
      { actual: media.length, minimum, maximum },
    )
    return false
  }
  return true
}

function checkEqual(
  add: AddError,
  field: string,
  expectedLabel: string,
  code: string,
  actual: unknown,
  expected: unknown,
): void {
  if (actual === expected) return
  add(field, 'equals', code, `${field} must be ${expectedLabel}.`, {
    actual,
    expected,
  })
}

function supportedChannel(channel: string): channel is ChannelType {
  return channel === 'facebook_page' || channel === 'instagram_professional'
}

function supportedFormat(channel: ChannelType, format: ComposerFormat): boolean {
  if (channel === 'facebook_page') {
    return format === 'text' || format === 'image' || format === 'reel'
  }
  if (channel === 'instagram_professional') {
    return format === 'image' || format === 'carousel' || format === 'reel'
  }
  return false
}
