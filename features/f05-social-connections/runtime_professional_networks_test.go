package socialconnections

import "testing"

func TestProfessionalNetworksRuntimeGatesAreFailClosed(t *testing.T) {
	key := make([]byte, 32)
	cipher, err := NewAESGCMCipher(
		"fixture-key",
		key,
	)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name     string
		values   map[string]string
		cipher   CredentialCipher
		provider Provider
		state    ProviderConfigurationState
		ready    bool
	}{
		{
			name:     "linkedin_missing_credentials",
			values:   map[string]string{configLinkedInEnabled: "true"},
			cipher:   cipher,
			provider: ProviderLinkedIn,
			state:    ProviderNotConfigured,
		},
		{
			name: "linkedin_review",
			values: professionalLinkedInFixture(map[string]string{
				configLinkedInReviewed: "false",
			}),
			cipher:   cipher,
			provider: ProviderLinkedIn,
			state:    ProviderReviewRequired,
		},
		{
			name: "linkedin_audit",
			values: professionalLinkedInFixture(map[string]string{
				configLinkedInAudited: "false",
			}),
			cipher:   cipher,
			provider: ProviderLinkedIn,
			state:    ProviderAuditRequired,
		},
		{
			name: "linkedin_smoke",
			values: professionalLinkedInFixture(map[string]string{
				configLinkedInSmoke: "false",
			}),
			cipher:   cipher,
			provider: ProviderLinkedIn,
			state:    ProviderAuditRequired,
		},
		{
			name:     "linkedin_central_cipher_dependency",
			values:   professionalLinkedInFixture(nil),
			provider: ProviderLinkedIn,
			state:    ProviderNotConfigured,
		},
		{
			name:     "linkedin_ready",
			values:   professionalLinkedInFixture(nil),
			cipher:   cipher,
			provider: ProviderLinkedIn,
			state:    ProviderReady,
			ready:    true,
		},
		{
			name: "google_review",
			values: professionalGoogleFixture(map[string]string{
				configGoogleGBPAccess: "false",
			}),
			cipher:   cipher,
			provider: ProviderGoogleBusinessProfile,
			state:    ProviderReviewRequired,
		},
		{
			name: "google_audit",
			values: professionalGoogleFixture(map[string]string{
				configGoogleGBPAudited: "false",
			}),
			cipher:   cipher,
			provider: ProviderGoogleBusinessProfile,
			state:    ProviderAuditRequired,
		},
		{
			name: "google_smoke",
			values: professionalGoogleFixture(map[string]string{
				configGoogleGBPSmoke: "false",
			}),
			cipher:   cipher,
			provider: ProviderGoogleBusinessProfile,
			state:    ProviderAuditRequired,
		},
		{
			name:     "google_central_cipher_dependency",
			values:   professionalGoogleFixture(nil),
			provider: ProviderGoogleBusinessProfile,
			state:    ProviderNotConfigured,
		},
		{
			name:     "google_ready",
			values:   professionalGoogleFixture(nil),
			cipher:   cipher,
			provider: ProviderGoogleBusinessProfile,
			state:    ProviderReady,
			ready:    true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			adapters := make(map[Provider]Adapter)
			availability := professionalAvailabilityFixture()
			configureProfessionalNetworksRuntime(
				test.values,
				test.cipher,
				adapters,
				availability,
			)
			got := availability[test.provider]
			if got.ConfigurationState != test.state {
				t.Fatalf("availability = %#v", got)
			}
			if (adapters[test.provider] != nil) != test.ready ||
				(got.Status == ProviderAvailable) != test.ready {
				t.Fatalf("adapter=%T availability=%#v", adapters[test.provider], got)
			}
		})
	}
}

func TestProfessionalNetworksRuntimeReportsOnlyRealCapabilities(t *testing.T) {
	cipher, err := NewAESGCMCipher("fixture-key", make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	values := professionalLinkedInFixture(map[string]string{
		configLinkedInRefresh: "false",
	})
	for key, value := range professionalGoogleFixture(nil) {
		values[key] = value
	}
	adapters := make(map[Provider]Adapter)
	availability := professionalAvailabilityFixture()
	configureProfessionalNetworksRuntime(values, cipher, adapters, availability)
	linkedIn := adapterCapabilities(adapters[ProviderLinkedIn])
	if linkedIn.TokenRefresh || linkedIn.RemoteRevocation || linkedIn.PKCE {
		t.Fatalf("LinkedIn capabilities = %#v", linkedIn)
	}
	google := adapterCapabilities(adapters[ProviderGoogleBusinessProfile])
	if !google.TokenRefresh || google.RemoteRevocation || google.PKCE {
		t.Fatalf("Google capabilities = %#v", google)
	}
	values[configLinkedInRefresh] = "true"
	adapters = make(map[Provider]Adapter)
	configureProfessionalNetworksRuntime(
		values,
		cipher,
		adapters,
		professionalAvailabilityFixture(),
	)
	if !adapterCapabilities(adapters[ProviderLinkedIn]).TokenRefresh {
		t.Fatal("approved LinkedIn programmatic refresh was not reported")
	}
}

func professionalLinkedInFixture(overrides map[string]string) map[string]string {
	values := map[string]string{
		configLinkedInEnabled:  "true",
		configLinkedInID:       "fixture-client",
		configLinkedInSecret:   "fixture-secret",
		configLinkedInRedirect: "https://api.example.test/social/callback",
		configLinkedInVersion:  "202607",
		configLinkedInReviewed: "true",
		configLinkedInAudited:  "true",
		configLinkedInSmoke:    "true",
	}
	for key, value := range overrides {
		values[key] = value
	}
	return values
}

func professionalGoogleFixture(overrides map[string]string) map[string]string {
	values := map[string]string{
		configGoogleGBPEnabled:  "true",
		configGoogleGBPID:       "fixture-client",
		configGoogleGBPSecret:   "fixture-secret",
		configGoogleGBPRedirect: "https://api.example.test/social/callback",
		configGoogleGBPAccess:   "true",
		configGoogleGBPReviewed: "true",
		configGoogleGBPAudited:  "true",
		configGoogleGBPSmoke:    "true",
	}
	for key, value := range overrides {
		values[key] = value
	}
	return values
}

func professionalAvailabilityFixture() map[Provider]ProviderAvailability {
	availability := make(map[Provider]ProviderAvailability)
	for _, provider := range SupportedProviders {
		availability[provider] = unavailableProvider(
			provider,
			ProviderNotConfigured,
		)
	}
	return availability
}
