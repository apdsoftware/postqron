package publishingruntime

import (
	"context"
	"errors"
	"testing"

	socialconnections "github.com/apdsoftware/postqron/features/f05-social-connections"
	publishing "github.com/apdsoftware/postqron/features/f08-publishing"
	staticproviders "github.com/apdsoftware/postqron/features/f08-publishing/providers/static"
)

func TestRuntimeAdapterRegistryIsEmptyAndFailClosed(t *testing.T) {
	registry, err := newRuntimeAdapterRegistry(nil, staticproviders.Config{})
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

type rejectingExecutor struct{}

func (rejectingExecutor) Execute(
	context.Context,
	socialconnections.PublishingRequest,
) (socialconnections.PublishingResponse, error) {
	return socialconnections.PublishingResponse{}, &socialconnections.ExecutorFailure{
		Kind: socialconnections.ExecutorFailurePermanent,
		Code: "fixture_rejected",
	}
}

func TestRuntimeRegistersOnlyExplicitlyGatedStaticProviders(t *testing.T) {
	registry, err := newRuntimeAdapterRegistry(rejectingExecutor{}, staticproviders.Config{
		LinkedInVersion: "202606",
		Gates: map[string]staticproviders.Gate{
			staticproviders.ProviderX: {
				Enabled: true, ReviewApproved: true,
				AuditVerified: true, QuotaConfigured: true,
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = registry.ResolvePublisher(
		context.Background(),
		staticproviders.ProviderX,
	); err != nil {
		t.Fatal(err)
	}
	for _, provider := range []string{
		staticproviders.ProviderLinkedIn,
		staticproviders.ProviderPinterest,
		staticproviders.ProviderGoogleBusinessProfile,
	} {
		if _, resolveErr := registry.ResolvePublisher(context.Background(), provider); !errors.Is(resolveErr, publishing.ErrProviderUnavailable) {
			t.Fatalf("%s resolution error=%v", provider, resolveErr)
		}
	}
}

func TestRuntimeEnvironmentGateFailsClosed(t *testing.T) {
	for _, key := range []string{
		"POSTQRON_F08_X_ENABLED",
		"POSTQRON_F08_X_REVIEW_APPROVED",
		"POSTQRON_F08_X_RUNTIME_AUDIT_VERIFIED",
		"POSTQRON_F08_X_QUOTA_CONFIGURED",
	} {
		t.Setenv(key, "true")
	}
	config := runtimeStaticProviderConfig()
	gate := config.Gates[staticproviders.ProviderX]
	if !gate.Enabled || !gate.ReviewApproved ||
		!gate.AuditVerified || !gate.QuotaConfigured {
		t.Fatalf("X gate=%+v", gate)
	}
	t.Setenv("POSTQRON_F08_X_QUOTA_CONFIGURED", "false")
	registry, err := newRuntimeAdapterRegistry(
		rejectingExecutor{},
		runtimeStaticProviderConfig(),
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = registry.ResolvePublisher(
		context.Background(),
		staticproviders.ProviderX,
	); !errors.Is(err, publishing.ErrProviderUnavailable) {
		t.Fatalf("resolution error=%v", err)
	}
}
