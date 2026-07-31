export type ChannelType = string
export type ComposerFormat =
  | 'text'
  | 'link'
  | 'image'
  | 'carousel'
  | 'video'
  | 'short_video'
  | 'thread'
export type MediaKind = 'image' | 'video'
export type InspectionStatus = 'pending' | 'ready' | 'rejected'

export interface Media {
  id: string
  kind: MediaKind
  content_type: string
  size_bytes: number
  width?: number
  height?: number
  color_space?: string
  video_codec?: string
  audio_codec?: string
  audio_sample_rate?: number
  frames_per_second?: number
  video_bitrate?: number
  audio_bitrate?: number
  duration_seconds?: number
  has_audio?: boolean
  has_edit_list?: boolean
  moov_before_media_data?: boolean
  inspection_status: InspectionStatus
  url: string
  expires_at?: string
}

export interface ThreadItem {
  text: string
  media_ids: string[]
}

export interface Destination {
  id: string
  channel_id: string
  channel_type: ChannelType
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
  media: Media[]
  thread: ThreadItem[]
  destinations: Destination[]
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
  channel_type: ChannelType
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
  details?: Record<string, unknown>
}

export interface DestinationValidation {
  destination_id: string
  channel_id: string
  channel_type: ChannelType
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

export interface DraftRevision {
  draft_id: string
  revision: number
  content: DraftContent
  autosave_key?: string
  saved_at: string
}

export interface MediaUploadRequest {
  file_name: string
  content_type: string
  size_bytes: number
}

export interface MediaUpload {
  id: string
  status: InspectionStatus
  upload_url: string
  upload_headers: Record<string, string>
  complete_url: string
  expires_at: string
  max_bytes: number
}

export interface MediaDownload {
  url: string
  expires_at: string
}
