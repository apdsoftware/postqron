package socialconnections

import (
	"fmt"
	"os"
	"slices"
	"strings"
)

type legacyRuntimeProviderConfigurer func(
	map[string]string,
	CredentialCipher,
	map[Provider]Adapter,
	map[Provider]ProviderAvailability,
)

type legacyRuntimeDynamicConfigurer func(
	map[string]string,
	CredentialCipher,
	map[Provider]DynamicAdapter,
	map[Provider]ProviderAvailability,
)

type runtimeProviderFamily struct {
	name    string
	static  legacyRuntimeProviderConfigurer
	dynamic legacyRuntimeDynamicConfigurer
}

type runtimeProviderRegistry struct {
	staticAdapters  map[Provider]Adapter
	dynamicAdapters map[Provider]DynamicAdapter
	availability    map[Provider]ProviderAvailability
}

var runtimeProviderFamilies = []runtimeProviderFamily{
	{name: "meta_extensions", static: configureMetaExtensionsRuntime},
	{name: "x", static: configureXRuntime},
	{name: "professional_networks", static: configureProfessionalNetworksRuntime},
	{name: "pinterest", static: configurePinterestRuntime},
	{name: "video_networks", static: configureVideoNetworksRuntime},
	{name: "decentralized_networks", static: configureDecentralizedNetworksRuntime},
}

func newRuntimeProviderRegistry() runtimeProviderRegistry {
	return runtimeProviderRegistry{
		staticAdapters:  make(map[Provider]Adapter, len(SupportedProviders)),
		dynamicAdapters: make(map[Provider]DynamicAdapter, len(SupportedProviders)),
		availability:    make(map[Provider]ProviderAvailability, len(SupportedProviders)),
	}
}

func (registry *runtimeProviderRegistry) RegisterStatic(
	provider Provider,
	adapter Adapter,
) error {
	if registry == nil {
		return fmt.Errorf("%w: runtime provider registry is required", ErrInvalidArgument)
	}
	if !supportedRuntimeProvider(provider) {
		return fmt.Errorf("%w: undeclared runtime provider %q", ErrInvalidArgument, provider)
	}
	if adapter == nil {
		return fmt.Errorf("%w: runtime static adapter %q is nil", ErrInvalidArgument, provider)
	}
	if _, exists := registry.staticAdapters[provider]; exists {
		return fmt.Errorf("%w: duplicate runtime static adapter for %s", ErrInvalidArgument, provider)
	}
	if _, exists := registry.dynamicAdapters[provider]; exists {
		return fmt.Errorf("%w: runtime provider %s cannot mount static and dynamic adapters together", ErrInvalidArgument, provider)
	}
	registry.staticAdapters[provider] = adapter
	return nil
}

func (registry *runtimeProviderRegistry) RegisterDynamic(
	provider Provider,
	adapter DynamicAdapter,
) error {
	if registry == nil {
		return fmt.Errorf("%w: runtime provider registry is required", ErrInvalidArgument)
	}
	if !supportedRuntimeProvider(provider) {
		return fmt.Errorf("%w: undeclared runtime provider %q", ErrInvalidArgument, provider)
	}
	if adapter == nil {
		return fmt.Errorf("%w: runtime dynamic adapter %q is nil", ErrInvalidArgument, provider)
	}
	if _, exists := registry.dynamicAdapters[provider]; exists {
		return fmt.Errorf("%w: duplicate runtime dynamic adapter for %s", ErrInvalidArgument, provider)
	}
	if _, exists := registry.staticAdapters[provider]; exists {
		return fmt.Errorf("%w: runtime provider %s cannot mount static and dynamic adapters together", ErrInvalidArgument, provider)
	}
	registry.dynamicAdapters[provider] = adapter
	return nil
}

func (registry *runtimeProviderRegistry) SetAvailability(
	provider Provider,
	availability ProviderAvailability,
) error {
	if registry == nil {
		return fmt.Errorf("%w: runtime provider registry is required", ErrInvalidArgument)
	}
	if !supportedRuntimeProvider(provider) {
		return fmt.Errorf("%w: undeclared runtime provider %q", ErrInvalidArgument, provider)
	}
	if availability.Provider != "" && availability.Provider != provider {
		return fmt.Errorf("%w: runtime availability provider mismatch for %s", ErrInvalidArgument, provider)
	}
	if _, exists := registry.availability[provider]; exists {
		return fmt.Errorf("%w: duplicate runtime availability for %s", ErrInvalidArgument, provider)
	}
	availability.Provider = provider
	registry.availability[provider] = availability
	return nil
}

func (registry *runtimeProviderRegistry) Merge(
	other runtimeProviderRegistry,
) error {
	for provider, adapter := range other.staticAdapters {
		if err := registry.RegisterStatic(provider, adapter); err != nil {
			return err
		}
	}
	for provider, adapter := range other.dynamicAdapters {
		if err := registry.RegisterDynamic(provider, adapter); err != nil {
			return err
		}
	}
	for provider, availability := range other.availability {
		if err := registry.SetAvailability(provider, availability); err != nil {
			return err
		}
	}
	return nil
}

func configureRuntimeProviderFamilies(
	values map[string]string,
	cipher CredentialCipher,
) (runtimeProviderRegistry, error) {
	registry := newRuntimeProviderRegistry()
	for _, family := range runtimeProviderFamilies {
		staticAdapters := make(map[Provider]Adapter)
		dynamicAdapters := make(map[Provider]DynamicAdapter)
		availability := make(map[Provider]ProviderAvailability)
		if family.static != nil {
			family.static(values, cipher, staticAdapters, availability)
		}
		if family.dynamic != nil {
			family.dynamic(values, cipher, dynamicAdapters, availability)
		}
		for provider := range staticAdapters {
			if _, declared := availability[provider]; !declared {
				return runtimeProviderRegistry{}, fmt.Errorf(
					"%w: runtime provider family %q registered %s without declaring availability",
					ErrInvalidArgument,
					family.name,
					provider,
				)
			}
		}
		for provider := range dynamicAdapters {
			if _, declared := availability[provider]; !declared {
				return runtimeProviderRegistry{}, fmt.Errorf(
					"%w: runtime provider family %q registered %s without declaring availability",
					ErrInvalidArgument,
					family.name,
					provider,
				)
			}
		}
		familyRegistry := newRuntimeProviderRegistry()
		for provider, adapter := range staticAdapters {
			if err := familyRegistry.RegisterStatic(provider, adapter); err != nil {
				return runtimeProviderRegistry{}, fmt.Errorf(
					"runtime provider family %q: %w",
					family.name,
					err,
				)
			}
		}
		for provider, adapter := range dynamicAdapters {
			if err := familyRegistry.RegisterDynamic(provider, adapter); err != nil {
				return runtimeProviderRegistry{}, fmt.Errorf(
					"runtime provider family %q: %w",
					family.name,
					err,
				)
			}
		}
		for provider, entry := range availability {
			if err := familyRegistry.SetAvailability(provider, entry); err != nil {
				return runtimeProviderRegistry{}, fmt.Errorf(
					"runtime provider family %q: %w",
					family.name,
					err,
				)
			}
		}
		if err := registry.Merge(familyRegistry); err != nil {
			return runtimeProviderRegistry{}, fmt.Errorf(
				"runtime provider family %q: %w",
				family.name,
				err,
			)
		}
	}
	return registry, nil
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

func supportedRuntimeProvider(provider Provider) bool {
	return slices.Contains(SupportedProviders, provider)
}
