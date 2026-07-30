package socialconnections

import (
	"encoding/base64"
	"testing"
	"time"
)

func TestRuntimeConfigurationIsFailClosedUntilReviewAndSecretsAreComplete(
	t *testing.T,
) {
	module := runtimeModuleFixture()
	if err := module.Configure(map[string]string{
		configEnabled: "true",
	}); err != nil {
		t.Fatal(err)
	}
	for _, provider := range module.service.Bootstrap().Providers {
		if provider.Status != ProviderUnavailable || !provider.Retryable {
			t.Fatalf("incomplete provider availability = %#v", provider)
		}
	}

	key := base64.StdEncoding.EncodeToString(
		[]byte("0123456789abcdef0123456789abcdef"),
	)
	values := map[string]string{
		configEnabled:           "true",
		configGraphVersion:      "v25.0",
		configCipherKeyID:       "fixture-key",
		configCipherKey:         key,
		configFacebookID:        "facebook-client",
		configFacebookSecret:    "fixture-facebook-secret",
		configFacebookRedirect:  "https://app.example.test/api/v1/social-authorizations/callback",
		configFacebookConfigID:  "login-config",
		configFacebookReviewed:  "true",
		configInstagramID:       "instagram-client",
		configInstagramSecret:   "fixture-instagram-secret",
		configInstagramRedirect: "https://app.example.test/api/v1/social-authorizations/callback",
		configInstagramReviewed: "true",
	}
	if err := module.Configure(values); err != nil {
		t.Fatal(err)
	}
	for _, provider := range module.service.Bootstrap().Providers {
		if provider.Status != ProviderAvailable || provider.Retryable {
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
		configEnabled:          "TRUE",
		configGraphVersion:     "v25.0",
		configCipherKeyID:      "fixture-key",
		configCipherKey:        key,
		configFacebookID:       "facebook-client",
		configFacebookSecret:   "fixture-secret",
		configFacebookRedirect: "https://app.example.test/callback",
		configFacebookConfigID: "login-config",
		configFacebookReviewed: "true",
	}); err != nil {
		t.Fatal(err)
	}
	availability := module.service.Bootstrap().Providers[0]
	if availability.Status != ProviderUnavailable {
		t.Fatalf("non-exact feature flag enabled provider: %#v", availability)
	}
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
