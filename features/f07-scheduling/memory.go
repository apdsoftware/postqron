package scheduling

import (
	"context"
	"sort"
	"sync"
	"time"
)

type MemoryRepository struct {
	mutex      sync.RWMutex
	posts      map[string]ScheduledPost
	commands   map[string]PublicationCommand
	operations map[string]IdempotencyOperation
}

func NewMemoryRepository() *MemoryRepository {
	return &MemoryRepository{
		posts:      make(map[string]ScheduledPost),
		commands:   make(map[string]PublicationCommand),
		operations: make(map[string]IdempotencyOperation),
	}
}

func operationKey(workspaceID string, kind OperationKind, idempotencyKey string) string {
	return workspaceID + "\x00" + string(kind) + "\x00" + idempotencyKey
}

func (repository *MemoryRepository) ReserveOperation(
	_ context.Context,
	candidate IdempotencyOperation,
	now time.Time,
) (IdempotencyOperation, error) {
	repository.mutex.Lock()
	defer repository.mutex.Unlock()
	if candidate.ResponseSnapshotStatus == "" {
		candidate.ResponseSnapshotStatus = ResponseSnapshotPending
	}
	if candidate.Kind == OperationDuplicate && candidate.DownstreamIdempotencyKey == "" {
		candidate.DownstreamIdempotencyKey = deriveComposerDuplicateIdempotencyKey(candidate)
	}
	key := operationKey(candidate.WorkspaceID, candidate.Kind, candidate.IdempotencyKey)
	stored, exists := repository.operations[key]
	if exists {
		if stored.PayloadFingerprint != candidate.PayloadFingerprint {
			return IdempotencyOperation{}, ErrIdempotencyMismatch
		}
		if stored.State == OperationCompleted {
			return cloneOperation(stored), nil
		}
		if stored.LockedUntil.After(now) {
			return IdempotencyOperation{}, ErrOperationInProgress
		}
		stored.LeaseGeneration++
		stored.LockedUntil = now.Add(operationLease)
		stored.UpdatedAt = now
		repository.operations[key] = cloneOperation(stored)
		return cloneOperation(stored), nil
	}
	if candidate.WorkspaceID == "" || candidate.IdempotencyKey == "" ||
		candidate.PayloadFingerprint == "" ||
		candidate.PostID == "" || candidate.PublicationCommandID == "" {
		return IdempotencyOperation{}, ErrInvalidArgument
	}
	if (candidate.Kind == OperationSchedule && candidate.DownstreamIdempotencyKey != "") ||
		(candidate.Kind == OperationDuplicate && candidate.DownstreamIdempotencyKey == "") ||
		candidate.ResponseSnapshotStatus != ResponseSnapshotPending {
		return IdempotencyOperation{}, ErrInvalidArgument
	}
	candidate.State = OperationReserved
	candidate.LeaseGeneration = 1
	candidate.LockedUntil = now.Add(operationLease)
	candidate.CreatedAt = now
	candidate.UpdatedAt = now
	repository.operations[key] = cloneOperation(candidate)
	return cloneOperation(candidate), nil
}

func (repository *MemoryRepository) ReleaseOperation(
	_ context.Context,
	operation IdempotencyOperation,
	now time.Time,
) error {
	repository.mutex.Lock()
	defer repository.mutex.Unlock()
	key := operationKey(operation.WorkspaceID, operation.Kind, operation.IdempotencyKey)
	stored, exists := repository.operations[key]
	if !exists || stored.State == OperationCompleted ||
		stored.LeaseGeneration != operation.LeaseGeneration {
		return nil
	}
	stored.LockedUntil = now
	stored.UpdatedAt = now
	repository.operations[key] = cloneOperation(stored)
	return nil
}

func (repository *MemoryRepository) PrepareDuplicateOperation(
	_ context.Context,
	operation IdempotencyOperation,
	source ScheduledPost,
	schedule resolvedSchedule,
	now time.Time,
) (IdempotencyOperation, error) {
	repository.mutex.Lock()
	defer repository.mutex.Unlock()
	stored, err := repository.ownedOperationLocked(operation, OperationReserved)
	if err != nil {
		return IdempotencyOperation{}, err
	}
	current, exists := repository.posts[schedulingKey(operation.WorkspaceID, source.ID)]
	if !exists {
		return IdempotencyOperation{}, ErrNotFound
	}
	if current.Revision != source.Revision || current.Status != StatusScheduled {
		return IdempotencyOperation{}, ErrConflict
	}
	stored.State = OperationPrepared
	stored.SourcePostID = current.ID
	stored.SourcePostRevision = current.Revision
	stored.SourceDraftID = current.DraftID
	stored.SourceDraftRevision = current.DraftRevision
	stored.ChannelIDs = append([]string(nil), current.ChannelIDs...)
	stored.Schedule = schedule
	stored.UpdatedAt = now
	repository.operations[operationKey(stored.WorkspaceID, stored.Kind, stored.IdempotencyKey)] = cloneOperation(stored)
	return cloneOperation(stored), nil
}

func (repository *MemoryRepository) RecordDuplicateClone(
	_ context.Context,
	operation IdempotencyOperation,
	clone DuplicatedDraft,
	now time.Time,
) (IdempotencyOperation, error) {
	repository.mutex.Lock()
	defer repository.mutex.Unlock()
	stored, err := repository.ownedOperationLocked(operation, OperationPrepared)
	if err != nil {
		return IdempotencyOperation{}, err
	}
	stored.State = OperationCloneCreated
	stored.CloneDraftID = clone.DraftID
	stored.CloneDraftRevision = clone.DraftRevision
	stored.UpdatedAt = now
	repository.operations[operationKey(stored.WorkspaceID, stored.Kind, stored.IdempotencyKey)] = cloneOperation(stored)
	return cloneOperation(stored), nil
}

func (repository *MemoryRepository) CompleteScheduleOperation(
	_ context.Context,
	operation IdempotencyOperation,
	post ScheduledPost,
	command PublicationCommand,
	now time.Time,
) (ScheduledPost, error) {
	return repository.completeOperation(operation, OperationReserved, post, command, now)
}

func (repository *MemoryRepository) CompleteDuplicateOperation(
	_ context.Context,
	operation IdempotencyOperation,
	post ScheduledPost,
	command PublicationCommand,
	now time.Time,
) (ScheduledPost, error) {
	return repository.completeOperation(operation, OperationCloneCreated, post, command, now)
}

func (repository *MemoryRepository) completeOperation(
	operation IdempotencyOperation,
	wantState OperationState,
	post ScheduledPost,
	command PublicationCommand,
	now time.Time,
) (ScheduledPost, error) {
	repository.mutex.Lock()
	defer repository.mutex.Unlock()
	stored, err := repository.ownedOperationLocked(operation, wantState)
	if err != nil {
		return ScheduledPost{}, err
	}
	if post.ID != stored.PostID || command.ID != stored.PublicationCommandID {
		return ScheduledPost{}, ErrInvalidArgument
	}
	if wantState == OperationCloneCreated &&
		(post.DuplicatedFromPostID != stored.SourcePostID ||
			post.DraftID != stored.CloneDraftID ||
			post.DraftRevision != stored.CloneDraftRevision ||
			!equalStrings(post.ChannelIDs, stored.ChannelIDs) ||
			!post.ScheduledForUTC.Equal(stored.Schedule.utc)) {
		return ScheduledPost{}, ErrInvalidArgument
	}
	if wantState == OperationCloneCreated {
		source, exists := repository.posts[schedulingKey(stored.WorkspaceID, stored.SourcePostID)]
		if !exists || source.Revision != stored.SourcePostRevision ||
			source.Status != StatusScheduled {
			return ScheduledPost{}, ErrConflict
		}
	}
	if err := validateAtomicPair(post, command); err != nil {
		return ScheduledPost{}, err
	}
	postKey := schedulingKey(post.WorkspaceID, post.ID)
	commandKey := schedulingKey(command.WorkspaceID, command.ID)
	if _, exists := repository.posts[postKey]; exists {
		return ScheduledPost{}, ErrConflict
	}
	if _, exists := repository.commands[commandKey]; exists {
		return ScheduledPost{}, ErrConflict
	}
	repository.posts[postKey] = clonePost(post)
	repository.commands[commandKey] = clonePublicationCommand(command)
	stored.State = OperationCompleted
	stored.LockedUntil = time.Time{}
	stored.UpdatedAt = now
	completedAt := now
	stored.CompletedAt = &completedAt
	view := scheduledPostView(post)
	stored.ResponseSnapshotStatus = ResponseSnapshotAvailable
	stored.ResponseSnapshot = &view
	repository.operations[operationKey(stored.WorkspaceID, stored.Kind, stored.IdempotencyKey)] = cloneOperation(stored)
	return clonePost(post), nil
}

func (repository *MemoryRepository) ownedOperationLocked(
	operation IdempotencyOperation,
	wantState OperationState,
) (IdempotencyOperation, error) {
	stored, exists := repository.operations[operationKey(operation.WorkspaceID, operation.Kind, operation.IdempotencyKey)]
	if !exists || stored.State != wantState ||
		stored.LeaseGeneration != operation.LeaseGeneration {
		return IdempotencyOperation{}, ErrOperationInProgress
	}
	return stored, nil
}

func cloneOperation(operation IdempotencyOperation) IdempotencyOperation {
	operation.ChannelIDs = append([]string(nil), operation.ChannelIDs...)
	if operation.CompletedAt != nil {
		completedAt := *operation.CompletedAt
		operation.CompletedAt = &completedAt
	}
	if operation.ResponseSnapshot != nil {
		view := cloneScheduledPostView(*operation.ResponseSnapshot)
		operation.ResponseSnapshot = &view
	}
	return operation
}

func schedulingKey(workspaceID, id string) string {
	return workspaceID + "\x00" + id
}

func (repository *MemoryRepository) Create(
	_ context.Context,
	post ScheduledPost,
	command PublicationCommand,
) (ScheduledPost, error) {
	repository.mutex.Lock()
	defer repository.mutex.Unlock()
	if err := validateAtomicPair(post, command); err != nil {
		return ScheduledPost{}, err
	}
	postKey := schedulingKey(post.WorkspaceID, post.ID)
	commandKey := schedulingKey(command.WorkspaceID, command.ID)
	if _, exists := repository.posts[postKey]; exists {
		return ScheduledPost{}, ErrConflict
	}
	if _, exists := repository.commands[commandKey]; exists {
		return ScheduledPost{}, ErrConflict
	}
	repository.posts[postKey] = clonePost(post)
	repository.commands[commandKey] = clonePublicationCommand(command)
	return clonePost(post), nil
}

func (repository *MemoryRepository) Get(
	_ context.Context,
	workspaceID, postID string,
) (ScheduledPost, error) {
	repository.mutex.RLock()
	defer repository.mutex.RUnlock()
	post, exists := repository.posts[schedulingKey(workspaceID, postID)]
	if !exists {
		return ScheduledPost{}, ErrNotFound
	}
	return clonePost(post), nil
}

func (repository *MemoryRepository) List(
	_ context.Context,
	workspaceID string,
	filter CalendarFilter,
) ([]ScheduledPost, error) {
	repository.mutex.RLock()
	defer repository.mutex.RUnlock()
	posts := make([]ScheduledPost, 0)
	for _, post := range repository.posts {
		if post.WorkspaceID != workspaceID ||
			post.ScheduledForUTC.Before(filter.FromUTC) ||
			!post.ScheduledForUTC.Before(filter.UntilUTC) ||
			filter.Status != "" && post.Status != filter.Status ||
			filter.ChannelID != "" && !contains(post.ChannelIDs, filter.ChannelID) {
			continue
		}
		posts = append(posts, clonePost(post))
	}
	sort.Slice(posts, func(left, right int) bool {
		if posts[left].ScheduledForUTC.Equal(posts[right].ScheduledForUTC) {
			return posts[left].ID < posts[right].ID
		}
		return posts[left].ScheduledForUTC.Before(posts[right].ScheduledForUTC)
	})
	return posts, nil
}

func (repository *MemoryRepository) Replace(
	_ context.Context,
	replacement ScheduledPost,
	expectedRevision int64,
	command PublicationCommand,
) (ScheduledPost, error) {
	repository.mutex.Lock()
	defer repository.mutex.Unlock()
	if err := validateAtomicPair(replacement, command); err != nil {
		return ScheduledPost{}, err
	}
	key := schedulingKey(replacement.WorkspaceID, replacement.ID)
	current, exists := repository.posts[key]
	if !exists {
		return ScheduledPost{}, ErrNotFound
	}
	if current.Revision != expectedRevision {
		return ScheduledPost{}, ErrConflict
	}
	if current.Status != StatusScheduled {
		return ScheduledPost{}, ErrImmutable
	}
	if replacement.Revision != expectedRevision+1 ||
		!replacement.CreatedAt.Equal(current.CreatedAt) ||
		replacement.CreatedBy != current.CreatedBy ||
		replacement.DuplicatedFromPostID != current.DuplicatedFromPostID {
		return ScheduledPost{}, ErrInvalidArgument
	}
	commandKey := schedulingKey(command.WorkspaceID, command.ID)
	if _, exists := repository.commands[commandKey]; exists {
		return ScheduledPost{}, ErrConflict
	}
	if err := repository.invalidateLocked(
		current.WorkspaceID,
		current.ActiveCommandID,
		replacement.UpdatedAt,
	); err != nil {
		return ScheduledPost{}, err
	}
	repository.posts[key] = clonePost(replacement)
	repository.commands[commandKey] = clonePublicationCommand(command)
	return clonePost(replacement), nil
}

func (repository *MemoryRepository) Cancel(
	_ context.Context,
	workspaceID, postID string,
	expectedRevision int64,
	now time.Time,
) (ScheduledPost, error) {
	repository.mutex.Lock()
	defer repository.mutex.Unlock()
	key := schedulingKey(workspaceID, postID)
	current, exists := repository.posts[key]
	if !exists {
		return ScheduledPost{}, ErrNotFound
	}
	if current.Revision != expectedRevision {
		return ScheduledPost{}, ErrConflict
	}
	if current.Status != StatusScheduled {
		return ScheduledPost{}, ErrImmutable
	}
	if err := repository.invalidateLocked(workspaceID, current.ActiveCommandID, now); err != nil {
		return ScheduledPost{}, err
	}
	current.Status = StatusCancelled
	current.Revision++
	current.ActiveCommandID = ""
	current.UpdatedAt = now
	cancelledAt := now
	current.CancelledAt = &cancelledAt
	repository.posts[key] = clonePost(current)
	return clonePost(current), nil
}

func (repository *MemoryRepository) Duplicate(
	_ context.Context,
	workspaceID, sourcePostID string,
	expectedRevision int64,
	duplicate ScheduledPost,
	command PublicationCommand,
) (ScheduledPost, error) {
	repository.mutex.Lock()
	defer repository.mutex.Unlock()
	source, exists := repository.posts[schedulingKey(workspaceID, sourcePostID)]
	if !exists {
		return ScheduledPost{}, ErrNotFound
	}
	if source.Revision != expectedRevision {
		return ScheduledPost{}, ErrConflict
	}
	if source.Status != StatusScheduled {
		return ScheduledPost{}, ErrImmutable
	}
	if duplicate.DuplicatedFromPostID != source.ID {
		return ScheduledPost{}, ErrInvalidArgument
	}
	if err := validateAtomicPair(duplicate, command); err != nil {
		return ScheduledPost{}, err
	}
	postKey := schedulingKey(duplicate.WorkspaceID, duplicate.ID)
	commandKey := schedulingKey(command.WorkspaceID, command.ID)
	if _, exists := repository.posts[postKey]; exists {
		return ScheduledPost{}, ErrConflict
	}
	if _, exists := repository.commands[commandKey]; exists {
		return ScheduledPost{}, ErrConflict
	}
	repository.posts[postKey] = clonePost(duplicate)
	repository.commands[commandKey] = clonePublicationCommand(command)
	return clonePost(duplicate), nil
}

func (repository *MemoryRepository) GetPublicationCommand(
	_ context.Context,
	workspaceID, commandID string,
) (PublicationCommand, error) {
	repository.mutex.RLock()
	defer repository.mutex.RUnlock()
	command, exists := repository.commands[schedulingKey(workspaceID, commandID)]
	if !exists {
		return PublicationCommand{}, ErrNotFound
	}
	return clonePublicationCommand(command), nil
}

func (repository *MemoryRepository) ListPublicationCommands(
	_ context.Context,
	workspaceID, postID string,
) ([]PublicationCommand, error) {
	repository.mutex.RLock()
	defer repository.mutex.RUnlock()
	commands := make([]PublicationCommand, 0)
	for _, command := range repository.commands {
		if command.WorkspaceID == workspaceID && command.PostID == postID {
			commands = append(commands, clonePublicationCommand(command))
		}
	}
	sort.Slice(commands, func(left, right int) bool {
		return commands[left].Generation < commands[right].Generation
	})
	return commands, nil
}

func (repository *MemoryRepository) invalidateLocked(
	workspaceID, commandID string,
	now time.Time,
) error {
	key := schedulingKey(workspaceID, commandID)
	command, exists := repository.commands[key]
	if !exists || command.State != CommandPending {
		return ErrConflict
	}
	command.State = CommandInvalidated
	invalidatedAt := now
	command.InvalidatedAt = &invalidatedAt
	repository.commands[key] = command
	return nil
}

func validateAtomicPair(post ScheduledPost, command PublicationCommand) error {
	if post.ID == "" || post.WorkspaceID == "" || post.ActiveCommandID == "" ||
		post.Status != StatusScheduled || post.Revision < 1 || post.DraftRevision < 1 ||
		command.ID != post.ActiveCommandID ||
		command.WorkspaceID != post.WorkspaceID ||
		command.PostID != post.ID ||
		command.DraftID != post.DraftID ||
		command.DraftRevision != post.DraftRevision ||
		command.Generation != post.Revision ||
		command.State != CommandPending ||
		!command.ExecuteAtUTC.Equal(post.ScheduledForUTC) ||
		!equalStrings(command.ChannelIDs, post.ChannelIDs) {
		return ErrInvalidArgument
	}
	return nil
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
