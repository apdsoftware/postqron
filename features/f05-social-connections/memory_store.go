package socialconnections

import (
	"context"
	"slices"
	"sync"
	"time"
)

type memorySelection struct {
	StoredSelection
	selected map[string]bool
}

type MemoryRepository struct {
	mu             sync.Mutex
	attempts       map[string]OAuthAttempt
	attemptByState map[string]string
	selections     map[string]memorySelection
	connections    map[string]StoredCredential
	connectionKey  map[string]string
	events         []Event
}

func NewMemoryRepository() *MemoryRepository {
	return &MemoryRepository{
		attempts:       make(map[string]OAuthAttempt),
		attemptByState: make(map[string]string),
		selections:     make(map[string]memorySelection),
		connections:    make(map[string]StoredCredential),
		connectionKey:  make(map[string]string),
	}
}

func (repository *MemoryRepository) CreateAttempt(
	_ context.Context,
	attempt OAuthAttempt,
) error {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	if _, exists := repository.attempts[attempt.ID]; exists {
		return ErrInvalidState
	}
	if _, exists := repository.attemptByState[attempt.StateHash]; exists {
		return ErrInvalidState
	}
	repository.attempts[attempt.ID] = cloneAttempt(attempt)
	repository.attemptByState[attempt.StateHash] = attempt.ID
	return nil
}

func (repository *MemoryRepository) ConsumeAttempt(
	_ context.Context,
	stateHash string,
	now time.Time,
) (OAuthAttempt, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	id, exists := repository.attemptByState[stateHash]
	if !exists {
		return OAuthAttempt{}, ErrInvalidState
	}
	attempt := repository.attempts[id]
	if attempt.ConsumedAt != nil {
		return OAuthAttempt{}, ErrInvalidState
	}
	if !now.Before(attempt.ExpiresAt) {
		return OAuthAttempt{}, ErrFlowExpired
	}
	attempt.ConsumedAt = cloneTimePointer(&now)
	repository.attempts[id] = attempt
	return cloneAttempt(attempt), nil
}

func (repository *MemoryRepository) SaveSelection(
	_ context.Context,
	selection StoredSelection,
) error {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	if _, exists := repository.selections[selection.ID]; exists {
		return ErrInvalidState
	}
	repository.selections[selection.ID] = memorySelection{
		StoredSelection: cloneSelection(selection),
		selected:        make(map[string]bool),
	}
	return nil
}

func (repository *MemoryRepository) Connect(
	_ context.Context,
	command ConnectCommand,
) (Connection, bool, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	selection, exists := repository.selections[command.SelectionID]
	if !exists ||
		selection.WorkspaceID != command.WorkspaceID ||
		selection.ActorID != command.ActorID {
		return Connection{}, false, ErrResourceNotFound
	}
	if !command.Now.Before(selection.ExpiresAt) {
		return Connection{}, false, ErrFlowExpired
	}
	var selected StoredResource
	found := false
	for _, resource := range selection.Resources {
		if resource.Candidate.RemoteID == command.RemoteID {
			selected = cloneStoredResource(resource)
			found = true
			break
		}
	}
	if !found || selection.selected[command.RemoteID] {
		return Connection{}, false, ErrResourceNotFound
	}
	key := uniqueConnectionKey(
		command.WorkspaceID,
		selection.Provider,
		command.RemoteID,
	)
	if existingID, ok := repository.connectionKey[key]; ok {
		existing := repository.connections[existingID]
		if existing.Status == StatusConnected {
			return Connection{}, false, ErrResourceAlreadyUsed
		}
		existing.ResourceType = selected.Candidate.ResourceType
		existing.AccountType = selected.Candidate.AccountType
		existing.DisplayName = selected.Candidate.DisplayName
		existing.Handle = selected.Candidate.Handle
		existing.PictureURL = selected.Candidate.PictureURL
		existing.Scopes = append([]string(nil), selected.Candidate.Scopes...)
		existing.Status = StatusConnected
		existing.ReconnectReason = ""
		existing.TokenExpiresAt = cloneTimePointer(selected.TokenExpiresAt)
		existing.LastVerifiedAt = cloneTimePointer(&command.Now)
		existing.ConnectedByActorID = command.ActorID
		existing.UpdatedAt = command.Now
		existing.RevokedAt = nil
		existing.AccessTokenCiphertext = cloneCiphertext(selected.AccessTokenCiphertext)
		existing.RefreshTokenCiphertext = cloneCiphertext(selected.RefreshTokenCiphertext)
		existing.RefreshLockedUntil = nil
		repository.connections[existingID] = existing
		selection.selected[command.RemoteID] = true
		repository.selections[command.SelectionID] = selection
		event := command.Event
		event.Type = EventReconnected
		event.ConnectionID = existing.ID
		event.Provider = existing.Provider
		event.RemoteID = existing.RemoteID
		event.OccurredAt = command.Now
		repository.events = append(repository.events, event)
		return cloneConnection(existing.Connection), true, nil
	}

	connection := StoredCredential{
		Connection: Connection{
			ID:                 command.NewConnectionID,
			WorkspaceID:        command.WorkspaceID,
			Provider:           selection.Provider,
			RemoteID:           selected.Candidate.RemoteID,
			ResourceType:       selected.Candidate.ResourceType,
			AccountType:        selected.Candidate.AccountType,
			DisplayName:        selected.Candidate.DisplayName,
			Handle:             selected.Candidate.Handle,
			PictureURL:         selected.Candidate.PictureURL,
			Scopes:             append([]string(nil), selected.Candidate.Scopes...),
			Status:             StatusConnected,
			TokenExpiresAt:     cloneTimePointer(selected.TokenExpiresAt),
			LastVerifiedAt:     cloneTimePointer(&command.Now),
			ConnectedByActorID: command.ActorID,
			CreatedAt:          command.Now,
			UpdatedAt:          command.Now,
		},
		AccessTokenCiphertext:  cloneCiphertext(selected.AccessTokenCiphertext),
		RefreshTokenCiphertext: cloneCiphertext(selected.RefreshTokenCiphertext),
	}
	repository.connections[connection.ID] = connection
	repository.connectionKey[key] = connection.ID
	selection.selected[command.RemoteID] = true
	repository.selections[command.SelectionID] = selection
	event := command.Event
	event.ConnectionID = connection.ID
	event.Provider = connection.Provider
	event.RemoteID = connection.RemoteID
	event.OccurredAt = command.Now
	repository.events = append(repository.events, event)
	return cloneConnection(connection.Connection), false, nil
}

func (repository *MemoryRepository) ListConnections(
	_ context.Context,
	workspaceID string,
) ([]Connection, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	var connections []Connection
	for _, stored := range repository.connections {
		if stored.WorkspaceID == workspaceID {
			connections = append(connections, cloneConnection(stored.Connection))
		}
	}
	slices.SortFunc(connections, func(left, right Connection) int {
		if left.Provider < right.Provider {
			return -1
		}
		if left.Provider > right.Provider {
			return 1
		}
		if left.DisplayName < right.DisplayName {
			return -1
		}
		if left.DisplayName > right.DisplayName {
			return 1
		}
		return 0
	})
	return connections, nil
}

func (repository *MemoryRepository) GetCredential(
	_ context.Context,
	workspaceID, connectionID string,
) (StoredCredential, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	stored, exists := repository.connections[connectionID]
	if !exists || stored.WorkspaceID != workspaceID {
		return StoredCredential{}, ErrResourceNotFound
	}
	return cloneStoredCredential(stored), nil
}

func (repository *MemoryRepository) ClaimRefresh(
	_ context.Context,
	workspaceID, connectionID string,
	now time.Time,
	refreshAt time.Time,
	lockTTL time.Duration,
) (StoredCredential, bool, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	stored, exists := repository.connections[connectionID]
	if !exists || stored.WorkspaceID != workspaceID {
		return StoredCredential{}, false, ErrResourceNotFound
	}
	if stored.Status == StatusReconnectRequired {
		return StoredCredential{}, false, ErrReconnectRequired
	}
	if stored.Status == StatusRevoked {
		return StoredCredential{}, false, ErrConnectionRevoked
	}
	if stored.TokenExpiresAt == nil || stored.TokenExpiresAt.After(refreshAt) {
		return cloneStoredCredential(stored), false, nil
	}
	if stored.RefreshLockedUntil != nil && stored.RefreshLockedUntil.After(now) {
		return StoredCredential{}, false, ErrRefreshInProgress
	}
	lockedUntil := now.Add(lockTTL)
	stored.RefreshLockedUntil = &lockedUntil
	repository.connections[connectionID] = stored
	return cloneStoredCredential(stored), true, nil
}

func (repository *MemoryRepository) CompleteRefresh(
	_ context.Context,
	command RefreshCommand,
) (Connection, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	stored, exists := repository.connections[command.ConnectionID]
	if !exists {
		return Connection{}, ErrResourceNotFound
	}
	if stored.Status != StatusConnected || stored.RefreshLockedUntil == nil {
		return Connection{}, ErrRefreshInProgress
	}
	stored.AccessTokenCiphertext = cloneCiphertext(command.AccessTokenCiphertext)
	stored.RefreshTokenCiphertext = cloneCiphertext(command.RefreshTokenCiphertext)
	stored.Scopes = append([]string(nil), command.Scopes...)
	stored.TokenExpiresAt = cloneTimePointer(command.ExpiresAt)
	stored.LastVerifiedAt = cloneTimePointer(&command.VerifiedAt)
	stored.RefreshLockedUntil = nil
	stored.UpdatedAt = command.Now
	repository.connections[stored.ID] = stored
	repository.events = append(repository.events, command.Event)
	return cloneConnection(stored.Connection), nil
}

func (repository *MemoryRepository) ReleaseRefresh(
	_ context.Context,
	workspaceID, connectionID string,
) error {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	stored, exists := repository.connections[connectionID]
	if !exists || stored.WorkspaceID != workspaceID {
		return ErrResourceNotFound
	}
	stored.RefreshLockedUntil = nil
	repository.connections[connectionID] = stored
	return nil
}

func (repository *MemoryRepository) MarkReconnectRequired(
	_ context.Context,
	workspaceID, connectionID, reason string,
	now time.Time,
	event Event,
) (Connection, bool, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	stored, exists := repository.connections[connectionID]
	if !exists || stored.WorkspaceID != workspaceID {
		return Connection{}, false, ErrResourceNotFound
	}
	if stored.Status == StatusRevoked {
		return Connection{}, false, ErrConnectionRevoked
	}
	if stored.Status == StatusReconnectRequired {
		return cloneConnection(stored.Connection), false, nil
	}
	stored.Status = StatusReconnectRequired
	stored.ReconnectReason = reason
	stored.AccessTokenCiphertext = Ciphertext{}
	stored.RefreshTokenCiphertext = Ciphertext{}
	stored.TokenExpiresAt = nil
	stored.RefreshLockedUntil = nil
	stored.UpdatedAt = now
	repository.connections[connectionID] = stored
	repository.events = append(repository.events, event)
	return cloneConnection(stored.Connection), true, nil
}

func (repository *MemoryRepository) Revoke(
	_ context.Context,
	workspaceID, connectionID string,
	now time.Time,
	event Event,
) (Connection, bool, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	stored, exists := repository.connections[connectionID]
	if !exists || stored.WorkspaceID != workspaceID {
		return Connection{}, false, ErrResourceNotFound
	}
	if stored.Status == StatusRevoked {
		return cloneConnection(stored.Connection), false, nil
	}
	stored.Status = StatusRevoked
	stored.ReconnectReason = ""
	stored.AccessTokenCiphertext = Ciphertext{}
	stored.RefreshTokenCiphertext = Ciphertext{}
	stored.TokenExpiresAt = nil
	stored.RefreshLockedUntil = nil
	stored.RevokedAt = cloneTimePointer(&now)
	stored.UpdatedAt = now
	repository.connections[connectionID] = stored
	repository.events = append(repository.events, event)
	return cloneConnection(stored.Connection), true, nil
}

func (repository *MemoryRepository) Events() []Event {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	return append([]Event(nil), repository.events...)
}

func uniqueConnectionKey(
	workspaceID string,
	provider Provider,
	remoteID string,
) string {
	return workspaceID + "\x00" + string(provider) + "\x00" + remoteID
}

func cloneAttempt(attempt OAuthAttempt) OAuthAttempt {
	attempt.PKCEVerifierCiphertext = cloneCiphertext(attempt.PKCEVerifierCiphertext)
	attempt.ConsumedAt = cloneTimePointer(attempt.ConsumedAt)
	return attempt
}

func cloneSelection(selection StoredSelection) StoredSelection {
	resources := selection.Resources
	selection.Resources = make([]StoredResource, len(resources))
	for index, resource := range resources {
		selection.Resources[index] = cloneStoredResource(resource)
	}
	return selection
}

func cloneStoredResource(resource StoredResource) StoredResource {
	resource.Candidate = cloneCandidate(resource.Candidate)
	resource.AccessTokenCiphertext = cloneCiphertext(resource.AccessTokenCiphertext)
	resource.RefreshTokenCiphertext = cloneCiphertext(resource.RefreshTokenCiphertext)
	resource.TokenExpiresAt = cloneTimePointer(resource.TokenExpiresAt)
	return resource
}

func cloneConnection(connection Connection) Connection {
	connection.Scopes = append([]string(nil), connection.Scopes...)
	connection.TokenExpiresAt = cloneTimePointer(connection.TokenExpiresAt)
	connection.LastVerifiedAt = cloneTimePointer(connection.LastVerifiedAt)
	connection.RevokedAt = cloneTimePointer(connection.RevokedAt)
	return connection
}

func cloneStoredCredential(stored StoredCredential) StoredCredential {
	stored.Connection = cloneConnection(stored.Connection)
	stored.AccessTokenCiphertext = cloneCiphertext(stored.AccessTokenCiphertext)
	stored.RefreshTokenCiphertext = cloneCiphertext(stored.RefreshTokenCiphertext)
	stored.RefreshLockedUntil = cloneTimePointer(stored.RefreshLockedUntil)
	return stored
}

func cloneCiphertext(ciphertext Ciphertext) Ciphertext {
	ciphertext.Data = append([]byte(nil), ciphertext.Data...)
	return ciphertext
}
