package socialconnections

// keepMetaNotificationProvidersUnavailable is deliberately defensive. Meta
// exposes no official F5 connection, discovery, verification, or revocation
// lifecycle for Facebook Groups or Instagram personal accounts. Notification
// publishing therefore cannot make either resource operational.
func keepMetaNotificationProvidersUnavailable(
	adapters map[Provider]Adapter,
	availability map[Provider]ProviderAvailability,
) {
	for _, provider := range []Provider{
		ProviderFacebookGroups,
		ProviderInstagramPersonal,
	} {
		delete(adapters, provider)
		availability[provider] = unavailableProvider(
			provider,
			ProviderNotConfigured,
		)
	}
}
