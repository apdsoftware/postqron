package socialconnections

import "strings"

const (
	configPinterestEnabled        = "social.pinterest.enabled"
	configPinterestClientID       = "social.pinterest.client_id"
	configPinterestClientSecret   = "social.pinterest.client_secret"
	configPinterestRedirectURL    = "social.pinterest.redirect_url"
	configPinterestAccessApproved = "social.pinterest.access_approved"
	configPinterestAuditVerified  = "social.pinterest.runtime_audit_verified"
)

var pinterestRuntimeEnvironmentKeys = map[string]string{
	configPinterestEnabled:        "POSTQRON_F05_PINTEREST_ENABLED",
	configPinterestClientID:       "POSTQRON_F05_PINTEREST_CLIENT_ID",
	configPinterestClientSecret:   "POSTQRON_F05_PINTEREST_CLIENT_SECRET",
	configPinterestRedirectURL:    "POSTQRON_F05_PINTEREST_REDIRECT_URL",
	configPinterestAccessApproved: "POSTQRON_F05_PINTEREST_ACCESS_APPROVED",
	configPinterestAuditVerified:  "POSTQRON_F05_PINTEREST_RUNTIME_AUDIT_VERIFIED",
}

func configurePinterestRuntime(
	values map[string]string,
	cipher CredentialCipher,
	adapters map[Provider]Adapter,
	availability map[Provider]ProviderAvailability,
) {
	pinterestValues := make(map[string]string, len(pinterestRuntimeEnvironmentKeys))
	for key, environmentKey := range pinterestRuntimeEnvironmentKeys {
		pinterestValues[key] = runtimeProviderValue(values, key, environmentKey)
	}
	if pinterestValues[configPinterestEnabled] != "true" {
		return
	}
	if strings.TrimSpace(pinterestValues[configPinterestClientID]) == "" ||
		strings.TrimSpace(pinterestValues[configPinterestClientSecret]) == "" ||
		strings.TrimSpace(pinterestValues[configPinterestRedirectURL]) == "" {
		return
	}
	if pinterestValues[configPinterestAccessApproved] != "true" {
		availability[ProviderPinterest] = unavailableProvider(
			ProviderPinterest,
			ProviderReviewRequired,
		)
		return
	}
	if pinterestValues[configPinterestAuditVerified] != "true" {
		availability[ProviderPinterest] = unavailableProvider(
			ProviderPinterest,
			ProviderAuditRequired,
		)
		return
	}
	if cipher == nil {
		// The provider-neutral cipher comes only from the #318/#321 central
		// bootstrap. Pinterest never falls back to the legacy Meta gate.
		return
	}
	adapter, err := NewPinterestAdapter(PinterestAdapterConfig{
		ClientID:     pinterestValues[configPinterestClientID],
		ClientSecret: pinterestValues[configPinterestClientSecret],
		RedirectURL:  pinterestValues[configPinterestRedirectURL],
	})
	if err != nil {
		return
	}
	adapters[ProviderPinterest] = adapter
	availability[ProviderPinterest] = ProviderAvailability{
		Provider:           ProviderPinterest,
		Status:             ProviderAvailable,
		ConfigurationState: ProviderReady,
	}
}
