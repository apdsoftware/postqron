package socialconnections

// configureVideoNetworksRuntime is reserved for TikTok and YouTube. It
// intentionally mounts nothing until both adapters have official fixtures,
// provider review, and quota validation.
func configureVideoNetworksRuntime(
	_ map[string]string,
	_ CredentialCipher,
	_ map[Provider]Adapter,
	_ map[Provider]ProviderAvailability,
) {
}
