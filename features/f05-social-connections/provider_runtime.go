package socialconnections

import (
	"os"
	"strings"
)

type runtimeProviderConfigurer func(
	map[string]string,
	CredentialCipher,
	map[Provider]Adapter,
	map[Provider]ProviderAvailability,
)

var runtimeProviderConfigurers = []runtimeProviderConfigurer{
	configureMetaExtensionsRuntime,
	configureXRuntime,
	configureProfessionalNetworksRuntime,
	configurePinterestRuntime,
	configureVideoNetworksRuntime,
	configureDecentralizedNetworksRuntime,
}

func configureRuntimeProviderFamilies(
	values map[string]string,
	cipher CredentialCipher,
	adapters map[Provider]Adapter,
	availability map[Provider]ProviderAvailability,
) {
	for _, configure := range runtimeProviderConfigurers {
		configure(values, cipher, adapters, availability)
	}
}

func runtimeProviderValue(
	values map[string]string,
	configKey, environmentKey string,
) string {
	value := strings.TrimSpace(values[configKey])
	if value != "" {
		return value
	}
	return strings.TrimSpace(os.Getenv(environmentKey))
}
