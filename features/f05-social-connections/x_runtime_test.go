package socialconnections

import (
	"context"
	"encoding/base64"
	"errors"
	"net/url"
	"testing"
	"time"
)

func TestXRuntimeGatesCredentialsAccessAuditAndSmoke(t *testing.T) {
	cipher, err := NewAESGCMCipher(
		"fixture-key",
		[]byte("0123456789abcdef0123456789abcdef"),
	)
	if err != nil {
		t.Fatal(err)
	}
	values := map[string]string{
		configXEnabled:      "true",
		configXClientID:     "fixture-x-client",
		configXClientSecret: "fixture-x-client-secret",
		configXRedirectURL:  "https://app.example.test/api/v1/social-authorizations/callback",
	}
	tests := []struct {
		name         string
		change       func(map[string]string)
		state        ProviderConfigurationState
		available    bool
		capabilities AdapterCapabilities
	}{
		{
			name: "review required",
			change: func(map[string]string) {
			},
			state: ProviderReviewRequired,
		},
		{
			name: "audit required",
			change: func(values map[string]string) {
				values[configXAccessApproved] = "true"
			},
			state: ProviderAuditRequired,
		},
		{
			name: "smoke required",
			change: func(values map[string]string) {
				values[configXAccessApproved] = "true"
				values[configXAuditVerified] = "true"
			},
			state: ProviderAuditRequired,
		},
		{
			name: "ready",
			change: func(values map[string]string) {
				values[configXAccessApproved] = "true"
				values[configXAuditVerified] = "true"
				values[configXSmokeVerified] = "true"
			},
			state:     ProviderReady,
			available: true,
			capabilities: AdapterCapabilities{
				Authorization:     true,
				PKCE:              true,
				ResourceSelection: true,
				TokenRefresh:      true,
				RemoteRevocation:  true,
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			current := make(map[string]string, len(values)+3)
			for key, value := range values {
				current[key] = value
			}
			test.change(current)
			adapters := make(map[Provider]Adapter)
			availability := map[Provider]ProviderAvailability{
				ProviderX: {
					Provider:           ProviderX,
					Status:             ProviderUnavailable,
					ConfigurationState: ProviderNotConfigured,
				},
			}
			configureXRuntime(current, cipher, adapters, availability)
			got := availability[ProviderX]
			if got.ConfigurationState != test.state ||
				(got.Status == ProviderAvailable) != test.available ||
				adapterCapabilities(adapters[ProviderX]) != test.capabilities {
				t.Fatalf(
					"availability = %#v, capabilities = %#v",
					got,
					adapterCapabilities(adapters[ProviderX]),
				)
			}
		})
	}
}

func TestXRuntimeRemainsFailClosedForIncompleteOrNonExactFlags(t *testing.T) {
	cipher, err := NewAESGCMCipher(
		"fixture-key",
		[]byte("0123456789abcdef0123456789abcdef"),
	)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name   string
		values map[string]string
		cipher CredentialCipher
	}{
		{
			name: "missing credentials",
			values: map[string]string{
				configXEnabled: "true",
			},
			cipher: cipher,
		},
		{
			name: "non exact flag",
			values: map[string]string{
				configXEnabled:        "TRUE",
				configXClientID:       "fixture-x-client",
				configXClientSecret:   "fixture-x-client-secret",
				configXRedirectURL:    "https://app.example.test/callback",
				configXAccessApproved: "true",
				configXAuditVerified:  "true",
				configXSmokeVerified:  "true",
			},
			cipher: cipher,
		},
		{
			name: "missing cipher",
			values: map[string]string{
				configXEnabled:        "true",
				configXClientID:       "fixture-x-client",
				configXClientSecret:   "fixture-x-client-secret",
				configXRedirectURL:    "https://app.example.test/callback",
				configXAccessApproved: "true",
				configXAuditVerified:  "true",
				configXSmokeVerified:  "true",
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			adapters := make(map[Provider]Adapter)
			availability := map[Provider]ProviderAvailability{
				ProviderX: {
					Provider:           ProviderX,
					Status:             ProviderUnavailable,
					ConfigurationState: ProviderNotConfigured,
				},
			}
			configureXRuntime(
				test.values,
				test.cipher,
				adapters,
				availability,
			)
			if adapters[ProviderX] != nil ||
				availability[ProviderX].Status != ProviderUnavailable ||
				availability[ProviderX].ConfigurationState !=
					ProviderNotConfigured {
				t.Fatalf(
					"fail-closed availability = %#v",
					availability[ProviderX],
				)
			}
		})
	}
}

func TestXRuntimeIsVisibleInProviderDiscoveryOnlyWhenReady(t *testing.T) {
	key := base64.StdEncoding.EncodeToString(
		[]byte("0123456789abcdef0123456789abcdef"),
	)
	module := runtimeModuleFixture()
	err := module.Configure(map[string]string{
		configEnabled:         "true",
		configCipherKeyID:     "fixture-key",
		configCipherKey:       key,
		configXEnabled:        "true",
		configXClientID:       "fixture-x-client",
		configXClientSecret:   "fixture-x-client-secret",
		configXRedirectURL:    "https://app.example.test/api/v1/social-authorizations/callback",
		configXAccessApproved: "true",
		configXAuditVerified:  "true",
		configXSmokeVerified:  "true",
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range module.service.Bootstrap().Catalog {
		if entry.Provider != ProviderX {
			continue
		}
		if entry.Status != ProviderAvailable ||
			entry.ConfigurationState != ProviderReady ||
			entry.Capabilities.TokenRefresh != true ||
			entry.Capabilities.RemoteRevocation != true {
			t.Fatalf("X discovery entry = %#v", entry)
		}
		return
	}
	t.Fatal("X provider is missing from runtime discovery")
}

func TestXFirstSmokeCanaryIsActorWorkspaceAndTimeScoped(t *testing.T) {
	now := serviceTestNow
	module := runtimeModuleFixture()
	module.clock = func() time.Time { return now }
	key := base64.StdEncoding.EncodeToString(
		[]byte("0123456789abcdef0123456789abcdef"),
	)
	expiresAt := now.Add(time.Hour)
	err := module.Configure(map[string]string{
		configEnabled:          "true",
		configCipherKeyID:      "fixture-key",
		configCipherKey:        key,
		configXEnabled:         "true",
		configXClientID:        "fixture-x-client",
		configXClientSecret:    "fixture-x-client-secret",
		configXRedirectURL:     "https://postqron.com/app/social-oauth/callback",
		configXAccessApproved:  "true",
		configXAuditVerified:   "true",
		configXSmokeVerified:   "false",
		configXCanaryEnabled:   "true",
		configXCanaryWorkspace: "workspace-canary",
		configXCanaryActor:     "actor-canary",
		configXCanaryExpires:   expiresAt.Format(time.RFC3339),
	})
	if err != nil {
		t.Fatal(err)
	}

	global := providerCatalogEntry(t, module.service.Bootstrap(), ProviderX)
	if global.Status != ProviderUnavailable ||
		global.ConfigurationState != ProviderAuditRequired ||
		global.Capabilities != (AdapterCapabilities{}) {
		t.Fatalf("global X entry = %#v, want audit-required", global)
	}
	if _, adapterErr := module.service.availableAdapter(ProviderX); !errors.Is(
		adapterErr,
		ErrProviderAuditRequired,
	) {
		t.Fatalf("publishing adapter error = %v, want audit gate", adapterErr)
	}
	for _, identity := range []struct {
		workspace string
		actor     string
	}{
		{workspace: "another-workspace", actor: "actor-canary"},
		{workspace: "workspace-canary", actor: "another-actor"},
	} {
		bootstrap, bootstrapErr := module.service.BootstrapForWorkspace(
			context.Background(),
			identity.workspace,
			identity.actor,
		)
		if bootstrapErr != nil {
			t.Fatal(bootstrapErr)
		}
		entry := providerCatalogEntry(t, bootstrap, ProviderX)
		if entry.Status != ProviderUnavailable ||
			entry.ConfigurationState != ProviderAuditRequired {
			t.Fatalf("non-canary X entry = %#v", entry)
		}
		_, beginErr := module.service.Begin(
			context.Background(),
			BeginRequest{
				WorkspaceID: identity.workspace,
				ActorID:     identity.actor,
				Provider:    ProviderX,
			},
		)
		if !errors.Is(beginErr, ErrProviderAuditRequired) {
			t.Fatalf("non-canary Begin() error = %v", beginErr)
		}
	}

	bootstrap, err := module.service.BootstrapForWorkspace(
		context.Background(),
		"workspace-canary",
		"actor-canary",
	)
	if err != nil {
		t.Fatal(err)
	}
	entry := providerCatalogEntry(t, bootstrap, ProviderX)
	if entry.Status != ProviderAvailable ||
		entry.ConfigurationState != ProviderReady ||
		!entry.Capabilities.Authorization ||
		!entry.Capabilities.PKCE {
		t.Fatalf("canary X entry = %#v", entry)
	}
	authorization, err := module.service.Begin(
		context.Background(),
		BeginRequest{
			WorkspaceID: "workspace-canary",
			ActorID:     "actor-canary",
			Provider:    ProviderX,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(authorization.URL)
	if err != nil || parsed.Host != "x.com" {
		t.Fatalf("canary authorization URL = %q, error = %v", authorization.URL, err)
	}
	if len(module.repository.(*MemoryRepository).attempts) != 1 {
		t.Fatalf("auditable canary attempts = %d, want 1", len(module.repository.(*MemoryRepository).attempts))
	}
	for _, attempt := range module.repository.(*MemoryRepository).attempts {
		if attempt.WorkspaceID != "workspace-canary" ||
			attempt.ActorID != "actor-canary" ||
			attempt.Provider != ProviderX {
			t.Fatalf("canary attempt audit fields = %#v", attempt)
		}
	}

	now = expiresAt
	expired, err := module.service.BootstrapForWorkspace(
		context.Background(),
		"workspace-canary",
		"actor-canary",
	)
	if err != nil {
		t.Fatal(err)
	}
	entry = providerCatalogEntry(t, expired, ProviderX)
	if entry.Status != ProviderUnavailable ||
		entry.ConfigurationState != ProviderAuditRequired {
		t.Fatalf("expired canary X entry = %#v", entry)
	}
	if _, beginErr := module.service.Begin(
		context.Background(),
		BeginRequest{
			WorkspaceID: "workspace-canary",
			ActorID:     "actor-canary",
			Provider:    ProviderX,
		},
	); !errors.Is(beginErr, ErrProviderAuditRequired) {
		t.Fatalf("expired canary Begin() error = %v", beginErr)
	}
}

func TestXFirstSmokeCanaryRejectsUnsafeConfiguration(t *testing.T) {
	key := base64.StdEncoding.EncodeToString(
		[]byte("0123456789abcdef0123456789abcdef"),
	)
	base := map[string]string{
		configEnabled:          "true",
		configCipherKeyID:      "fixture-key",
		configCipherKey:        key,
		configXEnabled:         "true",
		configXClientID:        "fixture-x-client",
		configXClientSecret:    "fixture-x-client-secret",
		configXRedirectURL:     "https://postqron.com/app/social-oauth/callback",
		configXAccessApproved:  "true",
		configXAuditVerified:   "true",
		configXSmokeVerified:   "false",
		configXCanaryEnabled:   "true",
		configXCanaryWorkspace: "workspace-canary",
		configXCanaryActor:     "actor-canary",
		configXCanaryExpires: serviceTestNow.Add(time.Hour).Format(
			time.RFC3339,
		),
	}
	tests := []struct {
		name   string
		change func(map[string]string)
	}{
		{
			name: "verified gate cannot retain canary",
			change: func(values map[string]string) {
				values[configXSmokeVerified] = "true"
			},
		},
		{
			name: "missing actor",
			change: func(values map[string]string) {
				delete(values, configXCanaryActor)
			},
		},
		{
			name: "non exact smoke flag",
			change: func(values map[string]string) {
				values[configXSmokeVerified] = "FALSE"
			},
		},
		{
			name: "expiry over two hours",
			change: func(values map[string]string) {
				values[configXCanaryExpires] = serviceTestNow.Add(
					firstSmokeCanaryMaxTTL + time.Second,
				).Format(time.RFC3339)
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			values := make(map[string]string, len(base))
			for key, value := range base {
				values[key] = value
			}
			test.change(values)
			module := runtimeModuleFixture()
			if configureErr := module.Configure(values); !errors.Is(
				configureErr,
				ErrInvalidArgument,
			) {
				t.Fatalf("Configure() error = %v, want ErrInvalidArgument", configureErr)
			}
		})
	}
}

func TestXFirstSmokeCanaryCompletesAuditedConnectAndCleanupWithoutPublishing(
	t *testing.T,
) {
	expiresAt := serviceTestNow.Add(time.Hour)
	fixture := newXFirstSmokeServiceFixture(
		t,
		func() time.Time { return serviceTestNow },
		expiresAt,
	)
	service := fixture.service
	selection := authorizeAndDiscover(t, service, ProviderX)
	connection, err := service.Select(context.Background(), SelectRequest{
		WorkspaceID: "workspace-1",
		ActorID:     "owner-1",
		SelectionID: selection.ID,
		RemoteID:    "x-profile-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.AccessToken(
		context.Background(),
		"workspace-1",
		connection.ID,
	); !errors.Is(err, ErrProviderAuditRequired) {
		t.Fatalf("canary AccessToken() error = %v, want audit gate", err)
	}
	result, err := service.Revoke(
		context.Background(),
		"workspace-1",
		"owner-1",
		connection.ID,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !result.ProviderRevoked || result.Connection.Status != StatusRevoked {
		t.Fatalf("canary revoke result = %#v", result)
	}
	if countEvents(fixture.repository.Events(), EventConnected) != 1 ||
		countEvents(fixture.repository.Events(), EventDisconnected) != 1 {
		t.Fatalf("canary audit events = %#v", fixture.repository.Events())
	}
	if len(fixture.repository.attempts) != 1 {
		t.Fatalf("canary OAuth attempts = %d, want 1", len(fixture.repository.attempts))
	}
}

func TestConfigureXFirstSmokeCanaryRetainsExpiredAdapterForCleanup(t *testing.T) {
	cipher, err := NewAESGCMCipher(
		"fixture-key",
		[]byte("0123456789abcdef0123456789abcdef"),
	)
	if err != nil {
		t.Fatal(err)
	}
	expiresAt := serviceTestNow.Add(-time.Minute)
	adapter, canary, err := configureXFirstSmokeCanary(
		xFirstSmokeRuntimeValues(expiresAt),
		cipher,
		serviceTestNow,
	)
	if err != nil {
		t.Fatalf("expired canary runtime configuration = %v", err)
	}
	if adapter == nil || canary.ExpiresAt != expiresAt {
		t.Fatalf("expired cleanup adapter = %T, policy = %#v", adapter, canary)
	}
}

func TestXExpiredFirstSmokeCanaryDoesNotFailModuleConfigure(t *testing.T) {
	key := base64.StdEncoding.EncodeToString(
		[]byte("0123456789abcdef0123456789abcdef"),
	)
	now := serviceTestNow
	expiresAt := now.Add(time.Minute)
	module := runtimeModuleFixture()
	module.clock = func() time.Time { return now }
	values := xFirstSmokeRuntimeValues(expiresAt)
	values[configCipherKey] = key
	if err := module.Configure(values); err != nil {
		t.Fatalf("Configure() before canary expiry = %v", err)
	}
	now = expiresAt.Add(time.Minute)
	if err := module.Configure(values); err != nil {
		t.Fatalf("Configure() after canary expiry = %v", err)
	}
	if module.service.adapters[ProviderX] != nil ||
		module.service.firstSmokeAdapters[ProviderX] == nil {
		t.Fatalf(
			"expired module adapters: normal=%T cleanup=%T",
			module.service.adapters[ProviderX],
			module.service.firstSmokeAdapters[ProviderX],
		)
	}
	bootstrap, err := module.service.BootstrapForWorkspace(
		context.Background(),
		"workspace-1",
		"owner-1",
	)
	if err != nil {
		t.Fatal(err)
	}
	entry := providerCatalogEntry(t, bootstrap, ProviderX)
	if entry.Status != ProviderUnavailable ||
		entry.ConfigurationState != ProviderAuditRequired {
		t.Fatalf("expired module X entry = %#v", entry)
	}
	if _, err = module.service.Begin(context.Background(), BeginRequest{
		WorkspaceID: "workspace-1",
		ActorID:     "owner-1",
		Provider:    ProviderX,
	}); !errors.Is(err, ErrProviderAuditRequired) {
		t.Fatalf("expired module Begin() error = %v", err)
	}
}

func TestXFirstSmokeCanaryCatalogRequiresChannelManager(t *testing.T) {
	expiresAt := serviceTestNow.Add(time.Hour)
	fixture := newXFirstSmokeServiceFixture(
		t,
		func() time.Time { return serviceTestNow },
		expiresAt,
	)
	fixture.authorizer.permissions[PermissionManageChannels] = false
	bootstrap, err := fixture.service.BootstrapForWorkspace(
		context.Background(),
		"workspace-1",
		"owner-1",
	)
	if err != nil {
		t.Fatal(err)
	}
	entry := providerCatalogEntry(t, bootstrap, ProviderX)
	if entry.Status != ProviderUnavailable ||
		entry.ConfigurationState != ProviderAuditRequired ||
		entry.Capabilities != (AdapterCapabilities{}) {
		t.Fatalf("view-only canary X entry = %#v", entry)
	}
}

func TestXExpiredFirstSmokeCanaryRestartBlocksFlowsAndPreservesCleanup(
	t *testing.T,
) {
	now := serviceTestNow
	expiresAt := now.Add(time.Hour)
	fixture := newXFirstSmokeServiceFixture(
		t,
		func() time.Time { return now },
		expiresAt,
	)
	_, pendingState := beginAuthorization(t, fixture.service, ProviderX)
	pendingSelection := authorizeAndDiscover(t, fixture.service, ProviderX)
	connection := connectResource(
		t,
		fixture.service,
		ProviderX,
		"x-profile-1",
	)

	now = expiresAt.Add(time.Minute)
	restarted := fixture.restart(t, func() time.Time { return now })
	if restarted.adapters[ProviderX] != nil ||
		restarted.firstSmokeAdapters[ProviderX] == nil {
		t.Fatalf(
			"restarted adapters: normal=%T cleanup=%T",
			restarted.adapters[ProviderX],
			restarted.firstSmokeAdapters[ProviderX],
		)
	}
	bootstrap, err := restarted.BootstrapForWorkspace(
		context.Background(),
		"workspace-1",
		"owner-1",
	)
	if err != nil {
		t.Fatal(err)
	}
	entry := providerCatalogEntry(t, bootstrap, ProviderX)
	if entry.Status != ProviderUnavailable ||
		entry.ConfigurationState != ProviderAuditRequired ||
		entry.Capabilities != (AdapterCapabilities{}) {
		t.Fatalf("restarted expired X entry = %#v", entry)
	}
	if _, err = restarted.Begin(context.Background(), BeginRequest{
		WorkspaceID: "workspace-1",
		ActorID:     "owner-1",
		Provider:    ProviderX,
	}); !errors.Is(err, ErrProviderAuditRequired) {
		t.Fatalf("restarted Begin() error = %v", err)
	}
	if _, err = restarted.Callback(context.Background(), CallbackRequest{
		State: pendingState,
		Code:  "provider-code",
	}); !errors.Is(err, ErrProviderAuditRequired) {
		t.Fatalf("restarted Callback() error = %v", err)
	}
	if _, err = restarted.Select(context.Background(), SelectRequest{
		WorkspaceID: "workspace-1",
		ActorID:     "owner-1",
		SelectionID: pendingSelection.ID,
		RemoteID:    "x-profile-1",
	}); !errors.Is(err, ErrProviderAuditRequired) {
		t.Fatalf("restarted Select() error = %v", err)
	}
	if _, err = restarted.AccessToken(
		context.Background(),
		"workspace-1",
		connection.ID,
	); !errors.Is(err, ErrProviderAuditRequired) {
		t.Fatalf("restarted AccessToken() error = %v", err)
	}
	result, err := restarted.Revoke(
		context.Background(),
		"workspace-1",
		"owner-1",
		connection.ID,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !result.ProviderRevoked || result.Connection.Status != StatusRevoked {
		t.Fatalf("restarted cleanup result = %#v", result)
	}
}

type xFirstSmokeServiceFixture struct {
	service    *Service
	repository *MemoryRepository
	authorizer *fakeAuthorizer
	cipher     CredentialCipher
	quota      *fakeChannelQuota
	adapter    *fakeAdapter
	expiresAt  time.Time
}

func newXFirstSmokeServiceFixture(
	t *testing.T,
	now func() time.Time,
	expiresAt time.Time,
) *xFirstSmokeServiceFixture {
	t.Helper()
	cipher, err := NewAESGCMCipher(
		"fixture-key",
		[]byte("0123456789abcdef0123456789abcdef"),
	)
	if err != nil {
		t.Fatal(err)
	}
	tokenExpiresAt := expiresAt.Add(time.Hour)
	fixture := &xFirstSmokeServiceFixture{
		repository: NewMemoryRepository(),
		authorizer: &fakeAuthorizer{permissions: map[Permission]bool{
			PermissionViewWorkspace:  true,
			PermissionManageChannels: true,
		}},
		cipher:    cipher,
		quota:     newFakeChannelQuota(),
		expiresAt: expiresAt,
		adapter: &fakeAdapter{
			config: OAuthConfig{
				ClientID:         "fixture-x-client",
				AuthorizationURL: xOfficialAuthorizationURL,
				RedirectURL:      "https://postqron.com/app/social-oauth/callback",
				Scopes:           append([]string(nil), xRequiredScopes...),
				ScopeSeparator:   OAuthScopeSeparatorSpace,
				SupportsPKCE:     true,
			},
			grant: Credential{
				AccessToken:  "fixture-x-access-token",
				RefreshToken: "fixture-x-refresh-token",
				ExpiresAt:    &tokenExpiresAt,
				Scopes:       append([]string(nil), xRequiredScopes...),
			},
			resources: []DiscoveredResource{
				{
					Candidate: Candidate{
						RemoteID:     "x-profile-1",
						ResourceType: ResourceXProfile,
						AccountType:  AccountTypeProfile,
						DisplayName:  "Canary profile",
					},
					Credential: Credential{
						AccessToken:  "fixture-x-access-token",
						RefreshToken: "fixture-x-refresh-token",
						ExpiresAt:    &tokenExpiresAt,
						Scopes:       append([]string(nil), xRequiredScopes...),
					},
				},
			},
		},
	}
	fixture.service = fixture.restart(t, now)
	return fixture
}

func (fixture *xFirstSmokeServiceFixture) restart(
	t *testing.T,
	now func() time.Time,
) *Service {
	t.Helper()
	service, err := NewService(Config{
		Repository: fixture.repository,
		Authorizer: fixture.authorizer,
		Cipher:     fixture.cipher,
		Quota:      fixture.quota,
		Availability: map[Provider]ProviderAvailability{
			ProviderX: unavailableProvider(ProviderX, ProviderAuditRequired),
		},
		Now:              now,
		AuthorizationTTL: 2 * time.Hour,
		SelectionTTL:     2 * time.Hour,
		firstSmokeAdapters: map[Provider]Adapter{
			ProviderX: fixture.adapter,
		},
		firstSmokeCanaries: map[Provider]firstSmokeCanary{
			ProviderX: {
				WorkspaceID:    "workspace-1",
				ActorAccountID: "owner-1",
				ExpiresAt:      fixture.expiresAt,
			},
		},
	})
	if err != nil {
		t.Fatalf("restart service: %v", err)
	}
	return service
}

func xFirstSmokeRuntimeValues(expiresAt time.Time) map[string]string {
	return map[string]string{
		configEnabled:          "true",
		configCipherKeyID:      "fixture-key",
		configXEnabled:         "true",
		configXClientID:        "fixture-x-client",
		configXClientSecret:    "fixture-x-client-secret",
		configXRedirectURL:     "https://postqron.com/app/social-oauth/callback",
		configXAccessApproved:  "true",
		configXAuditVerified:   "true",
		configXSmokeVerified:   "false",
		configXCanaryEnabled:   "true",
		configXCanaryWorkspace: "workspace-1",
		configXCanaryActor:     "owner-1",
		configXCanaryExpires:   expiresAt.Format(time.RFC3339),
	}
}
