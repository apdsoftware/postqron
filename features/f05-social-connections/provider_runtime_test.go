package socialconnections

import (
	"errors"
	"strings"
	"testing"
)

func TestRuntimeProviderValuePrefersInjectedConfiguration(t *testing.T) {
	t.Setenv("POSTQRON_F05_FIXTURE_VALUE", "environment")
	if value := runtimeProviderValue(
		map[string]string{"social.fixture": "injected"},
		"social.fixture",
		"POSTQRON_F05_FIXTURE_VALUE",
	); value != "injected" {
		t.Fatalf("runtime provider value = %q", value)
	}
	if value := runtimeProviderValue(
		map[string]string{},
		"social.fixture",
		"POSTQRON_F05_FIXTURE_VALUE",
	); value != "environment" {
		t.Fatalf("runtime provider environment value = %q", value)
	}
}

func TestRuntimeProviderFamiliesKeepStaticAndDynamicRegistriesSeparate(
	t *testing.T,
) {
	previous := runtimeProviderFamilies
	t.Cleanup(func() { runtimeProviderFamilies = previous })
	runtimeProviderFamilies = []runtimeProviderFamily{
		{
			name: "dynamic-only",
			dynamic: func(
				_ map[string]string,
				_ CredentialCipher,
				adapters map[Provider]DynamicAdapter,
				availability map[Provider]ProviderAvailability,
			) {
				adapters[ProviderMastodon] = runtimeDynamicAdapterFixture()
				availability[ProviderMastodon] = ProviderAvailability{
					Status:             ProviderAvailable,
					ConfigurationState: ProviderReady,
				}
			},
		},
	}

	registry, err := configureRuntimeProviderFamilies(map[string]string{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(registry.staticAdapters) != 0 {
		t.Fatalf("static registry = %#v, want empty", registry.staticAdapters)
	}
	if registry.dynamicAdapters[ProviderMastodon] == nil {
		t.Fatal("dynamic registry did not preserve Mastodon adapter")
	}
	availability := registry.availability[ProviderMastodon]
	if availability.Status != ProviderAvailable ||
		availability.ConfigurationState != ProviderReady {
		t.Fatalf("dynamic availability = %#v", availability)
	}
}

func TestRuntimeProviderFamiliesRejectUndeclaredDynamicProviders(t *testing.T) {
	previous := runtimeProviderFamilies
	t.Cleanup(func() { runtimeProviderFamilies = previous })
	runtimeProviderFamilies = []runtimeProviderFamily{
		{
			name: "undeclared",
			dynamic: func(
				_ map[string]string,
				_ CredentialCipher,
				adapters map[Provider]DynamicAdapter,
				availability map[Provider]ProviderAvailability,
			) {
				provider := Provider("fixture")
				adapters[provider] = runtimeDynamicAdapterFixture()
				availability[provider] = ProviderAvailability{
					Status:             ProviderAvailable,
					ConfigurationState: ProviderReady,
				}
			},
		},
	}

	_, err := configureRuntimeProviderFamilies(map[string]string{}, nil)
	if !errors.Is(err, ErrInvalidArgument) ||
		!strings.Contains(err.Error(), `undeclared runtime provider "fixture"`) {
		t.Fatalf("undeclared provider error = %v", err)
	}
}

func TestRuntimeProviderFamiliesRejectStaticDynamicCollisions(t *testing.T) {
	previous := runtimeProviderFamilies
	t.Cleanup(func() { runtimeProviderFamilies = previous })
	runtimeProviderFamilies = []runtimeProviderFamily{
		{
			name: "static",
			static: func(
				_ map[string]string,
				_ CredentialCipher,
				adapters map[Provider]Adapter,
				availability map[Provider]ProviderAvailability,
			) {
				adapters[ProviderBluesky] = &fakeAdapter{config: OAuthConfig{
					ClientID:         "fixture-client",
					AuthorizationURL: "https://example.test/oauth/authorize",
					RedirectURL:      "https://app.example.test/callback",
					Scopes:           []string{"atproto"},
					SupportsPKCE:     true,
				}}
				availability[ProviderBluesky] = ProviderAvailability{
					Status:             ProviderAvailable,
					ConfigurationState: ProviderReady,
				}
			},
		},
		{
			name: "dynamic",
			dynamic: func(
				_ map[string]string,
				_ CredentialCipher,
				adapters map[Provider]DynamicAdapter,
				availability map[Provider]ProviderAvailability,
			) {
				adapters[ProviderBluesky] = runtimeDynamicAdapterFixture()
				availability[ProviderBluesky] = ProviderAvailability{
					Status:             ProviderAvailable,
					ConfigurationState: ProviderReady,
				}
			},
		},
	}

	_, err := configureRuntimeProviderFamilies(map[string]string{}, nil)
	if !errors.Is(err, ErrInvalidArgument) ||
		!strings.Contains(err.Error(), "cannot mount static and dynamic adapters together") {
		t.Fatalf("collision error = %v", err)
	}
}

func runtimeDynamicAdapterFixture() DynamicAdapter {
	return &fakeDynamicAdapter{
		config: DynamicOAuthConfig{
			RedirectURL:      "https://app.example.test/api/v1/social-authorizations/callback",
			Scopes:           []string{"read:accounts", "write:media", "write:statuses"},
			RefreshTokenMode: RefreshTokenReusable,
			RevocationPolicy: RevocationBestEffort,
			NetworkPolicy: DynamicNetworkPolicy{
				RejectRedirects:   true,
				ValidateAndPinDNS: true,
				MaxResponseBytes:  4096,
			},
		},
	}
}
