import type {
  CapabilityCatalog,
  ContentCapability,
  Destination,
  DestinationValidation,
  DraftContent,
  FieldRules,
  LinkRules,
  Media,
  MediaRules,
  ThreadItem,
  ThreadRules,
  ValidationError,
  ValidationReport,
} from './contracts.ts'

type AddError = (
  field: string,
  rule: string,
  code: string,
  message: string,
  remedy: string,
  details?: Record<string, unknown>,
) => void

const blockedCatalog: CapabilityCatalog = {
  version: 'pending-d02-301',
  status: 'blocked',
  blocker:
    'Provider/channel availability and limits require the versioned matrix from issue #301.',
  capabilities: [],
}

// Browser validation consumes the exact versioned capability catalog returned
// by the server. It never infers provider limits; the server repeats all checks.
export function validateDraft(
  content: DraftContent,
  catalog: CapabilityCatalog = blockedCatalog,
): ValidationReport {
  const errors: ValidationError[] = []
  if (content.destinations.length === 0) {
    errors.push({
      field: 'destinations',
      rule: 'required',
      code: 'destinations_required',
      message: 'Select at least one destination.',
      remedy: 'Choose one or more connected channels before scheduling.',
    })
  }
  const mediaByID = new Map(content.media.map((item) => [item.id, item]))
  const capabilities = new Map(
    catalog.capabilities.map((capability) => [capability.id, capability]),
  )
  const destinations = content.destinations.map((destination) =>
    validateDestination(content, destination, mediaByID, capabilities),
  )
  return {
    capability_version: catalog.version,
    valid: errors.length === 0 && destinations.every((item) => item.valid),
    errors,
    destinations,
  }
}

function validateDestination(
  content: DraftContent,
  destination: Destination,
  mediaByID: ReadonlyMap<string, Media>,
  capabilities: ReadonlyMap<string, ContentCapability>,
): DestinationValidation {
  const errors: ValidationError[] = []
  const add: AddError = (field, rule, code, message, remedy, details) => {
    errors.push({
      destination_id: destination.id,
      field,
      rule,
      code,
      message,
      remedy,
      ...(details === undefined ? {} : { details }),
    })
  }
  const capability = capabilities.get(destination.capability_id)
  if (capability === undefined) {
    add(
      'capability_id',
      'catalog_reference',
      'capability_unknown',
      'The selected publishing capability is not in the active catalog.',
      'Refresh channel capabilities and select an available format.',
      { capability_id: destination.capability_id },
    )
    return destinationResult(destination, errors)
  }
  if (!capability.available) {
    add(
      'capability_id',
      'available',
      'capability_unavailable',
      'The selected publishing capability is not available.',
      'Choose an available channel format or wait for its provider prerequisites.',
      { reason: capability.unavailable_reason ?? '' },
    )
  }
  if (destination.channel_type !== capability.channel_type) {
    add(
      'channel_type',
      'matches_capability',
      'channel_capability_mismatch',
      'The selected channel type does not match the capability.',
      'Refresh the channel and format selection.',
      { actual: destination.channel_type, expected: capability.channel_type },
    )
  }
  if (destination.format !== capability.format) {
    add(
      'format',
      'matches_capability',
      'format_capability_mismatch',
      'The selected format does not match the capability.',
      'Select the format advertised by the channel capability.',
      { actual: destination.format, expected: capability.format },
    )
  }

  const text = (
    destination.text_override == null ? content.text : destination.text_override
  ).normalize('NFC')
  const link = (
    destination.link_override == null ? content.link : destination.link_override
  ).trim()
  const thread =
    destination.thread_override == null
      ? content.thread
      : destination.thread_override
  const media = resolveMedia(content.media, destination, mediaByID, add)
  validateText(text, capability, add)
  validateLink(link, text, capability.link, add)
  validateMedia(media, capability.media, add)
  validateThread(thread, mediaByID, capability.thread, add)
  validateFields(destination.fields ?? {}, capability.fields ?? [], add)
  return destinationResult(destination, errors)
}

function destinationResult(
  destination: Destination,
  errors: ValidationError[],
): DestinationValidation {
  return {
    destination_id: destination.id,
    channel_id: destination.channel_id,
    channel_type: destination.channel_type,
    capability_id: destination.capability_id,
    format: destination.format,
    valid: errors.length === 0,
    errors,
  }
}

function resolveMedia(
  defaults: Media[],
  destination: Destination,
  mediaByID: ReadonlyMap<string, Media>,
  add: AddError,
): Media[] {
  if (destination.media_ids == null) return defaults
  const resolved: Media[] = []
  const seen = new Set<string>()
  destination.media_ids.forEach((rawID, index) => {
    const id = rawID.trim()
    if (seen.has(id)) {
      add(
        `media_ids[${index}]`,
        'unique',
        'media_reference_duplicate',
        'A destination cannot reference the same media more than once.',
        'Remove the duplicate media selection.',
        { media_id: id },
      )
      return
    }
    seen.add(id)
    const item = mediaByID.get(id)
    if (item === undefined) {
      add(
        `media_ids[${index}]`,
        'references_draft_media',
        'media_not_found',
        'The selected media does not belong to this draft.',
        'Upload the media or select an asset already attached to this draft.',
        { media_id: id },
      )
      return
    }
    resolved.push(item)
  })
  return resolved
}

function validateText(
  text: string,
  capability: ContentCapability,
  add: AddError,
): void {
  const length = Array.from(text.normalize('NFC')).length
  const rules = capability.text
  if (!rules.allowed && length > 0) {
    add(
      'text',
      'not_allowed',
      'text_not_allowed',
      'Text is not supported for this destination.',
      'Remove the text or choose a format that supports it.',
    )
    return
  }
  const minimum = Math.max(rules.min_characters ?? 0, rules.required ? 1 : 0)
  if (length < minimum) {
    add(
      'text',
      'minimum_characters',
      'text_too_short',
      'The text is shorter than this destination allows.',
      'Add text until the minimum length is reached.',
      { actual: length, minimum },
    )
  }
  const maximum = rules.max_characters ?? 0
  if (maximum > 0 && length > maximum) {
    add(
      'text',
      'maximum_characters',
      'text_too_long',
      'The text is longer than this destination allows.',
      'Shorten the text or add a destination-specific override.',
      { actual: length, maximum },
    )
  }
}

function validateLink(
  link: string,
  text: string,
  rules: LinkRules,
  add: AddError,
): void {
  if (!rules.allowed && link !== '') {
    add(
      'link',
      'not_allowed',
      'link_not_allowed',
      'A separate link is not supported for this destination.',
      'Remove the link or choose a capability that accepts links.',
    )
    return
  }
  if (rules.required && link === '') {
    add(
      'link',
      'required',
      'link_required',
      'A link is required for this destination.',
      'Enter an absolute public URL.',
    )
  }
  const urls = link === '' ? [] : [link]
  for (const field of text.split(/\s+/u)) {
    try {
      const parsed = new URL(field)
      if (parsed.protocol !== '') urls.push(field)
    } catch {
      // Plain text is not a URL.
    }
  }
  const maximum = rules.maximum_urls ?? 0
  if (maximum > 0 && urls.length > maximum) {
    add(
      'link',
      'maximum_urls',
      'too_many_urls',
      'The destination contains more links than its capability allows.',
      'Remove links or use a destination-specific text override.',
      { actual: urls.length, maximum },
    )
  }
  urls.forEach((rawURL) => validateURL(rawURL, rules, add))
}

function validateURL(rawURL: string, rules: LinkRules, add: AddError): void {
  let parsed: URL
  try {
    parsed = new URL(rawURL)
  } catch {
    add(
      'link',
      'absolute_url',
      'url_invalid',
      'The link must be an absolute URL.',
      'Enter a complete URL including its scheme and host.',
      { url: rawURL },
    )
    return
  }
  if (parsed.username !== '' || parsed.password !== '') {
    add(
      'link',
      'no_credentials',
      'url_credentials_not_allowed',
      'Links cannot contain credentials.',
      'Remove the username and password from the URL.',
    )
  }
  if (rules.require_https === true && parsed.protocol !== 'https:') {
    add(
      'link',
      'https',
      'url_must_be_https',
      'The destination requires an HTTPS link.',
      'Use the HTTPS version of the URL.',
      { url: rawURL },
    )
  }
  if (
    rules.require_public_host === true &&
    isNonPublicLiteralHost(parsed.hostname)
  ) {
    add(
      'link',
      'public_host',
      'url_host_not_public',
      'The link targets a private or local address.',
      'Use a publicly reachable URL.',
      { host: parsed.hostname },
    )
  }
}

function isNonPublicLiteralHost(host: string): boolean {
  const normalized = host.replace(/^\[|\]$/gu, '').toLowerCase()
  if (
    normalized === 'localhost' ||
    normalized === '::' ||
    normalized === '::1'
  ) {
    return true
  }
  if (
    normalized.startsWith('fc') ||
    normalized.startsWith('fd') ||
    /^fe[89ab]/u.test(normalized)
  ) {
    return true
  }
  const octets = normalized.split('.').map(Number)
  if (
    octets.length !== 4 ||
    octets.some(
      (octet) => !Number.isInteger(octet) || octet < 0 || octet > 255,
    )
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

function validateMedia(media: Media[], rules: MediaRules, add: AddError): void {
  if (!rules.allowed && media.length > 0) {
    add(
      'media',
      'not_allowed',
      'media_not_allowed',
      'Media is not supported for this destination.',
      'Remove the media or choose a capability that supports it.',
    )
    return
  }
  const minimum = rules.minimum_items ?? 0
  const maximum = rules.maximum_items ?? 0
  if (media.length < minimum) {
    add(
      'media',
      'minimum_items',
      'media_too_few',
      'The destination does not have enough media items.',
      'Add media until the minimum is reached.',
      { actual: media.length, minimum },
    )
  }
  if (maximum > 0 && media.length > maximum) {
    add(
      'media',
      'maximum_items',
      'media_too_many',
      'The destination has too many media items.',
      'Remove media or choose a compatible format.',
      { actual: media.length, maximum },
    )
  }
  let total = 0
  media.forEach((item, index) => {
    total += item.size_bytes
    validateMediaItem(item, index, rules, add)
  })
  const totalMaximum = rules.maximum_bytes_total ?? 0
  if (totalMaximum > 0 && total > totalMaximum) {
    add(
      'media',
      'maximum_total_bytes',
      'media_total_too_large',
      'The combined media size exceeds the destination capability.',
      'Reduce the media size or number of items.',
      { actual: total, maximum: totalMaximum },
    )
  }
}

function validateMediaItem(
  item: Media,
  index: number,
  rules: MediaRules,
  add: AddError,
): void {
  const field = `media[${index}]`
  if (item.inspection_status !== 'ready') {
    add(
      `${field}.inspection_status`,
      'ready',
      'media_not_inspected',
      'The media upload has not passed server inspection.',
      'Wait for inspection or replace the rejected upload.',
    )
  }
  if (
    (rules.allowed_kinds?.length ?? 0) > 0 &&
    !rules.allowed_kinds?.includes(item.kind)
  ) {
    add(
      `${field}.kind`,
      'allowed_kind',
      'media_kind_invalid',
      'The media kind is not supported for this destination.',
      'Replace it with an allowed media kind.',
    )
  }
  if (
    (rules.allowed_content_types?.length ?? 0) > 0 &&
    !rules.allowed_content_types?.some(
      (value) => value.toLowerCase() === item.content_type.toLowerCase(),
    )
  ) {
    add(
      `${field}.content_type`,
      'allowed_content_type',
      'media_type_invalid',
      'The inspected media type is not supported for this destination.',
      'Upload media using one of the allowed content types.',
    )
  }
  checkRange(
    `${field}.size_bytes`,
    item.size_bytes,
    1,
    rules.maximum_bytes_each ?? 0,
    'media_size_invalid',
    add,
  )
  checkRange(
    `${field}.width`,
    item.width ?? 0,
    rules.minimum_width ?? 0,
    rules.maximum_width ?? 0,
    'media_dimension_invalid',
    add,
  )
  checkRange(
    `${field}.height`,
    item.height ?? 0,
    rules.minimum_height ?? 0,
    rules.maximum_height ?? 0,
    'media_dimension_invalid',
    add,
  )
  const width = item.width ?? 0
  const height = item.height ?? 0
  if (width > 0 && height > 0) {
    checkRange(
      `${field}.aspect_ratio`,
      width / height,
      rules.minimum_aspect_ratio ?? 0,
      rules.maximum_aspect_ratio ?? 0,
      'media_aspect_ratio_invalid',
      add,
    )
  }
  checkRange(
    `${field}.duration_seconds`,
    item.duration_seconds ?? 0,
    rules.minimum_duration_seconds ?? 0,
    rules.maximum_duration_seconds ?? 0,
    'media_duration_invalid',
    add,
  )
  if (
    (rules.allowed_video_codecs?.length ?? 0) > 0 &&
    !includesFold(rules.allowed_video_codecs ?? [], item.video_codec ?? '')
  ) {
    add(
      `${field}.video_codec`,
      'allowed_codec',
      'video_codec_invalid',
      'The inspected video codec is not supported.',
      'Transcode the video to an advertised codec.',
    )
  }
  if (rules.require_audio === true && item.has_audio !== true) {
    add(
      `${field}.has_audio`,
      'required',
      'audio_required',
      'This destination requires an audio track.',
      'Upload a video containing audio.',
    )
  }
  if (
    item.has_audio === true &&
    (rules.allowed_audio_codecs?.length ?? 0) > 0 &&
    !includesFold(rules.allowed_audio_codecs ?? [], item.audio_codec ?? '')
  ) {
    add(
      `${field}.audio_codec`,
      'allowed_codec',
      'audio_codec_invalid',
      'The inspected audio codec is not supported.',
      'Transcode audio to an advertised codec.',
    )
  }
}

function checkRange(
  field: string,
  actual: number,
  minimum: number,
  maximum: number,
  code: string,
  add: AddError,
): void {
  if ((minimum > 0 && actual < minimum) || (maximum > 0 && actual > maximum)) {
    add(
      field,
      'range',
      code,
      'The inspected media value is outside the allowed range.',
      'Adjust the media using the advertised capability limits.',
      { actual, minimum, maximum },
    )
  }
}

function validateThread(
  thread: ThreadItem[],
  mediaByID: ReadonlyMap<string, Media>,
  rules: ThreadRules,
  add: AddError,
): void {
  if (!rules.allowed && thread.length > 0) {
    add(
      'thread',
      'not_allowed',
      'thread_not_allowed',
      'A thread is not supported for this destination.',
      'Remove the thread or choose a thread capability.',
    )
    return
  }
  const minimum = Math.max(rules.minimum_items ?? 0, rules.required ? 1 : 0)
  const maximum = rules.maximum_items ?? 0
  if (thread.length < minimum) {
    add(
      'thread',
      'minimum_items',
      'thread_too_short',
      'The thread has too few items.',
      'Add thread items until the minimum is reached.',
    )
  }
  if (maximum > 0 && thread.length > maximum) {
    add(
      'thread',
      'maximum_items',
      'thread_too_long',
      'The thread has too many items.',
      'Remove thread items until the advertised limit is met.',
    )
  }
  thread.forEach((item, index) => {
    const length = Array.from(item.text.normalize('NFC')).length
    const textMaximum = rules.max_item_characters ?? 0
    if (textMaximum > 0 && length > textMaximum) {
      add(
        `thread[${index}].text`,
        'maximum_characters',
        'thread_item_text_too_long',
        'A thread item is longer than the destination allows.',
        'Shorten that thread item.',
      )
    }
    const mediaMaximum = rules.max_media_per_item ?? 0
    if (item.media_ids.length > mediaMaximum) {
      add(
        `thread[${index}].media_ids`,
        'maximum_items',
        'thread_item_media_too_many',
        'A thread item has too many media attachments.',
        'Remove media from that thread item.',
      )
    }
    item.media_ids.forEach((mediaID, mediaIndex) => {
      if (!mediaByID.has(mediaID)) {
        add(
          `thread[${index}].media_ids[${mediaIndex}]`,
          'references_draft_media',
          'media_not_found',
          'The thread references media that is not attached to the draft.',
          'Upload or attach the referenced media.',
        )
      }
    })
  })
}

function validateFields(
  values: Record<string, string>,
  rules: FieldRules[],
  add: AddError,
): void {
  const known = new Map(rules.map((rule) => [rule.name, rule]))
  rules.forEach((rule) => {
    const value = (values[rule.name] ?? '').trim()
    if (rule.required && value === '') {
      add(
        `fields.${rule.name}`,
        'required',
        'destination_field_required',
        'A destination-specific field is required.',
        'Complete the field using the capability definition.',
      )
    }
    if ((rule.max_length ?? 0) > 0 && Array.from(value).length > (rule.max_length ?? 0)) {
      add(
        `fields.${rule.name}`,
        'maximum_characters',
        'destination_field_too_long',
        'A destination-specific field is too long.',
        'Shorten the value to the advertised limit.',
      )
    }
    if (
      value !== '' &&
      (rule.allowed_values?.length ?? 0) > 0 &&
      !rule.allowed_values?.includes(value)
    ) {
      add(
        `fields.${rule.name}`,
        'allowed_value',
        'destination_field_invalid',
        'A destination-specific field has an unsupported value.',
        'Choose one of the values advertised by the capability.',
      )
    }
  })
  Object.keys(values).forEach((name) => {
    if (!known.has(name)) {
      add(
        `fields.${name}`,
        'declared_by_capability',
        'destination_field_unknown',
        'The destination-specific field is not declared by the capability.',
        'Remove the field or refresh the capability catalog.',
      )
    }
  })
}

function includesFold(values: string[], candidate: string): boolean {
  return values.some(
    (value) => value.toLowerCase() === candidate.toLowerCase(),
  )
}
