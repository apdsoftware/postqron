package pwa

import (
	"context"
	"errors"
	"time"
)

type EventKind string

const (
	EventPublicationFailed EventKind = "publication_failed"
	EventReviewRequested   EventKind = "review_requested"
	EventReviewApproved    EventKind = "review_approved"
	EventChangesRequested  EventKind = "review_changes_requested"
)

func (kind EventKind) Valid() bool {
	switch kind {
	case EventPublicationFailed, EventReviewRequested, EventReviewApproved,
		EventChangesRequested:
		return true
	default:
		return false
	}
}

type PushEvent struct {
	EventID             string    `json:"event_id"`
	Kind                EventKind `json:"kind"`
	RecipientAccountIDs []string  `json:"recipient_account_ids"`
	WorkspaceID         string    `json:"workspace_id"`
	ResourceID          string    `json:"resource_id"`
	Title               string    `json:"title"`
	Body                string    `json:"body"`
	ActionURL           string    `json:"action_url"`
	OccurredAt          time.Time `json:"occurred_at"`
}

type Subscription struct {
	ID             string     `json:"id"`
	AccountID      string     `json:"account_id"`
	Endpoint       string     `json:"-"`
	P256DH         string     `json:"-"`
	Auth           string     `json:"-"`
	ExpirationTime *time.Time `json:"expiration_time,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
	RevokedAt      *time.Time `json:"revoked_at,omitempty"`
}

type SubscriptionInput struct {
	AccountID      string
	Endpoint       string
	P256DH         string
	Auth           string
	ExpirationTime *time.Time
}

type DeliveryState string

const (
	DeliveryPending   DeliveryState = "pending"
	DeliverySending   DeliveryState = "sending"
	DeliveryRetry     DeliveryState = "retry"
	DeliveryDelivered DeliveryState = "delivered"
	DeliveryFailed    DeliveryState = "failed"
)

type Delivery struct {
	ID             string
	SourceEventID  string
	SubscriptionID string
	Event          PushEvent
	State          DeliveryState
	AttemptCount   int
	NextAttemptAt  time.Time
	LeaseToken     string
	LockedUntil    *time.Time
	CreatedAt      time.Time
}

type SendResult struct {
	StatusCode int
}

type Repository interface {
	UpsertSubscription(context.Context, Subscription) (Subscription, bool, error)
	RevokeSubscription(context.Context, string, string, time.Time) (bool, error)
	ActiveSubscriptions(context.Context, []string, time.Time) ([]Subscription, error)
	EnqueueDeliveries(context.Context, PushEvent, []Subscription, time.Time) (int, error)
	ClaimDelivery(context.Context, time.Time, time.Time, string) (Delivery, Subscription, bool, error)
	MarkDelivered(context.Context, string, string, time.Time) error
	MarkRetry(context.Context, string, string, int, time.Time) error
	MarkFailed(context.Context, string, string, time.Time) error
	ExpireSubscription(context.Context, string, time.Time) error
}

type Gateway interface {
	Send(context.Context, Subscription, PushEvent) (SendResult, error)
}

var (
	ErrInvalidArgument = errors.New("invalid argument")
	ErrUnauthenticated = errors.New("authentication required")
	ErrForbidden       = errors.New("operation forbidden")
	ErrNotFound        = errors.New("resource not found")
	ErrConflict        = errors.New("resource conflict")
)
