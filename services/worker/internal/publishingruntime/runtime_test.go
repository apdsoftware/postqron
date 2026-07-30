package publishingruntime

import (
	"context"
	"errors"
	"testing"

	publishing "github.com/apdsoftware/postqron/features/f08-publishing"
)

func TestRuntimeAdapterRegistryIsEmptyAndFailClosed(t *testing.T) {
	registry := newRuntimeAdapterRegistry()
	if _, err := registry.ResolvePublisher(
		context.Background(),
		"facebook_pages",
	); !errors.Is(err, publishing.ErrProviderUnavailable) {
		t.Fatalf("auto publisher resolution error=%v", err)
	}
	if _, err := registry.ResolveNotificationPublisher(
		context.Background(),
		"instagram_personal",
	); !errors.Is(err, publishing.ErrProviderUnavailable) {
		t.Fatalf("notification publisher resolution error=%v", err)
	}
}
