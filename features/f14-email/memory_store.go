package email

import (
	"context"
	"errors"
	"slices"
	"sync"
	"time"
)

// MemoryStore is intended for tests and local development. Production uses the
// SQL schema in migrations with atomic SKIP LOCKED claims.
type MemoryStore struct {
	mu          sync.Mutex
	deliveries  map[string]Delivery
	idempotency map[string]string
	providerIDs map[string]string
	order       []string
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		deliveries:  make(map[string]Delivery),
		idempotency: make(map[string]string),
		providerIDs: make(map[string]string),
	}
}

func (store *MemoryStore) Enqueue(
	_ context.Context,
	delivery Delivery,
) (EnqueueResult, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	key := string(delivery.Message.Channel) + ":" + delivery.Message.IdempotencyKey
	if existingID, exists := store.idempotency[key]; exists {
		existing := store.deliveries[existingID]
		return EnqueueResult{
			ID:      existingID,
			Created: false,
			State:   existing.State,
		}, nil
	}
	if _, exists := store.deliveries[delivery.Message.ID]; exists {
		return EnqueueResult{}, errors.New("message ID already exists")
	}
	store.deliveries[delivery.Message.ID] = cloneDelivery(delivery)
	store.idempotency[key] = delivery.Message.ID
	store.order = append(store.order, delivery.Message.ID)
	return EnqueueResult{
		ID:      delivery.Message.ID,
		Created: true,
		State:   delivery.State,
	}, nil
}

func (store *MemoryStore) ClaimDue(
	_ context.Context,
	now time.Time,
) (Delivery, bool, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	for _, id := range store.order {
		delivery := store.deliveries[id]
		if delivery.State != StatePending && delivery.State != StateRetry {
			continue
		}
		if delivery.NextAttemptAt.After(now) {
			continue
		}
		delivery.State = StateSending
		delivery.Attempt++
		store.deliveries[id] = delivery
		return cloneDelivery(delivery), true, nil
	}
	return Delivery{}, false, nil
}

func (store *MemoryStore) MarkAccepted(
	_ context.Context,
	id, providerID string,
	_ time.Time,
) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	delivery, ok := store.deliveries[id]
	if !ok || delivery.State != StateSending || providerID == "" {
		return errors.New("delivery is not claimable as accepted")
	}
	if existing, used := store.providerIDs[providerID]; used && existing != id {
		return errors.New("provider message ID already belongs to another delivery")
	}
	delivery.State = StateAccepted
	delivery.ProviderMessageID = providerID
	store.deliveries[id] = delivery
	store.providerIDs[providerID] = id
	return nil
}

func (store *MemoryStore) MarkRetry(
	_ context.Context,
	id string,
	diagnostic Diagnostic,
	next time.Time,
) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	delivery, ok := store.deliveries[id]
	if !ok || delivery.State != StateSending || next.IsZero() {
		return errors.New("delivery is not claimable for retry")
	}
	delivery.State = StateRetry
	delivery.LastDiagnostic = diagnostic
	delivery.NextAttemptAt = next
	store.deliveries[id] = delivery
	return nil
}

func (store *MemoryStore) MarkFailed(
	_ context.Context,
	id string,
	diagnostic Diagnostic,
) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	delivery, ok := store.deliveries[id]
	if !ok || delivery.State != StateSending {
		return errors.New("delivery is not claimable as failed")
	}
	delivery.State = StateFailed
	delivery.LastDiagnostic = diagnostic
	store.deliveries[id] = delivery
	return nil
}

func (store *MemoryStore) Delivery(id string) (Delivery, bool) {
	store.mu.Lock()
	defer store.mu.Unlock()
	delivery, exists := store.deliveries[id]
	return cloneDelivery(delivery), exists
}

func cloneDelivery(source Delivery) Delivery {
	return source
}

func (store *MemoryStore) IDs() []string {
	store.mu.Lock()
	defer store.mu.Unlock()
	return slices.Clone(store.order)
}
