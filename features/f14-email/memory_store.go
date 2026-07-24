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
	mu           sync.Mutex
	deliveries   map[string]Delivery
	idempotency  map[string]string
	providerIDs  map[string]string
	events       map[string]ProviderEvent
	suppressions map[string]Suppression
	order        []string
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		deliveries:   make(map[string]Delivery),
		idempotency:  make(map[string]string),
		providerIDs:  make(map[string]string),
		events:       make(map[string]ProviderEvent),
		suppressions: make(map[string]Suppression),
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
	if store.isSuppressedLocked(
		delivery.Message.Recipient.ID,
		delivery.Message.Channel,
	) {
		return EnqueueResult{}, ErrSuppressed
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
		if store.isSuppressedLocked(delivery.Message.Recipient.ID, delivery.Message.Channel) {
			delivery.State = StateSuppressed
			store.deliveries[id] = delivery
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

func (store *MemoryStore) RecordProviderEvent(
	_ context.Context,
	event ProviderEvent,
) (bool, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	if _, exists := store.events[event.ID]; exists {
		return false, nil
	}
	deliveryID, exists := store.providerIDs[event.ProviderMessageID]
	if !exists {
		return false, errors.New("provider event references an unknown delivery")
	}
	delivery := store.deliveries[deliveryID]
	if delivery.Message.Recipient.ID != event.RecipientID {
		return false, errors.New("provider event recipient does not match delivery")
	}
	switch event.Type {
	case EventDelivered:
		delivery.State = StateDelivered
	case EventHardBounce, EventSoftBounce:
		delivery.State = StateBounced
	case EventComplaint:
		delivery.State = StateComplained
	case EventDeferred:
		delivery.LastDiagnostic = event.Diagnostic
	}
	delivery.LastDiagnostic = event.Diagnostic
	store.deliveries[deliveryID] = delivery
	if event.Type == EventHardBounce || event.Type == EventComplaint {
		store.suppressions[event.RecipientID] = Suppression{
			RecipientID: event.RecipientID,
			Scope:       SuppressAll,
			Reason:      string(event.Type),
			OccurredAt:  event.OccurredAt,
		}
	}
	store.events[event.ID] = event
	return true, nil
}

func (store *MemoryStore) Suppress(_ context.Context, suppression Suppression) error {
	if suppression.RecipientID == "" ||
		(suppression.Scope != SuppressMarketing && suppression.Scope != SuppressAll) {
		return errors.New("invalid suppression")
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	current, exists := store.suppressions[suppression.RecipientID]
	if !exists || current.Scope == SuppressMarketing && suppression.Scope == SuppressAll {
		store.suppressions[suppression.RecipientID] = suppression
	}
	return nil
}

func (store *MemoryStore) IsSuppressed(
	_ context.Context,
	recipientID string,
	channel Channel,
) (bool, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	return store.isSuppressedLocked(recipientID, channel), nil
}

func (store *MemoryStore) isSuppressedLocked(recipientID string, channel Channel) bool {
	suppression, exists := store.suppressions[recipientID]
	return exists && (suppression.Scope == SuppressAll ||
		suppression.Scope == SuppressMarketing && channel == ChannelMarketing)
}

func (store *MemoryStore) Delivery(id string) (Delivery, bool) {
	store.mu.Lock()
	defer store.mu.Unlock()
	delivery, exists := store.deliveries[id]
	return cloneDelivery(delivery), exists
}

func cloneDelivery(source Delivery) Delivery {
	source.Rendered.Headers = cloneMap(source.Rendered.Headers)
	return source
}

func cloneMap(source map[string]string) map[string]string {
	if source == nil {
		return nil
	}
	target := make(map[string]string, len(source))
	for key, value := range source {
		target[key] = value
	}
	return target
}

func (store *MemoryStore) IDs() []string {
	store.mu.Lock()
	defer store.mu.Unlock()
	return slices.Clone(store.order)
}
