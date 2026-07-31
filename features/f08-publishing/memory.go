package publishing

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"sync"
	"time"
)

// MemoryStore is intended for unit tests and local development. Production
// uses PostgresStore, whose claims and idempotency constraints are persistent.
type MemoryStore struct {
	mutex        sync.Mutex
	jobs         map[string]Job
	commandJobs  map[string]string
	destinations map[string]Destination
	idempotency  map[string]string
	tidLast      map[string]uint64
	tidAllocated map[string]map[string]uint64
	order        []string
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		jobs:         make(map[string]Job),
		commandJobs:  make(map[string]string),
		destinations: make(map[string]Destination),
		idempotency:  make(map[string]string),
		tidLast:      make(map[string]uint64),
		tidAllocated: make(map[string]map[string]uint64),
	}
}

func (store *MemoryStore) AllocateMonotonicTID(
	_ context.Context,
	namespace, idempotencyKey string,
	physicalMicroseconds int64,
) (uint64, error) {
	store.mutex.Lock()
	defer store.mutex.Unlock()
	floor, err := monotonicTIDFloor(
		namespace, idempotencyKey, physicalMicroseconds,
	)
	if err != nil {
		return 0, err
	}
	allocated := store.tidAllocated[namespace]
	if allocated == nil {
		allocated = make(map[string]uint64)
		store.tidAllocated[namespace] = allocated
	}
	if existing, exists := allocated[idempotencyKey]; exists {
		return existing, nil
	}
	value := floor
	if last, exists := store.tidLast[namespace]; exists && value <= last {
		if last == uint64(^uint64(0)>>1) {
			return 0, ErrInvalidArgument
		}
		value = last + 1
	}
	allocated[idempotencyKey] = value
	store.tidLast[namespace] = value
	return value, nil
}

func (store *MemoryStore) Enqueue(
	_ context.Context,
	job Job,
) (EnqueueResult, error) {
	store.mutex.Lock()
	defer store.mutex.Unlock()
	if existingID, exists := store.commandJobs[job.CommandID]; exists {
		existing := store.jobs[existingID]
		if !store.sameSnapshotLocked(existingID, job.Destinations) {
			return EnqueueResult{}, ErrConflict
		}
		return EnqueueResult{
			JobID:   existing.ID,
			Created: false,
			Status:  existing.Status,
		}, nil
	}
	if _, exists := store.jobs[job.ID]; exists {
		return EnqueueResult{}, ErrConflict
	}
	if err := validateJob(job); err != nil {
		return EnqueueResult{}, err
	}
	for _, destination := range job.Destinations {
		if _, exists := store.destinations[destination.ID]; exists {
			return EnqueueResult{}, ErrConflict
		}
		if _, exists := store.idempotency[destination.IdempotencyKey]; exists {
			return EnqueueResult{}, ErrConflict
		}
	}
	stored := cloneJob(job)
	stored.Destinations = nil
	store.jobs[job.ID] = stored
	store.commandJobs[job.CommandID] = job.ID
	for _, destination := range job.Destinations {
		store.destinations[destination.ID] = cloneDestination(destination)
		store.idempotency[destination.IdempotencyKey] = destination.ID
		store.order = append(store.order, destination.ID)
	}
	return EnqueueResult{JobID: job.ID, Created: true, Status: job.Status}, nil
}

func (store *MemoryStore) ClaimDue(
	_ context.Context,
	now, lockedUntil time.Time,
	leaseToken string,
) (Destination, bool, error) {
	store.mutex.Lock()
	defer store.mutex.Unlock()
	if leaseToken == "" || !lockedUntil.After(now) {
		return Destination{}, false, ErrInvalidArgument
	}
	for _, id := range store.order {
		destination := store.destinations[id]
		claimable := destination.Status == DestinationPending ||
			destination.Status == DestinationRetryWait ||
			destination.Status == DestinationPublishing &&
				destination.LockedUntil != nil &&
				!destination.LockedUntil.After(now)
		if !claimable || destination.NextAttemptAt.After(now) {
			continue
		}
		if destination.Status == DestinationPublishing {
			destination.NeedsReconciliation = true
		}
		destination.Status = DestinationPublishing
		destination.AttemptCount++
		destination.CycleAttemptCount++
		destination.LeaseToken = leaseToken
		lock := lockedUntil
		destination.LockedUntil = &lock
		store.destinations[id] = cloneDestination(destination)
		store.refreshJobLocked(destination.JobID, now)
		return cloneDestination(destination), true, nil
	}
	return Destination{}, false, nil
}

func (store *MemoryStore) MarkCancelled(
	_ context.Context,
	id, leaseToken string,
	diagnostic Diagnostic,
	now time.Time,
) error {
	store.mutex.Lock()
	defer store.mutex.Unlock()
	destination, err := store.claimedLocked(id, leaseToken)
	if err != nil {
		return err
	}
	destination.Status = DestinationCancelled
	destination.LastDiagnostic = diagnostic
	destination.LeaseToken = ""
	destination.LockedUntil = nil
	cancelledAt := now
	destination.CancelledAt = &cancelledAt
	store.destinations[id] = destination
	store.refreshJobLocked(destination.JobID, now)
	return nil
}

func (store *MemoryStore) MarkPublished(
	_ context.Context,
	id, leaseToken string,
	result PublishResult,
	now time.Time,
) error {
	store.mutex.Lock()
	defer store.mutex.Unlock()
	destination, err := store.claimedLocked(id, leaseToken)
	if err != nil || strings.TrimSpace(result.RemoteID) == "" {
		if err != nil {
			return err
		}
		return ErrInvalidArgument
	}
	for otherID, other := range store.destinations {
		if otherID != id &&
			other.Provider == destination.Provider &&
			other.ConnectionID == destination.ConnectionID &&
			other.RemoteID == result.RemoteID {
			return ErrConflict
		}
	}
	destination.Status = DestinationPublished
	destination.RemoteID = strings.TrimSpace(result.RemoteID)
	destination.Permalink = strings.TrimSpace(result.Permalink)
	destination.Checkpoint = append([]byte(nil), result.Checkpoint...)
	destination.NeedsReconciliation = false
	destination.LastDiagnostic = Diagnostic{}
	destination.LeaseToken = ""
	destination.LockedUntil = nil
	publishedAt := now
	destination.PublishedAt = &publishedAt
	store.destinations[id] = destination
	store.refreshJobLocked(destination.JobID, now)
	return nil
}

func (store *MemoryStore) MarkProgress(
	_ context.Context,
	id, leaseToken string,
	checkpoint json.RawMessage,
	next time.Time,
	now time.Time,
) error {
	store.mutex.Lock()
	defer store.mutex.Unlock()
	destination, err := store.claimedLocked(id, leaseToken)
	if err != nil {
		return err
	}
	if !jsonValidObject(checkpoint) || next.IsZero() || now.IsZero() {
		return ErrInvalidArgument
	}
	destination.Status = DestinationRetryWait
	destination.Checkpoint = append([]byte(nil), checkpoint...)
	destination.NextAttemptAt = next
	destination.NeedsReconciliation = false
	if destination.CycleAttemptCount > 0 {
		destination.CycleAttemptCount--
	}
	destination.LeaseToken = ""
	destination.LockedUntil = nil
	store.destinations[id] = destination
	store.refreshJobLocked(destination.JobID, now)
	return nil
}

func (store *MemoryStore) MarkNotified(
	_ context.Context,
	id, leaseToken, notificationID string,
	now time.Time,
) error {
	store.mutex.Lock()
	defer store.mutex.Unlock()
	destination, err := store.claimedLocked(id, leaseToken)
	if err != nil {
		return err
	}
	if strings.TrimSpace(notificationID) == "" {
		return ErrInvalidArgument
	}
	destination.Status = DestinationNotified
	destination.NotificationID = strings.TrimSpace(notificationID)
	destination.LastDiagnostic = Diagnostic{}
	destination.LeaseToken = ""
	destination.LockedUntil = nil
	publishedAt := now
	destination.PublishedAt = &publishedAt
	store.destinations[id] = destination
	store.refreshJobLocked(destination.JobID, now)
	return nil
}

func (store *MemoryStore) MarkRetry(
	_ context.Context,
	id, leaseToken string,
	diagnostic Diagnostic,
	next time.Time,
) error {
	store.mutex.Lock()
	defer store.mutex.Unlock()
	destination, err := store.claimedLocked(id, leaseToken)
	if err != nil {
		return err
	}
	if !next.After(diagnostic.At) {
		return ErrInvalidArgument
	}
	destination.Status = DestinationRetryWait
	destination.LastDiagnostic = diagnostic
	destination.NextAttemptAt = next
	destination.NeedsReconciliation = diagnostic.Ambiguous
	destination.LeaseToken = ""
	destination.LockedUntil = nil
	store.destinations[id] = destination
	store.refreshJobLocked(destination.JobID, diagnostic.At)
	return nil
}

func (store *MemoryStore) MarkDeadLetter(
	_ context.Context,
	id, leaseToken string,
	diagnostic Diagnostic,
	now time.Time,
) error {
	store.mutex.Lock()
	defer store.mutex.Unlock()
	destination, err := store.claimedLocked(id, leaseToken)
	if err != nil {
		return err
	}
	destination.Status = DestinationDeadLetter
	destination.LastDiagnostic = diagnostic
	destination.NeedsReconciliation =
		destination.NeedsReconciliation || diagnostic.Ambiguous
	destination.LeaseToken = ""
	destination.LockedUntil = nil
	deadLetteredAt := now
	destination.DeadLetteredAt = &deadLetteredAt
	store.destinations[id] = destination
	store.refreshJobLocked(destination.JobID, now)
	return nil
}

func (store *MemoryStore) RetryDeadLetter(
	_ context.Context,
	workspaceID, destinationID, _ string,
	now time.Time,
) (Destination, error) {
	store.mutex.Lock()
	defer store.mutex.Unlock()
	destination, exists := store.destinations[destinationID]
	if !exists || destination.WorkspaceID != workspaceID {
		return Destination{}, ErrNotFound
	}
	if destination.Status != DestinationDeadLetter {
		return Destination{}, ErrConflict
	}
	destination.Status = DestinationRetryWait
	destination.CycleAttemptCount = 0
	destination.NextAttemptAt = now
	destination.DeadLetteredAt = nil
	destination.ManualRetryCount++
	store.destinations[destinationID] = destination
	store.refreshJobLocked(destination.JobID, now)
	return cloneDestination(destination), nil
}

func (store *MemoryStore) GetJob(
	_ context.Context,
	workspaceID, jobID string,
) (Job, error) {
	store.mutex.Lock()
	defer store.mutex.Unlock()
	job, exists := store.jobs[jobID]
	if !exists || job.WorkspaceID != workspaceID {
		return Job{}, ErrNotFound
	}
	result := cloneJob(job)
	for _, destinationID := range store.order {
		destination := store.destinations[destinationID]
		if destination.JobID == jobID {
			result.Destinations = append(
				result.Destinations,
				cloneDestination(destination),
			)
		}
	}
	return result, nil
}

func (store *MemoryStore) claimedLocked(
	id, leaseToken string,
) (Destination, error) {
	destination, exists := store.destinations[id]
	if !exists {
		return Destination{}, ErrNotFound
	}
	if destination.Status != DestinationPublishing ||
		leaseToken == "" ||
		destination.LeaseToken != leaseToken {
		return Destination{}, ErrConflict
	}
	return destination, nil
}

func (store *MemoryStore) refreshJobLocked(jobID string, now time.Time) {
	job := store.jobs[jobID]
	var pending, publishing, published, deadLetter, cancelled int
	for _, destination := range store.destinations {
		if destination.JobID != jobID {
			continue
		}
		switch destination.Status {
		case DestinationPending, DestinationRetryWait:
			pending++
		case DestinationPublishing:
			publishing++
		case DestinationPublished:
			published++
		case DestinationNotified:
			published++
		case DestinationDeadLetter:
			deadLetter++
		case DestinationCancelled:
			cancelled++
		}
	}
	switch {
	case pending > 0:
		job.Status = JobQueued
	case publishing > 0:
		job.Status = JobPublishing
	case published > 0 && deadLetter+cancelled > 0:
		job.Status = JobPartiallyFailed
	case published > 0:
		job.Status = JobPublished
	case deadLetter > 0:
		job.Status = JobFailed
	default:
		job.Status = JobCancelled
	}
	job.UpdatedAt = now
	store.jobs[jobID] = job
}

func validateJob(job Job) error {
	if job.ID == "" ||
		job.CommandID == "" ||
		job.WorkspaceID == "" ||
		job.PostID == "" ||
		job.DraftID == "" ||
		job.Generation < 1 ||
		job.InvalidationKey == "" ||
		job.Status != JobQueued ||
		job.ExecuteAtUTC.IsZero() ||
		job.CreatedAt.IsZero() ||
		job.UpdatedAt.IsZero() ||
		len(job.Destinations) == 0 {
		return ErrInvalidArgument
	}
	idempotencyKeys := make(map[string]struct{}, len(job.Destinations))
	destinationIDs := make(map[string]struct{}, len(job.Destinations))
	for _, destination := range job.Destinations {
		if destination.ID == "" ||
			destination.JobID != job.ID ||
			destination.CommandID != job.CommandID ||
			destination.WorkspaceID != job.WorkspaceID ||
			destination.PostID != job.PostID ||
			destination.Generation != job.Generation ||
			destination.DraftRevision < 1 ||
			destination.ChannelID == "" ||
			destination.Provider == "" ||
			(destination.Mode == PublishingModeAuto && destination.ConnectionID == "") ||
			(destination.Mode != PublishingModeAuto &&
				destination.Mode != PublishingModeNotification) ||
			destination.CapabilityID == "" ||
			destination.Capabilities.Version == "" ||
			destination.Capabilities.Mode != destination.Mode ||
			len(destination.SnapshotHash) != 64 ||
			destination.IdempotencyKey == "" ||
			destination.Status != DestinationPending ||
			destination.AttemptCount != 0 ||
			destination.CycleAttemptCount != 0 ||
			destination.MaxAttempts < 1 ||
			destination.NextAttemptAt.IsZero() {
			return ErrInvalidArgument
		}
		if _, duplicate := idempotencyKeys[destination.IdempotencyKey]; duplicate {
			return ErrInvalidArgument
		}
		if _, duplicate := destinationIDs[destination.ID]; duplicate {
			return ErrInvalidArgument
		}
		idempotencyKeys[destination.IdempotencyKey] = struct{}{}
		destinationIDs[destination.ID] = struct{}{}
	}
	return nil
}

func cloneJob(source Job) Job {
	source.Destinations = slices.Clone(source.Destinations)
	for index := range source.Destinations {
		source.Destinations[index] = cloneDestination(source.Destinations[index])
	}
	return source
}

func cloneDestination(source Destination) Destination {
	source.Payload = append([]byte(nil), source.Payload...)
	source.Checkpoint = append([]byte(nil), source.Checkpoint...)
	if source.LockedUntil != nil {
		value := *source.LockedUntil
		source.LockedUntil = &value
	}
	if source.PublishedAt != nil {
		value := *source.PublishedAt
		source.PublishedAt = &value
	}
	if source.DeadLetteredAt != nil {
		value := *source.DeadLetteredAt
		source.DeadLetteredAt = &value
	}
	if source.CancelledAt != nil {
		value := *source.CancelledAt
		source.CancelledAt = &value
	}
	return source
}

func (store *MemoryStore) sameSnapshotLocked(
	jobID string,
	destinations []Destination,
) bool {
	stored := make(map[string]string)
	for _, destination := range store.destinations {
		if destination.JobID == jobID {
			stored[destination.ChannelID] = destination.SnapshotHash
		}
	}
	if len(stored) != len(destinations) {
		return false
	}
	for _, destination := range destinations {
		if stored[destination.ChannelID] != destination.SnapshotHash {
			return false
		}
	}
	return true
}

var _ Store = (*MemoryStore)(nil)
