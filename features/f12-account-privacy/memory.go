package accountprivacy

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"
)

// MemoryRepository is a deterministic development/test adapter. Production
// adapters must preserve the same transition checks in database transactions.
type MemoryRepository struct {
	mu          sync.Mutex
	profiles    map[string]Profile
	providers   map[string]map[string]Provider
	workspaces  map[string][]WorkspaceRef
	exports     map[string]ExportRequest
	deletions   map[string]DeletionRequest
	auditEvents []AuditEvent
	auditSeq    uint64
}

func NewMemoryRepository() *MemoryRepository {
	return &MemoryRepository{
		profiles:   make(map[string]Profile),
		providers:  make(map[string]map[string]Provider),
		workspaces: make(map[string][]WorkspaceRef),
		exports:    make(map[string]ExportRequest),
		deletions:  make(map[string]DeletionRequest),
	}
}

func (repository *MemoryRepository) PutProfile(profile Profile) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	repository.profiles[profile.AccountID] = profile
}

func (repository *MemoryRepository) PutProviders(accountID string, providers []Provider) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	indexed := make(map[string]Provider, len(providers))
	for _, provider := range providers {
		indexed[provider.ID] = provider
	}
	repository.providers[accountID] = indexed
}

func (repository *MemoryRepository) PutWorkspaces(accountID string, workspaces []WorkspaceRef) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	repository.workspaces[accountID] = append([]WorkspaceRef(nil), workspaces...)
}

func (repository *MemoryRepository) Profile(_ context.Context, accountID string) (Profile, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	profile, found := repository.profiles[accountID]
	if !found {
		return Profile{}, ErrNotFound
	}
	return profile, nil
}

func (repository *MemoryRepository) UpdateProfile(
	_ context.Context,
	command ProfileUpdateCommand,
) (Profile, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	profile, found := repository.profiles[command.AccountID]
	if !found {
		return Profile{}, ErrNotFound
	}
	profile.DisplayName = command.DisplayName
	profile.Locale = command.Locale
	profile.Timezone = command.Timezone
	profile.UpdatedAt = command.Now
	repository.profiles[command.AccountID] = profile
	repository.auditLocked(command.AccountID, command.AccountID, "profile.updated", "success", command.Now)
	return profile, nil
}

func (repository *MemoryRepository) Providers(
	_ context.Context,
	accountID string,
) ([]Provider, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	indexed, found := repository.providers[accountID]
	if !found {
		return []Provider{}, nil
	}
	providers := make([]Provider, 0, len(indexed))
	for _, provider := range indexed {
		providers = append(providers, provider)
	}
	sort.Slice(providers, func(left, right int) bool {
		return providers[left].ID < providers[right].ID
	})
	return providers, nil
}

func (repository *MemoryRepository) Provider(
	_ context.Context,
	accountID, providerID string,
) (Provider, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	provider, found := repository.providers[accountID][providerID]
	if !found {
		return Provider{}, ErrNotFound
	}
	return provider, nil
}

func (repository *MemoryRepository) Workspaces(
	_ context.Context,
	accountID string,
) ([]WorkspaceRef, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	return append([]WorkspaceRef(nil), repository.workspaces[accountID]...), nil
}

func (repository *MemoryRepository) CreateExport(
	_ context.Context,
	request ExportRequest,
) error {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	if _, found := repository.exports[request.ID]; found {
		return ErrConflict
	}
	repository.exports[request.ID] = request
	repository.auditLocked(request.AccountID, request.ID, "export.requested", "queued", request.RequestedAt)
	return nil
}

func (repository *MemoryRepository) Export(
	_ context.Context,
	requestID string,
) (ExportRequest, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	request, found := repository.exports[requestID]
	if !found {
		return ExportRequest{}, ErrNotFound
	}
	return request, nil
}

func (repository *MemoryRepository) ActiveExport(
	_ context.Context,
	accountID string,
	scope ExportScope,
	workspaceID string,
) (ExportRequest, bool, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	var (
		active ExportRequest
		found  bool
	)
	for _, request := range repository.exports {
		if request.AccountID != accountID || request.Scope != scope ||
			request.WorkspaceID != workspaceID {
			continue
		}
		if request.Status != ExportQueued && request.Status != ExportReady {
			continue
		}
		if !found || request.RequestedAt.After(active.RequestedAt) {
			active = request
			found = true
		}
	}
	return active, found, nil
}

func (repository *MemoryRepository) MarkExportReady(
	_ context.Context,
	command ExportReadyCommand,
) error {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	request, found := repository.exports[command.RequestID]
	if !found {
		return ErrNotFound
	}
	if request.Status != ExportQueued {
		return ErrConflict
	}
	request.Status = ExportReady
	request.ObjectKey = command.ObjectKey
	request.SHA256 = command.SHA256
	request.SizeBytes = command.SizeBytes
	request.ReadyAt = timePointer(command.ReadyAt)
	repository.exports[command.RequestID] = request
	repository.auditLocked(request.AccountID, request.ID, "export.ready", "success", command.ReadyAt)
	return nil
}

func (repository *MemoryRepository) MarkExportFailed(
	_ context.Context,
	requestID string,
	now time.Time,
) error {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	request, found := repository.exports[requestID]
	if !found {
		return ErrNotFound
	}
	request.Status = ExportFailed
	repository.exports[requestID] = request
	repository.auditLocked(request.AccountID, request.ID, "export.failed", "failed", now)
	return nil
}

func (repository *MemoryRepository) ExpiredExports(
	_ context.Context,
	now time.Time,
	limit int,
) ([]ExportRequest, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	ids := make([]string, 0)
	for id, request := range repository.exports {
		if request.Status == ExportReady && !request.ExpiresAt.After(now) {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	if len(ids) > limit {
		ids = ids[:limit]
	}
	requests := make([]ExportRequest, 0, len(ids))
	for _, id := range ids {
		requests = append(requests, repository.exports[id])
	}
	return requests, nil
}

func (repository *MemoryRepository) MarkExportExpired(
	_ context.Context,
	requestID string,
	now time.Time,
) error {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	request, found := repository.exports[requestID]
	if !found {
		return ErrNotFound
	}
	if (request.Status != ExportReady && request.Status != ExportQueued) ||
		request.ExpiresAt.After(now) {
		return ErrConflict
	}
	request.Status = ExportExpired
	request.ObjectKey = ""
	request.SHA256 = ""
	request.SizeBytes = 0
	repository.exports[requestID] = request
	repository.auditLocked(request.AccountID, request.ID, "export.expired", "success", now)
	return nil
}

func (repository *MemoryRepository) CreateDeletion(
	_ context.Context,
	request DeletionRequest,
) error {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	for _, existing := range repository.deletions {
		if deletionTarget(existing) == deletionTarget(request) && activeDeletion(existing.Status) {
			return ErrConflict
		}
	}
	repository.deletions[request.ID] = cloneDeletion(request)
	repository.auditLocked(
		request.AccountID,
		request.ID,
		"deletion.requested",
		"deactivating",
		request.RequestedAt,
	)
	return nil
}

func (repository *MemoryRepository) MarkGracePeriod(
	_ context.Context,
	requestID string,
	now time.Time,
) error {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	request, found := repository.deletions[requestID]
	if !found {
		return ErrNotFound
	}
	if request.Status != DeletionDeactivating {
		return ErrConflict
	}
	request.Status = DeletionGracePeriod
	request.FailureCode = ""
	repository.deletions[requestID] = request
	repository.auditLocked(request.AccountID, request.ID, "deletion.deactivated", "success", now)
	return nil
}

func (repository *MemoryRepository) MarkDeactivationFailed(
	_ context.Context,
	requestID, code string,
	now time.Time,
) error {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	request, found := repository.deletions[requestID]
	if !found {
		return ErrNotFound
	}
	request.Status = DeletionDeactivationFailed
	request.FailureCode = code
	repository.deletions[requestID] = request
	repository.auditLocked(request.AccountID, request.ID, "deletion.deactivated", "failed", now)
	return nil
}

func (repository *MemoryRepository) Deletion(
	_ context.Context,
	requestID string,
) (DeletionRequest, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	request, found := repository.deletions[requestID]
	if !found {
		return DeletionRequest{}, ErrNotFound
	}
	return cloneDeletion(request), nil
}

func (repository *MemoryRepository) CancelDeletion(
	_ context.Context,
	requestID string,
	now time.Time,
) error {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	request, found := repository.deletions[requestID]
	if !found {
		return ErrNotFound
	}
	if request.Status != DeletionGracePeriod || !now.Before(request.GraceEndsAt) {
		return ErrDeletionInactive
	}
	request.Status = DeletionCancelled
	repository.deletions[requestID] = request
	repository.auditLocked(request.AccountID, request.ID, "deletion.cancelled", "success", now)
	return nil
}

func (repository *MemoryRepository) ClaimDueDeletions(
	_ context.Context,
	now time.Time,
	limit int,
) ([]DeletionRequest, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	ids := make([]string, 0, len(repository.deletions))
	for id, request := range repository.deletions {
		if (request.Status == DeletionGracePeriod ||
			request.Status == DeletionFinalizationFailed) &&
			!request.GraceEndsAt.After(now) {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	if len(ids) > limit {
		ids = ids[:limit]
	}
	requests := make([]DeletionRequest, 0, len(ids))
	for _, id := range ids {
		request := repository.deletions[id]
		request.Status = DeletionFinalizing
		request.FailureCode = ""
		repository.deletions[id] = request
		requests = append(requests, cloneDeletion(request))
	}
	return requests, nil
}

func (repository *MemoryRepository) CompleteDeletion(
	_ context.Context,
	command DeletionCompleteCommand,
) error {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	request, found := repository.deletions[command.RequestID]
	if !found {
		return ErrNotFound
	}
	if request.Status != DeletionFinalizing {
		return ErrConflict
	}
	request.Status = DeletionCompleted
	request.CompletedAt = timePointer(command.CompletedAt)
	request.TombstoneID = command.TombstoneID
	request.TombstoneExpiresAt = timePointer(command.TombstoneExpiresAt)
	repository.deletions[command.RequestID] = request
	repository.auditLocked(request.AccountID, request.ID, "deletion.completed", "success", command.CompletedAt)
	return nil
}

func (repository *MemoryRepository) MarkFinalizationFailed(
	_ context.Context,
	requestID, code string,
	now time.Time,
) error {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	request, found := repository.deletions[requestID]
	if !found {
		return ErrNotFound
	}
	request.Status = DeletionFinalizationFailed
	request.FailureCode = code
	repository.deletions[requestID] = request
	repository.auditLocked(request.AccountID, request.ID, "deletion.completed", "failed", now)
	return nil
}

func (repository *MemoryRepository) AuditEvents() []AuditEvent {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	events := make([]AuditEvent, len(repository.auditEvents))
	copy(events, repository.auditEvents)
	return events
}

func (repository *MemoryRepository) auditLocked(
	accountID, targetID, eventType, outcome string,
	occurredAt time.Time,
) {
	repository.auditSeq++
	repository.auditEvents = append(repository.auditEvents, AuditEvent{
		ID:         fmt.Sprintf("audit-%d", repository.auditSeq),
		AccountID:  accountID,
		TargetID:   targetID,
		Type:       eventType,
		Outcome:    outcome,
		OccurredAt: occurredAt,
	})
}

func activeDeletion(status DeletionStatus) bool {
	switch status {
	case DeletionDeactivating, DeletionGracePeriod, DeletionFinalizing,
		DeletionDeactivationFailed, DeletionFinalizationFailed:
		return true
	default:
		return false
	}
}

func deletionTarget(request DeletionRequest) string {
	if request.Scope == DeleteWorkspace {
		return string(request.Scope) + ":" + request.WorkspaceID
	}
	return string(request.Scope) + ":" + request.AccountID
}

func cloneDeletion(request DeletionRequest) DeletionRequest {
	request.Ownership.Actions = append([]OwnershipAction(nil), request.Ownership.Actions...)
	return request
}
