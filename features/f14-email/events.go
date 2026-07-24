package email

import (
	"context"
	"errors"
	"time"
)

type ProviderEventType string

const (
	EventDelivered  ProviderEventType = "delivered"
	EventDeferred   ProviderEventType = "deferred"
	EventSoftBounce ProviderEventType = "soft_bounce"
	EventHardBounce ProviderEventType = "hard_bounce"
	EventComplaint  ProviderEventType = "complaint"
)

type ProviderEvent struct {
	ID                string
	ProviderMessageID string
	Type              ProviderEventType
	RecipientID       string
	Diagnostic        Diagnostic
	OccurredAt        time.Time
}

type SuppressionScope string

const (
	SuppressMarketing SuppressionScope = "marketing"
	SuppressAll       SuppressionScope = "all"
)

type Suppression struct {
	RecipientID string
	Scope       SuppressionScope
	Reason      string
	OccurredAt  time.Time
}

func ProcessProviderEvent(
	ctx context.Context,
	store Store,
	event ProviderEvent,
) (bool, error) {
	if store == nil {
		return false, errors.New("email store is required")
	}
	if event.ID == "" ||
		event.ProviderMessageID == "" ||
		event.RecipientID == "" ||
		event.OccurredAt.IsZero() {
		return false, errors.New("invalid provider event")
	}
	switch event.Type {
	case EventDelivered, EventDeferred, EventSoftBounce, EventHardBounce, EventComplaint:
	default:
		return false, errors.New("unknown provider event")
	}
	event.Diagnostic.Code = sanitizeCode(event.Diagnostic.Code)
	event.Diagnostic.Detail = SanitizeDiagnostic(event.Diagnostic.Detail)
	event.Diagnostic.At = event.OccurredAt.UTC()

	// RecordProviderEvent owns the atomic state transition and suppression so a
	// replay cannot observe an event without its hard-bounce/complaint block.
	return store.RecordProviderEvent(ctx, event)
}
