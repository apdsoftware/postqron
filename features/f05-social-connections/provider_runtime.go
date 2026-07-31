package socialconnections

import (
	"fmt"
	"os"
	"slices"
	"sort"
	"strings"
)

const RuntimeDynamicProviderCompatibilityVersion = "f05_dynamic_runtime_v1"

type runtimeProviderFamilyInput struct {
	Values map[string]string
	Cipher CredentialCipher
}

type runtimeProviderFamilyConfigurer func(
	runtimeProviderFamilyInput,
	*runtimeProviderFamilyRegistrar,
) error

// RuntimeDynamicProviderRegistration is the central hook consumed by provider
// family implementations such as #314. The runtime registry computes
// availability itself from typed attestations and never trusts a provider to
// mark itself available/ready directly.
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

type runtimeProviderFamily struct {
	name      string
	providers []Provider
	configure runtimeProviderFamilyConfigurer
}

type runtimeProviderRegistry struct {
	staticAdapters  map[Provider]Adapter
	dynamicAdapters map[Provider]DynamicAdapter
	availability    map[Provider]ProviderAvailability
}

type runtimeProviderFamilyRegistrar struct {
	family           runtimeProviderFamily
	registry         runtimeProviderRegistry
	staticSeen       map[Provider]struct{}
	dynamicSeen      map[Provider]struct{}
	availabilitySeen map[Provider]struct{}
}

var runtimeProviderFamilies = []runtimeProviderFamily{
	{
		name:      "meta_extensions",
		providers: []Provider{ProviderFacebookGroups, ProviderInstagramPersonal, ProviderThreads},
		configure: configureMetaExtensionsRuntime,
	},
	{
		name:      "x",
		providers: []Provider{ProviderX},
		configure: configureXRuntime,
	},
	{
		name:      "professional_networks",
		providers: []Provider{ProviderLinkedIn, ProviderGoogleBusinessProfile},
		configure: configureProfessionalNetworksRuntime,
	},
	{
		name:      "pinterest",
		providers: []Provider{ProviderPinterest},
		configure: configurePinterestRuntime,
	},
	{
		name:      "video_networks",
		providers: []Provider{ProviderTikTok, ProviderYouTube},
		configure: configureVideoNetworksRuntime,
	},
	{
		name:      "decentralized_networks",
		providers: []Provider{ProviderBluesky, ProviderMastodon},
		configure: configureDecentralizedNetworksRuntime,
	},
}

func newRuntimeProviderRegistry() runtimeProviderRegistry {
	return runtimeProviderRegistry{
		staticAdapters:  make(map[Provider]Adapter, len(SupportedProviders)),
		dynamicAdapters: make(map[Provider]DynamicAdapter, len(SupportedProviders)),
		availability:    make(map[Provider]ProviderAvailability, len(SupportedProviders)),
	}
}

func newRuntimeProviderFamilyRegistrar(
	family runtimeProviderFamily,
) *runtimeProviderFamilyRegistrar {
	return &runtimeProviderFamilyRegistrar{
		family:           family,
		registry:         newRuntimeProviderRegistry(),
		staticSeen:       make(map[Provider]struct{}, len(family.providers)),
		dynamicSeen:      make(map[Provider]struct{}, len(family.providers)),
		availabilitySeen: make(map[Provider]struct{}, len(family.providers)),
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

func (registrar *runtimeProviderFamilyRegistrar) RegisterStatic(
	provider Provider,
	adapter Adapter,
	availability ProviderAvailability,
) error {
	if registrar == nil {
		return fmt.Errorf("%w: runtime provider family registrar is required", ErrInvalidArgument)
	}
	if err := registrar.requireOwnedProvider(provider); err != nil {
		return err
	}
	if _, exists := registrar.staticSeen[provider]; exists {
		return fmt.Errorf("%w: runtime provider family %q registered duplicate static adapter for %s", ErrInvalidArgument, registrar.family.name, provider)
	}
	registrar.staticSeen[provider] = struct{}{}
	if err := registrar.registry.RegisterStatic(provider, adapter); err != nil {
		return err
	}
	return registrar.setAvailability(provider, availability)
}

func (registrar *runtimeProviderFamilyRegistrar) RegisterDynamic(
	registration RuntimeDynamicProviderRegistration,
	attestations runtimeDynamicProviderAttestations,
) error {
	if registrar == nil {
		return fmt.Errorf("%w: runtime provider family registrar is required", ErrInvalidArgument)
	}
	if err := registrar.requireOwnedProvider(registration.Provider); err != nil {
		return err
	}
	if _, exists := registrar.dynamicSeen[registration.Provider]; exists {
		return fmt.Errorf("%w: runtime provider family %q registered duplicate dynamic adapter for %s", ErrInvalidArgument, registrar.family.name, registration.Provider)
	}
	registrar.dynamicSeen[registration.Provider] = struct{}{}
	if err := registrar.setAvailability(
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
		return nil
	}
	return registrar.registry.RegisterDynamic(
		registration.Provider,
		registration.Adapter,
	)
}

func (registrar *runtimeProviderFamilyRegistrar) SetDynamicAvailability(
	provider Provider,
	attestations runtimeDynamicProviderAttestations,
) error {
	if registrar == nil {
		return fmt.Errorf("%w: runtime provider family registrar is required", ErrInvalidArgument)
	}
	if err := registrar.requireOwnedProvider(provider); err != nil {
		return err
	}
	return registrar.setAvailability(provider, attestations.availability(provider))
}

func (registrar *runtimeProviderFamilyRegistrar) setAvailability(
	provider Provider,
	availability ProviderAvailability,
) error {
	if _, exists := registrar.availabilitySeen[provider]; exists {
		return fmt.Errorf("%w: runtime provider family %q registered duplicate availability for %s", ErrInvalidArgument, registrar.family.name, provider)
	}
	registrar.availabilitySeen[provider] = struct{}{}
	return registrar.registry.SetAvailability(provider, availability)
}

func (registrar *runtimeProviderFamilyRegistrar) requireOwnedProvider(
	provider Provider,
) error {
	if !supportedRuntimeProvider(provider) {
		return fmt.Errorf("%w: undeclared runtime provider %q", ErrInvalidArgument, provider)
	}
	if slices.Contains(registrar.family.providers, provider) {
		return nil
	}
	return fmt.Errorf(
		"%w: runtime provider family %q does not own %s",
		ErrInvalidArgument,
		registrar.family.name,
		provider,
	)
}

func configureRuntimeProviderFamilies(
	values map[string]string,
	cipher CredentialCipher,
) (runtimeProviderRegistry, error) {
	registry := newRuntimeProviderRegistry()
	input := runtimeProviderFamilyInput{Values: values, Cipher: cipher}
	for _, family := range runtimeProviderFamilies {
		registrar := newRuntimeProviderFamilyRegistrar(family)
		if family.configure != nil {
			if err := family.configure(input, registrar); err != nil {
				return runtimeProviderRegistry{}, fmt.Errorf(
					"runtime provider family %q: %w",
					family.name,
					err,
				)
			}
		}
		if err := registry.Merge(registrar.registry); err != nil {
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
