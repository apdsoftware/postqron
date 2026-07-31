package socialconnections

// configureMetaExtensionsRuntime is reserved for Facebook Groups, Instagram
// personal notification publishing, and Threads. It intentionally mounts
// nothing until the Meta-family follow-up has official fixtures and review
// gates.
func configureMetaExtensionsRuntime(
	_ map[string]string,
	_ CredentialCipher,
	_ map[Provider]Adapter,
	_ map[Provider]ProviderAvailability,
) {
}
