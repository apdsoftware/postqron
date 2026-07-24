package medialibrary

import (
	"context"
	"sort"
	"strings"
	"sync"
	"time"
)

type MemoryRepository struct {
	mutex    sync.RWMutex
	uploads  map[string]Upload
	assets   map[string]Asset
	requests map[string]string
}

func NewMemoryRepository() *MemoryRepository {
	return &MemoryRepository{
		uploads:  make(map[string]Upload),
		assets:   make(map[string]Asset),
		requests: make(map[string]string),
	}
}

func mediaKey(workspaceID, id string) string { return workspaceID + "\x00" + id }

func (repository *MemoryRepository) CreateUpload(
	_ context.Context, upload Upload,
) (Upload, bool, error) {
	repository.mutex.Lock()
	defer repository.mutex.Unlock()
	requestKey := mediaKey(upload.WorkspaceID, upload.IdempotencyKey)
	if existingID, exists := repository.requests[requestKey]; exists {
		existing := repository.uploads[mediaKey(upload.WorkspaceID, existingID)]
		return existing, false, nil
	}
	key := mediaKey(upload.WorkspaceID, upload.ID)
	if _, exists := repository.uploads[key]; exists {
		return Upload{}, false, ErrConflict
	}
	repository.uploads[key] = upload
	repository.requests[requestKey] = upload.ID
	return upload, true, nil
}

func (repository *MemoryRepository) GetUpload(
	_ context.Context, workspaceID, uploadID string,
) (Upload, error) {
	repository.mutex.RLock()
	defer repository.mutex.RUnlock()
	upload, exists := repository.uploads[mediaKey(workspaceID, uploadID)]
	if !exists {
		return Upload{}, ErrNotFound
	}
	return upload, nil
}

func (repository *MemoryRepository) CancelUpload(
	_ context.Context, workspaceID, uploadID string,
) error {
	repository.mutex.Lock()
	defer repository.mutex.Unlock()
	key := mediaKey(workspaceID, uploadID)
	upload, exists := repository.uploads[key]
	if !exists {
		return ErrNotFound
	}
	if upload.Status == UploadCompleted {
		return ErrConflict
	}
	upload.Status = UploadCanceled
	repository.uploads[key] = upload
	return nil
}

func (repository *MemoryRepository) CompleteUpload(
	_ context.Context, upload Upload, asset Asset,
) (Asset, bool, error) {
	repository.mutex.Lock()
	defer repository.mutex.Unlock()
	uploadKey := mediaKey(upload.WorkspaceID, upload.ID)
	current, exists := repository.uploads[uploadKey]
	if !exists {
		return Asset{}, false, ErrNotFound
	}
	if current.Status == UploadCompleted {
		existing, found := repository.assets[mediaKey(upload.WorkspaceID, upload.AssetID)]
		if !found {
			return Asset{}, false, ErrConflict
		}
		return cloneAsset(existing), false, nil
	}
	if current.Status != UploadPending {
		return Asset{}, false, ErrConflict
	}
	assetKey := mediaKey(asset.WorkspaceID, asset.ID)
	if _, exists := repository.assets[assetKey]; exists {
		return Asset{}, false, ErrConflict
	}
	completedAt := asset.CreatedAt
	current.Status = UploadCompleted
	current.CompletedAt = &completedAt
	repository.uploads[uploadKey] = current
	repository.assets[assetKey] = cloneAsset(asset)
	return cloneAsset(asset), true, nil
}

func (repository *MemoryRepository) GetAsset(
	_ context.Context, workspaceID, assetID string,
) (Asset, error) {
	repository.mutex.RLock()
	defer repository.mutex.RUnlock()
	asset, exists := repository.assets[mediaKey(workspaceID, assetID)]
	if !exists {
		return Asset{}, ErrNotFound
	}
	return cloneAsset(asset), nil
}

func (repository *MemoryRepository) Search(
	_ context.Context, workspaceID string, query SearchQuery,
) ([]Asset, error) {
	repository.mutex.RLock()
	defer repository.mutex.RUnlock()
	assets := make([]Asset, 0)
	for _, asset := range repository.assets {
		if asset.WorkspaceID != workspaceID || asset.Status != StatusReady {
			continue
		}
		if query.Kind != "" && asset.Kind != query.Kind {
			continue
		}
		haystack := strings.ToLower(asset.OriginalName + " " + asset.AltText + " " + strings.Join(asset.Tags, " "))
		if query.Text != "" && !strings.Contains(haystack, query.Text) {
			continue
		}
		if !containsAllTags(asset.Tags, query.Tags) {
			continue
		}
		assets = append(assets, cloneAsset(asset))
	}
	sort.Slice(assets, func(left, right int) bool {
		if assets[left].UpdatedAt.Equal(assets[right].UpdatedAt) {
			return assets[left].ID < assets[right].ID
		}
		return assets[left].UpdatedAt.After(assets[right].UpdatedAt)
	})
	if len(assets) > query.Limit {
		assets = assets[:query.Limit]
	}
	return assets, nil
}

func (repository *MemoryRepository) UpdateMetadata(
	_ context.Context, asset Asset, expectedRevision int64,
) (Asset, error) {
	repository.mutex.Lock()
	defer repository.mutex.Unlock()
	key := mediaKey(asset.WorkspaceID, asset.ID)
	current, exists := repository.assets[key]
	if !exists {
		return Asset{}, ErrNotFound
	}
	if current.Revision != expectedRevision {
		return Asset{}, ErrConflict
	}
	asset.Revision = current.Revision + 1
	asset.CreatedAt = current.CreatedAt
	repository.assets[key] = cloneAsset(asset)
	return cloneAsset(asset), nil
}

func (repository *MemoryRepository) Archive(
	_ context.Context, workspaceID, assetID string, expectedRevision int64, now time.Time,
) (Asset, error) {
	repository.mutex.Lock()
	defer repository.mutex.Unlock()
	key := mediaKey(workspaceID, assetID)
	asset, exists := repository.assets[key]
	if !exists {
		return Asset{}, ErrNotFound
	}
	if asset.Revision != expectedRevision {
		return Asset{}, ErrConflict
	}
	if asset.Status != StatusReady {
		return Asset{}, ErrAssetArchived
	}
	asset.Status = StatusArchived
	asset.Revision++
	asset.UpdatedAt = now
	asset.ArchivedAt = &now
	repository.assets[key] = cloneAsset(asset)
	return cloneAsset(asset), nil
}

func (repository *MemoryRepository) MarkPurged(
	_ context.Context, workspaceID, assetID string, expectedRevision int64, now time.Time,
) error {
	repository.mutex.Lock()
	defer repository.mutex.Unlock()
	key := mediaKey(workspaceID, assetID)
	asset, exists := repository.assets[key]
	if !exists {
		return ErrNotFound
	}
	if asset.Revision != expectedRevision {
		return ErrConflict
	}
	if asset.Status != StatusArchived {
		return ErrConflict
	}
	asset.Status = StatusPurged
	asset.Revision++
	asset.UpdatedAt = now
	asset.PurgedAt = &now
	repository.assets[key] = cloneAsset(asset)
	return nil
}

func containsAllTags(assetTags, requested []string) bool {
	set := make(map[string]struct{}, len(assetTags))
	for _, tag := range assetTags {
		set[tag] = struct{}{}
	}
	for _, tag := range requested {
		if _, exists := set[tag]; !exists {
			return false
		}
	}
	return true
}

func cloneAsset(asset Asset) Asset {
	asset.Tags = append([]string(nil), asset.Tags...)
	return asset
}
