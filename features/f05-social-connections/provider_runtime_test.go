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
			name:      "dynamic-only",
			providers: []Provider{ProviderMastodon},
			configure: func(
				_ runtimeProviderFamilyInput,
				registrar *runtimeProviderFamilyRegistrar,
			) error {
				return registrar.RegisterDynamic(
					RuntimeDynamicProviderRegistration{
						Provider:         ProviderMastodon,
						Adapter:          runtimeDynamicAdapterFixture(),
						Configured:       true,
						SupportedVersion: RuntimeDynamicProviderCompatibilityVersion,
					},
					runtimeDynamicProviderAttestations{
						Enabled:              true,
						Configured:           true,
						AuditVerified:        true,
						SmokeVerified:        true,
						CompatibilityVersion: RuntimeDynamicProviderCompatibilityVersion,
						SupportedVersion:     RuntimeDynamicProviderCompatibilityVersion,
					},
				)
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
			name:      "undeclared",
			providers: []Provider{ProviderMastodon},
			configure: func(
				_ runtimeProviderFamilyInput,
				registrar *runtimeProviderFamilyRegistrar,
			) error {
				return registrar.RegisterDynamic(
					RuntimeDynamicProviderRegistration{
						Provider:         Provider("fixture"),
						Adapter:          runtimeDynamicAdapterFixture(),
						Configured:       true,
						SupportedVersion: RuntimeDynamicProviderCompatibilityVersion,
					},
					runtimeDynamicProviderAttestations{
						Enabled:              true,
						Configured:           true,
						AuditVerified:        true,
						SmokeVerified:        true,
						CompatibilityVersion: RuntimeDynamicProviderCompatibilityVersion,
						SupportedVersion:     RuntimeDynamicProviderCompatibilityVersion,
					},
				)
			},
		},
	}

	_, err := configureRuntimeProviderFamilies(map[string]string{}, nil)
	if !errors.Is(err, ErrInvalidArgument) ||
		!strings.Contains(err.Error(), `undeclared runtime provider "fixture"`) {
		t.Fatalf("undeclared provider error = %v", err)
	}
}

func TestRuntimeProviderFamiliesRejectFamilyOwnershipViolations(t *testing.T) {
	previous := runtimeProviderFamilies
	t.Cleanup(func() { runtimeProviderFamilies = previous })
	runtimeProviderFamilies = []runtimeProviderFamily{
		{
			name:      "decentralized",
			providers: []Provider{ProviderMastodon},
			configure: func(
				_ runtimeProviderFamilyInput,
				registrar *runtimeProviderFamilyRegistrar,
			) error {
				return registrar.RegisterDynamic(
					RuntimeDynamicProviderRegistration{
						Provider:         ProviderBluesky,
						Adapter:          runtimeDynamicAdapterFixture(),
						Configured:       true,
						SupportedVersion: RuntimeDynamicProviderCompatibilityVersion,
					},
					runtimeDynamicProviderAttestations{
						Enabled:              true,
						Configured:           true,
						AuditVerified:        true,
						SmokeVerified:        true,
						CompatibilityVersion: RuntimeDynamicProviderCompatibilityVersion,
						SupportedVersion:     RuntimeDynamicProviderCompatibilityVersion,
					},
				)
			},
		},
	}

	_, err := configureRuntimeProviderFamilies(map[string]string{}, nil)
	if !errors.Is(err, ErrInvalidArgument) ||
		!strings.Contains(err.Error(), "does not own bluesky") {
		t.Fatalf("ownership error = %v", err)
	}
}

func TestRuntimeProviderFamiliesRejectDeterministicDynamicDuplicates(t *testing.T) {
	previous := runtimeProviderFamilies
	t.Cleanup(func() { runtimeProviderFamilies = previous })
	runtimeProviderFamilies = []runtimeProviderFamily{
		{
			name:      "decentralized",
			providers: []Provider{ProviderBluesky, ProviderMastodon},
			configure: func(
				_ runtimeProviderFamilyInput,
				registrar *runtimeProviderFamilyRegistrar,
			) error {
				if err := registrar.RegisterDynamic(
					RuntimeDynamicProviderRegistration{
						Provider:         ProviderMastodon,
						Adapter:          runtimeDynamicAdapterFixture(),
						Configured:       true,
						SupportedVersion: RuntimeDynamicProviderCompatibilityVersion,
					},
					runtimeDynamicProviderAttestations{
						Enabled:              true,
						Configured:           true,
						AuditVerified:        true,
						SmokeVerified:        true,
						CompatibilityVersion: RuntimeDynamicProviderCompatibilityVersion,
						SupportedVersion:     RuntimeDynamicProviderCompatibilityVersion,
					},
				); err != nil {
					return err
				}
				return registrar.RegisterDynamic(
					RuntimeDynamicProviderRegistration{
						Provider:         ProviderMastodon,
						Adapter:          runtimeDynamicAdapterFixture(),
						Configured:       true,
						SupportedVersion: RuntimeDynamicProviderCompatibilityVersion,
					},
					runtimeDynamicProviderAttestations{
						Enabled:              true,
						Configured:           true,
						AuditVerified:        true,
						SmokeVerified:        true,
						CompatibilityVersion: RuntimeDynamicProviderCompatibilityVersion,
						SupportedVersion:     RuntimeDynamicProviderCompatibilityVersion,
					},
				)
			},
		},
	}

	_, err := configureRuntimeProviderFamilies(map[string]string{}, nil)
	if !errors.Is(err, ErrInvalidArgument) ||
		!strings.Contains(err.Error(), "duplicate dynamic adapter for mastodon") {
		t.Fatalf("duplicate error = %v", err)
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
