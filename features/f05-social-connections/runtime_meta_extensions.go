package socialconnections

// configureMetaExtensionsRuntime is reserved for Facebook Groups, Instagram
// personal notification publishing, and Threads. It intentionally mounts
// nothing until the Meta-family follow-up has official fixtures and review
// gates.
func configureMetaExtensionsRuntime(
	_ runtimeProviderFamilyInput,
	_ *runtimeProviderFamilyRegistrar,
) error {
	return nil
}
