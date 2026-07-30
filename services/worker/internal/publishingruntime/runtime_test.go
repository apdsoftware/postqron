package publishingruntime

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	publishing "github.com/apdsoftware/postqron/features/f08-publishing"
	metapublishing "github.com/apdsoftware/postqron/features/f08-publishing/providers/meta"
)

func TestRuntimeAdapterRegistryIsEmptyAndFailClosed(t *testing.T) {
	registry, err := newRuntimeAdapterRegistry(
		metapublishing.RegistrationConfig{},
	)
	if err != nil {
		t.Fatal(err)
	}
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

func TestRuntimeAdapterRegistryRegistersInjectedMetaNotifications(t *testing.T) {
	registry, err := newRuntimeAdapterRegistry(
		metapublishing.RegistrationConfig{
			NotificationStore: runtimeNotificationStore{},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	for _, provider := range []string{"facebook_groups", "instagram_personal"} {
		if _, err = registry.ResolveNotificationPublisher(
			context.Background(),
			provider,
		); err != nil {
			t.Fatalf("resolve %s: %v", provider, err)
		}
	}
}

type runtimeNotificationStore struct{}

func (runtimeNotificationStore) PutIfAbsent(
	context.Context,
	string,
	string,
	string,
	json.RawMessage,
) (string, error) {
	return "meta_notification_0123456789abcdef0123456789abcdef", nil
}
