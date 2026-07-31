package socialconnections

import "testing"

func TestMetaNotificationResourcesCannotMountUnverifiedAdapters(t *testing.T) {
	adapters := map[Provider]Adapter{
		ProviderFacebookGroups:    nil,
		ProviderInstagramPersonal: nil,
	}
	availability := map[Provider]ProviderAvailability{
		ProviderFacebookGroups: {
			Provider:           ProviderFacebookGroups,
			Status:             ProviderAvailable,
			ConfigurationState: ProviderReady,
		},
		ProviderInstagramPersonal: {
			Provider:           ProviderInstagramPersonal,
			Status:             ProviderAvailable,
			ConfigurationState: ProviderReady,
		},
	}

	configureMetaExtensionsRuntime(
		map[string]string{},
		nil,
		adapters,
		availability,
	)

	for _, provider := range []Provider{
		ProviderFacebookGroups,
		ProviderInstagramPersonal,
	} {
		if _, mounted := adapters[provider]; mounted {
			t.Fatalf("%s adapter was mounted", provider)
		}
		got := availability[provider]
		if got.Status != ProviderUnavailable ||
			got.ConfigurationState != ProviderNotConfigured ||
			got.Retryable {
			t.Fatalf("%s availability = %#v", provider, got)
		}
	}
}
