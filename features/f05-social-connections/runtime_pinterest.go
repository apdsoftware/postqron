package socialconnections

// configurePinterestRuntime intentionally mounts nothing until the Pinterest
// follow-up has official fixtures and audit gates.
func configurePinterestRuntime(
	_ runtimeProviderFamilyInput,
	_ *runtimeProviderFamilyRegistrar,
) error {
	return nil
}
