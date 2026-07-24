package composer

import (
	"context"
	"sort"
	"sync"
)

type MemoryRepository struct {
	mutex  sync.RWMutex
	drafts map[string]Draft
}

func NewMemoryRepository() *MemoryRepository {
	return &MemoryRepository{drafts: make(map[string]Draft)}
}

func draftKey(workspaceID, draftID string) string {
	return workspaceID + "\x00" + draftID
}

func (repository *MemoryRepository) Create(_ context.Context, draft Draft) (Draft, error) {
	repository.mutex.Lock()
	defer repository.mutex.Unlock()
	key := draftKey(draft.WorkspaceID, draft.ID)
	if _, exists := repository.drafts[key]; exists {
		return Draft{}, ErrConflict
	}
	repository.drafts[key] = cloneDraft(draft)
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
) (Draft, error) {
	repository.mutex.Lock()
	defer repository.mutex.Unlock()
	key := draftKey(draft.WorkspaceID, draft.ID)
	current, exists := repository.drafts[key]
	if !exists {
		return Draft{}, ErrNotFound
	}
	if current.Revision != expectedRevision {
		return Draft{}, ErrConflict
	}
	draft.Revision = current.Revision + 1
	draft.CreatedAt = current.CreatedAt
	draft.CreatedBy = current.CreatedBy
	repository.drafts[key] = cloneDraft(draft)
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
	return nil
}
