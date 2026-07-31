package publishingruntime

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
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

type runtimeTargetResolver struct{}

func (runtimeTargetResolver) ResolveTarget(
	context.Context, string, string,
) (staticproviders.ConnectionTarget, error) {
	return staticproviders.ConnectionTarget{
		Provider: socialconnections.ProviderX, RemoteID: "123",
	}, nil
}

type fixtureRuntimeMediaResolver struct{}

func (fixtureRuntimeMediaResolver) OpenMedia(
	context.Context, string, string,
) (staticproviders.ResolvedMedia, error) {
	return staticproviders.ResolvedMedia{
		Body: io.NopCloser(bytes.NewReader(nil)),
	}, nil
}

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
		Targets:         runtimeTargetResolver{},
		Media:           fixtureRuntimeMediaResolver{},
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
		if _, resolveErr := registry.ResolvePublisher(
			context.Background(), provider,
		); !errors.Is(resolveErr, publishing.ErrProviderUnavailable) {
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
	config := runtimeStaticProviderConfig(nil)
	gate := config.Gates[staticproviders.ProviderX]
	if !gate.Enabled || !gate.ReviewApproved ||
		!gate.AuditVerified || !gate.QuotaConfigured {
		t.Fatalf("X gate=%+v", gate)
	}
	t.Setenv("POSTQRON_F08_X_QUOTA_CONFIGURED", "false")
	registry, err := newRuntimeAdapterRegistry(
		rejectingExecutor{},
		runtimeStaticProviderConfig(nil),
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

func TestVideoAdapterRegistrationFailsClosedWithoutEveryGate(t *testing.T) {
	registry, err := NewVideoAdapterRegistry(VideoAdapterDependencies{
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

func TestTikTokRegistrationFailsClosedUntilF5TrailingSlashSupport(t *testing.T) {
	ready := ProviderGate{
		Configured: true, ReviewApproved: true,
		AuditVerified: true, QuotaVerified: true,
	}
	_, err := NewVideoAdapterRegistry(VideoAdapterDependencies{
		TikTok:                   ready,
		TikTokVerifiedPullPrefix: "https://media.example/tiktok/",
	})
	if err == nil || !strings.Contains(err.Error(), "issue #342") {
		t.Fatalf("registration error=%v", err)
	}
}

func TestRuntimeRegistryPreservesStaticAndVideoWiring(t *testing.T) {
	ready := ProviderGate{
		Configured: true, ReviewApproved: true,
		AuditVerified: true, QuotaVerified: true,
	}
	registry, err := newRuntimeAdapterRegistry(
		rejectingExecutor{},
		staticproviders.Config{
			LinkedInVersion: "202606",
			Targets:         runtimeTargetResolver{},
			Media:           fixtureRuntimeMediaResolver{},
			Gates: map[string]staticproviders.Gate{
				staticproviders.ProviderX: {
					Enabled: true, ReviewApproved: true,
					AuditVerified: true, QuotaConfigured: true,
				},
			},
		},
		VideoAdapterDependencies{
			Executor:                 &socialconnections.AuthenticatedExecutor{},
			TikTokVerifiedPullPrefix: "https://media.example/tiktok/",
			F5TrailingSlashPaths:     true,
			TikTok:                   ready,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	for _, provider := range []string{
		staticproviders.ProviderX,
		string(socialconnections.ProviderTikTok),
	} {
		if _, err = registry.ResolvePublisher(
			context.Background(), provider,
		); err != nil {
			t.Fatalf("%s resolution error=%v", provider, err)
		}
	}
}
