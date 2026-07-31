package socialconnections

// configureProfessionalNetworksRuntime is reserved for LinkedIn and Google
// Business Profile. It intentionally mounts nothing until both adapters have
// official fixtures and review gates.
func configureProfessionalNetworksRuntime(
	_ map[string]string,
	_ CredentialCipher,
	_ map[Provider]Adapter,
	_ map[Provider]ProviderAvailability,
) {
}
