package socialconnections

import "testing"

func TestDecentralizedRuntimeRequiresAuditSmokeVersionAndCentralContract(
	t *testing.T,
) {
	t.Parallel()
	tests := []struct {
		name  string
		input map[string]string
		want  ProviderConfigurationState
	}{
		{
			name:  "disabled",
			input: map[string]string{},
			want:  ProviderNotConfigured,
		},
		{
			name: "audit required",
			input: map[string]string{
				"social.mastodon.enabled":                "true",
				"social.mastodon.runtime_audit_verified": "false",
				mastodonRuntimeClientIDKey:               "client-id",
				mastodonRuntimeClientSecretKey:           "client-secret",
				mastodonRuntimeRedirectURLKey:            "https://app.example.test/api/v1/social-authorizations/callback",
			},
			want: ProviderAuditRequired,
		},
		{
			name: "smoke required",
			input: map[string]string{
				"social.mastodon.enabled":                "true",
				"social.mastodon.runtime_audit_verified": "true",
				mastodonRuntimeClientIDKey:               "client-id",
				mastodonRuntimeClientSecretKey:           "client-secret",
				mastodonRuntimeRedirectURLKey:            "https://app.example.test/api/v1/social-authorizations/callback",
			},
			want: ProviderAuditRequired,
		},
		{
			name: "version required",
			input: map[string]string{
				"social.mastodon.enabled":                "true",
				"social.mastodon.runtime_audit_verified": "true",
				"social.mastodon.runtime_smoke_verified": "true",
				mastodonRuntimeClientIDKey:               "client-id",
				mastodonRuntimeClientSecretKey:           "client-secret",
				mastodonRuntimeRedirectURLKey:            "https://app.example.test/api/v1/social-authorizations/callback",
			},
			want: ProviderAuditRequired,
		},
		{
			name: "central contract version enables activation",
			input: map[string]string{
				"social.mastodon.enabled":                "true",
				"social.mastodon.runtime_audit_verified": "true",
				"social.mastodon.runtime_smoke_verified": "true",
				"social.mastodon.compatibility_version":  RuntimeDynamicProviderCompatibilityVersion,
				mastodonRuntimeClientIDKey:               "client-id",
				mastodonRuntimeClientSecretKey:           "client-secret",
				mastodonRuntimeRedirectURLKey:            "https://app.example.test/api/v1/social-authorizations/callback",
			},
			want: ProviderReady,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			registry, err := configureRuntimeProviderFamilies(test.input, nil)
			if err != nil {
				t.Fatal(err)
			}
			if len(registry.staticAdapters) != 0 {
				t.Fatalf("static adapters = %#v, want none", registry.staticAdapters)
			}
			if test.want == ProviderReady &&
				registry.dynamicAdapters[ProviderMastodon] == nil {
				t.Fatal("Mastodon dynamic adapter was not registered")
			}
			if got := registry.availability[ProviderMastodon]; got.Status != runtimeStatusForState(test.want) ||
				got.ConfigurationState != test.want {
				t.Fatalf("Mastodon availability = %#v", got)
			}
			if got := registry.availability[ProviderBluesky]; got.Status != ProviderUnavailable {
				t.Fatalf("Bluesky availability = %#v", got)
			}
		})
	}
}

func runtimeStatusForState(
	state ProviderConfigurationState,
) ProviderAvailabilityStatus {
	if state == ProviderReady {
		return ProviderAvailable
	}
	return ProviderUnavailable
}
