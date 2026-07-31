package publishing

import (
	"context"
	"fmt"
	"strings"
	"sync"
)

// AdapterRegistry is populated by worker adapter discovery. Registration is
// explicit and fail-closed: duplicate providers, invalid modes, and adapters
// without safe ambiguity handling are rejected before the worker starts.
type AdapterRegistry struct {
	mu            sync.RWMutex
	publishers    map[string]Publisher
	notifications map[string]NotificationPublisher
}

func NewAdapterRegistry() *AdapterRegistry {
	return &AdapterRegistry{
		publishers:    make(map[string]Publisher),
		notifications: make(map[string]NotificationPublisher),
	}
}

func (registry *AdapterRegistry) RegisterPublisher(
	provider string,
	publisher Publisher,
) error {
	provider = strings.TrimSpace(provider)
	if registry == nil || publisher == nil || !providerPattern.MatchString(provider) {
		return ErrInvalidArgument
	}
	capabilities := publisher.Capabilities()
	if capabilities.Mode != PublishingModeAuto ||
		strings.TrimSpace(capabilities.Version) == "" ||
		(!capabilities.NativeIdempotency && !capabilities.Reconciliation &&
			!capabilities.AmbiguousFailClosed) {
		return ErrUnsafeAdapter
	}
	registry.mu.Lock()
	defer registry.mu.Unlock()
	if _, exists := registry.publishers[provider]; exists {
		return fmt.Errorf("%w: duplicate publisher %s", ErrConflict, provider)
	}
	registry.publishers[provider] = publisher
	return nil
}

func (registry *AdapterRegistry) RegisterNotificationPublisher(
	provider string,
	publisher NotificationPublisher,
) error {
	provider = strings.TrimSpace(provider)
	if registry == nil || publisher == nil || !providerPattern.MatchString(provider) {
		return ErrInvalidArgument
	}
	capabilities := publisher.Capabilities()
	if capabilities.Mode != PublishingModeNotification ||
		strings.TrimSpace(capabilities.Version) == "" ||
		!capabilities.NotificationIdempotency {
		return ErrUnsafeAdapter
	}
	registry.mu.Lock()
	defer registry.mu.Unlock()
	if _, exists := registry.notifications[provider]; exists {
		return fmt.Errorf("%w: duplicate notification publisher %s", ErrConflict, provider)
	}
	registry.notifications[provider] = publisher
	return nil
}

func (registry *AdapterRegistry) ResolvePublisher(
	_ context.Context,
	provider string,
) (Publisher, error) {
	if registry == nil {
		return nil, ErrProviderUnavailable
	}
	registry.mu.RLock()
	defer registry.mu.RUnlock()
	publisher, exists := registry.publishers[strings.TrimSpace(provider)]
	if !exists {
		return nil, ErrProviderUnavailable
	}
	return publisher, nil
}

func (registry *AdapterRegistry) ResolveNotificationPublisher(
	_ context.Context,
	provider string,
) (NotificationPublisher, error) {
	if registry == nil {
		return nil, ErrProviderUnavailable
	}
	registry.mu.RLock()
	defer registry.mu.RUnlock()
	publisher, exists := registry.notifications[strings.TrimSpace(provider)]
	if !exists {
		return nil, ErrProviderUnavailable
	}
	return publisher, nil
}

var (
	_ PublisherResolver    = (*AdapterRegistry)(nil)
	_ NotificationResolver = (*AdapterRegistry)(nil)
)
