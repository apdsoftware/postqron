package composer

import "time"

type ChannelType string

const (
	ChannelFacebookPage          ChannelType = "facebook_page"
	ChannelInstagramProfessional ChannelType = "instagram_professional"
)

type Format string

const (
	FormatText     Format = "text"
	FormatImage    Format = "image"
	FormatCarousel Format = "carousel"
	FormatReel     Format = "reel"
)

type MediaKind string

const (
	MediaImage MediaKind = "image"
	MediaVideo MediaKind = "video"
)

type Media struct {
	ID                  string    `json:"id"`
	StorageKey          string    `json:"storage_key"`
	Kind                MediaKind `json:"kind"`
	ContentType         string    `json:"content_type"`
	SizeBytes           int64     `json:"size_bytes"`
	Width               int       `json:"width"`
	Height              int       `json:"height"`
	ColorSpace          string    `json:"color_space,omitempty"`
	VideoCodec          string    `json:"video_codec,omitempty"`
	AudioCodec          string    `json:"audio_codec,omitempty"`
	AudioSampleRate     int       `json:"audio_sample_rate,omitempty"`
	FramesPerSecond     float64   `json:"frames_per_second,omitempty"`
	VideoBitrate        int64     `json:"video_bitrate,omitempty"`
	AudioBitrate        int64     `json:"audio_bitrate,omitempty"`
	DurationSeconds     float64   `json:"duration_seconds,omitempty"`
	HasAudio            bool      `json:"has_audio,omitempty"`
	HasEditList         bool      `json:"has_edit_list,omitempty"`
	MoovBeforeMediaData bool      `json:"moov_before_media_data,omitempty"`
}

// Destination identifies a connected channel without storing provider
// credentials. Nil override fields inherit the draft-level value; an explicit
// empty override is preserved.
type Destination struct {
	ID           string      `json:"id"`
	ChannelID    string      `json:"channel_id"`
	ChannelType  ChannelType `json:"channel_type"`
	Format       Format      `json:"format"`
	TextOverride *string     `json:"text_override,omitempty"`
	MediaIDs     *[]string   `json:"media_ids,omitempty"`
}

type DraftContent struct {
	Text         string        `json:"text"`
	Media        []Media       `json:"media"`
	Destinations []Destination `json:"destinations"`
}

type Draft struct {
	ID          string       `json:"id"`
	WorkspaceID string       `json:"workspace_id"`
	CreatedBy   string       `json:"created_by"`
	Content     DraftContent `json:"content"`
	Revision    int64        `json:"revision"`
	CreatedAt   time.Time    `json:"created_at"`
	UpdatedAt   time.Time    `json:"updated_at"`
}

type ValidationError struct {
	DestinationID string         `json:"destination_id,omitempty"`
	Field         string         `json:"field"`
	Rule          string         `json:"rule"`
	Code          string         `json:"code"`
	Message       string         `json:"message"`
	Details       map[string]any `json:"details,omitempty"`
}

type DestinationValidation struct {
	DestinationID string            `json:"destination_id"`
	ChannelID     string            `json:"channel_id"`
	ChannelType   ChannelType       `json:"channel_type"`
	Format        Format            `json:"format"`
	Valid         bool              `json:"valid"`
	Errors        []ValidationError `json:"errors"`
}

type ValidationReport struct {
	Valid        bool                    `json:"valid"`
	Errors       []ValidationError       `json:"errors"`
	Destinations []DestinationValidation `json:"destinations"`
}

type DraftView struct {
	Draft      Draft            `json:"draft"`
	Validation ValidationReport `json:"validation"`
}

type CreateDraftCommand struct {
	WorkspaceID string
	ActorID     string
	Content     DraftContent
}

type UpdateDraftCommand struct {
	WorkspaceID      string
	ActorID          string
	DraftID          string
	ExpectedRevision int64
	Content          DraftContent
}
