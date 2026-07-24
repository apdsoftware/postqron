package statusnotifications

import (
	"context"
	"time"
)

type Repository interface {
	ApplyLifecycle(context.Context, LifecycleEvent) (ApplyResult, error)
	ApplyPublication(context.Context, PublicationEvent) (ApplyResult, error)
	GetPost(context.Context, string, string) (PostView, error)

	EnqueueNotification(context.Context, Notification) (EnqueueResult, error)
	ClaimNotification(
		context.Context,
		time.Time,
		time.Time,
		string,
	) (Notification, bool, error)
	MarkNotificationDelivered(context.Context, string, string, time.Time) error
	MarkNotificationRetry(
		context.Context,
		string,
		string,
		time.Time,
	) error

	EnqueueManualRetry(context.Context, ManualRetry) (EnqueueResult, error)
	ClaimManualRetry(
		context.Context,
		time.Time,
		time.Time,
		string,
	) (ManualRetry, bool, error)
	MarkManualRetryDelivered(context.Context, string, string, time.Time) error
	MarkManualRetryRetry(context.Context, string, string, time.Time) error
}
