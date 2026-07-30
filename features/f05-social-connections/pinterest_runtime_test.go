package socialconnections

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestPinterestRuntimeRequiresCredentialsApprovalAuditAndSharedCipher(
	t *testing.T,
) {
	for _, environmentKey := range pinterestRuntimeEnvironmentKeys {
		t.Setenv(environmentKey, "")
	}
	complete := map[string]string{
		configPinterestEnabled:        "true",
		configPinterestClientID:       "fixture-client-id",
		configPinterestClientSecret:   "fixture-client-secret",
		configPinterestRedirectURL:    "https://app.example.test/social/callback",
		configPinterestAccessApproved: "true",
		configPinterestAuditVerified:  "true",
	}
	cipher, err := NewAESGCMCipher("fixture-key", make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name      string
		values    map[string]string
		cipher    CredentialCipher
		wantState ProviderConfigurationState
		wantReady bool
	}{
		{
			name:      "disabled",
			values:    map[string]string{},
			cipher:    cipher,
			wantState: ProviderNotConfigured,
		},
		{
			name: "credentials missing",
			values: map[string]string{
				configPinterestEnabled: "true",
			},
			cipher:    cipher,
			wantState: ProviderNotConfigured,
		},
		{
			name: "access approval missing",
			values: pinterestRuntimeValues(complete, map[string]string{
				configPinterestAccessApproved: "false",
			}),
			cipher:    cipher,
			wantState: ProviderReviewRequired,
		},
		{
			name: "audit missing",
			values: pinterestRuntimeValues(complete, map[string]string{
				configPinterestAuditVerified: "false",
			}),
			cipher:    cipher,
			wantState: ProviderAuditRequired,
		},
		{
			name:      "provider-neutral cipher missing",
			values:    complete,
			wantState: ProviderNotConfigured,
		},
		{
			name:      "ready",
			values:    complete,
			cipher:    cipher,
			wantState: ProviderReady,
			wantReady: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			adapters := make(map[Provider]Adapter)
			availability := map[Provider]ProviderAvailability{
				ProviderPinterest: {
					Provider:           ProviderPinterest,
					Status:             ProviderUnavailable,
					ConfigurationState: ProviderNotConfigured,
				},
			}
			configurePinterestRuntime(
				test.values,
				test.cipher,
				adapters,
				availability,
			)
			got := availability[ProviderPinterest]
			if got.ConfigurationState != test.wantState {
				t.Fatalf(
					"Pinterest state = %q, want %q",
					got.ConfigurationState,
					test.wantState,
				)
			}
			adapter, ready := adapters[ProviderPinterest]
			if ready != test.wantReady ||
				(got.Status == ProviderAvailable) != test.wantReady {
				t.Fatalf(
					"Pinterest adapter ready=%t availability=%#v",
					ready,
					got,
				)
			}
			if ready &&
				adapter.Config().ScopeSeparator != OAuthScopeSeparatorSpace {
				t.Fatalf("Pinterest runtime adapter config = %#v", adapter.Config())
			}
		})
	}
}

func TestPinterestRuntimeRejectsInvalidRedirectWithoutBecomingAvailable(
	t *testing.T,
) {
	for _, environmentKey := range pinterestRuntimeEnvironmentKeys {
		t.Setenv(environmentKey, "")
	}
	cipher, err := NewAESGCMCipher("fixture-key", make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	values := map[string]string{
		configPinterestEnabled:        "true",
		configPinterestClientID:       "fixture-client-id",
		configPinterestClientSecret:   "fixture-client-secret",
		configPinterestRedirectURL:    "http://app.example.test/callback",
		configPinterestAccessApproved: "true",
		configPinterestAuditVerified:  "true",
	}
	adapters := make(map[Provider]Adapter)
	availability := map[Provider]ProviderAvailability{
		ProviderPinterest: {
			Provider:           ProviderPinterest,
			Status:             ProviderUnavailable,
			ConfigurationState: ProviderNotConfigured,
		},
	}
	configurePinterestRuntime(values, cipher, adapters, availability)
	if adapters[ProviderPinterest] != nil ||
		availability[ProviderPinterest].Status != ProviderUnavailable {
		t.Fatalf(
			"invalid Pinterest redirect became available: %#v",
			availability[ProviderPinterest],
		)
	}
}

func TestPinterestModuleUsesProviderNeutralCipherWithoutMetaGate(
	t *testing.T,
) {
	for _, environmentKey := range pinterestRuntimeEnvironmentKeys {
		t.Setenv(environmentKey, "")
	}
	key := base64.StdEncoding.EncodeToString(make([]byte, 32))
	module := &Module{
		clock:      func() time.Time { return pinterestFixtureNow },
		repository: NewMemoryRepository(),
		authorizer: &fakeAuthorizer{permissions: map[Permission]bool{
			PermissionViewWorkspace:  true,
			PermissionManageChannels: true,
		}},
		quota: newFakeChannelQuota(),
	}
	values := map[string]string{
		configEnabled:                 "true",
		configMetaEnabled:             "false",
		configCipherKeyID:             "fixture-provider-neutral-key",
		configCipherKey:               key,
		configAllowedOrigins:          "https://app.example.test",
		configPinterestEnabled:        "true",
		configPinterestClientID:       "fixture-client-id",
		configPinterestClientSecret:   "fixture-client-secret",
		configPinterestRedirectURL:    "https://app.example.test/social/callback",
		configPinterestAccessApproved: "true",
		configPinterestAuditVerified:  "true",
	}
	if err := module.Configure(values); err != nil {
		t.Fatal(err)
	}
	bootstrap := module.service.Bootstrap()
	var pinterest ProviderCatalogEntry
	for _, entry := range bootstrap.Catalog {
		if entry.Provider == ProviderPinterest {
			pinterest = entry
			break
		}
	}
	if pinterest.Status != ProviderAvailable ||
		pinterest.ConfigurationState != ProviderReady ||
		!pinterest.Capabilities.Authorization ||
		!pinterest.Capabilities.ResourceSelection ||
		!pinterest.Capabilities.TokenRefresh ||
		pinterest.Capabilities.RemoteRevocation ||
		pinterest.Capabilities.PKCE {
		t.Fatalf("Pinterest runtime catalog = %#v", pinterest)
	}
	encoded, err := json.Marshal(bootstrap)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{
		"fixture-client-secret",
		"fixture-provider-neutral-key",
		key,
	} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("Pinterest bootstrap exposed secret: %s", encoded)
		}
	}
}

func pinterestRuntimeValues(
	base map[string]string,
	overrides map[string]string,
) map[string]string {
	values := make(map[string]string, len(base)+len(overrides))
	for key, value := range base {
		values[key] = value
	}
	for key, value := range overrides {
		values[key] = value
	}
	return values
}
