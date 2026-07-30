package publishingruntime

import (
	"context"
	"errors"
	"testing"

	socialconnections "github.com/apdsoftware/postqron/features/f05-social-connections"
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

func TestRuntimeDynamicAdaptersStayClosedWithoutTrustedExecutor(t *testing.T) {
	for name, dependencies := range map[string]DynamicAdapterDependencies{
		"mastodon": {
			Mastodon: ProviderGate{
				Configured: true, ReviewApproved: true,
				AuditVerified: true, QuotaVerified: true,
			},
		},
		"bluesky": {
			Bluesky: ProviderGate{
				Configured: true, ReviewApproved: true,
				AuditVerified: true, QuotaVerified: true,
			},
		},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := newRuntimeAdapterRegistry(
				dependencies,
			); !errors.Is(err, publishing.ErrInvalidArgument) {
				t.Fatalf("registry error=%v", err)
			}
		})
	}
}

func TestRuntimeDynamicAdaptersRemainUnavailableWithIncompleteGate(t *testing.T) {
	registry, err := newRuntimeAdapterRegistry(DynamicAdapterDependencies{
		Mastodon: ProviderGate{
			Configured: true, ReviewApproved: true,
			AuditVerified: true,
		},
		Bluesky: ProviderGate{
			Configured: true, ReviewApproved: true,
			QuotaVerified: true,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, provider := range []string{"mastodon", "bluesky"} {
		if _, resolveErr := registry.ResolvePublisher(
			context.Background(),
			provider,
		); !errors.Is(resolveErr, publishing.ErrProviderUnavailable) {
			t.Fatalf("%s resolution error=%v", provider, resolveErr)
		}
	}
}

func TestRuntimeRegistersOnlyFullyGatedDynamicAdapters(t *testing.T) {
	executor := &socialconnections.AuthenticatedExecutor{}
	ready := ProviderGate{
		Configured: true, ReviewApproved: true,
		AuditVerified: true, QuotaVerified: true,
	}
	registry, err := newRuntimeAdapterRegistry(DynamicAdapterDependencies{
		Executor: executor,
		Mastodon: ready,
		Bluesky:  ready,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, provider := range []string{"mastodon", "bluesky"} {
		publisher, resolveErr := registry.ResolvePublisher(
			context.Background(),
			provider,
		)
		if resolveErr != nil {
			t.Fatalf("%s resolution error=%v", provider, resolveErr)
		}
		capability := publisher.Capabilities()
		if capability.Mode != publishing.PublishingModeAuto ||
			!capability.Reconciliation || !capability.MultiStep ||
			!capability.RemotePermalink {
			t.Fatalf("%s capabilities=%+v", provider, capability)
		}
	}
}
