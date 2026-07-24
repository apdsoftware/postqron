// Package collaboration owns editorial comments, review decisions, and the
// approval gate consumed by scheduling.
package collaboration

import (
	"context"
	"errors"
	"time"
)

const FeatureID = "f17-collaboration"

type Permission string

const (
	PermissionComment       Permission = "collaboration.comment"
	PermissionResolve       Permission = "collaboration.comment.resolve"
	PermissionRequestReview Permission = "collaboration.review.request"
	PermissionApprove       Permission = "collaboration.review.approve"
)

type ReviewStatus string

const (
	ReviewPending          ReviewStatus = "pending"
	ReviewChangesRequested ReviewStatus = "changes_requested"
	ReviewApproved         ReviewStatus = "approved"
)

type ReviewDecision string

const (
	DecisionRequestChanges ReviewDecision = "request_changes"
	DecisionApprove        ReviewDecision = "approve"
)

type Comment struct {
	ID          string     `json:"id"`
	WorkspaceID string     `json:"workspace_id"`
	DraftID     string     `json:"draft_id"`
	AuthorID    string     `json:"author_id"`
	Body        string     `json:"body"`
	CreatedAt   time.Time  `json:"created_at"`
	ResolvedBy  string     `json:"resolved_by,omitempty"`
	ResolvedAt  *time.Time `json:"resolved_at,omitempty"`
}

type Review struct {
	ID            string       `json:"id"`
	WorkspaceID   string       `json:"workspace_id"`
	DraftID       string       `json:"draft_id"`
	DraftRevision int64        `json:"draft_revision"`
	Status        ReviewStatus `json:"status"`
	RequestedBy   string       `json:"requested_by"`
	RequestedAt   time.Time    `json:"requested_at"`
	DecidedBy     string       `json:"decided_by,omitempty"`
	DecidedAt     *time.Time   `json:"decided_at,omitempty"`
	DecisionNote  string       `json:"decision_note,omitempty"`
}

// DraftSnapshot is the read-only F6 boundary. Valid must reflect the
// authoritative composer validation, not client-side validation.
type DraftSnapshot struct {
	ID          string
	WorkspaceID string
	Revision    int64
	Valid       bool
}

type AuditEvent struct {
	ID          string    `json:"id"`
	WorkspaceID string    `json:"workspace_id"`
	ActorID     string    `json:"actor_id,omitempty"`
	TargetType  string    `json:"target_type"`
	TargetID    string    `json:"target_id"`
	Action      string    `json:"action"`
	Outcome     string    `json:"outcome"`
	OccurredAt  time.Time `json:"occurred_at"`
}

// Event is the F9-facing transactional outbox record. It contains opaque IDs
// and review metadata only; comment bodies are never copied into events.
type Event struct {
	ID            string         `json:"id"`
	Type          string         `json:"type"`
	WorkspaceID   string         `json:"workspace_id"`
	ActorID       string         `json:"actor_id,omitempty"`
	DraftID       string         `json:"draft_id"`
	CorrelationID string         `json:"correlation_id"`
	OccurredAt    time.Time      `json:"occurred_at"`
	Data          map[string]any `json:"data"`
	PublishedAt   *time.Time     `json:"published_at,omitempty"`
}

type CreateCommentCommand struct {
	WorkspaceID string
	DraftID     string
	ActorID     string
	Body        string
}

type ResolveCommentCommand struct {
	WorkspaceID string
	DraftID     string
	CommentID   string
	ActorID     string
}

type RequestReviewCommand struct {
	WorkspaceID      string
	DraftID          string
	ActorID          string
	ExpectedRevision int64
}

type DecideReviewCommand struct {
	WorkspaceID string
	DraftID     string
	ReviewID    string
	ActorID     string
	Decision    ReviewDecision
	Note        string
}

type ScheduleAuthorization struct {
	WorkspaceID   string
	DraftID       string
	DraftRevision int64
	CorrelationID string
}

type Authorizer interface {
	Authorize(context.Context, string, string, Permission) error
}

type DraftReader interface {
	Draft(context.Context, string, string) (DraftSnapshot, error)
}

type Repository interface {
	CreateComment(context.Context, Comment, AuditEvent, Event) (Comment, error)
	ListComments(context.Context, string, string) ([]Comment, error)
	ResolveComment(
		context.Context,
		string,
		string,
		string,
		string,
		time.Time,
		AuditEvent,
		Event,
	) (Comment, error)
	RequestReview(context.Context, Review, AuditEvent, Event) (Review, bool, error)
	LatestReview(context.Context, string, string) (Review, error)
	DecideReview(
		context.Context,
		string,
		string,
		string,
		ReviewDecision,
		string,
		time.Time,
		AuditEvent,
		Event,
	) (Review, error)
	RecordSchedulingBlocked(context.Context, AuditEvent, Event) error
	PendingEvents(context.Context, int) ([]Event, error)
	MarkEventPublished(context.Context, string, time.Time) error
}

var (
	ErrInvalidArgument   = errors.New("invalid argument")
	ErrUnauthenticated   = errors.New("authentication required")
	ErrForbidden         = errors.New("operation forbidden")
	ErrNotFound          = errors.New("resource not found")
	ErrConflict          = errors.New("resource conflict")
	ErrDraftInvalid      = errors.New("draft is not valid for review")
	ErrReviewPending     = errors.New("a review is already pending")
	ErrReviewNotPending  = errors.New("review is not pending")
	ErrSelfApproval      = errors.New("review requester cannot approve their own draft")
	ErrApprovalRequired  = errors.New("an approval for the current draft revision is required")
	ErrUnresolvedComment = errors.New("unresolved review comments block approval")
)
