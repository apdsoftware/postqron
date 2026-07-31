package socialconnections

// configureXRuntime intentionally mounts nothing until the X follow-up has
// official fixtures and audit gates.
func configureXRuntime(
	_ runtimeProviderFamilyInput,
	_ *runtimeProviderFamilyRegistrar,
) error {
	return nil
}
