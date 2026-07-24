package statusnotifications

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"
)

func validateLifecycleEvent(event LifecycleEvent) error {
	if strings.TrimSpace(event.EventID) == "" ||
		strings.TrimSpace(event.WorkspaceID) == "" ||
		strings.TrimSpace(event.PostID) == "" ||
		event.Revision < 1 ||
		(event.Status != StatusDraft &&
			event.Status != StatusScheduled &&
			event.Status != StatusCancelled) ||
		event.OccurredAt.IsZero() {
		return fmt.Errorf("%w: invalid lifecycle event", ErrInvalidArgument)
	}
	seen := make(map[string]struct{}, len(event.Destinations))
	for _, destination := range event.Destinations {
		if strings.TrimSpace(destination.ID) == "" ||
			strings.TrimSpace(destination.ChannelID) == "" {
			return fmt.Errorf("%w: invalid lifecycle destination", ErrInvalidArgument)
		}
		if _, exists := seen[destination.ID]; exists {
			return fmt.Errorf("%w: duplicate lifecycle destination", ErrInvalidArgument)
		}
		seen[destination.ID] = struct{}{}
	}
	return nil
}

func validatePublicationEvent(event PublicationEvent) error {
	if strings.TrimSpace(event.EventID) == "" ||
		strings.TrimSpace(event.WorkspaceID) == "" ||
		strings.TrimSpace(event.JobID) == "" ||
		strings.TrimSpace(event.PostID) == "" ||
		strings.TrimSpace(event.DestinationID) == "" ||
		strings.TrimSpace(event.ChannelID) == "" ||
		event.OccurredAt.IsZero() ||
		!destinationStatusFromPublication(event.Status).Valid() {
		return fmt.Errorf("%w: invalid publication event", ErrInvalidArgument)
	}
	if event.Status == "published" && strings.TrimSpace(event.RemoteID) == "" {
		return fmt.Errorf("%w: published event requires remote id", ErrInvalidArgument)
	}
	return nil
}

func validateNotification(notification Notification) error {
	if strings.TrimSpace(notification.ID) == "" ||
		strings.TrimSpace(notification.SourceEventID) == "" ||
		!notification.Kind.Valid() ||
		strings.TrimSpace(notification.IdempotencyKey) == "" ||
		len(notification.IdempotencyKey) > 255 ||
		notification.State != QueuePending ||
		notification.CreatedAt.IsZero() ||
		notification.NextAttemptAt.IsZero() {
		return fmt.Errorf("%w: invalid notification", ErrInvalidArgument)
	}
	if notification.Kind == NotificationWelcome ||
		notification.Kind == NotificationSecurityAlert {
		if strings.TrimSpace(notification.AccountID) == "" {
			return fmt.Errorf("%w: notification account is required", ErrInvalidArgument)
		}
	} else if strings.TrimSpace(notification.WorkspaceID) == "" {
		return fmt.Errorf("%w: notification workspace is required", ErrInvalidArgument)
	}
	if notification.ActionURL != "" && !absoluteHTTPS(notification.ActionURL) {
		return fmt.Errorf("%w: notification action URL", ErrInvalidArgument)
	}
	if (notification.ActionURL == "") != (notification.ActionLabel == "") {
		return fmt.Errorf(
			"%w: notification action label and URL must be paired",
			ErrInvalidArgument,
		)
	}
	return nil
}

func validateManualRetry(retry ManualRetry) error {
	if strings.TrimSpace(retry.ID) == "" ||
		strings.TrimSpace(retry.WorkspaceID) == "" ||
		strings.TrimSpace(retry.PostID) == "" ||
		strings.TrimSpace(retry.DestinationID) == "" ||
		strings.TrimSpace(retry.FailureEventID) == "" ||
		strings.TrimSpace(retry.ActorID) == "" ||
		strings.TrimSpace(retry.IdempotencyKey) == "" ||
		len(retry.IdempotencyKey) > 255 ||
		retry.State != QueuePending ||
		retry.CreatedAt.IsZero() ||
		retry.NextAttemptAt.IsZero() {
		return fmt.Errorf("%w: invalid manual retry", ErrInvalidArgument)
	}
	return nil
}

func lifecycleFingerprint(event LifecycleEvent) string {
	return hashJSON(struct {
		WorkspaceID  string
		PostID       string
		DraftID      string
		Revision     int64
		Status       PostStatus
		Destinations []DestinationRef
		OccurredAt   string
	}{
		WorkspaceID:  event.WorkspaceID,
		PostID:       event.PostID,
		DraftID:      event.DraftID,
		Revision:     event.Revision,
		Status:       event.Status,
		Destinations: event.Destinations,
		OccurredAt:   event.OccurredAt.UTC().Format(time.RFC3339Nano),
	})
}

func publicationFingerprint(event PublicationEvent) string {
	return hashJSON(struct {
		WorkspaceID   string
		JobID         string
		PostID        string
		DestinationID string
		ChannelID     string
		Status        string
		RemoteID      string
		Diagnostic    SourceDiagnostic
		OccurredAt    string
	}{
		WorkspaceID:   event.WorkspaceID,
		JobID:         event.JobID,
		PostID:        event.PostID,
		DestinationID: event.DestinationID,
		ChannelID:     event.ChannelID,
		Status:        event.Status,
		RemoteID:      event.RemoteID,
		Diagnostic:    event.Diagnostic,
		OccurredAt:    event.OccurredAt.UTC().Format(time.RFC3339Nano),
	})
}

func derivePublicationEventID(event PublicationEvent) string {
	return "f08_" + publicationFingerprint(event)
}

func hashJSON(value any) string {
	payload, _ := json.Marshal(value)
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:])
}

func stableID(prefix string, values ...string) string {
	digest := sha256.Sum256([]byte(strings.Join(values, "\x00")))
	return prefix + "_" + hex.EncodeToString(digest[:16])
}

func absoluteHTTPS(value string) bool {
	parsed, err := url.ParseRequestURI(value)
	return err == nil && parsed.Scheme == "https" && parsed.Host != ""
}
