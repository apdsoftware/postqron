package statusnotifications

import "time"

type PostStatus string

const (
	StatusDraft      PostStatus = "draft"
	StatusScheduled  PostStatus = "scheduled"
	StatusPublishing PostStatus = "publishing"
	StatusPublished  PostStatus = "published"
	StatusFailed     PostStatus = "failed"
	StatusCancelled  PostStatus = "cancelled"
)

func (status PostStatus) Valid() bool {
	switch status {
	case StatusDraft, StatusScheduled, StatusPublishing, StatusPublished,
		StatusFailed, StatusCancelled:
		return true
	default:
		return false
	}
}

type DestinationStatus string

const (
	DestinationDraft      DestinationStatus = "draft"
	DestinationScheduled  DestinationStatus = "scheduled"
	DestinationPublishing DestinationStatus = "publishing"
	DestinationPublished  DestinationStatus = "published"
	DestinationFailed     DestinationStatus = "failed"
	DestinationCancelled  DestinationStatus = "cancelled"
)

func (status DestinationStatus) Valid() bool {
	switch status {
	case DestinationDraft, DestinationScheduled, DestinationPublishing,
		DestinationPublished, DestinationFailed, DestinationCancelled:
		return true
	default:
		return false
	}
}

type Diagnostic struct {
	Code      string    `json:"code,omitempty"`
	Message   string    `json:"message,omitempty"`
	Retryable bool      `json:"retryable"`
	At        time.Time `json:"at,omitempty"`
}

type DestinationView struct {
	ID          string            `json:"id"`
	ChannelID   string            `json:"channel_id"`
	Status      DestinationStatus `json:"status"`
	RemoteID    string            `json:"remote_id,omitempty"`
	Diagnostic  Diagnostic        `json:"diagnostic,omitempty"`
	LastEventID string            `json:"-"`
	UpdatedAt   time.Time         `json:"updated_at"`
}

type PostView struct {
	WorkspaceID  string            `json:"workspace_id"`
	PostID       string            `json:"post_id"`
	DraftID      string            `json:"draft_id,omitempty"`
	Status       PostStatus        `json:"status"`
	Destinations []DestinationView `json:"destinations"`
	CreatedAt    time.Time         `json:"created_at"`
	UpdatedAt    time.Time         `json:"updated_at"`
}

// LifecycleEvent is the discovery boundary for F6/F7 state. EventID and
// Revision come from the durable producer envelope.
type LifecycleEvent struct {
	EventID      string
	WorkspaceID  string
	PostID       string
	DraftID      string
	Revision     int64
	Status       PostStatus
	Destinations []DestinationRef
	OccurredAt   time.Time
}

type DestinationRef struct {
	ID        string
	ChannelID string
}

// PublicationEvent mirrors the F8 per-destination payload. EventID and
// WorkspaceID are durable envelope metadata supplied by the event transport.
// If EventID is absent, F9 derives a stable fingerprint from the payload.
type PublicationEvent struct {
	EventID       string
	WorkspaceID   string
	JobID         string
	PostID        string
	DestinationID string
	ChannelID     string
	Status        string
	RemoteID      string
	Diagnostic    SourceDiagnostic
	OccurredAt    time.Time
}

type SourceDiagnostic struct {
	Code      string
	Detail    string
	Retryable bool
}

type ApplyResult struct {
	FirstDelivery bool
	StateChanged  bool
	View          PostView
}

type NotificationKind string

const (
	NotificationWelcome           NotificationKind = "welcome"
	NotificationPlanChanged       NotificationKind = "plan_changed"
	NotificationPublicationFailed NotificationKind = "publication_failed"
	NotificationSecurityAlert     NotificationKind = "security_alert"
)

func (kind NotificationKind) Valid() bool {
	switch kind {
	case NotificationWelcome, NotificationPlanChanged,
		NotificationPublicationFailed, NotificationSecurityAlert:
		return true
	default:
		return false
	}
}

type NotificationEvent struct {
	EventID       string
	Kind          NotificationKind
	AccountID     string
	WorkspaceID   string
	PostID        string
	DestinationID string
	Subject       string
	Detail        string
	ActionLabel   string
	ActionURL     string
	OccurredAt    time.Time
}

type QueueState string

const (
	QueuePending   QueueState = "pending"
	QueueSending   QueueState = "sending"
	QueueRetry     QueueState = "retry"
	QueueDelivered QueueState = "delivered"
)

type Notification struct {
	ID             string
	SourceEventID  string
	Kind           NotificationKind
	AccountID      string
	WorkspaceID    string
	PostID         string
	DestinationID  string
	Subject        string
	Detail         string
	ActionLabel    string
	ActionURL      string
	IdempotencyKey string
	State          QueueState
	AttemptCount   int
	NextAttemptAt  time.Time
	LeaseToken     string
	LockedUntil    *time.Time
	CreatedAt      time.Time
}

type Recipient struct {
	ID    string
	Email string
	Name  string
}

// EmailCommand deliberately mirrors the F14 command contract without
// importing another feature implementation package.
type EmailCommand struct {
	IdempotencyKey  string
	Channel         string
	TemplateID      string
	TemplateVersion string
	Recipient       Recipient
	Data            EmailData
	OccurredAt      time.Time
}

type EmailData struct {
	Heading     string
	Intro       string
	Body        string
	Detail      string
	ActionLabel string
	ActionURL   string
}

type ManualRetry struct {
	ID             string
	WorkspaceID    string
	PostID         string
	DestinationID  string
	FailureEventID string
	ActorID        string
	IdempotencyKey string
	State          QueueState
	AttemptCount   int
	NextAttemptAt  time.Time
	LeaseToken     string
	LockedUntil    *time.Time
	CreatedAt      time.Time
}

type ManualRetryRequest struct {
	WorkspaceID    string
	PostID         string
	DestinationID  string
	ActorID        string
	IdempotencyKey string
}

type EnqueueResult struct {
	ID      string     `json:"id"`
	Created bool       `json:"created"`
	State   QueueState `json:"state"`
}
