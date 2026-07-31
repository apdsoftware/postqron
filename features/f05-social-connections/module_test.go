package socialconnections

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestLegacyMetaGateStillBootstrapsCipherAndMetaAdapters(
	t *testing.T,
) {
	module := runtimeModuleFixture()
	if err := module.Configure(map[string]string{
		configMetaEnabled: "true",
	}); err != nil {
		t.Fatal(err)
	}
	for _, provider := range module.service.Bootstrap().Providers {
		if provider.Status != ProviderUnavailable ||
			provider.ConfigurationState != ProviderNotConfigured ||
			provider.Retryable {
			t.Fatalf("incomplete provider availability = %#v", provider)
		}
	}

	key := base64.StdEncoding.EncodeToString(
		[]byte("0123456789abcdef0123456789abcdef"),
	)
	values := map[string]string{
		configMetaEnabled:       "true",
		configGraphVersion:      "v25.0",
		configLegacyCipherKeyID: "fixture-key",
		configLegacyCipherKey:   key,
		configFacebookID:        "facebook-client",
		configFacebookSecret:    "fixture-facebook-secret",
		configFacebookRedirect:  "https://app.example.test/api/v1/social-authorizations/callback",
		configFacebookConfigID:  "login-config",
		configFacebookReviewed:  "true",
		configFacebookAudited:   "true",
		configInstagramID:       "instagram-client",
		configInstagramSecret:   "fixture-instagram-secret",
		configInstagramRedirect: "https://app.example.test/api/v1/social-authorizations/callback",
		configInstagramReviewed: "true",
		configInstagramAudited:  "true",
	}
	if err := module.Configure(values); err != nil {
		t.Fatal(err)
	}
	if module.service.cipher == nil {
		t.Fatal("legacy Meta gate did not bootstrap the shared F5 cipher")
	}
	for _, provider := range module.service.Bootstrap().Providers {
		if provider.Status != ProviderAvailable ||
			provider.ConfigurationState != ProviderReady ||
			provider.Retryable {
			t.Fatalf("complete provider availability = %#v", provider)
		}
	}
}

func TestRuntimeConfigurationRequiresExactTrueFeatureFlag(t *testing.T) {
	module := runtimeModuleFixture()
	key := base64.StdEncoding.EncodeToString(
		[]byte("0123456789abcdef0123456789abcdef"),
	)
	if err := module.Configure(map[string]string{
		configMetaEnabled:      "TRUE",
		configGraphVersion:     "v25.0",
		configCipherKeyID:      "fixture-key",
		configCipherKey:        key,
		configFacebookID:       "facebook-client",
		configFacebookSecret:   "fixture-secret",
		configFacebookRedirect: "https://app.example.test/callback",
		configFacebookConfigID: "login-config",
		configFacebookReviewed: "true",
		configFacebookAudited:  "true",
	}); err != nil {
		t.Fatal(err)
	}
	availability := module.service.Bootstrap().Providers[0]
	if availability.Status != ProviderUnavailable {
		t.Fatalf("non-exact feature flag enabled provider: %#v", availability)
	}
}

func TestRuntimeConfigurationDistinguishesReviewAndAuditGates(t *testing.T) {
	key := base64.StdEncoding.EncodeToString(
		[]byte("0123456789abcdef0123456789abcdef"),
	)
	values := map[string]string{
		configMetaEnabled:      "true",
		configGraphVersion:     "v25.0",
		configCipherKeyID:      "fixture-key",
		configCipherKey:        key,
		configFacebookID:       "facebook-client",
		configFacebookSecret:   "fixture-secret",
		configFacebookRedirect: "https://app.example.test/callback",
		configFacebookConfigID: "login-config",
	}
	module := runtimeModuleFixture()
	if err := module.Configure(values); err != nil {
		t.Fatal(err)
	}
	availability := module.service.Bootstrap().Providers[0]
	if availability.ConfigurationState != ProviderReviewRequired {
		t.Fatalf("review gate = %#v", availability)
	}

	values[configFacebookReviewed] = "true"
	module = runtimeModuleFixture()
	if err := module.Configure(values); err != nil {
		t.Fatal(err)
	}
	availability = module.service.Bootstrap().Providers[0]
	if availability.ConfigurationState != ProviderAuditRequired {
		t.Fatalf("audit gate = %#v", availability)
	}
}

func TestProviderNeutralGateBootstrapsCipherWithoutEnablingMeta(t *testing.T) {
	key := base64.StdEncoding.EncodeToString(
		[]byte("0123456789abcdef0123456789abcdef"),
	)
	module := runtimeModuleFixture()
	if err := module.Configure(map[string]string{
		configEnabled:     "true",
		configCipherKeyID: "fixture-key",
		configCipherKey:   key,
	}); err != nil {
		t.Fatal(err)
	}
	if module.service.cipher == nil {
		t.Fatal("provider-neutral F5 gate did not bootstrap the shared cipher")
	}
	for _, provider := range module.service.Bootstrap().Providers {
		if provider.Status != ProviderUnavailable ||
			provider.ConfigurationState != ProviderNotConfigured {
			t.Fatalf("provider-neutral cipher gate enabled Meta: %#v", provider)
		}
	}
}

func TestProviderNeutralGateIsAuthoritativeAndFailClosed(t *testing.T) {
	key := base64.StdEncoding.EncodeToString(
		[]byte("0123456789abcdef0123456789abcdef"),
	)
	values := completeMetaRuntimeValues(key)
	values[configEnabled] = "false"
	module := runtimeModuleFixture()
	if err := module.Configure(values); err != nil {
		t.Fatal(err)
	}
	if module.service.cipher != nil {
		t.Fatal("explicit provider-neutral false was bypassed by the legacy Meta gate")
	}
	for _, provider := range module.service.Bootstrap().Providers {
		if provider.Status != ProviderUnavailable {
			t.Fatalf("disabled provider-neutral runtime enabled provider: %#v", provider)
		}
	}
}

func TestProviderNeutralCipherConfigurationRemainsFailClosedWhenPartial(
	t *testing.T,
) {
	validKey := base64.StdEncoding.EncodeToString(
		[]byte("0123456789abcdef0123456789abcdef"),
	)
	tests := []map[string]string{
		{configEnabled: "true"},
		{
			configEnabled:     "true",
			configCipherKeyID: "fixture-key",
			configCipherKey:   "not-base64",
		},
		{
			configEnabled:           "true",
			configCipherKeyID:       "fixture-key",
			configCipherKey:         "not-base64",
			configLegacyCipherKey:   validKey,
			configLegacyCipherKeyID: "legacy-fixture-key",
		},
		{
			configEnabled:   "true",
			configCipherKey: validKey,
		},
		{
			configEnabled:     "TRUE",
			configCipherKeyID: "fixture-key",
			configCipherKey:   validKey,
		},
	}
	for index, values := range tests {
		module := runtimeModuleFixture()
		if err := module.Configure(values); err != nil {
			t.Fatalf("config %d: %v", index, err)
		}
		if module.service.cipher != nil {
			t.Fatalf("partial config %d bootstrapped a cipher", index)
		}
		for _, entry := range module.service.Bootstrap().Catalog {
			if entry.Status != ProviderUnavailable {
				t.Fatalf("partial config %d enabled provider: %#v", index, entry)
			}
		}
	}
}

func TestRuntimeBootstrapNeverExposesCipherOrProviderSecrets(t *testing.T) {
	key := base64.StdEncoding.EncodeToString(
		[]byte("0123456789abcdef0123456789abcdef"),
	)
	values := completeMetaRuntimeValues(key)
	module := runtimeModuleFixture()
	if err := module.Configure(values); err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(module.service.Bootstrap())
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{
		key,
		"fixture-key",
		"facebook-client",
		"fixture-facebook-secret",
		"instagram-client",
		"fixture-instagram-secret",
	} {
		if strings.Contains(string(payload), secret) {
			t.Fatalf("bootstrap exposed secret-bearing configuration %q", secret)
		}
	}
}

func TestProductionStyleEnvironmentBootstrapsOnlyReadyProvider(t *testing.T) {
	key := base64.StdEncoding.EncodeToString(
		[]byte("0123456789abcdef0123456789abcdef"),
	)
	callback := "https://postqron.com/app/social-oauth/callback"
	for name, value := range map[string]string{
		"POSTQRON_AUTH_ALLOWED_ORIGINS":                "https://postqron.com",
		"POSTQRON_F05_ENABLED":                         "true",
		"POSTQRON_F05_CIPHER_KEY_ID":                   "production-key-fixture",
		"POSTQRON_F05_CIPHER_KEY_BASE64":               key,
		"POSTQRON_F05_META_ENABLED":                    "false",
		"POSTQRON_F05_THREADS_ENABLED":                 "false",
		"POSTQRON_F05_X_ENABLED":                       "true",
		"POSTQRON_F05_X_CLIENT_ID":                     "production-client-fixture",
		"POSTQRON_F05_X_CLIENT_SECRET":                 "production-secret-fixture",
		"POSTQRON_F05_X_REDIRECT_URL":                  callback,
		"POSTQRON_F05_X_API_ACCESS_APPROVED":           "true",
		"POSTQRON_F05_X_RUNTIME_AUDIT_VERIFIED":        "true",
		"POSTQRON_F05_X_SMOKE_TEST_VERIFIED":           "true",
		"POSTQRON_F05_LINKEDIN_ENABLED":                "false",
		"POSTQRON_F05_PINTEREST_ENABLED":               "false",
		"POSTQRON_F05_TIKTOK_ENABLED":                  "false",
		"POSTQRON_F05_YOUTUBE_ENABLED":                 "false",
		"POSTQRON_F05_GOOGLE_BUSINESS_PROFILE_ENABLED": "false",
		"POSTQRON_F05_MASTODON_ENABLED":                "false",
		"POSTQRON_F05_BLUESKY_ENABLED":                 "false",
	} {
		t.Setenv(name, value)
	}

	module := runtimeModuleFixture()
	if err := module.Configure(map[string]string{}); err != nil {
		t.Fatal(err)
	}
	for _, entry := range module.service.Bootstrap().Catalog {
		if entry.Provider == ProviderX {
			if entry.Status != ProviderAvailable ||
				entry.ConfigurationState != ProviderReady {
				t.Fatalf("X bootstrap entry = %#v", entry)
			}
			continue
		}
		if entry.Status != ProviderUnavailable {
			t.Fatalf("unconfigured provider became available: %#v", entry)
		}
	}

	authorization, err := module.service.Begin(
		context.Background(),
		BeginRequest{
			WorkspaceID: "workspace-production-fixture",
			ActorID:     "owner-production-fixture",
			Provider:    ProviderX,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	providerURL, err := url.Parse(authorization.URL)
	if err != nil {
		t.Fatal(err)
	}
	if providerURL.Query().Get("redirect_uri") != callback {
		t.Fatalf("redirect_uri = %q", providerURL.Query().Get("redirect_uri"))
	}
}

func TestRuntimeConfigurationUsesExactAuthAllowedOrigins(t *testing.T) {
	t.Setenv(
		"POSTQRON_AUTH_ALLOWED_ORIGINS",
		"https://postqron.com, https://preview.postqron.com,https://postqron.com",
	)
	module := runtimeModuleFixture()
	if err := module.Configure(map[string]string{}); err != nil {
		t.Fatal(err)
	}
	for _, origin := range []string{
		"https://postqron.com",
		"https://preview.postqron.com",
	} {
		if _, ok := module.origins[origin]; !ok {
			t.Fatalf("configured origin %q is missing", origin)
		}
	}
	if len(module.origins) != 2 {
		t.Fatalf("origin policy = %#v, want two exact origins", module.origins)
	}

	invalid := runtimeModuleFixture()
	err := invalid.Configure(map[string]string{
		configAllowedOrigins: "https://postqron.com/path",
	})
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("invalid origin error = %v, want ErrInvalidArgument", err)
	}
}

func TestRuntimeProviderExtensionPointsRemainFailClosed(t *testing.T) {
	if len(runtimeProviderFamilies) != 6 {
		t.Fatalf(
			"runtime provider families = %d, want six isolated families",
			len(runtimeProviderFamilies),
		)
	}
	module := runtimeModuleFixture()
	if err := module.Configure(map[string]string{
		"social.x.enabled":       "true",
		"social.bluesky.enabled": "true",
	}); err != nil {
		t.Fatal(err)
	}
	for _, entry := range module.service.Bootstrap().Catalog {
		if entry.Provider == ProviderFacebookPages ||
			entry.Provider == ProviderInstagramProfessional {
			continue
		}
		if entry.Status != ProviderUnavailable ||
			entry.ConfigurationState != ProviderNotConfigured ||
			entry.Capabilities != (AdapterCapabilities{}) {
			t.Fatalf("unverified runtime extension enabled %#v", entry)
		}
	}
}

func TestRuntimeDynamicAdaptersPropagateToNewService(t *testing.T) {
	previous := decentralizedNetworksRuntimeDynamicHook
	t.Cleanup(func() { decentralizedNetworksRuntimeDynamicHook = previous })
	decentralizedNetworksRuntimeDynamicHook = func(
		_ runtimeProviderFamilyInput,
	) ([]RuntimeDynamicProviderRegistration, error) {
		return []RuntimeDynamicProviderRegistration{
			{
				Provider:         ProviderMastodon,
				Adapter:          runtimeDynamicAdapterFixture(),
				Configured:       true,
				SupportedVersion: RuntimeDynamicProviderCompatibilityVersion,
			},
		}, nil
	}
	module := runtimeModuleFixture()
	key := base64.StdEncoding.EncodeToString(
		[]byte("0123456789abcdef0123456789abcdef"),
	)
	if err := module.Configure(map[string]string{
		configEnabled:                            "true",
		configCipherKeyID:                        "fixture-key",
		configCipherKey:                          key,
		"social.mastodon.enabled":                "true",
		"social.mastodon.runtime_audit_verified": "true",
		"social.mastodon.runtime_smoke_verified": "true",
		"social.mastodon.compatibility_version":  RuntimeDynamicProviderCompatibilityVersion,
	}); err != nil {
		t.Fatal(err)
	}
	if module.service.dynamicAdapters[ProviderMastodon] == nil {
		t.Fatal("Mastodon dynamic adapter was not propagated to the service")
	}
	entry := providerCatalogEntry(
		t,
		module.service.Bootstrap(),
		ProviderMastodon,
	)
	if entry.Status != ProviderAvailable ||
		entry.ConfigurationState != ProviderReady ||
		!entry.Capabilities.Authorization ||
		!entry.Capabilities.ResourceSelection ||
		!entry.Capabilities.DynamicDiscovery ||
		!entry.Capabilities.AuthenticatedHTTP {
		t.Fatalf("Mastodon bootstrap entry = %#v", entry)
	}
}

func TestRuntimeDynamicProvidersRemainUnavailableWithOnlyCipher(t *testing.T) {
	module := runtimeModuleFixture()
	key := base64.StdEncoding.EncodeToString(
		[]byte("0123456789abcdef0123456789abcdef"),
	)
	if err := module.Configure(map[string]string{
		configEnabled:     "true",
		configCipherKeyID: "fixture-key",
		configCipherKey:   key,
	}); err != nil {
		t.Fatal(err)
	}
	entry := providerCatalogEntry(
		t,
		module.service.Bootstrap(),
		ProviderMastodon,
	)
	if entry.Status != ProviderUnavailable ||
		entry.ConfigurationState != ProviderNotConfigured {
		t.Fatalf("Mastodon bootstrap entry = %#v", entry)
	}
	if module.service.dynamicAdapters[ProviderMastodon] != nil {
		t.Fatal("Mastodon dynamic adapter should remain unavailable with only cipher")
	}
}

func TestRuntimeDynamicAdapterOwnershipFailsClosed(t *testing.T) {
	previous := decentralizedNetworksRuntimeDynamicHook
	t.Cleanup(func() { decentralizedNetworksRuntimeDynamicHook = previous })
	decentralizedNetworksRuntimeDynamicHook = func(
		_ runtimeProviderFamilyInput,
	) ([]RuntimeDynamicProviderRegistration, error) {
		return []RuntimeDynamicProviderRegistration{
			{
				Provider:         ProviderFacebookPages,
				Adapter:          runtimeDynamicAdapterFixture(),
				Configured:       true,
				SupportedVersion: RuntimeDynamicProviderCompatibilityVersion,
			},
		}, nil
	}
	module := runtimeModuleFixture()
	key := base64.StdEncoding.EncodeToString(
		[]byte("0123456789abcdef0123456789abcdef"),
	)
	if err := module.Configure(map[string]string{
		configEnabled:                            "true",
		configCipherKeyID:                        "fixture-key",
		configCipherKey:                          key,
		"social.mastodon.enabled":                "true",
		"social.mastodon.runtime_audit_verified": "true",
		"social.mastodon.runtime_smoke_verified": "true",
		"social.mastodon.compatibility_version":  RuntimeDynamicProviderCompatibilityVersion,
	}); !errors.Is(err, ErrInvalidArgument) ||
		!strings.Contains(err.Error(), "does not own facebook_pages") {
		t.Fatalf("ownership error = %v", err)
	}
}

func TestRuntimeDynamicAdapterDuplicateRegistrationsFailClosed(t *testing.T) {
	previous := decentralizedNetworksRuntimeDynamicHook
	t.Cleanup(func() { decentralizedNetworksRuntimeDynamicHook = previous })
	decentralizedNetworksRuntimeDynamicHook = func(
		_ runtimeProviderFamilyInput,
	) ([]RuntimeDynamicProviderRegistration, error) {
		return []RuntimeDynamicProviderRegistration{
			{
				Provider:         ProviderMastodon,
				Adapter:          runtimeDynamicAdapterFixture(),
				Configured:       true,
				SupportedVersion: RuntimeDynamicProviderCompatibilityVersion,
			},
			{
				Provider:         ProviderMastodon,
				Adapter:          runtimeDynamicAdapterFixture(),
				Configured:       true,
				SupportedVersion: RuntimeDynamicProviderCompatibilityVersion,
			},
		}, nil
	}
	module := runtimeModuleFixture()
	key := base64.StdEncoding.EncodeToString(
		[]byte("0123456789abcdef0123456789abcdef"),
	)
	if err := module.Configure(map[string]string{
		configEnabled:                            "true",
		configCipherKeyID:                        "fixture-key",
		configCipherKey:                          key,
		"social.mastodon.enabled":                "true",
		"social.mastodon.runtime_audit_verified": "true",
		"social.mastodon.runtime_smoke_verified": "true",
		"social.mastodon.compatibility_version":  RuntimeDynamicProviderCompatibilityVersion,
	}); !errors.Is(err, ErrInvalidArgument) ||
		!strings.Contains(err.Error(), "duplicate dynamic adapter for mastodon") {
		t.Fatalf("duplicate error = %v", err)
	}
}

func providerCatalogEntry(
	t *testing.T,
	bootstrap ClientBootstrap,
	provider Provider,
) ProviderCatalogEntry {
	t.Helper()
	for _, entry := range bootstrap.Catalog {
		if entry.Provider == provider {
			return entry
		}
	}
	t.Fatalf("provider %s not found in bootstrap catalog", provider)
	return ProviderCatalogEntry{}
}

func runtimeModuleFixture() *Module {
	return &Module{
		clock:      func() time.Time { return serviceTestNow },
		repository: NewMemoryRepository(),
		authorizer: &fakeAuthorizer{permissions: map[Permission]bool{
			PermissionViewWorkspace:  true,
			PermissionManageChannels: true,
		}},
		quota: newFakeChannelQuota(),
	}
}

func completeMetaRuntimeValues(key string) map[string]string {
	return map[string]string{
		configMetaEnabled:       "true",
		configGraphVersion:      "v25.0",
		configCipherKeyID:       "fixture-key",
		configCipherKey:         key,
		configFacebookID:        "facebook-client",
		configFacebookSecret:    "fixture-facebook-secret",
		configFacebookRedirect:  "https://app.example.test/api/v1/social-authorizations/callback",
		configFacebookConfigID:  "login-config",
		configFacebookReviewed:  "true",
		configFacebookAudited:   "true",
		configInstagramID:       "instagram-client",
		configInstagramSecret:   "fixture-instagram-secret",
		configInstagramRedirect: "https://app.example.test/api/v1/social-authorizations/callback",
		configInstagramReviewed: "true",
		configInstagramAudited:  "true",
	}
}
