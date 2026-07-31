package socialconnections

const (
	configTikTokEnabled        = "social.tiktok.enabled"
	configTikTokClientKey      = "social.tiktok.client_key"
	configTikTokClientSecret   = "social.tiktok.client_secret"
	configTikTokRedirect       = "social.tiktok.redirect_url"
	configTikTokReviewed       = "social.tiktok.content_posting_review_approved"
	configTikTokAudited        = "social.tiktok.content_posting_audit_approved"
	configTikTokSmokeVerified  = "social.tiktok.runtime_smoke_verified"
	configTikTokAccessVerified = "social.tiktok.quota_access_confirmed"

	configYouTubeEnabled        = "social.youtube.enabled"
	configYouTubeClientID       = "social.youtube.client_id"
	configYouTubeClientSecret   = "social.youtube.client_secret"
	configYouTubeRedirect       = "social.youtube.redirect_url"
	configYouTubeVerified       = "social.youtube.oauth_verification_approved"
	configYouTubeAudited        = "social.youtube.api_audit_approved"
	configYouTubeSmokeVerified  = "social.youtube.runtime_smoke_verified"
	configYouTubeAccessVerified = "social.youtube.quota_access_confirmed"
)

func init() {
	videoNetworkEnvironmentKeys := map[string]string{
		configTikTokEnabled:         "POSTQRON_F05_TIKTOK_ENABLED",
		configTikTokClientKey:       "POSTQRON_F05_TIKTOK_CLIENT_KEY",
		configTikTokClientSecret:    "POSTQRON_F05_TIKTOK_CLIENT_SECRET",
		configTikTokRedirect:        "POSTQRON_F05_TIKTOK_REDIRECT_URL",
		configTikTokReviewed:        "POSTQRON_F05_TIKTOK_CONTENT_POSTING_REVIEW_APPROVED",
		configTikTokAudited:         "POSTQRON_F05_TIKTOK_CONTENT_POSTING_AUDIT_APPROVED",
		configTikTokSmokeVerified:   "POSTQRON_F05_TIKTOK_RUNTIME_SMOKE_VERIFIED",
		configTikTokAccessVerified:  "POSTQRON_F05_TIKTOK_QUOTA_ACCESS_CONFIRMED",
		configYouTubeEnabled:        "POSTQRON_F05_YOUTUBE_ENABLED",
		configYouTubeClientID:       "POSTQRON_F05_YOUTUBE_CLIENT_ID",
		configYouTubeClientSecret:   "POSTQRON_F05_YOUTUBE_CLIENT_SECRET",
		configYouTubeRedirect:       "POSTQRON_F05_YOUTUBE_REDIRECT_URL",
		configYouTubeVerified:       "POSTQRON_F05_YOUTUBE_OAUTH_VERIFICATION_APPROVED",
		configYouTubeAudited:        "POSTQRON_F05_YOUTUBE_API_AUDIT_APPROVED",
		configYouTubeSmokeVerified:  "POSTQRON_F05_YOUTUBE_RUNTIME_SMOKE_VERIFIED",
		configYouTubeAccessVerified: "POSTQRON_F05_YOUTUBE_QUOTA_ACCESS_CONFIRMED",
	}
	for key, environmentKey := range videoNetworkEnvironmentKeys {
		runtimeEnvironmentKeys[key] = environmentKey
	}
}

// configureVideoNetworksRuntime mounts only adapters whose credentials and
// provider-side approval gates have all been explicitly confirmed. Client-safe
// states deliberately do not reveal which credential or operational check is
// missing.
func configureVideoNetworksRuntime(
	values map[string]string,
	cipher CredentialCipher,
	adapters map[Provider]Adapter,
	availability map[Provider]ProviderAvailability,
) {
	configureTikTokRuntime(values, cipher, adapters, availability)
	configureYouTubeRuntime(values, cipher, adapters, availability)
}

func configureTikTokRuntime(
	values map[string]string,
	cipher CredentialCipher,
	adapters map[Provider]Adapter,
	availability map[Provider]ProviderAvailability,
) {
	if values[configTikTokEnabled] != "true" {
		return
	}
	if cipher == nil || !allPresent(
		values,
		configTikTokClientKey,
		configTikTokClientSecret,
		configTikTokRedirect,
	) {
		return
	}
	if values[configTikTokReviewed] != "true" {
		availability[ProviderTikTok] = unavailableProvider(
			ProviderTikTok,
			ProviderReviewRequired,
		)
		return
	}
	if values[configTikTokAudited] != "true" ||
		values[configTikTokSmokeVerified] != "true" {
		availability[ProviderTikTok] = unavailableProvider(
			ProviderTikTok,
			ProviderAuditRequired,
		)
		return
	}
	if values[configTikTokAccessVerified] != "true" {
		availability[ProviderTikTok] = unavailableProvider(
			ProviderTikTok,
			ProviderReviewRequired,
		)
		return
	}
	adapter, err := NewTikTokAdapter(TikTokAdapterConfig{
		ClientKey:    values[configTikTokClientKey],
		ClientSecret: values[configTikTokClientSecret],
		RedirectURL:  values[configTikTokRedirect],
	})
	if err != nil {
		return
	}
	adapters[ProviderTikTok] = adapter
	availability[ProviderTikTok] = ProviderAvailability{
		Provider:           ProviderTikTok,
		Status:             ProviderAvailable,
		ConfigurationState: ProviderReady,
	}
}

func configureYouTubeRuntime(
	values map[string]string,
	cipher CredentialCipher,
	adapters map[Provider]Adapter,
	availability map[Provider]ProviderAvailability,
) {
	if values[configYouTubeEnabled] != "true" {
		return
	}
	if cipher == nil || !allPresent(
		values,
		configYouTubeClientID,
		configYouTubeClientSecret,
		configYouTubeRedirect,
	) {
		return
	}
	if values[configYouTubeVerified] != "true" {
		availability[ProviderYouTube] = unavailableProvider(
			ProviderYouTube,
			ProviderReviewRequired,
		)
		return
	}
	if values[configYouTubeAudited] != "true" ||
		values[configYouTubeSmokeVerified] != "true" {
		availability[ProviderYouTube] = unavailableProvider(
			ProviderYouTube,
			ProviderAuditRequired,
		)
		return
	}
	if values[configYouTubeAccessVerified] != "true" {
		availability[ProviderYouTube] = unavailableProvider(
			ProviderYouTube,
			ProviderReviewRequired,
		)
		return
	}
	adapter, err := NewYouTubeAdapter(YouTubeAdapterConfig{
		ClientID:     values[configYouTubeClientID],
		ClientSecret: values[configYouTubeClientSecret],
		RedirectURL:  values[configYouTubeRedirect],
	})
	if err != nil {
		return
	}
	adapters[ProviderYouTube] = adapter
	availability[ProviderYouTube] = ProviderAvailability{
		Provider:           ProviderYouTube,
		Status:             ProviderAvailable,
		ConfigurationState: ProviderReady,
	}
}
