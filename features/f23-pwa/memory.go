package pwa

import (
	"context"
	"fmt"
	"slices"
	"sync"
	"time"
)

// MemoryRepository is deterministic and concurrency-safe. It is useful for
// tests and local adapters; production uses the same Repository contract with
// the feature-owned migration.
type MemoryRepository struct {
	mu            sync.Mutex
	subscriptions map[string]Subscription
	endpoints     map[string]string
	events        map[string]PushEvent
	deliveries    map[string]Delivery
}

func NewMemoryRepository() *MemoryRepository {
	return &MemoryRepository{
		subscriptions: make(map[string]Subscription),
		endpoints:     make(map[string]string),
		events:        make(map[string]PushEvent),
		deliveries:    make(map[string]Delivery),
	}
}

func (repository *MemoryRepository) UpsertSubscription(
	_ context.Context,
	subscription Subscription,
) (Subscription, bool, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()

	endpointOwner := repository.endpoints[subscription.Endpoint]
	if endpointOwner != "" && endpointOwner != subscription.ID {
		return Subscription{}, false, ErrConflict
	}
	current, found := repository.subscriptions[subscription.ID]
	created := !found
	if found {
		subscription.CreatedAt = current.CreatedAt
	}
	repository.subscriptions[subscription.ID] = subscription
	repository.endpoints[subscription.Endpoint] = subscription.ID
	return subscription, created, nil
}

func (repository *MemoryRepository) RevokeSubscription(
	_ context.Context,
	accountID, endpoint string,
	now time.Time,
) (bool, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()

	id := repository.endpoints[endpoint]
	subscription, found := repository.subscriptions[id]
	if !found || subscription.AccountID != accountID {
		return false, nil
	}
	if subscription.RevokedAt != nil {
		return false, nil
	}
	subscription.RevokedAt = &now
	subscription.UpdatedAt = now
	repository.subscriptions[id] = subscription
	return true, nil
}

func (repository *MemoryRepository) ActiveSubscriptions(
	_ context.Context,
	accountIDs []string,
	now time.Time,
) ([]Subscription, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()

	accounts := make(map[string]struct{}, len(accountIDs))
	for _, accountID := range accountIDs {
		accounts[accountID] = struct{}{}
	}
	result := make([]Subscription, 0)
	for _, subscription := range repository.subscriptions {
		_, wanted := accounts[subscription.AccountID]
		active := subscription.RevokedAt == nil &&
			(subscription.ExpirationTime == nil ||
				subscription.ExpirationTime.After(now))
		if wanted && active {
			result = append(result, subscription)
		}
	}
	slices.SortFunc(result, func(left, right Subscription) int {
		switch {
		case left.ID < right.ID:
			return -1
		case left.ID > right.ID:
			return 1
		default:
			return 0
		}
	})
	return result, nil
}

func (repository *MemoryRepository) EnqueueDeliveries(
	_ context.Context,
	event PushEvent,
	subscriptions []Subscription,
	now time.Time,
) (int, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()

	if existing, found := repository.events[event.EventID]; found {
		if eventFingerprint(existing) != eventFingerprint(event) {
			return 0, ErrConflict
		}
		return 0, nil
	}
	repository.events[event.EventID] = event
	created := 0
	for _, subscription := range subscriptions {
		id := stableID("delivery", event.EventID, subscription.ID)
		if _, found := repository.deliveries[id]; found {
			continue
		}
		repository.deliveries[id] = Delivery{
			ID:             id,
			SourceEventID:  event.EventID,
			SubscriptionID: subscription.ID,
			Event:          event,
			State:          DeliveryPending,
			NextAttemptAt:  now,
			CreatedAt:      now,
		}
		created++
	}
	return created, nil
}

func (repository *MemoryRepository) ClaimDelivery(
	_ context.Context,
	now, lockedUntil time.Time,
	leaseToken string,
) (Delivery, Subscription, bool, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()

	ids := make([]string, 0, len(repository.deliveries))
	for id := range repository.deliveries {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	for _, id := range ids {
		delivery := repository.deliveries[id]
		due := (delivery.State == DeliveryPending ||
			delivery.State == DeliveryRetry ||
			(delivery.State == DeliverySending &&
				delivery.LockedUntil != nil &&
				!delivery.LockedUntil.After(now))) &&
			!delivery.NextAttemptAt.After(now)
		if !due {
			continue
		}
		subscription, found := repository.subscriptions[delivery.SubscriptionID]
		if !found || subscription.RevokedAt != nil {
			delivery.State = DeliveryFailed
			delivery.LeaseToken = ""
			delivery.LockedUntil = nil
			repository.deliveries[id] = delivery
			continue
		}
		delivery.State = DeliverySending
		delivery.LeaseToken = leaseToken
		delivery.LockedUntil = &lockedUntil
		repository.deliveries[id] = delivery
		return delivery, subscription, true, nil
	}
	return Delivery{}, Subscription{}, false, nil
}

func (repository *MemoryRepository) MarkDelivered(
	ctx context.Context,
	id, leaseToken string,
	now time.Time,
) error {
	return repository.complete(ctx, id, leaseToken, DeliveryDelivered, 0, now)
}

func (repository *MemoryRepository) MarkRetry(
	ctx context.Context,
	id, leaseToken string,
	attempt int,
	nextAttemptAt time.Time,
) error {
	return repository.complete(
		ctx,
		id,
		leaseToken,
		DeliveryRetry,
		attempt,
		nextAttemptAt,
	)
}

func (repository *MemoryRepository) MarkFailed(
	ctx context.Context,
	id, leaseToken string,
	now time.Time,
) error {
	return repository.complete(ctx, id, leaseToken, DeliveryFailed, 0, now)
}

func (repository *MemoryRepository) complete(
	_ context.Context,
	id, leaseToken string,
	state DeliveryState,
	attempt int,
	at time.Time,
) error {
	repository.mu.Lock()
	defer repository.mu.Unlock()

	delivery, found := repository.deliveries[id]
	if !found {
		return ErrNotFound
	}
	if delivery.State != DeliverySending || delivery.LeaseToken != leaseToken {
		return ErrConflict
	}
	delivery.State = state
	delivery.LeaseToken = ""
	delivery.LockedUntil = nil
	if state == DeliveryRetry {
		delivery.AttemptCount = attempt
		delivery.NextAttemptAt = at
	}
	repository.deliveries[id] = delivery
	return nil
}

func (repository *MemoryRepository) ExpireSubscription(
	_ context.Context,
	id string,
	now time.Time,
) error {
	repository.mu.Lock()
	defer repository.mu.Unlock()

	subscription, found := repository.subscriptions[id]
	if !found {
		return ErrNotFound
	}
	if subscription.RevokedAt == nil {
		subscription.RevokedAt = &now
		subscription.UpdatedAt = now
		repository.subscriptions[id] = subscription
	}
	return nil
}

func eventFingerprint(event PushEvent) string {
	return fmt.Sprintf(
		"%s\x00%s\x00%q\x00%s\x00%s\x00%s\x00%s\x00%s\x00%s",
		event.EventID,
		event.Kind,
		event.RecipientAccountIDs,
		event.WorkspaceID,
		event.ResourceID,
		event.Title,
		event.Body,
		event.ActionURL,
		event.OccurredAt.Format(time.RFC3339Nano),
	)
}

func (repository *MemoryRepository) DeliverySnapshot() []Delivery {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	result := make([]Delivery, 0, len(repository.deliveries))
	for _, delivery := range repository.deliveries {
		result = append(result, delivery)
	}
	return result
}

func (repository *MemoryRepository) SubscriptionSnapshot() []Subscription {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	result := make([]Subscription, 0, len(repository.subscriptions))
	for _, subscription := range repository.subscriptions {
		result = append(result, subscription)
	}
	return result
}
