package socialconnections

import "strings"

// configureDecentralizedNetworksRuntime keeps both providers fail-closed until
// #324 extends the central attempt and authenticated-request boundaries. The
// provider-specific clients are complete enough for offline verification, but
// mounting them through the static Adapter interface would lose dynamic
// instance/PDS state and Bluesky DPoP key/nonce state.
func configureDecentralizedNetworksRuntime(
	values map[string]string,
	_ CredentialCipher,
	_ map[Provider]Adapter,
	availability map[Provider]ProviderAvailability,
) {
	availability[ProviderMastodon] = decentralizedAvailability(
		ProviderMastodon,
		runtimeProviderValue(
			values,
			"social.mastodon.enabled",
			"POSTQRON_F05_MASTODON_ENABLED",
		),
		runtimeProviderValue(
			values,
			"social.mastodon.audit_verified",
			"POSTQRON_F05_MASTODON_AUDIT_VERIFIED",
		),
		runtimeProviderValue(
			values,
			"social.mastodon.smoke_verified",
			"POSTQRON_F05_MASTODON_SMOKE_VERIFIED",
		),
		runtimeProviderValue(
			values,
			"social.mastodon.compatibility_version",
			"POSTQRON_F05_MASTODON_COMPATIBILITY_VERSION",
		),
	)
	availability[ProviderBluesky] = decentralizedAvailability(
		ProviderBluesky,
		runtimeProviderValue(
			values,
			"social.bluesky.enabled",
			"POSTQRON_F05_BLUESKY_ENABLED",
		),
		runtimeProviderValue(
			values,
			"social.bluesky.audit_verified",
			"POSTQRON_F05_BLUESKY_AUDIT_VERIFIED",
		),
		runtimeProviderValue(
			values,
			"social.bluesky.smoke_verified",
			"POSTQRON_F05_BLUESKY_SMOKE_VERIFIED",
		),
		runtimeProviderValue(
			values,
			"social.bluesky.compatibility_version",
			"POSTQRON_F05_BLUESKY_COMPATIBILITY_VERSION",
		),
	)
}

func decentralizedAvailability(
	provider Provider,
	enabled, audit, smoke, compatibility string,
) ProviderAvailability {
	result := ProviderAvailability{
		Provider:           provider,
		Status:             ProviderUnavailable,
		ConfigurationState: ProviderNotConfigured,
		Retryable:          false,
	}
	if !decentralizedFlag(enabled) {
		return result
	}
	if strings.TrimSpace(audit) == "" {
		return result
	}
	if !decentralizedFlag(audit) {
		result.ConfigurationState = ProviderAuditRequired
		return result
	}
	if !decentralizedFlag(smoke) ||
		strings.TrimSpace(compatibility) == "" {
		result.ConfigurationState = ProviderReviewRequired
		return result
	}
	// Even with explicit operator attestations, activation remains blocked by
	// #324. Reporting not_configured is deliberate: ready/available would make
	// the browser believe a conforming central flow exists.
	return result
}

func decentralizedFlag(value string) bool {
	return strings.EqualFold(strings.TrimSpace(value), "true")
}
