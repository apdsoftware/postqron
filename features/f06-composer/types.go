package composer

import "time"

// ChannelType is intentionally opaque. Provider/channel identifiers are
// supplied by the versioned capability catalog, not compiled into F6.
type ChannelType string

// Format names content families. A capability decides whether a concrete
// provider/channel supports one of them and which constraints apply.
type Format string

const (
	FormatText       Format = "text"
	FormatLink       Format = "link"
	FormatImage      Format = "image"
	FormatCarousel   Format = "carousel"
	FormatVideo      Format = "video"
	FormatShortVideo Format = "short_video"
	FormatThread     Format = "thread"
)

type MediaKind string

const (
	MediaImage MediaKind = "image"
	MediaVideo MediaKind = "video"
)

type InspectionStatus string

const (
	InspectionPending  InspectionStatus = "pending"
	InspectionReady    InspectionStatus = "ready"
	InspectionRejected InspectionStatus = "rejected"
)

// Media contains server-inspected metadata only. URL is a same-origin,
// authenticated API path; storage keys and provider credentials never cross
// the client contract.
type Media struct {
	ID                  string           `json:"id"`
	Kind                MediaKind        `json:"kind"`
	ContentType         string           `json:"content_type"`
	SizeBytes           int64            `json:"size_bytes"`
	Width               int              `json:"width,omitempty"`
	Height              int              `json:"height,omitempty"`
	ColorSpace          string           `json:"color_space,omitempty"`
	VideoCodec          string           `json:"video_codec,omitempty"`
	AudioCodec          string           `json:"audio_codec,omitempty"`
	AudioSampleRate     int              `json:"audio_sample_rate,omitempty"`
	FramesPerSecond     float64          `json:"frames_per_second,omitempty"`
	VideoBitrate        int64            `json:"video_bitrate,omitempty"`
	AudioBitrate        int64            `json:"audio_bitrate,omitempty"`
	DurationSeconds     float64          `json:"duration_seconds,omitempty"`
	HasAudio            bool             `json:"has_audio,omitempty"`
	HasEditList         bool             `json:"has_edit_list,omitempty"`
	MoovBeforeMediaData bool             `json:"moov_before_media_data,omitempty"`
	InspectionStatus    InspectionStatus `json:"inspection_status"`
	URL                 string           `json:"url"`
	ExpiresAt           *time.Time       `json:"expires_at,omitempty"`

	// StorageKey is retained only for decoding legacy draft JSON. It is never
	// serialized and is not accepted as an authorization signal.
	StorageKey string `json:"-"`
}

type ThreadItem struct {
	Text     string   `json:"text"`
	MediaIDs []string `json:"media_ids"`
}

// Destination identifies a connected channel without storing provider
// credentials. Nil override fields inherit the draft-level value; explicit
// empty overrides remain meaningful.
type Destination struct {
	ID             string            `json:"id"`
	ChannelID      string            `json:"channel_id"`
	ChannelType    ChannelType       `json:"channel_type"`
	CapabilityID   string            `json:"capability_id"`
	Format         Format            `json:"format"`
	TextOverride   *string           `json:"text_override,omitempty"`
	LinkOverride   *string           `json:"link_override,omitempty"`
	MediaIDs       *[]string         `json:"media_ids,omitempty"`
	ThreadOverride *[]ThreadItem     `json:"thread_override,omitempty"`
	Fields         map[string]string `json:"fields,omitempty"`
}

type DraftContent struct {
	Text         string        `json:"text"`
	Link         string        `json:"link"`
	Media        []Media       `json:"media"`
	Thread       []ThreadItem  `json:"thread"`
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

type DraftRevision struct {
	DraftID     string       `json:"draft_id"`
	Revision    int64        `json:"revision"`
	Content     DraftContent `json:"content"`
	AutosaveKey string       `json:"autosave_key,omitempty"`
	SavedAt     time.Time    `json:"saved_at"`
}

type ValidationError struct {
	DestinationID string         `json:"destination_id,omitempty"`
	Field         string         `json:"field"`
	Rule          string         `json:"rule"`
	Code          string         `json:"code"`
	Message       string         `json:"message"`
	Remedy        string         `json:"remedy,omitempty"`
	Details       map[string]any `json:"details,omitempty"`
}

type DestinationValidation struct {
	DestinationID string            `json:"destination_id"`
	ChannelID     string            `json:"channel_id"`
	ChannelType   ChannelType       `json:"channel_type"`
	CapabilityID  string            `json:"capability_id"`
	Format        Format            `json:"format"`
	Valid         bool              `json:"valid"`
	Errors        []ValidationError `json:"errors"`
}

type ValidationReport struct {
	CapabilityVersion string                  `json:"capability_version"`
	Valid             bool                    `json:"valid"`
	Errors            []ValidationError       `json:"errors"`
	Destinations      []DestinationValidation `json:"destinations"`
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
	AutosaveKey      string
	Content          DraftContent
}

type MediaUploadRequest struct {
	FileName    string `json:"file_name"`
	ContentType string `json:"content_type"`
	SizeBytes   int64  `json:"size_bytes"`
}

type MediaUpload struct {
	ID            string            `json:"id"`
	Status        InspectionStatus  `json:"status"`
	UploadURL     string            `json:"upload_url"`
	UploadHeaders map[string]string `json:"upload_headers"`
	CompleteURL   string            `json:"complete_url"`
	ExpiresAt     time.Time         `json:"expires_at"`
	MaxBytes      int64             `json:"max_bytes"`
}

type MediaDownload struct {
	URL       string    `json:"url"`
	ExpiresAt time.Time `json:"expires_at"`
}

// SchedulingValidationCommand is the narrow, revision-aware F6 boundary
// consumed by scheduling. ChannelIDs must match the exact normalized set of
// destinations stored in the validated draft revision.
type SchedulingValidationCommand struct {
	WorkspaceID string
	ActorID     string
	DraftID     string
	ChannelIDs  []string
}

// SchedulingDraftReference is immutable once returned. Consumers must persist
// DraftRevision and treat a later composer revision as stale.
type SchedulingDraftReference struct {
	DraftID           string   `json:"draft_id"`
	DraftRevision     int64    `json:"draft_revision"`
	ChannelIDs        []string `json:"channel_ids"`
	CapabilityVersion string   `json:"capability_version"`
}

type DuplicateDraftCommand struct {
	WorkspaceID    string
	ActorID        string
	SourceDraftID  string
	SourceRevision int64
}

type DuplicatedDraft struct {
	DraftID             string `json:"draft_id"`
	DraftRevision       int64  `json:"draft_revision"`
	SourceDraftID       string `json:"source_draft_id"`
	SourceDraftRevision int64  `json:"source_draft_revision"`
}
