package socialconnections

// configureDecentralizedNetworksRuntime is reserved for Mastodon and Bluesky.
// It intentionally mounts nothing until instance/PDS discovery, SSRF
// hardening, DPoP where required, and complete fixtures are verified.
func configureDecentralizedNetworksRuntime(
	_ map[string]string,
	_ CredentialCipher,
	_ map[Provider]Adapter,
	_ map[Provider]ProviderAvailability,
) {
}
