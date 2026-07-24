export type ChannelType = 'facebook_page' | 'instagram_professional'
export type ComposerFormat = 'text' | 'image' | 'carousel' | 'reel'
export type MediaKind = 'image' | 'video'

export interface Media {
  id: string
  storage_key: string
  kind: MediaKind
  content_type: string
  size_bytes: number
  width: number
  height: number
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
}

export interface Destination {
  id: string
  channel_id: string
  channel_type: ChannelType
  format: ComposerFormat
  text_override?: string | null
  media_ids?: string[] | null
}

export interface DraftContent {
  text: string
  media: Media[]
  destinations: Destination[]
}

export interface ValidationError {
  destination_id?: string
  field: string
  rule: string
  code: string
  message: string
  details?: Record<string, unknown>
}

export interface DestinationValidation {
  destination_id: string
  channel_id: string
  channel_type: ChannelType
  format: ComposerFormat
  valid: boolean
  errors: ValidationError[]
}

export interface ValidationReport {
  valid: boolean
  errors: ValidationError[]
  destinations: DestinationValidation[]
}
