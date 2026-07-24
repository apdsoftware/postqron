package smartqueue

import "time"

type Weekday string

const (
	Monday    Weekday = "monday"
	Tuesday   Weekday = "tuesday"
	Wednesday Weekday = "wednesday"
	Thursday  Weekday = "thursday"
	Friday    Weekday = "friday"
	Saturday  Weekday = "saturday"
	Sunday    Weekday = "sunday"
)

type RecurringWindow struct {
	Weekday   Weekday `json:"weekday"`
	StartTime string  `json:"start_time"`
	EndTime   string  `json:"end_time"`
}

type Queue struct {
	ID              string            `json:"id"`
	WorkspaceID     string            `json:"workspace_id"`
	Name            string            `json:"name"`
	TimeZone        string            `json:"time_zone"`
	IntervalMinutes int               `json:"interval_minutes"`
	HorizonDays     int               `json:"horizon_days"`
	Windows         []RecurringWindow `json:"windows"`
	Revision        int64             `json:"revision"`
	CreatedBy       string            `json:"created_by"`
	CreatedAt       time.Time         `json:"created_at"`
	UpdatedAt       time.Time         `json:"updated_at"`
}

type Slot struct {
	StartsAtUTC      time.Time `json:"starts_at_utc"`
	LocalDateTime    string    `json:"local_date_time"`
	TimeZone         string    `json:"time_zone"`
	UTCOffsetMinutes int       `json:"utc_offset_minutes"`
}

type Preview struct {
	Token            string     `json:"preview_token"`
	WorkspaceID      string     `json:"workspace_id"`
	QueueID          string     `json:"queue_id"`
	QueueRevision    int64      `json:"queue_revision"`
	Slot             Slot       `json:"slot"`
	NotBeforeUTC     time.Time  `json:"not_before_utc"`
	SearchUntilUTC   time.Time  `json:"search_until_utc"`
	CreatedAt        time.Time  `json:"created_at"`
	ExpiresAt        time.Time  `json:"expires_at"`
	ConfirmedAt      *time.Time `json:"confirmed_at,omitempty"`
	ReservationID    string     `json:"reservation_id,omitempty"`
	IdempotencyKey   string     `json:"-"`
	ConfirmationHash string     `json:"-"`
}

type Reservation struct {
	ID             string    `json:"id"`
	WorkspaceID    string    `json:"workspace_id"`
	QueueID        string    `json:"queue_id"`
	DraftID        string    `json:"draft_id"`
	ChannelIDs     []string  `json:"channel_ids"`
	Slot           Slot      `json:"slot"`
	IdempotencyKey string    `json:"-"`
	CreatedBy      string    `json:"created_by"`
	CreatedAt      time.Time `json:"created_at"`
}

// SchedulingCommand is the durable F7 boundary. It is committed atomically
// with the reservation and consumed by a trusted server-side adapter.
type SchedulingCommand struct {
	ID             string    `json:"id"`
	ReservationID  string    `json:"reservation_id"`
	WorkspaceID    string    `json:"workspace_id"`
	DraftID        string    `json:"draft_id"`
	ChannelIDs     []string  `json:"channel_ids"`
	ScheduledAt    Slot      `json:"scheduled_at"`
	State          string    `json:"state"`
	IdempotencyKey string    `json:"idempotency_key"`
	CreatedAt      time.Time `json:"created_at"`
}

type Confirmation struct {
	Reservation       Reservation       `json:"reservation"`
	SchedulingCommand SchedulingCommand `json:"scheduling_command"`
}

type PlanLimits struct {
	Enabled                bool `json:"enabled"`
	MaxQueues              int  `json:"max_queues"`
	MaxPendingReservations int  `json:"max_pending_reservations"`
	MaxHorizonDays         int  `json:"max_horizon_days"`
}

type CreateQueueCommand struct {
	WorkspaceID     string
	ActorID         string
	Name            string
	TimeZone        string
	IntervalMinutes int
	HorizonDays     int
	Windows         []RecurringWindow
}

type UpdateQueueCommand struct {
	WorkspaceID      string
	ActorID          string
	QueueID          string
	ExpectedRevision int64
	Name             string
	TimeZone         string
	IntervalMinutes  int
	HorizonDays      int
	Windows          []RecurringWindow
}

type PreviewCommand struct {
	WorkspaceID  string
	ActorID      string
	QueueID      string
	NotBeforeUTC time.Time
	UntilUTC     *time.Time
}

type ConfirmCommand struct {
	WorkspaceID    string
	ActorID        string
	QueueID        string
	PreviewToken   string
	DraftID        string
	ChannelIDs     []string
	IdempotencyKey string
}

type ConfirmRequest struct {
	Preview                Preview
	Reservation            Reservation
	SchedulingCommand      SchedulingCommand
	ConfirmationHash       string
	MaxPendingReservations int
}
