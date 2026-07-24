package statusnotifications

import (
	"context"
	"slices"
	"strings"
	"sync"
	"time"
)

type postRecord struct {
	view        PostView
	revision    int64
	lastEventID string
	lastEventAt time.Time
}

// MemoryRepository is intended for unit tests and local development.
// Production uses the schema in migrations with transactional event ledgers
// and SKIP LOCKED work queues.
type MemoryRepository struct {
	mutex                sync.Mutex
	posts                map[string]postRecord
	events               map[string]string
	notifications        map[string]Notification
	notificationBySource map[string]string
	notificationOrder    []string
	retries              map[string]ManualRetry
	retryByKey           map[string]string
	retryByFailure       map[string]string
	retryOrder           []string
}

func NewMemoryRepository() *MemoryRepository {
	return &MemoryRepository{
		posts:                make(map[string]postRecord),
		events:               make(map[string]string),
		notifications:        make(map[string]Notification),
		notificationBySource: make(map[string]string),
		retries:              make(map[string]ManualRetry),
		retryByKey:           make(map[string]string),
		retryByFailure:       make(map[string]string),
	}
}

func (repository *MemoryRepository) ApplyLifecycle(
	_ context.Context,
	event LifecycleEvent,
) (ApplyResult, error) {
	if err := validateLifecycleEvent(event); err != nil {
		return ApplyResult{}, err
	}
	repository.mutex.Lock()
	defer repository.mutex.Unlock()

	fingerprint := lifecycleFingerprint(event)
	if previous, exists := repository.events[event.EventID]; exists {
		if previous != fingerprint {
			return ApplyResult{}, ErrConflict
		}
		return repository.duplicateResultLocked(event.WorkspaceID, event.PostID)
	}
	repository.events[event.EventID] = fingerprint

	key := postKey(event.WorkspaceID, event.PostID)
	record, exists := repository.posts[key]
	if exists && (record.view.Status == StatusPublished ||
		record.view.Status == StatusCancelled ||
		event.OccurredAt.Before(record.view.UpdatedAt) ||
		event.Revision < record.revision ||
		event.Revision == record.revision &&
			!event.OccurredAt.After(record.lastEventAt)) {
		return ApplyResult{
			FirstDelivery: true,
			View:          clonePostView(record.view),
		}, nil
	}

	now := event.OccurredAt.UTC()
	if !exists {
		record.view = PostView{
			WorkspaceID:  event.WorkspaceID,
			PostID:       event.PostID,
			CreatedAt:    now,
			Destinations: []DestinationView{},
		}
	}
	record.view.DraftID = event.DraftID
	record.view.Status = event.Status
	record.view.UpdatedAt = now
	record.revision = event.Revision
	record.lastEventID = event.EventID
	record.lastEventAt = now

	current := make(map[string]DestinationView, len(record.view.Destinations))
	for _, destination := range record.view.Destinations {
		current[destination.ID] = destination
	}
	next := make([]DestinationView, 0, len(event.Destinations))
	for _, reference := range event.Destinations {
		destination, found := current[reference.ID]
		if !found {
			destination = DestinationView{
				ID:        reference.ID,
				ChannelID: reference.ChannelID,
			}
		}
		destination.ChannelID = reference.ChannelID
		destination.Status = destinationStatusForPost(event.Status)
		destination.RemoteID = ""
		destination.Diagnostic = Diagnostic{}
		destination.LastEventID = event.EventID
		destination.UpdatedAt = now
		next = append(next, destination)
	}
	slices.SortFunc(next, compareDestination)
	record.view.Destinations = next
	repository.posts[key] = record
	return ApplyResult{
		FirstDelivery: true,
		StateChanged:  true,
		View:          clonePostView(record.view),
	}, nil
}

func (repository *MemoryRepository) ApplyPublication(
	_ context.Context,
	event PublicationEvent,
) (ApplyResult, error) {
	if err := validatePublicationEvent(event); err != nil {
		return ApplyResult{}, err
	}
	repository.mutex.Lock()
	defer repository.mutex.Unlock()

	fingerprint := publicationFingerprint(event)
	if previous, exists := repository.events[event.EventID]; exists {
		if previous != fingerprint {
			return ApplyResult{}, ErrConflict
		}
		return repository.duplicateResultLocked(event.WorkspaceID, event.PostID)
	}
	repository.events[event.EventID] = fingerprint

	key := postKey(event.WorkspaceID, event.PostID)
	record, exists := repository.posts[key]
	if !exists {
		record = postRecord{view: PostView{
			WorkspaceID:  event.WorkspaceID,
			PostID:       event.PostID,
			Status:       StatusScheduled,
			Destinations: []DestinationView{},
			CreatedAt:    event.OccurredAt.UTC(),
			UpdatedAt:    event.OccurredAt.UTC(),
		}}
	}

	index := -1
	for position := range record.view.Destinations {
		if record.view.Destinations[position].ID == event.DestinationID {
			index = position
			break
		}
	}
	incoming := destinationStatusFromPublication(event.Status)
	changed := false
	if index == -1 {
		record.view.Destinations = append(record.view.Destinations, DestinationView{
			ID:        event.DestinationID,
			ChannelID: event.ChannelID,
			Status:    DestinationScheduled,
			UpdatedAt: record.view.CreatedAt,
		})
		index = len(record.view.Destinations) - 1
	}

	destination := record.view.Destinations[index]
	if publicationEventWins(destination, incoming, event.OccurredAt.UTC()) {
		destination.ChannelID = event.ChannelID
		destination.Status = incoming
		destination.RemoteID = strings.TrimSpace(event.RemoteID)
		destination.LastEventID = event.EventID
		destination.UpdatedAt = event.OccurredAt.UTC()
		if incoming == DestinationFailed {
			destination.Diagnostic = ClientDiagnostic(
				event.Diagnostic,
				event.OccurredAt,
			)
		} else {
			destination.Diagnostic = Diagnostic{}
		}
		record.view.Destinations[index] = destination
		changed = true
	}

	if changed {
		slices.SortFunc(record.view.Destinations, compareDestination)
		record.view.Status = aggregateStatus(record.view.Destinations)
		if event.OccurredAt.After(record.view.UpdatedAt) {
			record.view.UpdatedAt = event.OccurredAt.UTC()
		}
		repository.posts[key] = record
	}
	return ApplyResult{
		FirstDelivery: true,
		StateChanged:  changed,
		View:          clonePostView(record.view),
	}, nil
}

func (repository *MemoryRepository) GetPost(
	_ context.Context,
	workspaceID, postID string,
) (PostView, error) {
	repository.mutex.Lock()
	defer repository.mutex.Unlock()
	record, exists := repository.posts[postKey(workspaceID, postID)]
	if !exists {
		return PostView{}, ErrNotFound
	}
	return clonePostView(record.view), nil
}

func (repository *MemoryRepository) EnqueueNotification(
	_ context.Context,
	notification Notification,
) (EnqueueResult, error) {
	if err := validateNotification(notification); err != nil {
		return EnqueueResult{}, err
	}
	repository.mutex.Lock()
	defer repository.mutex.Unlock()
	sourceKey := string(notification.Kind) + ":" + notification.SourceEventID
	if existingID, exists := repository.notificationBySource[sourceKey]; exists {
		existing := repository.notifications[existingID]
		return EnqueueResult{ID: existingID, State: existing.State}, nil
	}
	if _, exists := repository.notifications[notification.ID]; exists {
		return EnqueueResult{}, ErrConflict
	}
	repository.notifications[notification.ID] = notification
	repository.notificationBySource[sourceKey] = notification.ID
	repository.notificationOrder = append(repository.notificationOrder, notification.ID)
	return EnqueueResult{
		ID:      notification.ID,
		Created: true,
		State:   notification.State,
	}, nil
}

func (repository *MemoryRepository) ClaimNotification(
	_ context.Context,
	now, lockedUntil time.Time,
	leaseToken string,
) (Notification, bool, error) {
	repository.mutex.Lock()
	defer repository.mutex.Unlock()
	for _, id := range repository.notificationOrder {
		item := repository.notifications[id]
		if !claimable(item.State, item.NextAttemptAt, item.LockedUntil, now) {
			continue
		}
		item.State = QueueSending
		item.AttemptCount++
		item.LeaseToken = leaseToken
		lock := lockedUntil
		item.LockedUntil = &lock
		repository.notifications[id] = item
		return item, true, nil
	}
	return Notification{}, false, nil
}

func (repository *MemoryRepository) MarkNotificationDelivered(
	_ context.Context,
	id, leaseToken string,
	_ time.Time,
) error {
	repository.mutex.Lock()
	defer repository.mutex.Unlock()
	item, exists := repository.notifications[id]
	if !exists || item.State != QueueSending || item.LeaseToken != leaseToken {
		return ErrLeaseLost
	}
	item.State = QueueDelivered
	item.LeaseToken = ""
	item.LockedUntil = nil
	repository.notifications[id] = item
	return nil
}

func (repository *MemoryRepository) MarkNotificationRetry(
	_ context.Context,
	id, leaseToken string,
	next time.Time,
) error {
	repository.mutex.Lock()
	defer repository.mutex.Unlock()
	item, exists := repository.notifications[id]
	if !exists || item.State != QueueSending || item.LeaseToken != leaseToken {
		return ErrLeaseLost
	}
	item.State = QueueRetry
	item.NextAttemptAt = next
	item.LeaseToken = ""
	item.LockedUntil = nil
	repository.notifications[id] = item
	return nil
}

func (repository *MemoryRepository) EnqueueManualRetry(
	_ context.Context,
	retry ManualRetry,
) (EnqueueResult, error) {
	if err := validateManualRetry(retry); err != nil {
		return EnqueueResult{}, err
	}
	repository.mutex.Lock()
	defer repository.mutex.Unlock()

	post, exists := repository.posts[postKey(retry.WorkspaceID, retry.PostID)]
	if !exists {
		return EnqueueResult{}, ErrNotFound
	}
	var destination DestinationView
	found := false
	for _, candidate := range post.view.Destinations {
		if candidate.ID == retry.DestinationID {
			destination = candidate
			found = true
			break
		}
	}
	if !found {
		return EnqueueResult{}, ErrNotFound
	}
	if destination.Status != DestinationFailed ||
		destination.LastEventID != retry.FailureEventID {
		return EnqueueResult{}, ErrConflict
	}
	if existingID, exists := repository.retryByKey[retry.WorkspaceID+":"+retry.IdempotencyKey]; exists {
		existing := repository.retries[existingID]
		return EnqueueResult{ID: existingID, State: existing.State}, nil
	}
	failureKey := retry.DestinationID + ":" + retry.FailureEventID
	if existingID, exists := repository.retryByFailure[failureKey]; exists {
		existing := repository.retries[existingID]
		return EnqueueResult{ID: existingID, State: existing.State}, nil
	}
	repository.retries[retry.ID] = retry
	repository.retryByKey[retry.WorkspaceID+":"+retry.IdempotencyKey] = retry.ID
	repository.retryByFailure[failureKey] = retry.ID
	repository.retryOrder = append(repository.retryOrder, retry.ID)
	return EnqueueResult{ID: retry.ID, Created: true, State: retry.State}, nil
}

func (repository *MemoryRepository) ClaimManualRetry(
	_ context.Context,
	now, lockedUntil time.Time,
	leaseToken string,
) (ManualRetry, bool, error) {
	repository.mutex.Lock()
	defer repository.mutex.Unlock()
	for _, id := range repository.retryOrder {
		item := repository.retries[id]
		if !claimable(item.State, item.NextAttemptAt, item.LockedUntil, now) {
			continue
		}
		item.State = QueueSending
		item.AttemptCount++
		item.LeaseToken = leaseToken
		lock := lockedUntil
		item.LockedUntil = &lock
		repository.retries[id] = item
		return item, true, nil
	}
	return ManualRetry{}, false, nil
}

func (repository *MemoryRepository) MarkManualRetryDelivered(
	_ context.Context,
	id, leaseToken string,
	_ time.Time,
) error {
	repository.mutex.Lock()
	defer repository.mutex.Unlock()
	item, exists := repository.retries[id]
	if !exists || item.State != QueueSending || item.LeaseToken != leaseToken {
		return ErrLeaseLost
	}
	item.State = QueueDelivered
	item.LeaseToken = ""
	item.LockedUntil = nil
	repository.retries[id] = item
	return nil
}

func (repository *MemoryRepository) MarkManualRetryRetry(
	_ context.Context,
	id, leaseToken string,
	next time.Time,
) error {
	repository.mutex.Lock()
	defer repository.mutex.Unlock()
	item, exists := repository.retries[id]
	if !exists || item.State != QueueSending || item.LeaseToken != leaseToken {
		return ErrLeaseLost
	}
	item.State = QueueRetry
	item.NextAttemptAt = next
	item.LeaseToken = ""
	item.LockedUntil = nil
	repository.retries[id] = item
	return nil
}

func (repository *MemoryRepository) Notifications() []Notification {
	repository.mutex.Lock()
	defer repository.mutex.Unlock()
	result := make([]Notification, 0, len(repository.notificationOrder))
	for _, id := range repository.notificationOrder {
		result = append(result, repository.notifications[id])
	}
	return result
}

func (repository *MemoryRepository) ManualRetries() []ManualRetry {
	repository.mutex.Lock()
	defer repository.mutex.Unlock()
	result := make([]ManualRetry, 0, len(repository.retryOrder))
	for _, id := range repository.retryOrder {
		result = append(result, repository.retries[id])
	}
	return result
}

func (repository *MemoryRepository) duplicateResultLocked(
	workspaceID, postID string,
) (ApplyResult, error) {
	record, exists := repository.posts[postKey(workspaceID, postID)]
	if !exists {
		return ApplyResult{}, ErrNotFound
	}
	return ApplyResult{View: clonePostView(record.view)}, nil
}

func postKey(workspaceID, postID string) string {
	return workspaceID + ":" + postID
}

func clonePostView(source PostView) PostView {
	source.Destinations = slices.Clone(source.Destinations)
	return source
}

func compareDestination(left, right DestinationView) int {
	return strings.Compare(left.ID, right.ID)
}

func destinationStatusForPost(status PostStatus) DestinationStatus {
	switch status {
	case StatusDraft:
		return DestinationDraft
	case StatusScheduled:
		return DestinationScheduled
	case StatusPublishing:
		return DestinationPublishing
	case StatusPublished:
		return DestinationPublished
	case StatusFailed:
		return DestinationFailed
	case StatusCancelled:
		return DestinationCancelled
	default:
		return ""
	}
}

func destinationStatusFromPublication(status string) DestinationStatus {
	switch status {
	case "pending":
		return DestinationScheduled
	case "publishing", "retry_wait":
		return DestinationPublishing
	case "published":
		return DestinationPublished
	case "dead_letter":
		return DestinationFailed
	case "cancelled":
		return DestinationCancelled
	default:
		return ""
	}
}

func publicationEventWins(
	current DestinationView,
	incoming DestinationStatus,
	occurredAt time.Time,
) bool {
	if current.Status == DestinationPublished ||
		current.Status == DestinationCancelled {
		return false
	}
	if occurredAt.Before(current.UpdatedAt) {
		return false
	}
	if occurredAt.Equal(current.UpdatedAt) {
		return destinationPrecedence(incoming) > destinationPrecedence(current.Status)
	}
	return true
}

func destinationPrecedence(status DestinationStatus) int {
	switch status {
	case DestinationDraft:
		return 0
	case DestinationScheduled:
		return 1
	case DestinationPublishing:
		return 2
	case DestinationFailed:
		return 3
	case DestinationPublished:
		return 4
	case DestinationCancelled:
		return 5
	default:
		return -1
	}
}

func aggregateStatus(destinations []DestinationView) PostStatus {
	if len(destinations) == 0 {
		return StatusDraft
	}
	counts := make(map[DestinationStatus]int)
	for _, destination := range destinations {
		counts[destination.Status]++
	}
	if counts[DestinationFailed] > 0 {
		return StatusFailed
	}
	if counts[DestinationPublishing] > 0 {
		return StatusPublishing
	}
	if counts[DestinationScheduled] > 0 {
		return StatusScheduled
	}
	if counts[DestinationDraft] > 0 {
		return StatusDraft
	}
	if counts[DestinationPublished] > 0 {
		return StatusPublished
	}
	return StatusCancelled
}

func claimable(
	state QueueState,
	next time.Time,
	lockedUntil *time.Time,
	now time.Time,
) bool {
	if next.After(now) {
		return false
	}
	return state == QueuePending || state == QueueRetry ||
		state == QueueSending && lockedUntil != nil && !lockedUntil.After(now)
}
