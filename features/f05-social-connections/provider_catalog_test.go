package socialconnections

import (
	"context"
	"errors"
	"slices"
	"testing"
)

func TestProviderCatalogCoversTheIssue302DecisionWithoutEnablingAdapters(
	t *testing.T,
) {
	fixture := newServiceFixture(t)
	bootstrap := fixture.service.Bootstrap()
	if bootstrap.CatalogVersion != ProviderCatalogVersion {
		t.Fatalf("catalog version = %q", bootstrap.CatalogVersion)
	}
	if len(bootstrap.Providers) != len(LegacyBootstrapProviders) {
		t.Fatalf("legacy providers = %#v", bootstrap.Providers)
	}
	if len(bootstrap.Catalog) != len(SupportedProviders) {
		t.Fatalf("catalog length = %d, want %d", len(bootstrap.Catalog), len(SupportedProviders))
	}
	for index, expected := range SupportedProviders {
		entry := bootstrap.Catalog[index]
		if entry.Provider != expected {
			t.Fatalf("catalog[%d] = %q, want %q", index, entry.Provider, expected)
		}
		if len(entry.Resources) == 0 {
			t.Fatalf("%s has no resource capabilities", entry.Provider)
		}
		if expected == ProviderFacebookPages ||
			expected == ProviderInstagramProfessional {
			if entry.Status != ProviderAvailable ||
				!entry.Capabilities.Authorization ||
				!entry.Capabilities.ResourceSelection {
				t.Fatalf("implemented Meta entry = %#v", entry)
			}
			continue
		}
		if entry.Status != ProviderUnavailable ||
			entry.ConfigurationState != ProviderNotConfigured ||
			entry.Capabilities != (AdapterCapabilities{}) {
			t.Fatalf("unimplemented provider is not fail-closed: %#v", entry)
		}
	}
}

func TestProviderCatalogPublishingModesMatchTheDeliberatedScope(t *testing.T) {
	tests := []struct {
		provider Provider
		resource ResourceType
		mode     PublishingMode
	}{
		{ProviderFacebookPages, ResourceFacebookPage, PublishingModeAuto},
		{ProviderFacebookGroups, ResourceFacebookGroup, PublishingModeNotification},
		{
			ProviderInstagramProfessional,
			ResourceInstagramProfessional,
			PublishingModeAuto,
		},
		{
			ProviderInstagramPersonal,
			ResourceInstagramPersonal,
			PublishingModeNotification,
		},
		{ProviderX, ResourceXProfile, PublishingModeAuto},
		{ProviderLinkedIn, ResourceLinkedInProfile, PublishingModeAuto},
		{ProviderLinkedIn, ResourceLinkedInPage, PublishingModeAuto},
		{ProviderPinterest, ResourcePinterestBoard, PublishingModeAuto},
		{ProviderTikTok, ResourceTikTokProfile, PublishingModeAuto},
		{
			ProviderGoogleBusinessProfile,
			ResourceGoogleBusinessLocation,
			PublishingModeAuto,
		},
		{ProviderMastodon, ResourceMastodonAccount, PublishingModeAuto},
		{ProviderYouTube, ResourceYouTubeChannel, PublishingModeAuto},
		{ProviderThreads, ResourceThreadsProfile, PublishingModeAuto},
		{ProviderBluesky, ResourceBlueskyAccount, PublishingModeAuto},
	}
	for _, test := range tests {
		resources := providerResources(test.provider)
		found := false
		for _, resource := range resources {
			if resource.ResourceType == test.resource &&
				slices.Contains(resource.PublishingModes, test.mode) {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("%s lacks %s/%s", test.provider, test.resource, test.mode)
		}
	}
}

func TestProviderCatalogCopiesCannotMutateServiceState(t *testing.T) {
	fixture := newServiceFixture(t)
	first := fixture.service.Bootstrap()
	first.Catalog[0].Resources[0].AccountTypes[0] = AccountTypePersonal
	first.Catalog[0].Resources[0].PublishingModes[0] = PublishingModeNotification

	second := fixture.service.Bootstrap()
	resource := second.Catalog[0].Resources[0]
	if resource.AccountTypes[0] != AccountTypePage ||
		resource.PublishingModes[0] != PublishingModeAuto {
		t.Fatalf("catalog state was mutated through bootstrap: %#v", resource)
	}
}

func TestAdapterCannotBypassReviewOrAuditAvailability(t *testing.T) {
	adapter := &fakeAdapter{config: OAuthConfig{
		ClientID:         "x-client",
		AuthorizationURL: "https://x.example.test/oauth/authorize",
		RedirectURL:      "https://app.example.test/social/callback",
		Scopes:           []string{"tweet.read", "tweet.write", "users.read"},
		SupportsPKCE:     true,
	}}
	service, err := NewService(Config{
		Repository: NewMemoryRepository(),
		Authorizer: &fakeAuthorizer{permissions: map[Permission]bool{
			PermissionManageChannels: true,
		}},
		Quota: newFakeChannelQuota(),
		Adapters: map[Provider]Adapter{
			ProviderX: adapter,
		},
		Availability: map[Provider]ProviderAvailability{
			ProviderX: {
				Status:             ProviderUnavailable,
				ConfigurationState: ProviderAuditRequired,
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.Begin(context.Background(), BeginRequest{
		WorkspaceID: "workspace-1",
		ActorID:     "owner-1",
		Provider:    ProviderX,
	})
	if !errors.Is(err, ErrProviderAuditRequired) {
		t.Fatalf("Begin() error = %v, want audit gate", err)
	}
	for _, entry := range service.Bootstrap().Catalog {
		if entry.Provider == ProviderX &&
			(entry.Status != ProviderUnavailable ||
				entry.Capabilities != (AdapterCapabilities{})) {
			t.Fatalf("gated adapter leaked capabilities: %#v", entry)
		}
	}
}
