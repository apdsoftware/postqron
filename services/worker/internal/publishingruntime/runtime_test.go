package publishingruntime

import (
	"context"
	"errors"
	"testing"

	publishing "github.com/apdsoftware/postqron/features/f08-publishing"
)

func TestRuntimeAdapterRegistryIsEmptyAndFailClosed(t *testing.T) {
	registry, err := newRuntimeAdapterRegistry()
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

func TestVideoAdapterRegistrationFailsClosedWithoutEveryGate(t *testing.T) {
	registry, err := newRuntimeAdapterRegistry(VideoAdapterDependencies{
		TikTok: ProviderGate{
			Configured: true, ReviewApproved: true,
			AuditVerified: true, QuotaVerified: false,
		},
		YouTube: ProviderGate{
			Configured: true, ReviewApproved: false,
			AuditVerified: true, QuotaVerified: true,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, provider := range []string{"tiktok", "youtube"} {
		if _, err := registry.ResolvePublisher(
			context.Background(), provider,
		); !errors.Is(err, publishing.ErrProviderUnavailable) {
			t.Fatalf("%s resolution error=%v", provider, err)
		}
	}
}
