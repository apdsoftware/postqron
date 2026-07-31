package socialconnections

const (
	configThreadsEnabled  = "social.meta.threads.enabled"
	configThreadsID       = "social.meta.threads.client_id"
	configThreadsSecret   = "social.meta.threads.client_secret"
	configThreadsRedirect = "social.meta.threads.redirect_url"
	configThreadsReviewed = "social.meta.threads.app_review_approved"
	configThreadsAudited  = "social.meta.threads.runtime_audit_verified"
)

var threadsRuntimeEnvironmentKeys = map[string]string{
	configThreadsEnabled:  "POSTQRON_F05_THREADS_ENABLED",
	configThreadsID:       "POSTQRON_F05_THREADS_CLIENT_ID",
	configThreadsSecret:   "POSTQRON_F05_THREADS_CLIENT_SECRET",
	configThreadsRedirect: "POSTQRON_F05_THREADS_REDIRECT_URL",
	configThreadsReviewed: "POSTQRON_F05_THREADS_APP_REVIEW_APPROVED",
	configThreadsAudited:  "POSTQRON_F05_THREADS_RUNTIME_AUDIT_VERIFIED",
}

// configureMetaExtensionsRuntime keeps notification-only Meta resources
// unavailable and mounts Threads only after credentials, App Review, and the
// runtime audit have each passed their independent fail-closed gate.
func configureMetaExtensionsRuntime(
	values map[string]string,
	cipher CredentialCipher,
	adapters map[Provider]Adapter,
	availability map[Provider]ProviderAvailability,
) {
	keepMetaNotificationProvidersUnavailable(adapters, availability)

	threadsValues := make(map[string]string, len(threadsRuntimeEnvironmentKeys))
	for configKey, environmentKey := range threadsRuntimeEnvironmentKeys {
		threadsValues[configKey] = runtimeProviderValue(
			values,
			configKey,
			environmentKey,
		)
	}
	if threadsValues[configThreadsEnabled] != "true" ||
		cipher == nil ||
		!allPresent(
			threadsValues,
			configThreadsID,
			configThreadsSecret,
			configThreadsRedirect,
		) {
		return
	}
	if threadsValues[configThreadsReviewed] != "true" {
		availability[ProviderThreads] = unavailableProvider(
			ProviderThreads,
			ProviderReviewRequired,
		)
		return
	}
	if threadsValues[configThreadsAudited] != "true" {
		availability[ProviderThreads] = unavailableProvider(
			ProviderThreads,
			ProviderAuditRequired,
		)
		return
	}
	adapter, err := NewThreadsAdapter(ThreadsAdapterConfig{
		ClientID:     threadsValues[configThreadsID],
		ClientSecret: threadsValues[configThreadsSecret],
		RedirectURL:  threadsValues[configThreadsRedirect],
	})
	if err != nil {
		return
	}
	adapters[ProviderThreads] = adapter
	availability[ProviderThreads] = ProviderAvailability{
		Provider:           ProviderThreads,
		Status:             ProviderAvailable,
		ConfigurationState: ProviderReady,
	}
}
