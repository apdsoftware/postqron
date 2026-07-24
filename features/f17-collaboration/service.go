package collaboration

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	"golang.org/x/text/unicode/norm"
)

const (
	maximumCommentLength  = 4000
	maximumDecisionLength = 1000
)

type Service struct {
	repository Repository
	authorizer Authorizer
	drafts     DraftReader
	now        func() time.Time
	random     func([]byte) error
}

type Option func(*Service)

func WithClock(clock func() time.Time) Option {
	return func(service *Service) { service.now = clock }
}

func WithRandom(random func([]byte) error) Option {
	return func(service *Service) { service.random = random }
}

func NewService(
	repository Repository,
	authorizer Authorizer,
	drafts DraftReader,
	options ...Option,
) (*Service, error) {
	if repository == nil || authorizer == nil || drafts == nil {
		return nil, fmt.Errorf(
			"%w: repository, authorizer, and draft reader are required",
			ErrInvalidArgument,
		)
	}
	service := &Service{
		repository: repository,
		authorizer: authorizer,
		drafts:     drafts,
		now:        time.Now,
		random: func(destination []byte) error {
			_, err := rand.Read(destination)
			return err
		},
	}
	for _, option := range options {
		option(service)
	}
	return service, nil
}

func (service *Service) AddComment(
	ctx context.Context,
	command CreateCommentCommand,
) (Comment, error) {
	if err := service.authorize(
		ctx,
		command.WorkspaceID,
		command.ActorID,
		PermissionComment,
	); err != nil {
		return Comment{}, err
	}
	if _, err := service.requireDraft(ctx, command.WorkspaceID, command.DraftID); err != nil {
		return Comment{}, err
	}
	body, err := normalizeRequiredText(command.Body, maximumCommentLength, "comment")
	if err != nil {
		return Comment{}, err
	}
	id, err := service.newID("comment")
	if err != nil {
		return Comment{}, err
	}
	now := service.now().UTC()
	comment := Comment{
		ID:          id,
		WorkspaceID: command.WorkspaceID,
		DraftID:     command.DraftID,
		AuthorID:    command.ActorID,
		Body:        body,
		CreatedAt:   now,
	}
	audit, event, err := service.records(
		"comment.created",
		command.WorkspaceID,
		command.ActorID,
		"comment",
		id,
		command.DraftID,
		id,
		now,
		map[string]any{"comment_id": id},
	)
	if err != nil {
		return Comment{}, err
	}
	return service.repository.CreateComment(ctx, comment, audit, event)
}

func (service *Service) Comments(
	ctx context.Context,
	workspaceID, draftID, actorID string,
) ([]Comment, error) {
	if err := service.authorize(ctx, workspaceID, actorID, PermissionComment); err != nil {
		return nil, err
	}
	if _, err := service.requireDraft(ctx, workspaceID, draftID); err != nil {
		return nil, err
	}
	return service.repository.ListComments(ctx, workspaceID, draftID)
}

func (service *Service) ResolveComment(
	ctx context.Context,
	command ResolveCommentCommand,
) (Comment, error) {
	if err := service.authorize(
		ctx,
		command.WorkspaceID,
		command.ActorID,
		PermissionResolve,
	); err != nil {
		return Comment{}, err
	}
	if _, err := service.requireDraft(ctx, command.WorkspaceID, command.DraftID); err != nil {
		return Comment{}, err
	}
	if strings.TrimSpace(command.CommentID) == "" {
		return Comment{}, fmt.Errorf("%w: comment id is required", ErrInvalidArgument)
	}
	now := service.now().UTC()
	audit, event, err := service.records(
		"comment.resolved",
		command.WorkspaceID,
		command.ActorID,
		"comment",
		command.CommentID,
		command.DraftID,
		command.CommentID,
		now,
		map[string]any{"comment_id": command.CommentID},
	)
	if err != nil {
		return Comment{}, err
	}
	return service.repository.ResolveComment(
		ctx,
		command.WorkspaceID,
		command.DraftID,
		command.CommentID,
		command.ActorID,
		now,
		audit,
		event,
	)
}

func (service *Service) RequestReview(
	ctx context.Context,
	command RequestReviewCommand,
) (Review, bool, error) {
	if err := service.authorize(
		ctx,
		command.WorkspaceID,
		command.ActorID,
		PermissionRequestReview,
	); err != nil {
		return Review{}, false, err
	}
	if command.ExpectedRevision < 1 {
		return Review{}, false, fmt.Errorf(
			"%w: positive expected revision is required",
			ErrInvalidArgument,
		)
	}
	draft, err := service.requireDraft(ctx, command.WorkspaceID, command.DraftID)
	if err != nil {
		return Review{}, false, err
	}
	if draft.Revision != command.ExpectedRevision {
		return Review{}, false, ErrConflict
	}
	if !draft.Valid {
		return Review{}, false, ErrDraftInvalid
	}
	id, err := service.newID("review")
	if err != nil {
		return Review{}, false, err
	}
	now := service.now().UTC()
	review := Review{
		ID:            id,
		WorkspaceID:   command.WorkspaceID,
		DraftID:       command.DraftID,
		DraftRevision: draft.Revision,
		Status:        ReviewPending,
		RequestedBy:   command.ActorID,
		RequestedAt:   now,
	}
	audit, event, err := service.records(
		"review.requested",
		command.WorkspaceID,
		command.ActorID,
		"review",
		id,
		command.DraftID,
		id,
		now,
		map[string]any{"review_id": id, "draft_revision": draft.Revision},
	)
	if err != nil {
		return Review{}, false, err
	}
	return service.repository.RequestReview(ctx, review, audit, event)
}

func (service *Service) Review(
	ctx context.Context,
	workspaceID, draftID, actorID string,
) (Review, error) {
	if err := service.authorize(
		ctx,
		workspaceID,
		actorID,
		PermissionRequestReview,
	); err != nil {
		return Review{}, err
	}
	if _, err := service.requireDraft(ctx, workspaceID, draftID); err != nil {
		return Review{}, err
	}
	return service.repository.LatestReview(ctx, workspaceID, draftID)
}

func (service *Service) DecideReview(
	ctx context.Context,
	command DecideReviewCommand,
) (Review, error) {
	if err := service.authorize(
		ctx,
		command.WorkspaceID,
		command.ActorID,
		PermissionApprove,
	); err != nil {
		return Review{}, err
	}
	if command.Decision != DecisionApprove && command.Decision != DecisionRequestChanges {
		return Review{}, fmt.Errorf("%w: unsupported review decision", ErrInvalidArgument)
	}
	if strings.TrimSpace(command.ReviewID) == "" {
		return Review{}, fmt.Errorf("%w: review id is required", ErrInvalidArgument)
	}
	note, err := normalizeOptionalText(command.Note, maximumDecisionLength, "decision note")
	if err != nil {
		return Review{}, err
	}
	if command.Decision == DecisionRequestChanges && note == "" {
		return Review{}, fmt.Errorf(
			"%w: a note is required when requesting changes",
			ErrInvalidArgument,
		)
	}
	current, err := service.repository.LatestReview(
		ctx,
		command.WorkspaceID,
		command.DraftID,
	)
	if err != nil {
		return Review{}, err
	}
	if current.ID != command.ReviewID || current.Status != ReviewPending {
		return Review{}, ErrReviewNotPending
	}
	if command.Decision == DecisionApprove && current.RequestedBy == command.ActorID {
		return Review{}, ErrSelfApproval
	}
	draft, err := service.requireDraft(ctx, command.WorkspaceID, command.DraftID)
	if err != nil {
		return Review{}, err
	}
	if draft.Revision != current.DraftRevision || !draft.Valid {
		return Review{}, ErrConflict
	}
	if command.Decision == DecisionApprove {
		comments, listErr := service.repository.ListComments(
			ctx,
			command.WorkspaceID,
			command.DraftID,
		)
		if listErr != nil {
			return Review{}, listErr
		}
		for _, comment := range comments {
			if comment.ResolvedAt == nil {
				return Review{}, ErrUnresolvedComment
			}
		}
	}
	now := service.now().UTC()
	action := "review.changes_requested"
	status := ReviewChangesRequested
	if command.Decision == DecisionApprove {
		action = "review.approved"
		status = ReviewApproved
	}
	audit, event, err := service.records(
		action,
		command.WorkspaceID,
		command.ActorID,
		"review",
		command.ReviewID,
		command.DraftID,
		command.ReviewID,
		now,
		map[string]any{
			"review_id":      command.ReviewID,
			"draft_revision": current.DraftRevision,
			"status":         string(status),
		},
	)
	if err != nil {
		return Review{}, err
	}
	return service.repository.DecideReview(
		ctx,
		command.WorkspaceID,
		command.DraftID,
		command.ReviewID,
		command.Decision,
		note,
		now,
		audit,
		event,
	)
}

// AuthorizeScheduling is the fail-closed F7 boundary. It authorizes only the
// exact composer revision that an authorized reviewer approved.
func (service *Service) AuthorizeScheduling(
	ctx context.Context,
	request ScheduleAuthorization,
) error {
	if strings.TrimSpace(request.CorrelationID) == "" || request.DraftRevision < 1 {
		return fmt.Errorf(
			"%w: correlation id and positive draft revision are required",
			ErrInvalidArgument,
		)
	}
	draft, draftErr := service.requireDraft(ctx, request.WorkspaceID, request.DraftID)
	if draftErr != nil {
		return draftErr
	}
	review, err := service.repository.LatestReview(ctx, request.WorkspaceID, request.DraftID)
	approved := err == nil &&
		review.Status == ReviewApproved &&
		review.DraftRevision == request.DraftRevision &&
		draft.Revision == request.DraftRevision &&
		draft.Valid
	if approved {
		comments, listErr := service.repository.ListComments(
			ctx,
			request.WorkspaceID,
			request.DraftID,
		)
		if listErr != nil {
			return listErr
		}
		unresolved := false
		for _, comment := range comments {
			if comment.ResolvedAt == nil {
				unresolved = true
				break
			}
		}
		if !unresolved {
			return nil
		}
	}
	if err != nil && !errors.Is(err, ErrNotFound) {
		return err
	}
	reason := "approval_required"
	result := ErrApprovalRequired
	if approved {
		reason = "unresolved_comments"
		result = ErrUnresolvedComment
	}
	now := service.now().UTC()
	audit, event, recordErr := service.records(
		"scheduling.blocked",
		request.WorkspaceID,
		"",
		"draft",
		request.DraftID,
		request.DraftID,
		request.CorrelationID,
		now,
		map[string]any{
			"draft_revision": request.DraftRevision,
			"reason":         reason,
		},
	)
	if recordErr != nil {
		return recordErr
	}
	audit.Outcome = "denied"
	if persistErr := service.repository.RecordSchedulingBlocked(ctx, audit, event); persistErr != nil {
		return persistErr
	}
	return result
}

func (service *Service) PendingEvents(ctx context.Context, limit int) ([]Event, error) {
	if limit < 1 || limit > 1000 {
		return nil, fmt.Errorf("%w: event limit must be between 1 and 1000", ErrInvalidArgument)
	}
	return service.repository.PendingEvents(ctx, limit)
}

func (service *Service) MarkEventPublished(
	ctx context.Context,
	eventID string,
	publishedAt time.Time,
) error {
	if strings.TrimSpace(eventID) == "" || publishedAt.IsZero() {
		return fmt.Errorf("%w: event id and publish time are required", ErrInvalidArgument)
	}
	return service.repository.MarkEventPublished(ctx, eventID, publishedAt.UTC())
}

func (service *Service) authorize(
	ctx context.Context,
	workspaceID, actorID string,
	permission Permission,
) error {
	if strings.TrimSpace(actorID) == "" {
		return ErrUnauthenticated
	}
	if strings.TrimSpace(workspaceID) == "" {
		return fmt.Errorf("%w: workspace id is required", ErrInvalidArgument)
	}
	if err := service.authorizer.Authorize(ctx, workspaceID, actorID, permission); err != nil {
		return fmt.Errorf("authorize %s: %w", permission, err)
	}
	return nil
}

func (service *Service) requireDraft(
	ctx context.Context,
	workspaceID, draftID string,
) (DraftSnapshot, error) {
	if strings.TrimSpace(workspaceID) == "" || strings.TrimSpace(draftID) == "" {
		return DraftSnapshot{}, fmt.Errorf(
			"%w: workspace id and draft id are required",
			ErrInvalidArgument,
		)
	}
	draft, err := service.drafts.Draft(ctx, workspaceID, draftID)
	if err != nil {
		return DraftSnapshot{}, fmt.Errorf("read composer draft: %w", err)
	}
	if draft.ID != draftID || draft.WorkspaceID != workspaceID || draft.Revision < 1 {
		return DraftSnapshot{}, fmt.Errorf("%w: inconsistent draft snapshot", ErrConflict)
	}
	return draft, nil
}

func (service *Service) records(
	action, workspaceID, actorID, targetType, targetID, draftID, correlationID string,
	now time.Time,
	data map[string]any,
) (AuditEvent, Event, error) {
	auditID, err := service.newID("audit")
	if err != nil {
		return AuditEvent{}, Event{}, err
	}
	eventID, err := service.newID("event")
	if err != nil {
		return AuditEvent{}, Event{}, err
	}
	return AuditEvent{
			ID:          auditID,
			WorkspaceID: workspaceID,
			ActorID:     actorID,
			TargetType:  targetType,
			TargetID:    targetID,
			Action:      action,
			Outcome:     "succeeded",
			OccurredAt:  now,
		}, Event{
			ID:            eventID,
			Type:          "collaboration." + action + ".v1",
			WorkspaceID:   workspaceID,
			ActorID:       actorID,
			DraftID:       draftID,
			CorrelationID: correlationID,
			OccurredAt:    now,
			Data:          cloneData(data),
		}, nil
}

func (service *Service) newID(prefix string) (string, error) {
	randomBytes := make([]byte, 18)
	if err := service.random(randomBytes); err != nil {
		return "", fmt.Errorf("generate %s id: %w", prefix, err)
	}
	return prefix + "_" + base64.RawURLEncoding.EncodeToString(randomBytes), nil
}

func normalizeRequiredText(value string, maximum int, field string) (string, error) {
	value, err := normalizeOptionalText(value, maximum, field)
	if err != nil {
		return "", err
	}
	if value == "" {
		return "", fmt.Errorf("%w: %s is required", ErrInvalidArgument, field)
	}
	return value, nil
}

func normalizeOptionalText(value string, maximum int, field string) (string, error) {
	value = strings.TrimSpace(norm.NFC.String(value))
	if len([]rune(value)) > maximum {
		return "", fmt.Errorf(
			"%w: %s exceeds %d characters",
			ErrInvalidArgument,
			field,
			maximum,
		)
	}
	return value, nil
}

func cloneData(source map[string]any) map[string]any {
	if source == nil {
		return map[string]any{}
	}
	target := make(map[string]any, len(source))
	for key, value := range source {
		target[key] = value
	}
	return target
}
