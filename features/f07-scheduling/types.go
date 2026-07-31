package scheduling

import "time"

type PostStatus string

const (
	StatusScheduled  PostStatus = "scheduled"
	StatusPublishing PostStatus = "publishing"
	StatusPublished  PostStatus = "published"
	StatusFailed     PostStatus = "failed"
	StatusCancelled  PostStatus = "cancelled"
)

func (status PostStatus) Valid() bool {
	switch status {
	case StatusScheduled, StatusPublishing, StatusPublished, StatusFailed, StatusCancelled:
		return true
	default:
		return false
	}
}

type CommandState string

const (
	CommandPending     CommandState = "pending"
	CommandInvalidated CommandState = "invalidated"
)

// ScheduleInput is a wall-clock choice plus an IANA time zone. OffsetMinutes
// is required only when the wall time occurs twice during a DST fallback.
type ScheduleInput struct {
	LocalDateTime string `json:"local_date_time"`
	TimeZone      string `json:"time_zone"`
	OffsetMinutes *int   `json:"utc_offset_minutes,omitempty"`
}

type ScheduledPost struct {
	ID                   string     `json:"id"`
	WorkspaceID          string     `json:"workspace_id"`
	DraftID              string     `json:"draft_id"`
	ChannelIDs           []string   `json:"channel_ids"`
	Status               PostStatus `json:"status"`
	ScheduledForUTC      time.Time  `json:"scheduled_for_utc"`
	ScheduledLocal       string     `json:"scheduled_local"`
	TimeZone             string     `json:"time_zone"`
	UTCOffsetMinutes     int        `json:"utc_offset_minutes"`
	Revision             int64      `json:"revision"`
	ActiveCommandID      string     `json:"active_command_id,omitempty"`
	DuplicatedFromPostID string     `json:"duplicated_from_post_id,omitempty"`
	CreatedBy            string     `json:"created_by"`
	CreatedAt            time.Time  `json:"created_at"`
	UpdatedAt            time.Time  `json:"updated_at"`
	CancelledAt          *time.Time `json:"cancelled_at,omitempty"`
}

// ScheduledPostView is the browser-safe representation. Publication command
// identifiers and the creating account remain internal implementation details.
type ScheduledPostView struct {
	ID                   string     `json:"id"`
	WorkspaceID          string     `json:"workspace_id"`
	DraftID              string     `json:"draft_id"`
	ChannelIDs           []string   `json:"channel_ids"`
	Status               PostStatus `json:"status"`
	ScheduledForUTC      time.Time  `json:"scheduled_for_utc"`
	ScheduledLocal       string     `json:"scheduled_local"`
	TimeZone             string     `json:"time_zone"`
	UTCOffsetMinutes     int        `json:"utc_offset_minutes"`
	Revision             int64      `json:"revision"`
	DuplicatedFromPostID string     `json:"duplicated_from_post_id,omitempty"`
	CreatedAt            time.Time  `json:"created_at"`
	UpdatedAt            time.Time  `json:"updated_at"`
	CancelledAt          *time.Time `json:"cancelled_at,omitempty"`
}

// PublicationCommand is the durable hand-off to F8. Generation matches the
// scheduled-post revision. Consumers must only execute pending commands whose
// ID is still the post's ActiveCommandID.
type PublicationCommand struct {
	ID              string       `json:"id"`
	WorkspaceID     string       `json:"workspace_id"`
	PostID          string       `json:"post_id"`
	DraftID         string       `json:"draft_id"`
	ChannelIDs      []string     `json:"channel_ids"`
	Generation      int64        `json:"generation"`
	ExecuteAtUTC    time.Time    `json:"execute_at_utc"`
	State           CommandState `json:"state"`
	CreatedAt       time.Time    `json:"created_at"`
	InvalidatedAt   *time.Time   `json:"invalidated_at,omitempty"`
	InvalidationKey string       `json:"invalidation_key"`
}

type CalendarEntry struct {
	PostID           string     `json:"post_id"`
	DraftID          string     `json:"draft_id"`
	ChannelIDs       []string   `json:"channel_ids"`
	Status           PostStatus `json:"status"`
	ScheduledForUTC  time.Time  `json:"scheduled_for_utc"`
	ScheduledLocal   string     `json:"scheduled_local"`
	TimeZone         string     `json:"time_zone"`
	UTCOffsetMinutes int        `json:"utc_offset_minutes"`
	Revision         int64      `json:"revision"`
}

type CalendarFilter struct {
	FromUTC   time.Time
	UntilUTC  time.Time
	ChannelID string
	Status    PostStatus
}

type SchedulePostCommand struct {
	WorkspaceID string
	ActorID     string
	DraftID     string
	ChannelIDs  []string
	Schedule    ScheduleInput
}

type EditPostCommand struct {
	WorkspaceID      string
	ActorID          string
	PostID           string
	ExpectedRevision int64
	DraftID          string
	ChannelIDs       []string
}

type ReschedulePostCommand struct {
	WorkspaceID      string
	ActorID          string
	PostID           string
	ExpectedRevision int64
	Schedule         ScheduleInput
}

type DuplicatePostCommand struct {
	WorkspaceID      string
	ActorID          string
	PostID           string
	ExpectedRevision int64
	Schedule         *ScheduleInput
}

type CancelPostCommand struct {
	WorkspaceID      string
	ActorID          string
	PostID           string
	ExpectedRevision int64
}
