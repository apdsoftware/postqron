package socialconnections

const (
	configXEnabled        = "social.x.enabled"
	configXClientID       = "social.x.client_id"
	configXClientSecret   = "social.x.client_secret"
	configXRedirectURL    = "social.x.redirect_url"
	configXAccessApproved = "social.x.api_access_approved"
	configXAuditVerified  = "social.x.runtime_audit_verified"
	configXSmokeVerified  = "social.x.smoke_test_verified"
)

var xRuntimeEnvironmentKeys = map[string]string{
	configXEnabled:        "POSTQRON_F05_X_ENABLED",
	configXClientID:       "POSTQRON_F05_X_CLIENT_ID",
	configXClientSecret:   "POSTQRON_F05_X_CLIENT_SECRET",
	configXRedirectURL:    "POSTQRON_F05_X_REDIRECT_URL",
	configXAccessApproved: "POSTQRON_F05_X_API_ACCESS_APPROVED",
	configXAuditVerified:  "POSTQRON_F05_X_RUNTIME_AUDIT_VERIFIED",
	configXSmokeVerified:  "POSTQRON_F05_X_SMOKE_TEST_VERIFIED",
}

func configureXRuntime(
	values map[string]string,
	cipher CredentialCipher,
	adapters map[Provider]Adapter,
	availability map[Provider]ProviderAvailability,
) {
	if xRuntimeValue(values, configXEnabled) != "true" || cipher == nil {
		return
	}
	if xRuntimeValue(values, configXClientID) == "" ||
		xRuntimeValue(values, configXClientSecret) == "" ||
		xRuntimeValue(values, configXRedirectURL) == "" {
		return
	}
	if xRuntimeValue(values, configXAccessApproved) != "true" {
		availability[ProviderX] = unavailableProvider(
			ProviderX,
			ProviderReviewRequired,
		)
		return
	}
	if xRuntimeValue(values, configXAuditVerified) != "true" ||
		xRuntimeValue(values, configXSmokeVerified) != "true" {
		availability[ProviderX] = unavailableProvider(
			ProviderX,
			ProviderAuditRequired,
		)
		return
	}
	adapter, err := NewXAdapter(XAdapterConfig{
		ClientID:     xRuntimeValue(values, configXClientID),
		ClientSecret: xRuntimeValue(values, configXClientSecret),
		RedirectURL:  xRuntimeValue(values, configXRedirectURL),
	})
	if err != nil {
		return
	}
	adapters[ProviderX] = adapter
	availability[ProviderX] = ProviderAvailability{
		Provider:           ProviderX,
		Status:             ProviderAvailable,
		ConfigurationState: ProviderReady,
	}
}

func xRuntimeValue(values map[string]string, key string) string {
	return runtimeProviderValue(values, key, xRuntimeEnvironmentKeys[key])
}
