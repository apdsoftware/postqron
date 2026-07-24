package medialibrary

import "time"

type MediaKind string

const (
	MediaImage MediaKind = "image"
	MediaVideo MediaKind = "video"
)

type AssetStatus string

const (
	StatusReady    AssetStatus = "ready"
	StatusArchived AssetStatus = "archived"
	StatusPurged   AssetStatus = "purged"
)

type UploadStatus string

const (
	UploadPending   UploadStatus = "pending"
	UploadCompleted UploadStatus = "completed"
	UploadCanceled  UploadStatus = "canceled"
)

type Upload struct {
	ID                string       `json:"id"`
	AssetID           string       `json:"asset_id"`
	WorkspaceID       string       `json:"workspace_id"`
	CreatedBy         string       `json:"created_by"`
	StorageKey        string       `json:"storage_key"`
	OriginalName      string       `json:"original_name"`
	DeclaredType      string       `json:"declared_content_type"`
	ReservedSizeBytes int64        `json:"reserved_size_bytes"`
	IdempotencyKey    string       `json:"-"`
	Status            UploadStatus `json:"status"`
	ExpiresAt         time.Time    `json:"expires_at"`
	CreatedAt         time.Time    `json:"created_at"`
	CompletedAt       *time.Time   `json:"completed_at,omitempty"`
}

type UploadAuthorization struct {
	Method    string            `json:"method"`
	URL       string            `json:"url"`
	Headers   map[string]string `json:"headers"`
	ExpiresAt time.Time         `json:"expires_at"`
}

type UploadTicket struct {
	Upload        Upload              `json:"upload"`
	Authorization UploadAuthorization `json:"authorization"`
}

// InspectedMedia comes exclusively from the trusted object-inspection
// boundary. Client-provided metadata is never copied into an Asset.
type InspectedMedia struct {
	Kind                MediaKind
	ContentType         string
	SizeBytes           int64
	Width               int
	Height              int
	ColorSpace          string
	VideoCodec          string
	AudioCodec          string
	AudioSampleRate     int
	FramesPerSecond     float64
	VideoBitrate        int64
	AudioBitrate        int64
	DurationSeconds     float64
	HasAudio            bool
	HasEditList         bool
	MoovBeforeMediaData bool
	ChecksumSHA256      string
}

type Asset struct {
	ID                  string      `json:"id"`
	WorkspaceID         string      `json:"workspace_id"`
	CreatedBy           string      `json:"created_by"`
	StorageKey          string      `json:"storage_key"`
	OriginalName        string      `json:"original_name"`
	Kind                MediaKind   `json:"kind"`
	ContentType         string      `json:"content_type"`
	SizeBytes           int64       `json:"size_bytes"`
	Width               int         `json:"width"`
	Height              int         `json:"height"`
	ColorSpace          string      `json:"color_space,omitempty"`
	VideoCodec          string      `json:"video_codec,omitempty"`
	AudioCodec          string      `json:"audio_codec,omitempty"`
	AudioSampleRate     int         `json:"audio_sample_rate,omitempty"`
	FramesPerSecond     float64     `json:"frames_per_second,omitempty"`
	VideoBitrate        int64       `json:"video_bitrate,omitempty"`
	AudioBitrate        int64       `json:"audio_bitrate,omitempty"`
	DurationSeconds     float64     `json:"duration_seconds,omitempty"`
	HasAudio            bool        `json:"has_audio,omitempty"`
	HasEditList         bool        `json:"has_edit_list,omitempty"`
	MoovBeforeMediaData bool        `json:"moov_before_media_data,omitempty"`
	ChecksumSHA256      string      `json:"checksum_sha256"`
	AltText             string      `json:"alt_text,omitempty"`
	Tags                []string    `json:"tags"`
	Status              AssetStatus `json:"status"`
	Revision            int64       `json:"revision"`
	CreatedAt           time.Time   `json:"created_at"`
	UpdatedAt           time.Time   `json:"updated_at"`
	ArchivedAt          *time.Time  `json:"archived_at,omitempty"`
	PurgedAt            *time.Time  `json:"purged_at,omitempty"`
}

// ComposerMedia is wire-compatible with F6's Media contract and deliberately
// excludes library-only metadata.
type ComposerMedia struct {
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

type SearchQuery struct {
	Text  string
	Kind  MediaKind
	Tags  []string
	Limit int
}

type SearchResult struct {
	Assets []Asset `json:"assets"`
}

type CreateUploadCommand struct {
	WorkspaceID    string
	ActorID        string
	OriginalName   string
	ContentType    string
	SizeBytes      int64
	IdempotencyKey string
}

type UpdateMetadataCommand struct {
	WorkspaceID      string
	ActorID          string
	AssetID          string
	ExpectedRevision int64
	OriginalName     string
	AltText          string
	Tags             []string
}
