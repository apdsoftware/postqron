package collaboration

import (
	"context"
	"sort"
	"sync"
	"time"
)

// MemoryRepository mirrors the transactional invariants for tests and local
// development. Domain changes, audit rows, and outbox events are appended
// while holding the same lock.
type MemoryRepository struct {
	mutex    sync.Mutex
	comments map[string]Comment
	reviews  map[string][]Review
	audits   []AuditEvent
	events   map[string]Event
	order    []string
}

func NewMemoryRepository() *MemoryRepository {
	return &MemoryRepository{
		comments: make(map[string]Comment),
		reviews:  make(map[string][]Review),
		events:   make(map[string]Event),
	}
}

func collaborationKey(workspaceID, draftID string) string {
	return workspaceID + "\x00" + draftID
}

func (repository *MemoryRepository) CreateComment(
	_ context.Context,
	comment Comment,
	audit AuditEvent,
	event Event,
) (Comment, error) {
	repository.mutex.Lock()
	defer repository.mutex.Unlock()
	if _, exists := repository.comments[comment.ID]; exists {
		return Comment{}, ErrConflict
	}
	repository.comments[comment.ID] = comment
	repository.appendRecordsLocked(audit, event)
	return cloneComment(comment), nil
}

func (repository *MemoryRepository) ListComments(
	_ context.Context,
	workspaceID, draftID string,
) ([]Comment, error) {
	repository.mutex.Lock()
	defer repository.mutex.Unlock()
	comments := make([]Comment, 0)
	for _, comment := range repository.comments {
		if comment.WorkspaceID == workspaceID && comment.DraftID == draftID {
			comments = append(comments, cloneComment(comment))
		}
	}
	sort.Slice(comments, func(left, right int) bool {
		if comments[left].CreatedAt.Equal(comments[right].CreatedAt) {
			return comments[left].ID < comments[right].ID
		}
		return comments[left].CreatedAt.Before(comments[right].CreatedAt)
	})
	return comments, nil
}

func (repository *MemoryRepository) ResolveComment(
	_ context.Context,
	workspaceID, draftID, commentID, actorID string,
	now time.Time,
	audit AuditEvent,
	event Event,
) (Comment, error) {
	repository.mutex.Lock()
	defer repository.mutex.Unlock()
	comment, exists := repository.comments[commentID]
	if !exists || comment.WorkspaceID != workspaceID || comment.DraftID != draftID {
		return Comment{}, ErrNotFound
	}
	if comment.ResolvedAt != nil {
		return cloneComment(comment), nil
	}
	comment.ResolvedBy = actorID
	comment.ResolvedAt = timePointer(now)
	repository.comments[commentID] = comment
	repository.appendRecordsLocked(audit, event)
	return cloneComment(comment), nil
}

func (repository *MemoryRepository) RequestReview(
	_ context.Context,
	review Review,
	audit AuditEvent,
	event Event,
) (Review, bool, error) {
	repository.mutex.Lock()
	defer repository.mutex.Unlock()
	key := collaborationKey(review.WorkspaceID, review.DraftID)
	reviews := repository.reviews[key]
	if len(reviews) > 0 {
		latest := reviews[len(reviews)-1]
		if latest.Status == ReviewPending {
			if latest.DraftRevision == review.DraftRevision &&
				latest.RequestedBy == review.RequestedBy {
				return cloneReview(latest), false, nil
			}
			return Review{}, false, ErrReviewPending
		}
	}
	repository.reviews[key] = append(reviews, review)
	repository.appendRecordsLocked(audit, event)
	return cloneReview(review), true, nil
}

func (repository *MemoryRepository) LatestReview(
	_ context.Context,
	workspaceID, draftID string,
) (Review, error) {
	repository.mutex.Lock()
	defer repository.mutex.Unlock()
	reviews := repository.reviews[collaborationKey(workspaceID, draftID)]
	if len(reviews) == 0 {
		return Review{}, ErrNotFound
	}
	return cloneReview(reviews[len(reviews)-1]), nil
}

func (repository *MemoryRepository) DecideReview(
	_ context.Context,
	workspaceID, draftID, reviewID string,
	decision ReviewDecision,
	note string,
	now time.Time,
	audit AuditEvent,
	event Event,
) (Review, error) {
	repository.mutex.Lock()
	defer repository.mutex.Unlock()
	key := collaborationKey(workspaceID, draftID)
	reviews := repository.reviews[key]
	if len(reviews) == 0 {
		return Review{}, ErrNotFound
	}
	index := len(reviews) - 1
	review := reviews[index]
	if review.ID != reviewID || review.Status != ReviewPending {
		return Review{}, ErrReviewNotPending
	}
	switch decision {
	case DecisionApprove:
		for _, comment := range repository.comments {
			if comment.WorkspaceID == workspaceID &&
				comment.DraftID == draftID &&
				comment.ResolvedAt == nil {
				return Review{}, ErrUnresolvedComment
			}
		}
		review.Status = ReviewApproved
	case DecisionRequestChanges:
		review.Status = ReviewChangesRequested
	default:
		return Review{}, ErrInvalidArgument
	}
	review.DecidedBy = audit.ActorID
	review.DecidedAt = timePointer(now)
	review.DecisionNote = note
	reviews[index] = review
	repository.reviews[key] = reviews
	repository.appendRecordsLocked(audit, event)
	return cloneReview(review), nil
}

func (repository *MemoryRepository) RecordSchedulingBlocked(
	_ context.Context,
	audit AuditEvent,
	event Event,
) error {
	repository.mutex.Lock()
	defer repository.mutex.Unlock()
	repository.appendRecordsLocked(audit, event)
	return nil
}

func (repository *MemoryRepository) PendingEvents(
	_ context.Context,
	limit int,
) ([]Event, error) {
	repository.mutex.Lock()
	defer repository.mutex.Unlock()
	events := make([]Event, 0, limit)
	for _, id := range repository.order {
		event := repository.events[id]
		if event.PublishedAt != nil {
			continue
		}
		events = append(events, cloneEvent(event))
		if len(events) == limit {
			break
		}
	}
	return events, nil
}

func (repository *MemoryRepository) MarkEventPublished(
	_ context.Context,
	eventID string,
	publishedAt time.Time,
) error {
	repository.mutex.Lock()
	defer repository.mutex.Unlock()
	event, exists := repository.events[eventID]
	if !exists {
		return ErrNotFound
	}
	if event.PublishedAt == nil {
		event.PublishedAt = timePointer(publishedAt)
		repository.events[eventID] = event
	}
	return nil
}

func (repository *MemoryRepository) AuditEvents() []AuditEvent {
	repository.mutex.Lock()
	defer repository.mutex.Unlock()
	return append([]AuditEvent(nil), repository.audits...)
}

func (repository *MemoryRepository) appendRecordsLocked(audit AuditEvent, event Event) {
	repository.audits = append(repository.audits, audit)
	repository.events[event.ID] = cloneEvent(event)
	repository.order = append(repository.order, event.ID)
}

func cloneComment(comment Comment) Comment {
	if comment.ResolvedAt != nil {
		comment.ResolvedAt = timePointer(*comment.ResolvedAt)
	}
	return comment
}

func cloneReview(review Review) Review {
	if review.DecidedAt != nil {
		review.DecidedAt = timePointer(*review.DecidedAt)
	}
	return review
}

func cloneEvent(event Event) Event {
	event.Data = cloneData(event.Data)
	if event.PublishedAt != nil {
		event.PublishedAt = timePointer(*event.PublishedAt)
	}
	return event
}

func timePointer(value time.Time) *time.Time {
	copyOfValue := value
	return &copyOfValue
}
