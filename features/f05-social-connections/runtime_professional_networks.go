package socialconnections

// configureProfessionalNetworksRuntime is reserved for LinkedIn and Google
// Business Profile. It intentionally mounts nothing until both adapters have
// official fixtures and review gates.
func configureProfessionalNetworksRuntime(
	_ runtimeProviderFamilyInput,
	_ *runtimeProviderFamilyRegistrar,
) error {
	return nil
}
