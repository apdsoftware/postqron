package socialconnections

import (
	"fmt"
	"sort"
	"strings"
)

type RuntimeDynamicProviderConfigurer func(
	runtimeProviderFamilyInput,
) ([]RuntimeDynamicProviderRegistration, error)

var decentralizedNetworksRuntimeConfigurer RuntimeDynamicProviderConfigurer

var decentralizedRuntimeGateConfig = map[Provider]struct {
	enabled       string
	enabledEnv    string
	audit         string
	auditEnv      string
	smoke         string
	smokeEnv      string
	compatibility string
	compatEnv     string
}{
	ProviderMastodon: {
		enabled:       "social.mastodon.enabled",
		enabledEnv:    "POSTQRON_F05_MASTODON_ENABLED",
		audit:         "social.mastodon.runtime_audit_verified",
		auditEnv:      "POSTQRON_F05_MASTODON_RUNTIME_AUDIT_VERIFIED",
		smoke:         "social.mastodon.runtime_smoke_verified",
		smokeEnv:      "POSTQRON_F05_MASTODON_RUNTIME_SMOKE_VERIFIED",
		compatibility: "social.mastodon.compatibility_version",
		compatEnv:     "POSTQRON_F05_MASTODON_COMPATIBILITY_VERSION",
	},
	ProviderBluesky: {
		enabled:       "social.bluesky.enabled",
		enabledEnv:    "POSTQRON_F05_BLUESKY_ENABLED",
		audit:         "social.bluesky.runtime_audit_verified",
		auditEnv:      "POSTQRON_F05_BLUESKY_RUNTIME_AUDIT_VERIFIED",
		smoke:         "social.bluesky.runtime_smoke_verified",
		smokeEnv:      "POSTQRON_F05_BLUESKY_RUNTIME_SMOKE_VERIFIED",
		compatibility: "social.bluesky.compatibility_version",
		compatEnv:     "POSTQRON_F05_BLUESKY_COMPATIBILITY_VERSION",
	},
}

// configureDecentralizedNetworksRuntime reserves Mastodon and Bluesky behind a
// typed dynamic hook. Central runtime gates remain authoritative for enable,
// audit, smoke, and compatibility; provider implementations only attest
// configuration completeness and expose the dynamic adapter boundary.
func configureDecentralizedNetworksRuntime(
	input runtimeProviderFamilyInput,
	registrar *runtimeProviderFamilyRegistrar,
) error {
	registrations := []RuntimeDynamicProviderRegistration{}
	if decentralizedNetworksRuntimeConfigurer != nil {
		configured, err := decentralizedNetworksRuntimeConfigurer(input)
		if err != nil {
			return err
		}
		registrations = append(registrations, configured...)
	}
	sort.Slice(registrations, func(left, right int) bool {
		return registrations[left].Provider < registrations[right].Provider
	})

	registered := make(map[Provider]struct{}, len(registrations))
	for _, registration := range registrations {
		attestations := decentralizedRuntimeAttestations(input.Values, registration)
		if err := registrar.RegisterDynamic(registration, attestations); err != nil {
			return err
		}
		registered[registration.Provider] = struct{}{}
	}
	for _, provider := range []Provider{ProviderBluesky, ProviderMastodon} {
		if _, exists := registered[provider]; exists {
			continue
		}
		attestations := decentralizedRuntimeGate(valuesOrEmpty(input.Values), provider)
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
		if err := registrar.SetDynamicAvailability(provider, attestations); err != nil {
			return err
		}
	}
	return nil
}

func decentralizedRuntimeAttestations(
	values map[string]string,
	registration RuntimeDynamicProviderRegistration,
) runtimeDynamicProviderAttestations {
	attestations := decentralizedRuntimeGate(values, registration.Provider)
	attestations.Configured = registration.Configured
	attestations.SupportedVersion = strings.TrimSpace(
		registration.SupportedVersion,
	)
	return attestations
}

func decentralizedRuntimeGate(
	values map[string]string,
	provider Provider,
) runtimeDynamicProviderAttestations {
	configured := decentralizedRuntimeGateConfig[provider]
	return runtimeDynamicProviderAttestations{
		Enabled: runtimeProviderValue(
			values,
			configured.enabled,
			configured.enabledEnv,
		) == "true",
		AuditVerified: runtimeProviderValue(
			values,
			configured.audit,
			configured.auditEnv,
		) == "true",
		SmokeVerified: runtimeProviderValue(
			values,
			configured.smoke,
			configured.smokeEnv,
		) == "true",
		CompatibilityVersion: runtimeProviderValue(
			values,
			configured.compatibility,
			configured.compatEnv,
		),
	}
}

func valuesOrEmpty(values map[string]string) map[string]string {
	if values != nil {
		return values
	}
	return map[string]string{}
}
