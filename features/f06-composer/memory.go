package composer

import (
	"context"
	"sort"
	"sync"
	"time"
)

type MemoryRepository struct {
	mutex      sync.RWMutex
	drafts     map[string]Draft
	revisions  map[string][]DraftRevision
	duplicates map[string]duplicateOperation
}

func NewMemoryRepository() *MemoryRepository {
	return &MemoryRepository{
		drafts:     make(map[string]Draft),
		revisions:  make(map[string][]DraftRevision),
		duplicates: make(map[string]duplicateOperation),
	}
}

func draftKey(workspaceID, draftID string) string {
	return workspaceID + "\x00" + draftID
}

func duplicateKey(workspaceID, idempotencyKey string) string {
	return workspaceID + "\x00" + idempotencyKey
}

func (repository *MemoryRepository) Create(_ context.Context, draft Draft) (Draft, error) {
	repository.mutex.Lock()
	defer repository.mutex.Unlock()
	key := draftKey(draft.WorkspaceID, draft.ID)
	if _, exists := repository.drafts[key]; exists {
		return Draft{}, ErrConflict
	}
	repository.drafts[key] = cloneDraft(draft)
	repository.revisions[key] = []DraftRevision{revisionOf(draft, "")}
	return cloneDraft(draft), nil
}

func (repository *MemoryRepository) Get(
	_ context.Context,
	workspaceID, draftID string,
) (Draft, error) {
	repository.mutex.RLock()
	defer repository.mutex.RUnlock()
	draft, exists := repository.drafts[draftKey(workspaceID, draftID)]
	if !exists {
		return Draft{}, ErrNotFound
	}
	return cloneDraft(draft), nil
}

func (repository *MemoryRepository) List(
	_ context.Context,
	workspaceID string,
) ([]Draft, error) {
	repository.mutex.RLock()
	defer repository.mutex.RUnlock()
	drafts := make([]Draft, 0)
	for _, draft := range repository.drafts {
		if draft.WorkspaceID == workspaceID {
			drafts = append(drafts, cloneDraft(draft))
		}
	}
	sort.Slice(drafts, func(left, right int) bool {
		if drafts[left].UpdatedAt.Equal(drafts[right].UpdatedAt) {
			return drafts[left].ID < drafts[right].ID
		}
		return drafts[left].UpdatedAt.After(drafts[right].UpdatedAt)
	})
	return drafts, nil
}

func (repository *MemoryRepository) Update(
	_ context.Context,
	draft Draft,
	expectedRevision int64,
	autosaveKey string,
) (Draft, error) {
	repository.mutex.Lock()
	defer repository.mutex.Unlock()
	key := draftKey(draft.WorkspaceID, draft.ID)
	current, exists := repository.drafts[key]
	if !exists {
		return Draft{}, ErrNotFound
	}
	if autosaveKey != "" {
		for _, revision := range repository.revisions[key] {
			if revision.AutosaveKey == autosaveKey {
				replayed := current
				replayed.Content = cloneContent(revision.Content)
				replayed.Revision = revision.Revision
				replayed.UpdatedAt = revision.SavedAt
				return cloneDraft(replayed), nil
			}
		}
	}
	if current.Revision != expectedRevision {
		return Draft{}, ErrConflict
	}
	draft.Revision = current.Revision + 1
	draft.CreatedAt = current.CreatedAt
	draft.CreatedBy = current.CreatedBy
	repository.drafts[key] = cloneDraft(draft)
	repository.revisions[key] = append(
		repository.revisions[key],
		revisionOf(draft, autosaveKey),
	)
	return cloneDraft(draft), nil
}

func (repository *MemoryRepository) Delete(
	_ context.Context,
	workspaceID, draftID string,
	expectedRevision int64,
) error {
	repository.mutex.Lock()
	defer repository.mutex.Unlock()
	key := draftKey(workspaceID, draftID)
	current, exists := repository.drafts[key]
	if !exists {
		return ErrNotFound
	}
	if current.Revision != expectedRevision {
		return ErrConflict
	}
	delete(repository.drafts, key)
	delete(repository.revisions, key)
	return nil
}

func (repository *MemoryRepository) ListRevisions(
	_ context.Context,
	workspaceID, draftID string,
) ([]DraftRevision, error) {
	repository.mutex.RLock()
	defer repository.mutex.RUnlock()
	key := draftKey(workspaceID, draftID)
	if _, exists := repository.drafts[key]; !exists {
		return nil, ErrNotFound
	}
	stored := repository.revisions[key]
	revisions := make([]DraftRevision, len(stored))
	for index, revision := range stored {
		revisions[index] = revision
		revisions[index].Content = cloneContent(revision.Content)
	}
	sort.Slice(revisions, func(left, right int) bool {
		return revisions[left].Revision > revisions[right].Revision
	})
	return revisions, nil
}

func (repository *MemoryRepository) GetRevision(
	_ context.Context,
	workspaceID, draftID string,
	revision int64,
) (DraftRevision, error) {
	repository.mutex.RLock()
	defer repository.mutex.RUnlock()
	key := draftKey(workspaceID, draftID)
	if _, exists := repository.drafts[key]; !exists {
		return DraftRevision{}, ErrNotFound
	}
	for _, item := range repository.revisions[key] {
		if item.Revision == revision {
			result := item
			result.Content = cloneContent(item.Content)
			return result, nil
		}
	}
	return DraftRevision{}, ErrConflict
}

func (repository *MemoryRepository) ReserveDuplicateOperation(
	_ context.Context,
	operation duplicateOperation,
	now time.Time,
) (duplicateOperation, bool, error) {
	repository.mutex.Lock()
	defer repository.mutex.Unlock()
	key := duplicateKey(operation.WorkspaceID, operation.IdempotencyKey)
	stored, exists := repository.duplicates[key]
	if !exists {
		operation.Status = duplicateOperationPending
		operation.LeaseGeneration = 1
		operation.LockedUntil = now.UTC().Add(duplicateOperationLease)
		operation.CreatedAt = now.UTC()
		operation.UpdatedAt = now.UTC()
		repository.duplicates[key] = operation
		return operation, false, nil
	}
	if stored.SourceDraftID != operation.SourceDraftID ||
		stored.SourceRevision != operation.SourceRevision ||
		stored.CreatedByAccount != operation.CreatedByAccount {
		return duplicateOperation{}, false, ErrConflict
	}
	if stored.Status == duplicateOperationCompleted {
		return stored, true, nil
	}
	if stored.LockedUntil.After(now.UTC()) {
		return duplicateOperation{}, false, ErrConflict
	}
	stored.LeaseGeneration++
	stored.LockedUntil = now.UTC().Add(duplicateOperationLease)
	stored.UpdatedAt = now.UTC()
	repository.duplicates[key] = stored
	return stored, false, nil
}

func (repository *MemoryRepository) CompleteDuplicateOperation(
	_ context.Context,
	operation duplicateOperation,
	cloneDraftID string,
	cloneDraftRevision int64,
	completedAt time.Time,
) error {
	repository.mutex.Lock()
	defer repository.mutex.Unlock()
	key := duplicateKey(operation.WorkspaceID, operation.IdempotencyKey)
	stored, exists := repository.duplicates[key]
	if !exists || stored.Status != duplicateOperationPending ||
		stored.LeaseGeneration != operation.LeaseGeneration {
		return ErrConflict
	}
	stored.Status = duplicateOperationCompleted
	stored.CloneDraftID = cloneDraftID
	stored.CloneDraftRevision = cloneDraftRevision
	stored.LockedUntil = time.Time{}
	stored.UpdatedAt = completedAt.UTC()
	repository.duplicates[key] = stored
	return nil
}

func (repository *MemoryRepository) AbandonDuplicateOperation(
	_ context.Context,
	operation duplicateOperation,
) (bool, error) {
	repository.mutex.Lock()
	defer repository.mutex.Unlock()
	key := duplicateKey(operation.WorkspaceID, operation.IdempotencyKey)
	stored, exists := repository.duplicates[key]
	if !exists {
		return false, nil
	}
	if stored.Status != duplicateOperationPending ||
		stored.LeaseGeneration != operation.LeaseGeneration {
		return false, nil
	}
	delete(repository.duplicates, key)
	return true, nil
}

func (repository *MemoryRepository) ResetDanglingCompletedDuplicateOperation(
	_ context.Context,
	operation duplicateOperation,
	now time.Time,
) (duplicateOperation, bool, error) {
	repository.mutex.Lock()
	defer repository.mutex.Unlock()
	key := duplicateKey(operation.WorkspaceID, operation.IdempotencyKey)
	stored, exists := repository.duplicates[key]
	if !exists {
		return duplicateOperation{}, false, nil
	}
	if stored.Status != duplicateOperationCompleted ||
		stored.LeaseGeneration != operation.LeaseGeneration ||
		stored.CloneDraftID != operation.CloneDraftID ||
		stored.CloneDraftRevision != operation.CloneDraftRevision {
		return duplicateOperation{}, false, nil
	}
	stored.Status = duplicateOperationPending
	stored.CloneDraftID = ""
	stored.CloneDraftRevision = 0
	stored.LeaseGeneration++
	stored.LockedUntil = now.UTC().Add(duplicateOperationLease)
	stored.UpdatedAt = now.UTC()
	repository.duplicates[key] = stored
	return stored, true, nil
}

func revisionOf(draft Draft, autosaveKey string) DraftRevision {
	return DraftRevision{
		DraftID:     draft.ID,
		Revision:    draft.Revision,
		Content:     cloneContent(draft.Content),
		AutosaveKey: autosaveKey,
		SavedAt:     draft.UpdatedAt,
	}
}
