package integrations

import (
	"context"
	"sync"
)

// MemoryIdempotencyStore is a bounded-lifetime development and test adapter.
// Production uses the unique database key and transaction described by the
// owned migration.
type MemoryIdempotencyStore struct {
	clock   Clock
	entries map[string]memoryIdempotencyEntry
	mu      sync.Mutex
}

type memoryIdempotencyEntry struct {
	fingerprint [32]byte
	response    StoredResponse
	expiresUnix int64
}

func NewMemoryIdempotencyStore(clock Clock) *MemoryIdempotencyStore {
	if clock == nil {
		clock = systemClock
	}
	return &MemoryIdempotencyStore{
		clock:   clock,
		entries: make(map[string]memoryIdempotencyEntry),
	}
}

func (store *MemoryIdempotencyStore) Execute(
	ctx context.Context,
	request IdempotencyRequest,
	operation func(context.Context) (StoredResponse, error),
) (StoredResponse, bool, error) {
	key := request.WorkspaceID + "\x00" + request.CredentialID + "\x00" +
		request.Operation + "\x00" + request.Key

	store.mu.Lock()
	defer store.mu.Unlock()
	now := store.clock()
	for storedKey, entry := range store.entries {
		if entry.expiresUnix <= now.Unix() {
			delete(store.entries, storedKey)
		}
	}
	if entry, exists := store.entries[key]; exists {
		if entry.fingerprint != request.Fingerprint {
			return StoredResponse{}, false, ErrIdempotencyConflict
		}
		return cloneStoredResponse(entry.response), true, nil
	}
	response, err := operation(ctx)
	if err != nil {
		return StoredResponse{}, false, err
	}
	store.entries[key] = memoryIdempotencyEntry{
		fingerprint: request.Fingerprint,
		response:    cloneStoredResponse(response),
		expiresUnix: request.ExpiresAt.Unix(),
	}
	return response, false, nil
}

func cloneStoredResponse(response StoredResponse) StoredResponse {
	return StoredResponse{
		Status: response.Status,
		Header: response.Header.Clone(),
		Body:   append([]byte(nil), response.Body...),
	}
}
