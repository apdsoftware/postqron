package socialconnections

import (
	"fmt"
	"os"
	"slices"
	"sort"
	"strings"
)

const RuntimeDynamicProviderCompatibilityVersion = "f05_dynamic_runtime_v1"

type legacyRuntimeProviderConfigurer func(
	map[string]string,
	CredentialCipher,
	map[Provider]Adapter,
	map[Provider]ProviderAvailability,
)

type runtimeProviderFamilyInput struct {
	Values map[string]string
	Cipher CredentialCipher
}

type RuntimeDynamicProviderRegistration struct {
	Provider         Provider
	Adapter          DynamicAdapter
	Configured       bool
	SupportedVersion string
}

type runtimeDynamicProviderAttestations struct {
	Enabled              bool
	Configured           bool
	AuditVerified        bool
	SmokeVerified        bool
	CompatibilityVersion string
	SupportedVersion     string
}

func (attestations runtimeDynamicProviderAttestations) ready() bool {
	return attestations.Enabled &&
		attestations.Configured &&
		attestations.AuditVerified &&
		attestations.SmokeVerified &&
		attestations.CompatibilityVersion != "" &&
		attestations.CompatibilityVersion ==
			attestations.SupportedVersion
}

func (attestations runtimeDynamicProviderAttestations) productionClaimed() bool {
	return attestations.Enabled &&
		attestations.AuditVerified &&
		attestations.SmokeVerified &&
		attestations.CompatibilityVersion != ""
}

func (attestations runtimeDynamicProviderAttestations) availability(
	provider Provider,
) ProviderAvailability {
	entry := ProviderAvailability{
		Provider:           provider,
		Status:             ProviderUnavailable,
		ConfigurationState: ProviderNotConfigured,
		Retryable:          false,
	}
	if !attestations.Enabled || !attestations.Configured {
		return entry
	}
	entry.ConfigurationState = ProviderAuditRequired
	if attestations.ready() {
		entry.Status = ProviderAvailable
		entry.ConfigurationState = ProviderReady
	}
	return entry
}

type runtimeDynamicProviderHook func(
	runtimeProviderFamilyInput,
) ([]RuntimeDynamicProviderRegistration, error)

type runtimeProviderFamily struct {
	name      string
	providers []Provider
	static    legacyRuntimeProviderConfigurer
	dynamic   runtimeDynamicProviderHook
}

type runtimeProviderRegistry struct {
	staticAdapters  map[Provider]Adapter
	dynamicAdapters map[Provider]DynamicAdapter
	availability    map[Provider]ProviderAvailability
}

var decentralizedNetworksRuntimeDynamicHook runtimeDynamicProviderHook

var runtimeProviderFamilies = []runtimeProviderFamily{
	{
		name:      "meta_extensions",
		providers: []Provider{ProviderFacebookGroups, ProviderInstagramPersonal, ProviderThreads},
		static:    configureMetaExtensionsRuntime,
	},
	{
		name:      "x",
		providers: []Provider{ProviderX},
		static:    configureXRuntime,
	},
	{
		name:      "professional_networks",
		providers: []Provider{ProviderLinkedIn, ProviderGoogleBusinessProfile},
		static:    configureProfessionalNetworksRuntime,
	},
	{
		name:      "pinterest",
		providers: []Provider{ProviderPinterest},
		static:    configurePinterestRuntime,
	},
	{
		name:      "video_networks",
		providers: []Provider{ProviderTikTok, ProviderYouTube},
		static:    configureVideoNetworksRuntime,
	},
	{
		name:      "decentralized_networks",
		providers: []Provider{ProviderBluesky, ProviderMastodon},
		static:    configureDecentralizedNetworksRuntime,
		dynamic:   configureDecentralizedNetworksDynamicRuntime,
	},
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
	for _, provider := range sortedStaticProviders(other.staticAdapters) {
		if err := registry.RegisterStatic(provider, other.staticAdapters[provider]); err != nil {
			return err
		}
	}
	for _, provider := range sortedDynamicProviders(other.dynamicAdapters) {
		if err := registry.RegisterDynamic(provider, other.dynamicAdapters[provider]); err != nil {
			return err
		}
	}
	for _, provider := range sortedAvailabilityProviders(other.availability) {
		if err := registry.SetAvailability(provider, other.availability[provider]); err != nil {
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
	input := runtimeProviderFamilyInput{Values: values, Cipher: cipher}
	for _, family := range runtimeProviderFamilies {
		familyRegistry, err := configureRuntimeProviderFamily(family, input)
		if err != nil {
			return runtimeProviderRegistry{}, fmt.Errorf(
				"runtime provider family %q: %w",
				family.name,
				err,
			)
		}
		if err = registry.Merge(familyRegistry); err != nil {
			return runtimeProviderRegistry{}, fmt.Errorf(
				"runtime provider family %q: %w",
				family.name,
				err,
			)
		}
	}
	return registry, nil
}

func configureRuntimeProviderFamily(
	family runtimeProviderFamily,
	input runtimeProviderFamilyInput,
) (runtimeProviderRegistry, error) {
	registry := newRuntimeProviderRegistry()
	staticAdapters := make(map[Provider]Adapter)
	availability := make(map[Provider]ProviderAvailability)
	if family.static != nil {
		family.static(input.Values, input.Cipher, staticAdapters, availability)
	}
	for _, provider := range sortedStaticProviders(staticAdapters) {
		if err := requireRuntimeFamilyOwnership(family, provider); err != nil {
			return runtimeProviderRegistry{}, err
		}
		if _, declared := availability[provider]; !declared {
			return runtimeProviderRegistry{}, fmt.Errorf(
				"%w: runtime provider family %q registered %s without declaring availability",
				ErrInvalidArgument,
				family.name,
				provider,
			)
		}
		if err := registry.RegisterStatic(provider, staticAdapters[provider]); err != nil {
			return runtimeProviderRegistry{}, err
		}
	}
	for _, provider := range sortedAvailabilityProviders(availability) {
		if err := requireRuntimeFamilyOwnership(family, provider); err != nil {
			return runtimeProviderRegistry{}, err
		}
		if err := registry.SetAvailability(provider, availability[provider]); err != nil {
			return runtimeProviderRegistry{}, err
		}
	}
	if family.dynamic == nil {
		return registry, nil
	}
	if err := configureRuntimeDynamicProviders(
		&registry,
		family,
		input,
	); err != nil {
		return runtimeProviderRegistry{}, err
	}
	return registry, nil
}

func configureRuntimeDynamicProviders(
	registry *runtimeProviderRegistry,
	family runtimeProviderFamily,
	input runtimeProviderFamilyInput,
) error {
	registrations, err := family.dynamic(input)
	if err != nil {
		return err
	}
	sort.Slice(registrations, func(left, right int) bool {
		return registrations[left].Provider < registrations[right].Provider
	})
	seen := make(map[Provider]struct{}, len(registrations))
	for _, registration := range registrations {
		if err := requireRuntimeFamilyOwnership(family, registration.Provider); err != nil {
			return err
		}
		if _, exists := seen[registration.Provider]; exists {
			return fmt.Errorf(
				"%w: runtime provider family %q registered duplicate dynamic adapter for %s",
				ErrInvalidArgument,
				family.name,
				registration.Provider,
			)
		}
		seen[registration.Provider] = struct{}{}
		attestations := runtimeDynamicProviderAttestationsFor(
			input.Values,
			registration,
		)
		if err := registry.SetAvailability(
			registration.Provider,
			attestations.availability(registration.Provider),
		); err != nil {
			return err
		}
		if !attestations.ready() {
			if attestations.productionClaimed() &&
				(registration.Adapter == nil || !registration.Configured) {
				return fmt.Errorf(
					"%w: runtime dynamic provider %s is production-gated without a configured adapter",
					ErrInvalidArgument,
					registration.Provider,
				)
			}
			continue
		}
		if err := registry.RegisterDynamic(
			registration.Provider,
			registration.Adapter,
		); err != nil {
			return err
		}
	}
	for _, provider := range family.providers {
		if _, exists := seen[provider]; exists {
			continue
		}
		attestations := runtimeDynamicProviderGate(input.Values, provider)
		if !attestations.Enabled {
			continue
		}
		if attestations.productionClaimed() {
			return fmt.Errorf(
				"%w: runtime dynamic provider %s is production-gated without a configured adapter hook",
				ErrInvalidArgument,
				provider,
			)
		}
		if err := registry.SetAvailability(
			provider,
			attestations.availability(provider),
		); err != nil {
			return err
		}
	}
	return nil
}

func configureDecentralizedNetworksDynamicRuntime(
	input runtimeProviderFamilyInput,
) ([]RuntimeDynamicProviderRegistration, error) {
	if decentralizedNetworksRuntimeDynamicHook == nil {
		return nil, nil
	}
	return decentralizedNetworksRuntimeDynamicHook(input)
}

func runtimeDynamicProviderAttestationsFor(
	values map[string]string,
	registration RuntimeDynamicProviderRegistration,
) runtimeDynamicProviderAttestations {
	attestations := runtimeDynamicProviderGate(values, registration.Provider)
	attestations.Configured = registration.Configured
	attestations.SupportedVersion = strings.TrimSpace(
		registration.SupportedVersion,
	)
	return attestations
}

func runtimeDynamicProviderGate(
	values map[string]string,
	provider Provider,
) runtimeDynamicProviderAttestations {
	switch provider {
	case ProviderMastodon:
		return runtimeDynamicProviderAttestations{
			Enabled: runtimeProviderValue(
				values,
				"social.mastodon.enabled",
				"POSTQRON_F05_MASTODON_ENABLED",
			) == "true",
			AuditVerified: runtimeProviderValue(
				values,
				"social.mastodon.runtime_audit_verified",
				"POSTQRON_F05_MASTODON_RUNTIME_AUDIT_VERIFIED",
			) == "true",
			SmokeVerified: runtimeProviderValue(
				values,
				"social.mastodon.runtime_smoke_verified",
				"POSTQRON_F05_MASTODON_RUNTIME_SMOKE_VERIFIED",
			) == "true",
			CompatibilityVersion: runtimeProviderValue(
				values,
				"social.mastodon.compatibility_version",
				"POSTQRON_F05_MASTODON_COMPATIBILITY_VERSION",
			),
		}
	case ProviderBluesky:
		return runtimeDynamicProviderAttestations{
			Enabled: runtimeProviderValue(
				values,
				"social.bluesky.enabled",
				"POSTQRON_F05_BLUESKY_ENABLED",
			) == "true",
			AuditVerified: runtimeProviderValue(
				values,
				"social.bluesky.runtime_audit_verified",
				"POSTQRON_F05_BLUESKY_RUNTIME_AUDIT_VERIFIED",
			) == "true",
			SmokeVerified: runtimeProviderValue(
				values,
				"social.bluesky.runtime_smoke_verified",
				"POSTQRON_F05_BLUESKY_RUNTIME_SMOKE_VERIFIED",
			) == "true",
			CompatibilityVersion: runtimeProviderValue(
				values,
				"social.bluesky.compatibility_version",
				"POSTQRON_F05_BLUESKY_COMPATIBILITY_VERSION",
			),
		}
	default:
		return runtimeDynamicProviderAttestations{}
	}
}

func requireRuntimeFamilyOwnership(
	family runtimeProviderFamily,
	provider Provider,
) error {
	if !supportedRuntimeProvider(provider) {
		return fmt.Errorf(
			"%w: undeclared runtime provider %q",
			ErrInvalidArgument,
			provider,
		)
	}
	if slices.Contains(family.providers, provider) {
		return nil
	}
	return fmt.Errorf(
		"%w: runtime provider family %q does not own %s",
		ErrInvalidArgument,
		family.name,
		provider,
	)
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

func sortedStaticProviders(values map[Provider]Adapter) []Provider {
	providers := make([]Provider, 0, len(values))
	for provider := range values {
		providers = append(providers, provider)
	}
	sort.Slice(providers, func(left, right int) bool {
		return providers[left] < providers[right]
	})
	return providers
}

func sortedDynamicProviders(values map[Provider]DynamicAdapter) []Provider {
	providers := make([]Provider, 0, len(values))
	for provider := range values {
		providers = append(providers, provider)
	}
	sort.Slice(providers, func(left, right int) bool {
		return providers[left] < providers[right]
	})
	return providers
}

func sortedAvailabilityProviders(
	values map[Provider]ProviderAvailability,
) []Provider {
	providers := make([]Provider, 0, len(values))
	for provider := range values {
		providers = append(providers, provider)
	}
	sort.Slice(providers, func(left, right int) bool {
		return providers[left] < providers[right]
	})
	return providers
}
