package socialconnections

import (
	"fmt"
	"strings"
	"time"
)

const firstSmokeCanaryMaxTTL = 2 * time.Hour

const (
	configXEnabled         = "social.x.enabled"
	configXClientID        = "social.x.client_id"
	configXClientSecret    = "social.x.client_secret"
	configXRedirectURL     = "social.x.redirect_url"
	configXAccessApproved  = "social.x.api_access_approved"
	configXAuditVerified   = "social.x.runtime_audit_verified"
	configXSmokeVerified   = "social.x.smoke_test_verified"
	configXCanaryEnabled   = "social.x.first_smoke_canary.enabled"
	configXCanaryWorkspace = "social.x.first_smoke_canary.workspace_id"
	configXCanaryActor     = "social.x.first_smoke_canary.actor_account_id"
	configXCanaryExpires   = "social.x.first_smoke_canary.expires_at"
)

var xRuntimeEnvironmentKeys = map[string]string{
	configXEnabled:         "POSTQRON_F05_X_ENABLED",
	configXClientID:        "POSTQRON_F05_X_CLIENT_ID",
	configXClientSecret:    "POSTQRON_F05_X_CLIENT_SECRET",
	configXRedirectURL:     "POSTQRON_F05_X_REDIRECT_URL",
	configXAccessApproved:  "POSTQRON_F05_X_API_ACCESS_APPROVED",
	configXAuditVerified:   "POSTQRON_F05_X_RUNTIME_AUDIT_VERIFIED",
	configXSmokeVerified:   "POSTQRON_F05_X_SMOKE_TEST_VERIFIED",
	configXCanaryEnabled:   "POSTQRON_F05_X_FIRST_SMOKE_CANARY_ENABLED",
	configXCanaryWorkspace: "POSTQRON_F05_X_FIRST_SMOKE_CANARY_WORKSPACE_ID",
	configXCanaryActor:     "POSTQRON_F05_X_FIRST_SMOKE_CANARY_ACTOR_ACCOUNT_ID",
	configXCanaryExpires:   "POSTQRON_F05_X_FIRST_SMOKE_CANARY_EXPIRES_AT",
}

func configureXRuntime(
	values map[string]string,
	cipher CredentialCipher,
	adapters map[Provider]Adapter,
	availability map[Provider]ProviderAvailability,
) {
	if xRuntimeValue(values, configXEnabled) != "true" || cipher == nil {
		return
	}
	if xRuntimeValue(values, configXClientID) == "" ||
		xRuntimeValue(values, configXClientSecret) == "" ||
		xRuntimeValue(values, configXRedirectURL) == "" {
		return
	}
	if xRuntimeValue(values, configXAccessApproved) != "true" {
		availability[ProviderX] = unavailableProvider(
			ProviderX,
			ProviderReviewRequired,
		)
		return
	}
	if xRuntimeValue(values, configXAuditVerified) != "true" ||
		xRuntimeValue(values, configXSmokeVerified) != "true" {
		availability[ProviderX] = unavailableProvider(
			ProviderX,
			ProviderAuditRequired,
		)
		return
	}
	adapter, err := NewXAdapter(XAdapterConfig{
		ClientID:     xRuntimeValue(values, configXClientID),
		ClientSecret: xRuntimeValue(values, configXClientSecret),
		RedirectURL:  xRuntimeValue(values, configXRedirectURL),
	})
	if err != nil {
		return
	}
	adapters[ProviderX] = adapter
	availability[ProviderX] = ProviderAvailability{
		Provider:           ProviderX,
		Status:             ProviderAvailable,
		ConfigurationState: ProviderReady,
	}
}

func configureXFirstSmokeCanary(
	values map[string]string,
	cipher CredentialCipher,
	now time.Time,
) (Adapter, firstSmokeCanary, error) {
	if xRuntimeValue(values, configXCanaryEnabled) != "true" {
		return nil, firstSmokeCanary{}, nil
	}
	if xRuntimeValue(values, configXSmokeVerified) == "true" {
		return nil, firstSmokeCanary{}, fmt.Errorf(
			"%w: X first-smoke canary must be removed after verification",
			ErrInvalidArgument,
		)
	}
	if xRuntimeValue(values, configXSmokeVerified) != "false" {
		return nil, firstSmokeCanary{}, fmt.Errorf(
			"%w: X smoke verification must be exactly false during canary",
			ErrInvalidArgument,
		)
	}
	if xRuntimeValue(values, configXEnabled) != "true" || cipher == nil ||
		xRuntimeValue(values, configXClientID) == "" ||
		xRuntimeValue(values, configXClientSecret) == "" ||
		xRuntimeValue(values, configXRedirectURL) == "" ||
		xRuntimeValue(values, configXAccessApproved) != "true" ||
		xRuntimeValue(values, configXAuditVerified) != "true" {
		return nil, firstSmokeCanary{}, fmt.Errorf(
			"%w: X first-smoke canary prerequisites are incomplete",
			ErrInvalidArgument,
		)
	}
	canary := firstSmokeCanary{
		WorkspaceID: strings.TrimSpace(
			xRuntimeValue(values, configXCanaryWorkspace),
		),
		ActorAccountID: strings.TrimSpace(
			xRuntimeValue(values, configXCanaryActor),
		),
	}
	if canary.WorkspaceID == "" || canary.ActorAccountID == "" {
		return nil, firstSmokeCanary{}, fmt.Errorf(
			"%w: X first-smoke canary workspace and actor are required",
			ErrInvalidArgument,
		)
	}
	expiresValue := xRuntimeValue(values, configXCanaryExpires)
	expiresAt, err := time.Parse(
		time.RFC3339,
		expiresValue,
	)
	if err != nil || !strings.HasSuffix(expiresValue, "Z") ||
		expiresAt.After(now.UTC().Add(firstSmokeCanaryMaxTTL)) {
		return nil, firstSmokeCanary{}, fmt.Errorf(
			"%w: X first-smoke canary expiry must be UTC and, while active, within two hours",
			ErrInvalidArgument,
		)
	}
	canary.ExpiresAt = expiresAt.UTC()
	adapter, err := NewXAdapter(XAdapterConfig{
		ClientID:     xRuntimeValue(values, configXClientID),
		ClientSecret: xRuntimeValue(values, configXClientSecret),
		RedirectURL:  xRuntimeValue(values, configXRedirectURL),
	})
	if err != nil {
		return nil, firstSmokeCanary{}, fmt.Errorf(
			"%w: X first-smoke canary adapter is invalid",
			ErrInvalidArgument,
		)
	}
	return adapter, canary, nil
}

func xRuntimeValue(values map[string]string, key string) string {
	return runtimeProviderValue(values, key, xRuntimeEnvironmentKeys[key])
}
