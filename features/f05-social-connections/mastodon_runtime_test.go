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
				"social.mastodon.enabled":        "true",
				"social.mastodon.audit_verified": "false",
			},
			want: ProviderAuditRequired,
		},
		{
			name: "smoke required",
			input: map[string]string{
				"social.mastodon.enabled":        "true",
				"social.mastodon.audit_verified": "true",
			},
			want: ProviderReviewRequired,
		},
		{
			name: "version required",
			input: map[string]string{
				"social.mastodon.enabled":        "true",
				"social.mastodon.audit_verified": "true",
				"social.mastodon.smoke_verified": "true",
			},
			want: ProviderReviewRequired,
		},
		{
			name: "central contract still blocks activation",
			input: map[string]string{
				"social.mastodon.enabled":               "true",
				"social.mastodon.audit_verified":        "true",
				"social.mastodon.smoke_verified":        "true",
				"social.mastodon.compatibility_version": "4.4",
			},
			want: ProviderNotConfigured,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			adapters := map[Provider]Adapter{}
			availability := map[Provider]ProviderAvailability{}
			configureDecentralizedNetworksRuntime(
				test.input,
				nil,
				adapters,
				availability,
			)
			if len(adapters) != 0 {
				t.Fatal("decentralized runtime mounted an adapter before #324")
			}
			if got := availability[ProviderMastodon]; got.Status != ProviderUnavailable ||
				got.ConfigurationState != test.want {
				t.Fatalf("Mastodon availability = %#v", got)
			}
			if got := availability[ProviderBluesky]; got.Status != ProviderUnavailable {
				t.Fatalf("Bluesky availability = %#v", got)
			}
		})
	}
}
