package pwa

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// StatusEvent is the F9 adapter boundary after its recipient resolver has
// selected workspace recipients. Raw provider diagnostics are never accepted.
type StatusEvent struct {
	EventID             string
	Kind                string
	RecipientAccountIDs []string
	WorkspaceID         string
	PostID              string
	SafeDetail          string
	OccurredAt          time.Time
}

func (service *Service) ConsumeStatusEvent(
	ctx context.Context,
	event StatusEvent,
) (int, error) {
	if event.Kind != "publication_failed" {
		return 0, fmt.Errorf("%w: unsupported F9 push event", ErrInvalidArgument)
	}
	return service.ConsumeEvent(ctx, PushEvent{
		EventID:             event.EventID,
		Kind:                EventPublicationFailed,
		RecipientAccountIDs: event.RecipientAccountIDs,
		WorkspaceID:         event.WorkspaceID,
		ResourceID:          event.PostID,
		Title:               "Pubblicazione non riuscita",
		Body:                truncate(event.SafeDetail, maxBodyLength),
		ActionURL: "/app/workspaces/" + url.PathEscape(event.WorkspaceID) +
			"/posts/" + url.PathEscape(event.PostID),
		OccurredAt: event.OccurredAt,
	})
}

// CollaborationEvent mirrors the stable F17 v1 envelope. Recipient IDs are
// added by the workspace membership adapter; they do not come from clients.
type CollaborationEvent struct {
	ID                  string
	Type                string
	RecipientAccountIDs []string
	WorkspaceID         string
	DraftID             string
	OccurredAt          time.Time
}

func (service *Service) ConsumeCollaborationEvent(
	ctx context.Context,
	event CollaborationEvent,
) (int, error) {
	var kind EventKind
	var title, body string
	switch event.Type {
	case "collaboration.review.requested.v1":
		kind = EventReviewRequested
		title = "Contenuto da approvare"
		body = "È stata richiesta la tua revisione editoriale."
	case "collaboration.review.approved.v1":
		kind = EventReviewApproved
		title = "Contenuto approvato"
		body = "La revisione editoriale è stata approvata."
	case "collaboration.review.changes_requested.v1":
		kind = EventChangesRequested
		title = "Modifiche richieste"
		body = "La revisione editoriale richiede modifiche."
	default:
		return 0, fmt.Errorf("%w: unsupported F17 push event", ErrInvalidArgument)
	}
	return service.ConsumeEvent(ctx, PushEvent{
		EventID:             event.ID,
		Kind:                kind,
		RecipientAccountIDs: event.RecipientAccountIDs,
		WorkspaceID:         event.WorkspaceID,
		ResourceID:          event.DraftID,
		Title:               title,
		Body:                body,
		ActionURL: "/app/workspaces/" + url.PathEscape(event.WorkspaceID) +
			"/drafts/" + url.PathEscape(event.DraftID) + "/review",
		OccurredAt: event.OccurredAt,
	})
}

func truncate(value string, maximum int) string {
	value = strings.TrimSpace(value)
	runes := []rune(value)
	if len(runes) <= maximum {
		return value
	}
	return string(runes[:maximum-1]) + "…"
}
