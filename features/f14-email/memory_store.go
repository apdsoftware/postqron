package email

import (
	"context"
	"errors"
	"fmt"
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
		if existing.Message.SourceWorkspaceID !=
			delivery.Message.SourceWorkspaceID ||
			existing.Rendered.Recipient.ID != delivery.Rendered.Recipient.ID ||
			existing.Message.Template != delivery.Message.Template ||
			existing.Message.TemplateVersion !=
				delivery.Message.TemplateVersion {
			return EnqueueResult{}, errors.New(
				"email idempotency binding conflict",
			)
		}
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
		claimable := delivery.State == StatePending ||
			delivery.State == StateRetry ||
			(delivery.State == StateSending &&
				!delivery.LockedUntil.After(now) &&
				delivery.ProviderCallAt.IsZero())
		if !claimable || delivery.Attempt >= delivery.Message.MaxAttempts {
			continue
		}
		if delivery.NextAttemptAt.After(now) {
			continue
		}
		delivery.State = StateSending
		delivery.Attempt++
		delivery.LeaseToken = fmt.Sprintf(
			"memory-lease-%s-%d",
			id,
			delivery.Attempt,
		)
		delivery.LockedUntil = now.Add(2 * time.Minute)
		delivery.ProviderCallAt = time.Time{}
		store.deliveries[id] = delivery
		return cloneDelivery(delivery), true, nil
	}
	return Delivery{}, false, nil
}

func (store *MemoryStore) MarkProviderCallStarted(
	_ context.Context,
	id, leaseToken string,
	now time.Time,
) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	delivery, ok := store.deliveries[id]
	if !ok || delivery.State != StateSending ||
		delivery.LeaseToken != leaseToken || leaseToken == "" ||
		!delivery.LockedUntil.After(now) {
		return errors.New("delivery provider call lease was lost")
	}
	delivery.ProviderCallAt = now
	store.deliveries[id] = delivery
	return nil
}

func (store *MemoryStore) MarkAccepted(
	_ context.Context,
	id, leaseToken, providerID string,
	now time.Time,
) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	delivery, ok := store.deliveries[id]
	if !ok || delivery.State != StateSending ||
		delivery.LeaseToken != leaseToken || delivery.ProviderCallAt.IsZero() ||
		providerID == "" {
		return errors.New("delivery is not claimable as accepted")
	}
	if existing, used := store.providerIDs[providerID]; used && existing != id {
		return errors.New("provider message ID already belongs to another delivery")
	}
	delivery.State = StateAccepted
	delivery.ProviderMessageID = providerID
	delivery.LeaseToken = ""
	delivery.LockedUntil = time.Time{}
	delivery.ProviderCallAt = time.Time{}
	delivery.NextAttemptAt = now
	store.deliveries[id] = delivery
	store.providerIDs[providerID] = id
	return nil
}

func (store *MemoryStore) MarkRetry(
	_ context.Context,
	id, leaseToken string,
	diagnostic Diagnostic,
	next time.Time,
) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	delivery, ok := store.deliveries[id]
	if !ok || delivery.State != StateSending ||
		delivery.LeaseToken != leaseToken || delivery.ProviderCallAt.IsZero() ||
		next.IsZero() {
		return errors.New("delivery is not claimable for retry")
	}
	delivery.State = StateRetry
	delivery.LastDiagnostic = diagnostic
	delivery.NextAttemptAt = next
	delivery.LeaseToken = ""
	delivery.LockedUntil = time.Time{}
	delivery.ProviderCallAt = time.Time{}
	store.deliveries[id] = delivery
	return nil
}

func (store *MemoryStore) MarkFailed(
	_ context.Context,
	id, leaseToken string,
	diagnostic Diagnostic,
) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	delivery, ok := store.deliveries[id]
	if !ok || delivery.State != StateSending ||
		delivery.LeaseToken != leaseToken || delivery.ProviderCallAt.IsZero() {
		return errors.New("delivery is not claimable as failed")
	}
	delivery.State = StateFailed
	delivery.LastDiagnostic = diagnostic
	delivery.LeaseToken = ""
	delivery.LockedUntil = time.Time{}
	delivery.ProviderCallAt = time.Time{}
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
