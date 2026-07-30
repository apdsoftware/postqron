package publishing

import (
	"encoding/json"
	"time"
)

type JobStatus string

const (
	JobQueued          JobStatus = "queued"
	JobPublishing      JobStatus = "publishing"
	JobPublished       JobStatus = "published"
	JobPartiallyFailed JobStatus = "partially_failed"
	JobFailed          JobStatus = "failed"
	JobCancelled       JobStatus = "cancelled"
)

type DestinationStatus string

const (
	DestinationPending    DestinationStatus = "pending"
	DestinationPublishing DestinationStatus = "publishing"
	DestinationRetryWait  DestinationStatus = "retry_wait"
	DestinationPublished  DestinationStatus = "published"
	DestinationNotified   DestinationStatus = "notified"
	DestinationDeadLetter DestinationStatus = "dead_letter"
	DestinationCancelled  DestinationStatus = "cancelled"
)

type PublishingMode string

const (
	PublishingModeAuto         PublishingMode = "auto"
	PublishingModeNotification PublishingMode = "notification"
)

// AdapterCapabilities is captured in the immutable destination snapshot.
// Provider names never imply safety: auto-publishing is enabled only when the
// adapter declares native idempotency or deterministic reconciliation.
type AdapterCapabilities struct {
	Version                 string         `json:"version"`
	Mode                    PublishingMode `json:"mode"`
	NativeIdempotency       bool           `json:"native_idempotency"`
	Reconciliation          bool           `json:"reconciliation"`
	FailClosedOnAmbiguous   bool           `json:"fail_closed_on_ambiguous,omitempty"`
	MultiStep               bool           `json:"multi_step"`
	RemotePermalink         bool           `json:"remote_permalink"`
	NotificationIdempotency bool           `json:"notification_idempotency"`
	// MediaFormats is a canonical, versioned JSON capability document. It is
	// kept as a string so immutable snapshots remain directly comparable.
	MediaFormats string `json:"media_formats,omitempty"`
}

type CommandState string

const (
	CommandPending CommandState = "pending"
)

// PublicationCommand mirrors the durable F7 hand-off without importing or
// coupling to another feature's implementation package.
type PublicationCommand struct {
	ID              string       `json:"id"`
	WorkspaceID     string       `json:"workspace_id"`
	PostID          string       `json:"post_id"`
	DraftID         string       `json:"draft_id"`
	Generation      int64        `json:"generation"`
	ExecuteAtUTC    time.Time    `json:"execute_at_utc"`
	State           CommandState `json:"state"`
	InvalidationKey string       `json:"invalidation_key"`
}

// DestinationInput is an immutable, channel-specific snapshot prepared from
// F5 and F6. ConnectionID is an opaque reference; credentials are never copied
// into the publishing store.
type DestinationInput struct {
	ChannelID         string          `json:"channel_id"`
	Provider          string          `json:"provider"`
	ConnectionID      string          `json:"connection_id,omitempty"`
	Mode              PublishingMode  `json:"mode"`
	DraftRevision     int64           `json:"draft_revision"`
	CapabilityID      string          `json:"capability_id"`
	CapabilityVersion string          `json:"capability_version"`
	Payload           json.RawMessage `json:"payload"`
	MaxAttempts       int             `json:"max_attempts,omitempty"`
}

type EnqueueRequest struct {
	Command      PublicationCommand `json:"command"`
	Destinations []DestinationInput `json:"destinations"`
}

type EnqueueResult struct {
	JobID   string    `json:"job_id"`
	Created bool      `json:"created"`
	Status  JobStatus `json:"status"`
}

type Job struct {
	ID              string        `json:"id"`
	CommandID       string        `json:"command_id"`
	WorkspaceID     string        `json:"workspace_id"`
	PostID          string        `json:"post_id"`
	DraftID         string        `json:"draft_id"`
	Generation      int64         `json:"generation"`
	InvalidationKey string        `json:"invalidation_key"`
	Status          JobStatus     `json:"status"`
	ExecuteAtUTC    time.Time     `json:"execute_at_utc"`
	CreatedAt       time.Time     `json:"created_at"`
	UpdatedAt       time.Time     `json:"updated_at"`
	Destinations    []Destination `json:"destinations"`
}

type Destination struct {
	ID                  string              `json:"id"`
	JobID               string              `json:"job_id"`
	CommandID           string              `json:"command_id"`
	WorkspaceID         string              `json:"workspace_id"`
	PostID              string              `json:"post_id"`
	Generation          int64               `json:"generation"`
	DraftRevision       int64               `json:"draft_revision"`
	ChannelID           string              `json:"channel_id"`
	Provider            string              `json:"provider"`
	ConnectionID        string              `json:"connection_id"`
	Mode                PublishingMode      `json:"mode"`
	CapabilityID        string              `json:"capability_id"`
	Capabilities        AdapterCapabilities `json:"capabilities"`
	Payload             json.RawMessage     `json:"payload"`
	SnapshotHash        string              `json:"snapshot_hash"`
	IdempotencyKey      string              `json:"idempotency_key"`
	Status              DestinationStatus   `json:"status"`
	AttemptCount        int                 `json:"attempt_count"`
	CycleAttemptCount   int                 `json:"cycle_attempt_count"`
	MaxAttempts         int                 `json:"max_attempts"`
	NextAttemptAt       time.Time           `json:"next_attempt_at"`
	LeaseToken          string              `json:"-"`
	LockedUntil         *time.Time          `json:"locked_until,omitempty"`
	RemoteID            string              `json:"remote_id,omitempty"`
	Permalink           string              `json:"permalink,omitempty"`
	NotificationID      string              `json:"notification_id,omitempty"`
	Checkpoint          json.RawMessage     `json:"checkpoint,omitempty"`
	NeedsReconciliation bool                `json:"needs_reconciliation"`
	LastDiagnostic      Diagnostic          `json:"last_diagnostic,omitempty"`
	PublishedAt         *time.Time          `json:"published_at,omitempty"`
	DeadLetteredAt      *time.Time          `json:"dead_lettered_at,omitempty"`
	CancelledAt         *time.Time          `json:"cancelled_at,omitempty"`
	ManualRetryCount    int                 `json:"manual_retry_count"`
}

type Diagnostic struct {
	Code      string    `json:"code,omitempty"`
	Detail    string    `json:"detail,omitempty"`
	Retryable bool      `json:"retryable"`
	Ambiguous bool      `json:"ambiguous"`
	At        time.Time `json:"at,omitempty"`
}

type PublishRequest struct {
	WorkspaceID    string
	PostID         string
	ChannelID      string
	ConnectionID   string
	Payload        json.RawMessage
	Checkpoint     json.RawMessage
	IdempotencyKey string
}

type PublishResult struct {
	Complete   bool
	RemoteID   string
	Permalink  string
	Checkpoint json.RawMessage
	RetryAfter time.Duration
}

type ReconciliationState string

const (
	ReconciliationFound    ReconciliationState = "found"
	ReconciliationNotFound ReconciliationState = "not_found"
	ReconciliationUnknown  ReconciliationState = "unknown"
)

type ReconcileRequest struct {
	WorkspaceID    string
	PostID         string
	ChannelID      string
	ConnectionID   string
	Payload        json.RawMessage
	Checkpoint     json.RawMessage
	IdempotencyKey string
}

type ReconcileResult struct {
	State      ReconciliationState
	RemoteID   string
	Permalink  string
	Checkpoint json.RawMessage
	Diagnostic string
}

type NotificationRequest struct {
	WorkspaceID    string
	PostID         string
	ChannelID      string
	Payload        json.RawMessage
	IdempotencyKey string
}

type NotificationResult struct {
	DeliveryID string
}

type RetryDestinationCommand struct {
	WorkspaceID   string
	ActorID       string
	DestinationID string
}
