package publishingruntime

import (
	"context"
	"errors"
	"testing"
	"time"

	publishing "github.com/apdsoftware/postqron/features/f08-publishing"
)

func TestRuntimeAdapterRegistryIsEmptyAndFailClosed(t *testing.T) {
	registry, err := newRuntimeAdapterRegistry(
		DynamicAdapterDependencies{}, time.Now,
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
				time.Now,
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
	}, time.Now)
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

func TestCompositionRootExplicitlyRejectsUnavailableF5Dependencies(t *testing.T) {
	for _, values := range [][2]string{{"true", "false"}, {"false", "true"}} {
		if _, err := FailClosedDynamicBootstrap(values[0], values[1]); err == nil {
			t.Fatalf("bootstrap(%q,%q) succeeded", values[0], values[1])
		}
	}
	dependencies, err := FailClosedDynamicBootstrap("false", "")
	if err != nil {
		t.Fatal(err)
	}
	registry, err := newRuntimeAdapterRegistry(dependencies, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = registry.ResolvePublisher(
		context.Background(), "mastodon",
	); !errors.Is(err, publishing.ErrProviderUnavailable) {
		t.Fatalf("Mastodon resolution error=%v", err)
	}
}
