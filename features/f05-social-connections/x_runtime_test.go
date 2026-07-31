package socialconnections

import (
	"encoding/base64"
	"testing"
)

func TestXRuntimeGatesCredentialsAccessAuditAndSmoke(t *testing.T) {
	cipher, err := NewAESGCMCipher(
		"fixture-key",
		[]byte("0123456789abcdef0123456789abcdef"),
	)
	if err != nil {
		t.Fatal(err)
	}
	values := map[string]string{
		configXEnabled:      "true",
		configXClientID:     "fixture-x-client",
		configXClientSecret: "fixture-x-client-secret",
		configXRedirectURL:  "https://app.example.test/api/v1/social-authorizations/callback",
	}
	tests := []struct {
		name         string
		change       func(map[string]string)
		state        ProviderConfigurationState
		available    bool
		capabilities AdapterCapabilities
	}{
		{
			name: "review required",
			change: func(map[string]string) {
			},
			state: ProviderReviewRequired,
		},
		{
			name: "audit required",
			change: func(values map[string]string) {
				values[configXAccessApproved] = "true"
			},
			state: ProviderAuditRequired,
		},
		{
			name: "smoke required",
			change: func(values map[string]string) {
				values[configXAccessApproved] = "true"
				values[configXAuditVerified] = "true"
			},
			state: ProviderAuditRequired,
		},
		{
			name: "ready",
			change: func(values map[string]string) {
				values[configXAccessApproved] = "true"
				values[configXAuditVerified] = "true"
				values[configXSmokeVerified] = "true"
			},
			state:     ProviderReady,
			available: true,
			capabilities: AdapterCapabilities{
				Authorization:     true,
				PKCE:              true,
				ResourceSelection: true,
				TokenRefresh:      true,
				RemoteRevocation:  true,
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			current := make(map[string]string, len(values)+3)
			for key, value := range values {
				current[key] = value
			}
			test.change(current)
			adapters := make(map[Provider]Adapter)
			availability := map[Provider]ProviderAvailability{
				ProviderX: {
					Provider:           ProviderX,
					Status:             ProviderUnavailable,
					ConfigurationState: ProviderNotConfigured,
				},
			}
			configureXRuntime(current, cipher, adapters, availability)
			got := availability[ProviderX]
			if got.ConfigurationState != test.state ||
				(got.Status == ProviderAvailable) != test.available ||
				adapterCapabilities(adapters[ProviderX]) != test.capabilities {
				t.Fatalf(
					"availability = %#v, capabilities = %#v",
					got,
					adapterCapabilities(adapters[ProviderX]),
				)
			}
		})
	}
}

func TestXRuntimeRemainsFailClosedForIncompleteOrNonExactFlags(t *testing.T) {
	cipher, err := NewAESGCMCipher(
		"fixture-key",
		[]byte("0123456789abcdef0123456789abcdef"),
	)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name   string
		values map[string]string
		cipher CredentialCipher
	}{
		{
			name: "missing credentials",
			values: map[string]string{
				configXEnabled: "true",
			},
			cipher: cipher,
		},
		{
			name: "non exact flag",
			values: map[string]string{
				configXEnabled:        "TRUE",
				configXClientID:       "fixture-x-client",
				configXClientSecret:   "fixture-x-client-secret",
				configXRedirectURL:    "https://app.example.test/callback",
				configXAccessApproved: "true",
				configXAuditVerified:  "true",
				configXSmokeVerified:  "true",
			},
			cipher: cipher,
		},
		{
			name: "missing cipher",
			values: map[string]string{
				configXEnabled:        "true",
				configXClientID:       "fixture-x-client",
				configXClientSecret:   "fixture-x-client-secret",
				configXRedirectURL:    "https://app.example.test/callback",
				configXAccessApproved: "true",
				configXAuditVerified:  "true",
				configXSmokeVerified:  "true",
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			adapters := make(map[Provider]Adapter)
			availability := map[Provider]ProviderAvailability{
				ProviderX: {
					Provider:           ProviderX,
					Status:             ProviderUnavailable,
					ConfigurationState: ProviderNotConfigured,
				},
			}
			configureXRuntime(
				test.values,
				test.cipher,
				adapters,
				availability,
			)
			if adapters[ProviderX] != nil ||
				availability[ProviderX].Status != ProviderUnavailable ||
				availability[ProviderX].ConfigurationState !=
					ProviderNotConfigured {
				t.Fatalf(
					"fail-closed availability = %#v",
					availability[ProviderX],
				)
			}
		})
	}
}

func TestXRuntimeIsVisibleInProviderDiscoveryOnlyWhenReady(t *testing.T) {
	key := base64.StdEncoding.EncodeToString(
		[]byte("0123456789abcdef0123456789abcdef"),
	)
	module := runtimeModuleFixture()
	err := module.Configure(map[string]string{
		configEnabled:         "true",
		configCipherKeyID:     "fixture-key",
		configCipherKey:       key,
		configXEnabled:        "true",
		configXClientID:       "fixture-x-client",
		configXClientSecret:   "fixture-x-client-secret",
		configXRedirectURL:    "https://app.example.test/api/v1/social-authorizations/callback",
		configXAccessApproved: "true",
		configXAuditVerified:  "true",
		configXSmokeVerified:  "true",
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range module.service.Bootstrap().Catalog {
		if entry.Provider != ProviderX {
			continue
		}
		if entry.Status != ProviderAvailable ||
			entry.ConfigurationState != ProviderReady ||
			entry.Capabilities.TokenRefresh != true ||
			entry.Capabilities.RemoteRevocation != true {
			t.Fatalf("X discovery entry = %#v", entry)
		}
		return
	}
	t.Fatal("X provider is missing from runtime discovery")
}
