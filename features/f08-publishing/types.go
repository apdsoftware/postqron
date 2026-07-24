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
	DestinationDeadLetter DestinationStatus = "dead_letter"
	DestinationCancelled  DestinationStatus = "cancelled"
)

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
	ChannelID    string          `json:"channel_id"`
	Provider     string          `json:"provider"`
	ConnectionID string          `json:"connection_id"`
	Payload      json.RawMessage `json:"payload"`
	MaxAttempts  int             `json:"max_attempts,omitempty"`
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
	ID                string            `json:"id"`
	JobID             string            `json:"job_id"`
	CommandID         string            `json:"command_id"`
	WorkspaceID       string            `json:"workspace_id"`
	PostID            string            `json:"post_id"`
	Generation        int64             `json:"generation"`
	ChannelID         string            `json:"channel_id"`
	Provider          string            `json:"provider"`
	ConnectionID      string            `json:"connection_id"`
	Payload           json.RawMessage   `json:"payload"`
	IdempotencyKey    string            `json:"idempotency_key"`
	Status            DestinationStatus `json:"status"`
	AttemptCount      int               `json:"attempt_count"`
	CycleAttemptCount int               `json:"cycle_attempt_count"`
	MaxAttempts       int               `json:"max_attempts"`
	NextAttemptAt     time.Time         `json:"next_attempt_at"`
	LeaseToken        string            `json:"-"`
	LockedUntil       *time.Time        `json:"locked_until,omitempty"`
	RemoteID          string            `json:"remote_id,omitempty"`
	LastDiagnostic    Diagnostic        `json:"last_diagnostic,omitempty"`
	PublishedAt       *time.Time        `json:"published_at,omitempty"`
	DeadLetteredAt    *time.Time        `json:"dead_lettered_at,omitempty"`
	CancelledAt       *time.Time        `json:"cancelled_at,omitempty"`
	ManualRetryCount  int               `json:"manual_retry_count"`
}

type Diagnostic struct {
	Code      string    `json:"code,omitempty"`
	Detail    string    `json:"detail,omitempty"`
	Retryable bool      `json:"retryable"`
	At        time.Time `json:"at,omitempty"`
}

type PublishRequest struct {
	WorkspaceID    string
	PostID         string
	ChannelID      string
	ConnectionID   string
	Payload        json.RawMessage
	IdempotencyKey string
}

type PublishResult struct {
	RemoteID string
}

type RetryDestinationCommand struct {
	WorkspaceID   string
	ActorID       string
	DestinationID string
}
