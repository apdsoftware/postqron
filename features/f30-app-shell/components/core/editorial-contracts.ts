// Typed, client-safe projection of the authoritative F6 (#317) and F7 (#308)
// browser contracts. No provider rule is reproduced here: validation and
// capability decisions remain owned by the backend catalogs.

export const COMPOSER_FORMATS = [
  'text',
  'link',
  'image',
  'carousel',
  'video',
  'short_video',
  'thread',
] as const
export type ComposerFormat = typeof COMPOSER_FORMATS[number]
export type MediaKind = 'image' | 'video'
export type InspectionStatus = 'pending' | 'ready' | 'rejected'

export interface ComposerMedia {
  id: string
  kind: MediaKind
  content_type: string
  size_bytes: number
  width?: number
  height?: number
  duration_seconds?: number
  inspection_status: InspectionStatus
  url: string
  expires_at?: string
}

export interface ThreadItem {
  text: string
  media_ids: string[]
}

export interface ComposerDestination {
  id: string
  channel_id: string
  channel_type: string
  capability_id: string
  format: ComposerFormat
  text_override?: string | null
  link_override?: string | null
  media_ids?: string[] | null
  thread_override?: ThreadItem[] | null
  fields?: Record<string, string>
}

export interface DraftContent {
  text: string
  link: string
  media: ComposerMedia[]
  thread: ThreadItem[]
  destinations: ComposerDestination[]
}

export interface TextRules {
  allowed: boolean
  required: boolean
  min_characters?: number
  max_characters?: number
}

export interface LinkRules {
  allowed: boolean
  required: boolean
  maximum_urls?: number
  require_https?: boolean
  require_public_host?: boolean
}

export interface MediaRules {
  allowed: boolean
  minimum_items?: number
  maximum_items?: number
  allowed_kinds?: MediaKind[]
  allowed_content_types?: string[]
  maximum_bytes_each?: number
  maximum_bytes_total?: number
  minimum_width?: number
  maximum_width?: number
  minimum_height?: number
  maximum_height?: number
  minimum_aspect_ratio?: number
  maximum_aspect_ratio?: number
  minimum_duration_seconds?: number
  maximum_duration_seconds?: number
  allowed_video_codecs?: string[]
  allowed_audio_codecs?: string[]
  require_audio?: boolean
}

export interface ThreadRules {
  allowed: boolean
  required: boolean
  minimum_items?: number
  maximum_items?: number
  max_item_characters?: number
  max_media_per_item?: number
}

export interface FieldRules {
  name: string
  required: boolean
  max_length?: number
  allowed_values?: string[]
}

export interface ContentCapability {
  id: string
  provider: string
  channel_type: string
  format: ComposerFormat
  available: boolean
  unavailable_reason?: string
  text: TextRules
  link: LinkRules
  media: MediaRules
  thread: ThreadRules
  fields?: FieldRules[]
}

export interface CapabilityCatalog {
  version: string
  status: string
  blocker?: string
  capabilities: ContentCapability[]
}

export interface ValidationError {
  destination_id?: string
  field: string
  rule: string
  code: string
  message: string
  remedy?: string
}

export interface DestinationValidation {
  destination_id: string
  channel_id: string
  channel_type: string
  capability_id: string
  format: ComposerFormat
  valid: boolean
  errors: ValidationError[]
}

export interface ValidationReport {
  capability_version: string
  valid: boolean
  errors: ValidationError[]
  destinations: DestinationValidation[]
}

export interface Draft {
  id: string
  workspace_id: string
  created_by: string
  content: DraftContent
  revision: number
  created_at: string
  updated_at: string
}

export interface DraftView {
  draft: Draft
  validation: ValidationReport
}

export interface MediaUpload {
  id: string
  status: 'pending'
  upload_url: string
  upload_headers: Record<string, string>
  complete_url: string
  expires_at: string
  max_bytes: number
}

export const SCHEDULING_STATUSES = [
  'scheduled',
  'publishing',
  'published',
  'failed',
  'cancelled',
] as const
export type SchedulingPostStatus = typeof SCHEDULING_STATUSES[number]

export interface ScheduleInput {
  local_date_time: string
  time_zone: string
  utc_offset_minutes?: number
}

export interface ScheduledPost {
  id: string
  workspace_id: string
  draft_id: string
  channel_ids: string[]
  status: SchedulingPostStatus
  scheduled_for_utc: string
  scheduled_local: string
  time_zone: string
  utc_offset_minutes: number
  revision: number
  duplicated_from_post_id?: string
  created_at: string
  updated_at: string
  cancelled_at?: string
}

export interface CalendarEntry {
  post_id: string
  draft_id: string
  channel_ids: string[]
  status: SchedulingPostStatus
  scheduled_for_utc: string
  scheduled_local: string
  time_zone: string
  utc_offset_minutes: number
  revision: number
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return Boolean(value) && typeof value === 'object' && !Array.isArray(value)
}

function requiredText(value: unknown, code: string): string {
  if (typeof value !== 'string' || value.trim() === '') {
    throw new Error(code)
  }
  return value
}

function optionalText(value: unknown): string | undefined {
  return typeof value === 'string' && value.trim() !== '' ? value : undefined
}

function requiredBoolean(value: unknown, code: string): boolean {
  if (typeof value !== 'boolean') {
    throw new Error(code)
  }
  return value
}

function requiredInteger(value: unknown, code: string, minimum = 0): number {
  if (!Number.isInteger(value) || Number(value) < minimum) {
    throw new Error(code)
  }
  return Number(value)
}

function optionalNumber(value: unknown): number | undefined {
  return typeof value === 'number' && Number.isFinite(value) ? value : undefined
}

function isoDateTime(value: unknown, code: string): string {
  const result = requiredText(value, code)
  if (Number.isNaN(Date.parse(result))) {
    throw new Error(code)
  }
  return result
}

function stringList(value: unknown, code: string): string[] {
  if (!Array.isArray(value) || value.some(item => typeof item !== 'string')) {
    throw new Error(code)
  }
  return value.map(String)
}

function optionalStringList(value: unknown, code: string): string[] | undefined {
  return value === undefined ? undefined : stringList(value, code)
}

function parseRuleObject(
  value: unknown,
  code: string,
): Record<string, unknown> {
  if (!isRecord(value)) {
    throw new Error(code)
  }
  return value
}

const INVALID_CAPABILITIES = 'EDITORIAL_INVALID_CAPABILITIES'
const INVALID_DRAFT = 'EDITORIAL_INVALID_DRAFT'
const INVALID_MEDIA = 'EDITORIAL_INVALID_MEDIA'
const INVALID_SCHEDULE = 'EDITORIAL_INVALID_SCHEDULE'

function parseTextRules(value: unknown): TextRules {
  const item = parseRuleObject(value, INVALID_CAPABILITIES)
  return {
    allowed: requiredBoolean(item.allowed, INVALID_CAPABILITIES),
    required: requiredBoolean(item.required, INVALID_CAPABILITIES),
    min_characters: optionalNumber(item.min_characters),
    max_characters: optionalNumber(item.max_characters),
  }
}

function parseLinkRules(value: unknown): LinkRules {
  const item = parseRuleObject(value, INVALID_CAPABILITIES)
  return {
    allowed: requiredBoolean(item.allowed, INVALID_CAPABILITIES),
    required: requiredBoolean(item.required, INVALID_CAPABILITIES),
    maximum_urls: optionalNumber(item.maximum_urls),
    require_https: typeof item.require_https === 'boolean'
      ? item.require_https
      : undefined,
    require_public_host: typeof item.require_public_host === 'boolean'
      ? item.require_public_host
      : undefined,
  }
}

function parseMediaRules(value: unknown): MediaRules {
  const item = parseRuleObject(value, INVALID_CAPABILITIES)
  const kinds = optionalStringList(item.allowed_kinds, INVALID_CAPABILITIES)
  if (kinds?.some(kind => kind !== 'image' && kind !== 'video')) {
    throw new Error(INVALID_CAPABILITIES)
  }
  return {
    allowed: requiredBoolean(item.allowed, INVALID_CAPABILITIES),
    minimum_items: optionalNumber(item.minimum_items),
    maximum_items: optionalNumber(item.maximum_items),
    allowed_kinds: kinds as MediaKind[] | undefined,
    allowed_content_types: optionalStringList(
      item.allowed_content_types,
      INVALID_CAPABILITIES,
    ),
    maximum_bytes_each: optionalNumber(item.maximum_bytes_each),
    maximum_bytes_total: optionalNumber(item.maximum_bytes_total),
    minimum_width: optionalNumber(item.minimum_width),
    maximum_width: optionalNumber(item.maximum_width),
    minimum_height: optionalNumber(item.minimum_height),
    maximum_height: optionalNumber(item.maximum_height),
    minimum_aspect_ratio: optionalNumber(item.minimum_aspect_ratio),
    maximum_aspect_ratio: optionalNumber(item.maximum_aspect_ratio),
    minimum_duration_seconds: optionalNumber(item.minimum_duration_seconds),
    maximum_duration_seconds: optionalNumber(item.maximum_duration_seconds),
    allowed_video_codecs: optionalStringList(
      item.allowed_video_codecs,
      INVALID_CAPABILITIES,
    ),
    allowed_audio_codecs: optionalStringList(
      item.allowed_audio_codecs,
      INVALID_CAPABILITIES,
    ),
    require_audio: typeof item.require_audio === 'boolean'
      ? item.require_audio
      : undefined,
  }
}

function parseThreadRules(value: unknown): ThreadRules {
  const item = parseRuleObject(value, INVALID_CAPABILITIES)
  return {
    allowed: requiredBoolean(item.allowed, INVALID_CAPABILITIES),
    required: requiredBoolean(item.required, INVALID_CAPABILITIES),
    minimum_items: optionalNumber(item.minimum_items),
    maximum_items: optionalNumber(item.maximum_items),
    max_item_characters: optionalNumber(item.max_item_characters),
    max_media_per_item: optionalNumber(item.max_media_per_item),
  }
}

function parseFieldRules(value: unknown): FieldRules {
  const item = parseRuleObject(value, INVALID_CAPABILITIES)
  return {
    name: requiredText(item.name, INVALID_CAPABILITIES),
    required: requiredBoolean(item.required, INVALID_CAPABILITIES),
    max_length: optionalNumber(item.max_length),
    allowed_values: optionalStringList(item.allowed_values, INVALID_CAPABILITIES),
  }
}

function parseCapability(value: unknown): ContentCapability {
  if (!isRecord(value)
    || !COMPOSER_FORMATS.includes(value.format as ComposerFormat)
    || typeof value.available !== 'boolean') {
    throw new Error(INVALID_CAPABILITIES)
  }
  return {
    id: requiredText(value.id, INVALID_CAPABILITIES),
    provider: requiredText(value.provider, INVALID_CAPABILITIES),
    channel_type: requiredText(value.channel_type, INVALID_CAPABILITIES),
    format: value.format as ComposerFormat,
    available: value.available,
    unavailable_reason: optionalText(value.unavailable_reason),
    text: parseTextRules(value.text),
    link: parseLinkRules(value.link),
    media: parseMediaRules(value.media),
    thread: parseThreadRules(value.thread),
    fields: Array.isArray(value.fields)
      ? value.fields.map(parseFieldRules)
      : undefined,
  }
}

export function parseCapabilityCatalog(value: unknown): CapabilityCatalog {
  if (!isRecord(value) || !Array.isArray(value.capabilities)) {
    throw new Error(INVALID_CAPABILITIES)
  }
  return {
    version: requiredText(value.version, INVALID_CAPABILITIES),
    status: requiredText(value.status, INVALID_CAPABILITIES),
    blocker: optionalText(value.blocker),
    capabilities: value.capabilities.map(parseCapability),
  }
}

function parseMedia(value: unknown): ComposerMedia {
  if (!isRecord(value)
    || (value.kind !== 'image' && value.kind !== 'video')
    || !['pending', 'ready', 'rejected'].includes(String(value.inspection_status))) {
    throw new Error(INVALID_MEDIA)
  }
  const url = requiredText(value.url, INVALID_MEDIA)
  if (!url.startsWith('/api/v1/')) {
    throw new Error(INVALID_MEDIA)
  }
  return {
    id: requiredText(value.id, INVALID_MEDIA),
    kind: value.kind,
    content_type: requiredText(value.content_type, INVALID_MEDIA),
    size_bytes: requiredInteger(value.size_bytes, INVALID_MEDIA, 1),
    width: optionalNumber(value.width),
    height: optionalNumber(value.height),
    duration_seconds: optionalNumber(value.duration_seconds),
    inspection_status: value.inspection_status as InspectionStatus,
    url,
    expires_at: value.expires_at === undefined
      ? undefined
      : isoDateTime(value.expires_at, INVALID_MEDIA),
  }
}

function parseThreadItem(value: unknown): ThreadItem {
  if (!isRecord(value)) {
    throw new Error(INVALID_DRAFT)
  }
  return {
    text: typeof value.text === 'string' ? value.text : (() => {
      throw new Error(INVALID_DRAFT)
    })(),
    media_ids: stringList(value.media_ids, INVALID_DRAFT),
  }
}

function parseDestination(value: unknown): ComposerDestination {
  if (!isRecord(value)
    || !COMPOSER_FORMATS.includes(value.format as ComposerFormat)) {
    throw new Error(INVALID_DRAFT)
  }
  const fields = value.fields
  if (fields !== undefined
    && (!isRecord(fields)
      || Object.values(fields).some(item => typeof item !== 'string'))) {
    throw new Error(INVALID_DRAFT)
  }
  return {
    id: requiredText(value.id, INVALID_DRAFT),
    channel_id: requiredText(value.channel_id, INVALID_DRAFT),
    channel_type: requiredText(value.channel_type, INVALID_DRAFT),
    capability_id: requiredText(value.capability_id, INVALID_DRAFT),
    format: value.format as ComposerFormat,
    text_override: value.text_override === null
      ? null
      : optionalText(value.text_override),
    link_override: value.link_override === null
      ? null
      : optionalText(value.link_override),
    media_ids: value.media_ids === null
      ? null
      : optionalStringList(value.media_ids, INVALID_DRAFT),
    thread_override: value.thread_override === null
      ? null
      : Array.isArray(value.thread_override)
        ? value.thread_override.map(parseThreadItem)
        : undefined,
    fields: fields as Record<string, string> | undefined,
  }
}

export function parseDraftContent(value: unknown): DraftContent {
  if (!isRecord(value)
    || typeof value.text !== 'string'
    || typeof value.link !== 'string'
    || !Array.isArray(value.media)
    || !Array.isArray(value.thread)
    || !Array.isArray(value.destinations)) {
    throw new Error(INVALID_DRAFT)
  }
  return {
    text: value.text,
    link: value.link,
    media: value.media.map(parseMedia),
    thread: value.thread.map(parseThreadItem),
    destinations: value.destinations.map(parseDestination),
  }
}

function parseValidationError(value: unknown): ValidationError {
  if (!isRecord(value)) {
    throw new Error(INVALID_DRAFT)
  }
  return {
    destination_id: optionalText(value.destination_id),
    field: requiredText(value.field, INVALID_DRAFT),
    rule: requiredText(value.rule, INVALID_DRAFT),
    code: requiredText(value.code, INVALID_DRAFT),
    message: requiredText(value.message, INVALID_DRAFT),
    remedy: optionalText(value.remedy),
  }
}

function parseValidation(value: unknown): ValidationReport {
  if (!isRecord(value)
    || typeof value.valid !== 'boolean'
    || !Array.isArray(value.errors)
    || !Array.isArray(value.destinations)) {
    throw new Error(INVALID_DRAFT)
  }
  return {
    capability_version: requiredText(value.capability_version, INVALID_DRAFT),
    valid: value.valid,
    errors: value.errors.map(parseValidationError),
    destinations: value.destinations.map((destination) => {
      if (!isRecord(destination)
        || !COMPOSER_FORMATS.includes(destination.format as ComposerFormat)
        || typeof destination.valid !== 'boolean'
        || !Array.isArray(destination.errors)) {
        throw new Error(INVALID_DRAFT)
      }
      return {
        destination_id: requiredText(destination.destination_id, INVALID_DRAFT),
        channel_id: requiredText(destination.channel_id, INVALID_DRAFT),
        channel_type: requiredText(destination.channel_type, INVALID_DRAFT),
        capability_id: requiredText(destination.capability_id, INVALID_DRAFT),
        format: destination.format as ComposerFormat,
        valid: destination.valid,
        errors: destination.errors.map(parseValidationError),
      }
    }),
  }
}

export function parseDraftView(value: unknown): DraftView {
  if (!isRecord(value) || !isRecord(value.draft)) {
    throw new Error(INVALID_DRAFT)
  }
  const draft = value.draft
  return {
    draft: {
      id: requiredText(draft.id, INVALID_DRAFT),
      workspace_id: requiredText(draft.workspace_id, INVALID_DRAFT),
      created_by: requiredText(draft.created_by, INVALID_DRAFT),
      content: parseDraftContent(draft.content),
      revision: requiredInteger(draft.revision, INVALID_DRAFT, 1),
      created_at: isoDateTime(draft.created_at, INVALID_DRAFT),
      updated_at: isoDateTime(draft.updated_at, INVALID_DRAFT),
    },
    validation: parseValidation(value.validation),
  }
}

export function parseDraftViews(value: unknown): DraftView[] {
  if (!isRecord(value) || !Array.isArray(value.drafts)) {
    throw new Error(INVALID_DRAFT)
  }
  return value.drafts.map(parseDraftView)
}

export function parseValidationResponse(value: unknown): ValidationReport {
  if (!isRecord(value)) {
    throw new Error(INVALID_DRAFT)
  }
  return parseValidation(value.validation)
}

export function parseMediaUpload(value: unknown): MediaUpload {
  if (!isRecord(value)
    || value.status !== 'pending'
    || !isRecord(value.upload_headers)) {
    throw new Error(INVALID_MEDIA)
  }
  const headers = value.upload_headers
  if (Object.values(headers).some(header => typeof header !== 'string')) {
    throw new Error(INVALID_MEDIA)
  }
  const uploadURL = requiredText(value.upload_url, INVALID_MEDIA)
  let parsed: URL
  try {
    parsed = new URL(uploadURL)
  } catch {
    throw new Error(INVALID_MEDIA)
  }
  if (parsed.protocol !== 'https:') {
    throw new Error(INVALID_MEDIA)
  }
  const completeURL = requiredText(value.complete_url, INVALID_MEDIA)
  if (!completeURL.startsWith('/api/v1/')) {
    throw new Error(INVALID_MEDIA)
  }
  return {
    id: requiredText(value.id, INVALID_MEDIA),
    status: 'pending',
    upload_url: parsed.href,
    upload_headers: headers as Record<string, string>,
    complete_url: completeURL,
    expires_at: isoDateTime(value.expires_at, INVALID_MEDIA),
    max_bytes: requiredInteger(value.max_bytes, INVALID_MEDIA, 1),
  }
}

export function parseComposerMedia(value: unknown): ComposerMedia {
  return parseMedia(value)
}

function parseScheduleFields(
  value: Record<string, unknown>,
  idField: 'id' | 'post_id',
): Omit<ScheduledPost, 'id' | 'created_at' | 'updated_at'> & { id: string } {
  if (!SCHEDULING_STATUSES.includes(value.status as SchedulingPostStatus)) {
    throw new Error(INVALID_SCHEDULE)
  }
  return {
    id: requiredText(value[idField], INVALID_SCHEDULE),
    workspace_id: typeof value.workspace_id === 'string' ? value.workspace_id : '',
    draft_id: requiredText(value.draft_id, INVALID_SCHEDULE),
    channel_ids: stringList(value.channel_ids, INVALID_SCHEDULE),
    status: value.status as SchedulingPostStatus,
    scheduled_for_utc: isoDateTime(value.scheduled_for_utc, INVALID_SCHEDULE),
    scheduled_local: requiredText(value.scheduled_local, INVALID_SCHEDULE),
    time_zone: requiredText(value.time_zone, INVALID_SCHEDULE),
    utc_offset_minutes: requiredInteger(
      value.utc_offset_minutes,
      INVALID_SCHEDULE,
      -1080,
    ),
    revision: requiredInteger(value.revision, INVALID_SCHEDULE, 1),
    duplicated_from_post_id: optionalText(value.duplicated_from_post_id),
    cancelled_at: value.cancelled_at === undefined
      ? undefined
      : isoDateTime(value.cancelled_at, INVALID_SCHEDULE),
  }
}

export function parseScheduledPost(value: unknown): ScheduledPost {
  if (!isRecord(value)) {
    throw new Error(INVALID_SCHEDULE)
  }
  return {
    ...parseScheduleFields(value, 'id'),
    created_at: isoDateTime(value.created_at, INVALID_SCHEDULE),
    updated_at: isoDateTime(value.updated_at, INVALID_SCHEDULE),
  }
}

export function parseCalendar(value: unknown): CalendarEntry[] {
  if (!isRecord(value) || !Array.isArray(value.entries)) {
    throw new Error(INVALID_SCHEDULE)
  }
  return value.entries.map((entry): CalendarEntry => {
    if (!isRecord(entry)) {
      throw new Error(INVALID_SCHEDULE)
    }
    const parsed = parseScheduleFields(entry, 'post_id')
    return {
      post_id: parsed.id,
      draft_id: parsed.draft_id,
      channel_ids: parsed.channel_ids,
      status: parsed.status,
      scheduled_for_utc: parsed.scheduled_for_utc,
      scheduled_local: parsed.scheduled_local,
      time_zone: parsed.time_zone,
      utc_offset_minutes: parsed.utc_offset_minutes,
      revision: parsed.revision,
    }
  })
}

export function emptyDraftContent(): DraftContent {
  return {
    text: '',
    link: '',
    media: [],
    thread: [],
    destinations: [],
  }
}
