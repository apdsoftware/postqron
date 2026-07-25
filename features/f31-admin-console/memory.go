package adminconsole

import (
	"context"
	"errors"
	"strings"
	"sync"
)

type MemoryDirectory struct {
	mu       sync.RWMutex
	accounts map[string]string
	admins   map[string]bool
}

func NewMemoryDirectory(accounts map[string]string) *MemoryDirectory {
	cloned := make(map[string]string, len(accounts))
	for accountID, email := range accounts {
		cloned[accountID] = email
	}
	return &MemoryDirectory{
		accounts: cloned,
		admins:   make(map[string]bool),
	}
}

func (store *MemoryDirectory) AccountIDByEmail(
	_ context.Context,
	email string,
) (string, bool, error) {
	store.mu.RLock()
	defer store.mu.RUnlock()
	for accountID, candidate := range store.accounts {
		if strings.EqualFold(strings.TrimSpace(candidate), strings.TrimSpace(email)) {
			return accountID, true, nil
		}
	}
	return "", false, nil
}

func (store *MemoryDirectory) Admin(
	_ context.Context,
	accountID string,
) (AdminRecord, bool, error) {
	store.mu.RLock()
	defer store.mu.RUnlock()
	email, exists := store.accounts[accountID]
	if !exists {
		return AdminRecord{}, false, nil
	}
	return AdminRecord{
		AccountID: accountID,
		Email:     email,
		Active:    store.admins[accountID],
	}, true, nil
}

func (store *MemoryDirectory) ListAdmins(
	context.Context,
) ([]AdminRecord, error) {
	store.mu.RLock()
	defer store.mu.RUnlock()
	records := make([]AdminRecord, 0, len(store.admins))
	for accountID, active := range store.admins {
		records = append(records, AdminRecord{
			AccountID: accountID,
			Email:     store.accounts[accountID],
			Active:    active,
		})
	}
	return records, nil
}

func (store *MemoryDirectory) SetAdmin(
	_ context.Context,
	accountID string,
	enabled bool,
) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	if _, exists := store.accounts[accountID]; !exists {
		return errors.New("account does not exist")
	}
	store.admins[accountID] = enabled
	return nil
}

type MemoryAudit struct {
	mu     sync.RWMutex
	events []AuditEvent
}

func (store *MemoryAudit) Append(_ context.Context, event AuditEvent) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.events = append(store.events, event)
	return nil
}

func (store *MemoryAudit) Events() []AuditEvent {
	store.mu.RLock()
	defer store.mu.RUnlock()
	return append([]AuditEvent(nil), store.events...)
}

type memoryIdempotencyEntry struct {
	result MutationResult
	err    error
}

type MemoryIdempotencyStore struct {
	mu      sync.Mutex
	entries map[string]memoryIdempotencyEntry
}

func NewMemoryIdempotencyStore() *MemoryIdempotencyStore {
	return &MemoryIdempotencyStore{
		entries: make(map[string]memoryIdempotencyEntry),
	}
}

func (store *MemoryIdempotencyStore) Do(
	ctx context.Context,
	key string,
	action func() (MutationResult, error),
) (MutationResult, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	if entry, exists := store.entries[key]; exists {
		return entry.result, entry.err
	}
	if err := ctx.Err(); err != nil {
		return MutationResult{}, err
	}
	result, err := action()
	if err == nil {
		store.entries[key] = memoryIdempotencyEntry{result: result}
	}
	return result, err
}

type MemoryAuthenticator struct {
	mu       sync.RWMutex
	sessions map[string]Session
}

func NewMemoryAuthenticator(sessions map[string]Session) *MemoryAuthenticator {
	cloned := make(map[string]Session, len(sessions))
	for token, session := range sessions {
		cloned[token] = session
	}
	return &MemoryAuthenticator{sessions: cloned}
}

func (authenticator *MemoryAuthenticator) Session(
	_ context.Context,
	token string,
) (Session, error) {
	authenticator.mu.RLock()
	defer authenticator.mu.RUnlock()
	session, exists := authenticator.sessions[token]
	if !exists {
		return Session{}, ErrUnauthenticated
	}
	return session, nil
}
