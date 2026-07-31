package socialconnections

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

const dynamicTestDID = "did:plc:abcdefghijklmnopqrstuvwx"

type fakeDynamicAdapter struct {
	mu             sync.Mutex
	config         DynamicOAuthConfig
	beginResult    DynamicAuthorization
	beginErr       error
	beginRequest   DynamicBeginRequest
	beginCalls     int
	completeResult DynamicCompletion
	completeErr    error
	completeCalls  int
	refreshResult  DynamicRefreshResult
	refreshErr     error
	refreshCalls   int
	requestResult  DynamicAuthenticatedResult
	requestErr     error
	requestCalls   int
	requestPaths   []string
	requestStates  [][]byte
	requestEntered chan struct{}
	requestRelease chan struct{}
	revokeErr      error
}

func (adapter *fakeDynamicAdapter) DynamicConfig() DynamicOAuthConfig {
	return adapter.config
}

func (adapter *fakeDynamicAdapter) BeginDynamic(
	_ context.Context,
	request DynamicBeginRequest,
) (DynamicAuthorization, error) {
	adapter.mu.Lock()
	defer adapter.mu.Unlock()
	adapter.beginRequest = request
	adapter.beginCalls++
	if adapter.beginResult.URL != "" || adapter.beginErr != nil {
		return adapter.beginResult, adapter.beginErr
	}
	return DynamicAuthorization{
		URL:           "https://auth.example.com/authorize?request_uri=urn%3Aexample%3Apar",
		ProviderState: []byte("encrypted-by-central-attempt-state"),
		Binding: OAuthBinding{
			Issuer:         "https://auth.example.com",
			ResourceServer: "https://pds.example.com",
			Subject:        dynamicTestDID,
		},
		PARRequestURI: "urn:example:par",
	}, adapter.beginErr
}

func (adapter *fakeDynamicAdapter) CompleteDynamic(
	_ context.Context,
	_ DynamicCallbackRequest,
) (DynamicCompletion, error) {
	adapter.mu.Lock()
	defer adapter.mu.Unlock()
	adapter.completeCalls++
	return adapter.completeResult, adapter.completeErr
}

func (adapter *fakeDynamicAdapter) RefreshDynamic(
	_ context.Context,
	_ DynamicSession,
) (DynamicRefreshResult, error) {
	adapter.mu.Lock()
	defer adapter.mu.Unlock()
	adapter.refreshCalls++
	return cloneDynamicRefreshResult(adapter.refreshResult), adapter.refreshErr
}

func (adapter *fakeDynamicAdapter) DoAuthenticated(
	_ context.Context,
	session DynamicSession,
	request AuthenticatedRequest,
) (DynamicAuthenticatedResult, error) {
	adapter.mu.Lock()
	adapter.requestCalls++
	adapter.requestPaths = append(adapter.requestPaths, request.Path)
	adapter.requestStates = append(
		adapter.requestStates,
		append([]byte(nil), session.ProviderState...),
	)
	result := cloneDynamicAuthenticatedResult(adapter.requestResult)
	err := adapter.requestErr
	entered := adapter.requestEntered
	release := adapter.requestRelease
	adapter.mu.Unlock()
	if entered != nil {
		select {
		case entered <- struct{}{}:
		default:
		}
	}
	if release != nil {
		<-release
	}
	return result, err
}

func (adapter *fakeDynamicAdapter) RevokeDynamic(
	context.Context,
	DynamicSession,
) error {
	return adapter.revokeErr
}

func (adapter *fakeDynamicAdapter) counts() (refresh, request int) {
	adapter.mu.Lock()
	defer adapter.mu.Unlock()
	return adapter.refreshCalls, adapter.requestCalls
}

func cloneDynamicRefreshResult(result DynamicRefreshResult) DynamicRefreshResult {
	result.Session = cloneDynamicSession(result.Session)
	return result
}

func cloneDynamicAuthenticatedResult(
	result DynamicAuthenticatedResult,
) DynamicAuthenticatedResult {
	result.Session = cloneDynamicSession(result.Session)
	result.Response.Body = append([]byte(nil), result.Response.Body...)
	result.Response.Header = result.Response.Header.Clone()
	return result
}

func cloneDynamicSession(session DynamicSession) DynamicSession {
	session.Credential = cloneCredential(session.Credential)
	session.ProviderState = append([]byte(nil), session.ProviderState...)
	return session
}

type dynamicServiceFixture struct {
	service      *Service
	repository   *MemoryRepository
	adapter      *fakeDynamicAdapter
	cipher       CredentialCipher
	now          *time.Time
	connectionID string
}

func newDynamicServiceFixture(t *testing.T) dynamicServiceFixture {
	t.Helper()
	now := serviceTestNow
	repository := NewMemoryRepository()
	cipher, err := NewAESGCMCipher(
		"dynamic-test-key",
		[]byte("0123456789abcdef0123456789abcdef"),
	)
	if err != nil {
		t.Fatal(err)
	}
	binding := OAuthBinding{
		Issuer:         "https://auth.example.com",
		ResourceServer: "https://pds.example.com",
		Subject:        dynamicTestDID,
	}
	connectionID := "dynamic-connection"
	remoteID := dynamicTestDID
	credentialAAD := credentialAdditionalData(
		"workspace-1",
		ProviderBluesky,
		remoteID,
	)
	access, err := cipher.Seal([]byte("access-1"), credentialAAD)
	if err != nil {
		t.Fatal(err)
	}
	refresh, err := cipher.Seal([]byte("refresh-1"), credentialAAD)
	if err != nil {
		t.Fatal(err)
	}
	session, err := cipher.Seal(
		[]byte("nonce-as-1|nonce-rs-1|dpop-key"),
		dynamicSessionAdditionalData(
			"workspace-1",
			ProviderBluesky,
			remoteID,
			binding,
		),
	)
	if err != nil {
		t.Fatal(err)
	}
	expiresAt := now.Add(time.Hour)
	repository.connections[connectionID] = StoredCredential{
		Connection: Connection{
			ID:                 connectionID,
			WorkspaceID:        "workspace-1",
			Provider:           ProviderBluesky,
			RemoteID:           remoteID,
			ResourceType:       ResourceBlueskyAccount,
			AccountType:        AccountTypeProfile,
			DisplayName:        "Dynamic profile",
			Scopes:             []string{"atproto"},
			Status:             StatusConnected,
			TokenExpiresAt:     &expiresAt,
			ConnectedByActorID: "owner-1",
			CreatedAt:          now,
			UpdatedAt:          now,
		},
		AccessTokenCiphertext:  access,
		RefreshTokenCiphertext: refresh,
		OAuthSessionCiphertext: session,
		Binding:                binding,
		RefreshTokenMode:       RefreshTokenSingleUse,
	}
	repository.connectionKey[uniqueConnectionKey("workspace-1", ProviderBluesky, remoteID)] = connectionID
	adapter := &fakeDynamicAdapter{
		config: DynamicOAuthConfig{
			RedirectURL:      "https://app.example.test/social/callback",
			Scopes:           []string{"atproto"},
			RequiresPAR:      true,
			RequiresDPoP:     true,
			RequiresATH:      true,
			RequiresIssuer:   true,
			RequiresSubject:  true,
			RefreshTokenMode: RefreshTokenSingleUse,
			RevocationPolicy: RevocationBestEffort,
			NetworkPolicy: DynamicNetworkPolicy{
				RejectRedirects:   true,
				ValidateAndPinDNS: true,
				MaxResponseBytes:  maximumAuthenticatedBody,
			},
		},
		requestResult: DynamicAuthenticatedResult{
			Response: AuthenticatedResponse{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body:       []byte(`{"ok":true}`),
			},
		},
	}
	service, err := NewService(Config{
		Repository: repository,
		Authorizer: &fakeAuthorizer{permissions: map[Permission]bool{
			PermissionViewWorkspace:  true,
			PermissionManageChannels: true,
		}},
		Cipher: cipher,
		Quota:  newFakeChannelQuota(),
		DynamicAdapters: map[Provider]DynamicAdapter{
			ProviderBluesky: adapter,
		},
		Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	// Keep a mutable clock without exposing it through production contracts.
	service.now = func() time.Time { return *(&now) }
	return dynamicServiceFixture{
		service:      service,
		repository:   repository,
		adapter:      adapter,
		cipher:       cipher,
		now:          &now,
		connectionID: connectionID,
	}
}

func TestValidateDIDWebRejectsMalformedSegments(t *testing.T) {
	valid := []string{
		"did:web:example.com",
		"did:web:example.com:users:alice",
		"did:web:example.com%3A8443:users:alice",
	}
	for _, value := range valid {
		if err := validateDID(value); err != nil {
			t.Fatalf("valid DID %q rejected: %v", value, err)
		}
	}
	invalid := []string{
		"did:web:example.com::alice",
		"did:web:example.com:.",
		"did:web:example.com:..",
		"did:web:example.com:%2e%2e",
		"did:web:example.com:%61lice",
		"did:web:example.com:%0A",
		"did:web:example.com:%2Falice",
		"did:web:EXAMPLE.com:alice",
		"did:web:example.com%3a8443:alice",
	}
	for _, value := range invalid {
		if err := validateDID(value); err == nil {
			t.Fatalf("malformed DID %q was accepted", value)
		}
	}
}

func TestDynamicDiscoveryAndBindingRejectSSRFOrigins(t *testing.T) {
	for _, input := range []DiscoveryInput{
		{Kind: DiscoveryInstanceOrigin, Value: "http://mastodon.example.com"},
		{Kind: DiscoveryInstanceOrigin, Value: "https://localhost"},
		{Kind: DiscoveryInstanceOrigin, Value: "https://127.0.0.1"},
		{Kind: DiscoveryInstanceOrigin, Value: "https://10.0.0.1"},
		{Kind: DiscoveryInstanceOrigin, Value: "https://service.internal"},
		{Kind: DiscoveryInstanceOrigin, Value: "https://user@example.com"},
	} {
		if err := validateDiscoveryInput(ProviderMastodon, input); err == nil {
			t.Fatalf("unsafe discovery input accepted: %#v", input)
		}
	}
	for _, binding := range []OAuthBinding{
		{
			Issuer:         "https://localhost",
			ResourceServer: "https://pds.example.com",
		},
		{
			Issuer:         "https://auth.example.com",
			ResourceServer: "https://192.168.1.10",
		},
		{
			Issuer:         "https://auth.example.com/path",
			ResourceServer: "https://pds.example.com",
		},
	} {
		if _, err := canonicalOAuthBinding(binding); err == nil {
			t.Fatalf("unsafe OAuth binding accepted: %#v", binding)
		}
	}
}

func TestDynamicAttemptSessionAndSelectionArePersistedAndBound(t *testing.T) {
	fixture := newDynamicServiceFixture(t)
	fixture.adapter.completeResult = DynamicCompletion{
		Resources: []DiscoveredResource{{
			Candidate: Candidate{
				RemoteID:     dynamicTestDID,
				ResourceType: ResourceBlueskyAccount,
				AccountType:  AccountTypeProfile,
				DisplayName:  "Bound profile",
			},
			Credential: Credential{
				AccessToken:  "callback-access-secret",
				RefreshToken: "callback-refresh-secret",
				Scopes:       []string{"atproto"},
			},
		}},
		ProviderState: []byte("callback-dpop-private-key-and-nonces"),
		Binding: OAuthBinding{
			Issuer:         "https://auth.example.com/",
			ResourceServer: "https://pds.example.com/",
			Subject:        dynamicTestDID,
		},
	}
	begin := func(t *testing.T) string {
		t.Helper()
		if _, err := fixture.service.Begin(context.Background(), BeginRequest{
			WorkspaceID: "workspace-1",
			ActorID:     "owner-1",
			Provider:    ProviderBluesky,
			Discovery: DiscoveryInput{
				Kind:  DiscoveryHandle,
				Value: "user.example.com",
			},
		}); err != nil {
			t.Fatal(err)
		}
		fixture.adapter.mu.Lock()
		state := fixture.adapter.beginRequest.State
		fixture.adapter.mu.Unlock()
		return state
	}

	wrongIssuerState := begin(t)
	if _, err := fixture.service.Callback(context.Background(), CallbackRequest{
		State:  wrongIssuerState,
		Code:   "code-1",
		Issuer: "https://other.example.com",
	}); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("callback issuer mismatch error = %v", err)
	}
	fixture.adapter.mu.Lock()
	completeCalls := fixture.adapter.completeCalls
	fixture.adapter.mu.Unlock()
	if completeCalls != 0 {
		t.Fatal("issuer mismatch reached token exchange")
	}

	subjectMismatchState := begin(t)
	fixture.adapter.completeResult.Binding.Subject =
		"did:plc:zyxwvutsrqponmlkjihgfedc"
	if _, err := fixture.service.Callback(context.Background(), CallbackRequest{
		State:  subjectMismatchState,
		Code:   "code-2",
		Issuer: "https://auth.example.com/",
	}); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("callback subject mismatch error = %v", err)
	}

	fixture.adapter.completeResult.Binding.Subject = dynamicTestDID
	validState := begin(t)
	selection, err := fixture.service.Callback(
		context.Background(),
		CallbackRequest{
			State:  validState,
			Code:   "code-3",
			Issuer: "https://auth.example.com/",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	func() {
		fixture.repository.mu.Lock()
		defer fixture.repository.mu.Unlock()
		for _, attempt := range fixture.repository.attempts {
			if attempt.Binding.Issuer != "https://auth.example.com" ||
				attempt.Binding.ResourceServer != "https://pds.example.com" ||
				attempt.Binding.Subject != dynamicTestDID {
				t.Fatalf("attempt binding = %#v", attempt.Binding)
			}
			if strings.Contains(
				string(attempt.OAuthStateCiphertext.Data),
				"encrypted-by-central-attempt-state",
			) ||
				strings.Contains(
					string(attempt.OAuthStateCiphertext.Data),
					"urn:example:par",
				) {
				t.Fatal("attempt ciphertext contains provider state or PAR request_uri")
			}
		}
		storedSelection := fixture.repository.selections[selection.ID]
		if len(storedSelection.Resources) != 1 {
			t.Fatalf("stored dynamic resources = %d", len(storedSelection.Resources))
		}
		resource := storedSelection.Resources[0]
		if resource.Binding.Subject != dynamicTestDID ||
			resource.RefreshTokenMode != RefreshTokenSingleUse {
			t.Fatalf("stored session contract = %#v/%q", resource.Binding, resource.RefreshTokenMode)
		}
		if strings.Contains(
			string(resource.OAuthSessionCiphertext.Data),
			"callback-dpop-private-key-and-nonces",
		) {
			t.Fatal("session ciphertext contains DPoP key or nonce state")
		}
		delete(fixture.repository.connections, fixture.connectionID)
		delete(
			fixture.repository.connectionKey,
			uniqueConnectionKey("workspace-1", ProviderBluesky, dynamicTestDID),
		)
	}()

	connection, err := fixture.service.Select(
		context.Background(),
		SelectRequest{
			WorkspaceID: "workspace-1",
			ActorID:     "owner-1",
			SelectionID: selection.ID,
			RemoteID:    dynamicTestDID,
		},
	)
	if err != nil {
		t.Fatalf("select dynamic resource: %v", err)
	}
	if connection.Provider != ProviderBluesky ||
		connection.RemoteID != dynamicTestDID ||
		connection.Status != StatusConnected {
		t.Fatalf("dynamic connection = %#v", connection)
	}
}

func TestDynamicReconnectDerivesPreviousBindingServerSide(t *testing.T) {
	fixture := newDynamicServiceFixture(t)
	fixture.repository.mu.Lock()
	stored := fixture.repository.connections[fixture.connectionID]
	stored.Status = StatusReconnectRequired
	stored.ReconnectReason = "test"
	stored.AccessTokenCiphertext = Ciphertext{}
	stored.RefreshTokenCiphertext = Ciphertext{}
	stored.OAuthSessionCiphertext = Ciphertext{}
	fixture.repository.connections[fixture.connectionID] = stored
	fixture.repository.mu.Unlock()
	if _, err := fixture.service.BeginReconnect(
		context.Background(),
		ReconnectRequest{
			WorkspaceID:  "workspace-1",
			ActorID:      "owner-1",
			ConnectionID: fixture.connectionID,
		},
	); err != nil {
		t.Fatal(err)
	}
	fixture.adapter.mu.Lock()
	defer fixture.adapter.mu.Unlock()
	if fixture.adapter.beginRequest.Discovery != (DiscoveryInput{}) ||
		fixture.adapter.beginRequest.PreviousBinding != stored.Binding {
		t.Fatalf(
			"dynamic reconnect begin request = %#v",
			fixture.adapter.beginRequest,
		)
	}
}

func TestAuthenticatedRequestRejectsCallerControlledTargetsAndHeaders(
	t *testing.T,
) {
	fixture := newDynamicServiceFixture(t)
	tests := []AuthenticatedRequest{
		{Method: "GET", Path: "https://evil.example/resource"},
		{Method: "GET", Path: "//evil.example/resource"},
		{Method: "GET", Path: "/safe/../secret"},
		{Method: "GET", Path: "/safe/%2e%2e/secret"},
		{Method: "GET", Path: "/%2F%2Fevil.example/resource"},
		{Method: "GET", Path: "/resource", Header: http.Header{"Authorization": {"DPoP injected"}}},
		{Method: "GET", Path: "/resource", Header: http.Header{"DPoP": {"injected"}}},
		{Method: "GET", Path: "/resource", Header: http.Header{"Host": {"evil.example"}}},
		{Method: "GET", Path: "/resource", Header: http.Header{"Cookie": {"session=secret"}}},
		{Method: "POST", Path: "/resource", Body: make([]byte, maximumAuthenticatedBody+1)},
	}
	for index, request := range tests {
		if _, err := fixture.service.AuthenticatedRequest(
			context.Background(),
			"workspace-1",
			fixture.connectionID,
			request,
		); !errors.Is(err, ErrInvalidArgument) {
			t.Fatalf("request %d error = %v, want ErrInvalidArgument", index, err)
		}
	}
	_, requestCalls := fixture.adapter.counts()
	if requestCalls != 0 {
		t.Fatalf("unsafe requests reached adapter %d times", requestCalls)
	}
}

func TestAuthenticatedRequestNetworkErrorIsRedactedAndPersistsSessionState(
	t *testing.T,
) {
	tests := []struct {
		name      string
		update    []byte
		wantState string
	}{
		{
			name:      "without nonce update",
			wantState: "nonce-as-1|nonce-rs-1|dpop-key",
		},
		{
			name:      "with nonce update",
			update:    []byte("nonce-as-1|nonce-rs-2|dpop-key"),
			wantState: "nonce-as-1|nonce-rs-2|dpop-key",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newDynamicServiceFixture(t)
			fixture.adapter.requestResult = DynamicAuthenticatedResult{
				Session: DynamicSession{ProviderState: test.update},
			}
			fixture.adapter.requestErr = errors.New(
				"dial tcp secret-provider-diagnostic",
			)
			_, err := fixture.service.AuthenticatedRequest(
				context.Background(),
				"workspace-1",
				fixture.connectionID,
				AuthenticatedRequest{Method: "GET", Path: "/xrpc/test"},
			)
			if !errors.Is(err, ErrProviderRequestFailed) {
				t.Fatalf("network error = %v, want redacted provider error", err)
			}
			if strings.Contains(err.Error(), "secret-provider-diagnostic") {
				t.Fatalf("network error leaked provider diagnostic: %v", err)
			}
			stored, getErr := fixture.repository.GetCredential(
				context.Background(),
				"workspace-1",
				fixture.connectionID,
			)
			if getErr != nil {
				t.Fatal(getErr)
			}
			session, openErr := fixture.service.openDynamicSession(stored)
			if openErr != nil {
				t.Fatal(openErr)
			}
			if string(session.ProviderState) != test.wantState {
				t.Fatalf(
					"persisted session = %q, want %q",
					session.ProviderState,
					test.wantState,
				)
			}
			if stored.SessionLockedUntil != nil || stored.SessionLeaseID != "" {
				t.Fatal("network failure left the session lease locked")
			}
		})
	}
}

func TestSingleUseRefreshIsDurableBeforeResourceRequest(t *testing.T) {
	fixture := newDynamicServiceFixture(t)
	expired := fixture.now.Add(-time.Minute)
	fixture.repository.mu.Lock()
	stored := fixture.repository.connections[fixture.connectionID]
	stored.TokenExpiresAt = &expired
	stored.Connection.TokenExpiresAt = &expired
	fixture.repository.connections[fixture.connectionID] = stored
	fixture.repository.mu.Unlock()
	refreshedExpiry := fixture.now.Add(time.Hour)
	fixture.adapter.refreshResult = DynamicRefreshResult{
		Session: DynamicSession{
			Binding: stored.Binding,
			Credential: Credential{
				AccessToken:  "access-2",
				RefreshToken: "refresh-2",
				Scopes:       []string{"atproto"},
				ExpiresAt:    &refreshedExpiry,
			},
			ProviderState: []byte("nonce-as-2|nonce-rs-1|dpop-key"),
		},
	}
	fixture.adapter.requestResult = DynamicAuthenticatedResult{
		Response: AuthenticatedResponse{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       []byte(`{"ok":true}`),
		},
		Session: DynamicSession{
			ProviderState: []byte("nonce-as-2|nonce-rs-2|dpop-key"),
		},
	}
	if _, err := fixture.service.AuthenticatedRequest(
		context.Background(),
		"workspace-1",
		fixture.connectionID,
		AuthenticatedRequest{Method: "POST", Path: "/xrpc/write"},
	); err != nil {
		t.Fatal(err)
	}
	fixture.adapter.mu.Lock()
	if len(fixture.adapter.requestStates) != 1 ||
		string(fixture.adapter.requestStates[0]) !=
			"nonce-as-2|nonce-rs-1|dpop-key" {
		t.Fatalf("resource request saw session states %q", fixture.adapter.requestStates)
	}
	fixture.adapter.mu.Unlock()
	persisted, err := fixture.repository.GetCredential(
		context.Background(),
		"workspace-1",
		fixture.connectionID,
	)
	if err != nil {
		t.Fatal(err)
	}
	credential, err := fixture.service.openCredential(persisted)
	if err != nil {
		t.Fatal(err)
	}
	if credential.RefreshToken != "refresh-2" ||
		credential.AccessToken != "access-2" {
		t.Fatalf("persisted rotated credential = %#v", credential)
	}
	session, err := fixture.service.openDynamicSession(persisted)
	if err != nil {
		t.Fatal(err)
	}
	if string(session.ProviderState) != "nonce-as-2|nonce-rs-2|dpop-key" {
		t.Fatalf("persisted AS/RS nonce state = %q", session.ProviderState)
	}
}

func TestAuthenticatedRequestSerializesNonceUpdatesAndRejectsStaleLeaseReplay(
	t *testing.T,
) {
	fixture := newDynamicServiceFixture(t)
	fixture.adapter.requestEntered = make(chan struct{}, 1)
	fixture.adapter.requestRelease = make(chan struct{})
	fixture.adapter.requestResult.Session.ProviderState = []byte(
		"nonce-as-1|nonce-rs-2|dpop-key",
	)
	firstDone := make(chan error, 1)
	go func() {
		_, err := fixture.service.AuthenticatedRequest(
			context.Background(),
			"workspace-1",
			fixture.connectionID,
			AuthenticatedRequest{Method: "GET", Path: "/xrpc/first"},
		)
		firstDone <- err
	}()
	<-fixture.adapter.requestEntered
	if _, err := fixture.service.AuthenticatedRequest(
		context.Background(),
		"workspace-1",
		fixture.connectionID,
		AuthenticatedRequest{Method: "GET", Path: "/xrpc/concurrent"},
	); !errors.Is(err, ErrAuthenticatedRequestInProgress) {
		t.Fatalf("concurrent request error = %v", err)
	}
	close(fixture.adapter.requestRelease)
	if err := <-firstDone; err != nil {
		t.Fatal(err)
	}
	_, requestCalls := fixture.adapter.counts()
	if requestCalls != 1 {
		t.Fatalf("concurrent adapter calls = %d, want 1", requestCalls)
	}

	now := *fixture.now
	stored, _, err := fixture.repository.ClaimSession(
		context.Background(),
		"workspace-1",
		fixture.connectionID,
		now,
		now.Add(-time.Hour),
		time.Second,
	)
	if err != nil {
		t.Fatal(err)
	}
	oldCommand, err := fixture.service.dynamicSessionCommand(
		stored,
		DynamicSession{
			Binding: stored.Binding,
			Credential: Credential{
				AccessToken:  "access-1",
				RefreshToken: "refresh-1",
				Scopes:       []string{"atproto"},
				ExpiresAt:    stored.TokenExpiresAt,
			},
			ProviderState: []byte("stale-nonce"),
		},
		false,
		now,
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = fixture.repository.ClaimSession(
		context.Background(),
		"workspace-1",
		fixture.connectionID,
		now.Add(2*time.Second),
		now.Add(-time.Hour),
		time.Second,
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = fixture.repository.CompleteSession(
		context.Background(),
		oldCommand,
	); !errors.Is(err, ErrAuthenticatedRequestInProgress) {
		t.Fatalf("stale lease completion error = %v", err)
	}
}

func TestSessionLeaseRejectsStaleReconnectAfterNewClaim(t *testing.T) {
	fixture := newDynamicServiceFixture(t)
	now := *fixture.now
	first, _, err := fixture.repository.ClaimSession(
		context.Background(),
		"workspace-1",
		fixture.connectionID,
		now,
		time.Time{},
		time.Second,
	)
	if err != nil {
		t.Fatal(err)
	}
	second, _, err := fixture.repository.ClaimSession(
		context.Background(),
		"workspace-1",
		fixture.connectionID,
		now.Add(2*time.Second),
		time.Time{},
		time.Second,
	)
	if err != nil {
		t.Fatal(err)
	}
	if first.SessionLeaseID == second.SessionLeaseID {
		t.Fatal("expired session lease was reused")
	}
	if _, _, err = fixture.repository.MarkReconnectRequired(
		context.Background(),
		ReconnectCommand{
			WorkspaceID:                  "workspace-1",
			ConnectionID:                 fixture.connectionID,
			ExpectedCredentialGeneration: first.CredentialGeneration,
			ExpectedSessionLeaseID:       first.SessionLeaseID,
			Reason:                       "stale_session_failure",
			Now:                          now.Add(2 * time.Second),
		},
	); !errors.Is(err, ErrAuthenticatedRequestInProgress) {
		t.Fatalf("stale session reconnect error = %v", err)
	}
	persisted, err := fixture.repository.GetCredential(
		context.Background(),
		"workspace-1",
		fixture.connectionID,
	)
	if err != nil {
		t.Fatal(err)
	}
	if persisted.Status != StatusConnected ||
		persisted.SessionLeaseID != second.SessionLeaseID ||
		len(persisted.AccessTokenCiphertext.Data) == 0 {
		t.Fatalf("credential after stale session reconnect = %#v", persisted)
	}
	if err = fixture.repository.ReleaseSession(
		context.Background(),
		"workspace-1",
		fixture.connectionID,
		second.SessionLeaseID,
	); err != nil {
		t.Fatal(err)
	}
}

type blockingReconnectRepository struct {
	Repository
	entered chan ReconnectCommand
	release <-chan struct{}
}

func (repository *blockingReconnectRepository) MarkReconnectRequired(
	ctx context.Context,
	command ReconnectCommand,
) (Connection, bool, error) {
	repository.entered <- command
	<-repository.release
	return repository.Repository.MarkReconnectRequired(ctx, command)
}

func TestDynamicPostCompleteSessionReconnectCannotDeleteNewSession(
	t *testing.T,
) {
	fixture := newDynamicServiceFixture(t)
	reconnectEntered := make(chan ReconnectCommand, 1)
	reconnectRelease := make(chan struct{})
	fixture.service.repository = &blockingReconnectRepository{
		Repository: fixture.repository,
		entered:    reconnectEntered,
		release:    reconnectRelease,
	}
	fixture.adapter.requestErr = &ProviderFailure{
		Kind: FailurePermissionMissing,
		Code: "stale_dynamic_permission",
	}
	requestDone := make(chan error, 1)
	go func() {
		_, err := fixture.service.AuthenticatedRequest(
			context.Background(),
			"workspace-1",
			fixture.connectionID,
			AuthenticatedRequest{Method: http.MethodGet, Path: "/xrpc/stale"},
		)
		requestDone <- err
	}()
	staleReconnect := <-reconnectEntered
	if staleReconnect.ExpectedSessionLeaseID != "" {
		t.Fatalf("post-completion reconnect retained lease %q", staleReconnect.ExpectedSessionLeaseID)
	}
	now := *fixture.now
	claimed, _, err := fixture.repository.ClaimSession(
		context.Background(),
		"workspace-1",
		fixture.connectionID,
		now,
		time.Time{},
		time.Second,
	)
	if err != nil {
		t.Fatal(err)
	}
	session, err := fixture.service.openDynamicSession(claimed)
	if err != nil {
		t.Fatal(err)
	}
	command, err := fixture.service.dynamicSessionCommand(
		claimed,
		session,
		false,
		now,
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = fixture.repository.CompleteSession(
		context.Background(),
		command,
	); err != nil {
		t.Fatal(err)
	}
	close(reconnectRelease)
	if err = <-requestDone; !errors.Is(err, ErrInvalidState) {
		t.Fatalf("stale post-completion reconnect error = %v", err)
	}
	persisted, err := fixture.repository.GetCredential(
		context.Background(),
		"workspace-1",
		fixture.connectionID,
	)
	if err != nil {
		t.Fatal(err)
	}
	if persisted.Status != StatusConnected ||
		len(persisted.AccessTokenCiphertext.Data) == 0 ||
		persisted.CredentialGeneration <= staleReconnect.ExpectedCredentialGeneration {
		t.Fatalf("credential after stale post-completion reconnect = %#v", persisted)
	}
}

type failFirstSessionCompletionRepository struct {
	Repository
	mu       sync.Mutex
	failNext bool
}

func (repository *failFirstSessionCompletionRepository) CompleteSession(
	ctx context.Context,
	command SessionCommand,
) (Connection, error) {
	repository.mu.Lock()
	if repository.failNext {
		repository.failNext = false
		repository.mu.Unlock()
		return Connection{}, errors.New("simulated crash before refresh persistence")
	}
	repository.mu.Unlock()
	return repository.Repository.CompleteSession(ctx, command)
}

func TestSingleUseRefreshCrashBeforePersistenceRemainsOutcomeUnknown(
	t *testing.T,
) {
	fixture := newDynamicServiceFixture(t)
	expired := fixture.now.Add(-time.Minute)
	fixture.repository.mu.Lock()
	stored := fixture.repository.connections[fixture.connectionID]
	stored.TokenExpiresAt = &expired
	stored.Connection.TokenExpiresAt = &expired
	fixture.repository.connections[fixture.connectionID] = stored
	fixture.repository.mu.Unlock()
	refreshedExpiry := fixture.now.Add(time.Hour)
	fixture.adapter.refreshResult = DynamicRefreshResult{
		Session: DynamicSession{
			Binding: stored.Binding,
			Credential: Credential{
				AccessToken:  "access-2",
				RefreshToken: "refresh-2",
				Scopes:       []string{"atproto"},
				ExpiresAt:    &refreshedExpiry,
			},
			ProviderState: []byte("nonce-as-2|nonce-rs-1|dpop-key"),
		},
	}
	crashingRepository := &failFirstSessionCompletionRepository{
		Repository: fixture.repository,
		failNext:   true,
	}
	fixture.service.repository = crashingRepository
	if _, err := fixture.service.AuthenticatedRequest(
		context.Background(),
		"workspace-1",
		fixture.connectionID,
		AuthenticatedRequest{Method: "POST", Path: "/xrpc/write"},
	); err == nil {
		t.Fatal("simulated persistence crash unexpectedly succeeded")
	}
	refreshCalls, requestCalls := fixture.adapter.counts()
	if refreshCalls != 1 || requestCalls != 0 {
		t.Fatalf(
			"calls after crash = refresh %d/request %d, want 1/0",
			refreshCalls,
			requestCalls,
		)
	}
	locked, err := fixture.repository.GetCredential(
		context.Background(),
		"workspace-1",
		fixture.connectionID,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !locked.SessionRefreshing || locked.SessionLockedUntil == nil {
		t.Fatal("single-use refresh crash did not preserve unknown-outcome lease")
	}
	oldCredential, err := fixture.service.openCredential(locked)
	if err != nil {
		t.Fatal(err)
	}
	if oldCredential.RefreshToken != "refresh-1" {
		t.Fatalf("uncommitted refresh replaced stored token: %q", oldCredential.RefreshToken)
	}
	*fixture.now = fixture.now.Add(2 * fixture.service.refreshLockTTL)
	if _, err = fixture.service.AuthenticatedRequest(
		context.Background(),
		"workspace-1",
		fixture.connectionID,
		AuthenticatedRequest{Method: "POST", Path: "/xrpc/write"},
	); !errors.Is(err, ErrReconnectRequired) {
		t.Fatalf("post-crash request error = %v, want reconnect", err)
	}
	refreshCalls, requestCalls = fixture.adapter.counts()
	if refreshCalls != 1 || requestCalls != 0 {
		t.Fatalf(
			"old single-use token was retried: refresh %d/request %d",
			refreshCalls,
			requestCalls,
		)
	}
}

func TestDynamicRevocationCanRequireRemoteSuccess(t *testing.T) {
	fixture := newDynamicServiceFixture(t)
	fixture.adapter.config.RevocationPolicy = RevocationRemoteRequired
	fixture.adapter.revokeErr = errors.New("remote revoke unavailable")
	if _, err := fixture.service.Revoke(
		context.Background(),
		"workspace-1",
		"owner-1",
		fixture.connectionID,
	); !errors.Is(err, ErrRemoteRevocationRequired) {
		t.Fatalf("fail-closed revoke error = %v", err)
	}
	stored, err := fixture.repository.GetCredential(
		context.Background(),
		"workspace-1",
		fixture.connectionID,
	)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != StatusConnected {
		t.Fatalf("failed remote revoke changed local status to %q", stored.Status)
	}
	fixture.adapter.revokeErr = nil
	result, err := fixture.service.Revoke(
		context.Background(),
		"workspace-1",
		"owner-1",
		fixture.connectionID,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !result.ProviderRevoked || result.Connection.Status != StatusRevoked {
		t.Fatalf("successful fail-closed revoke = %#v", result)
	}
}

func TestAuthenticatedRequestBoundaryIsNotMountedPublicly(t *testing.T) {
	for _, filename := range []string{
		"contracts/openapi.yaml",
		"feature.yaml",
		"http.go",
	} {
		content, err := os.ReadFile(filename)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(content), "AuthenticatedRequest") ||
			strings.Contains(string(content), "authenticated-request") {
			t.Fatalf("%s exposes the internal authenticated request boundary", filename)
		}
	}
	openAPI, err := os.ReadFile("contracts/openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var document struct {
		Paths map[string]any `yaml:"paths"`
	}
	if err = yaml.Unmarshal(openAPI, &document); err != nil {
		t.Fatalf("OpenAPI YAML is invalid: %v", err)
	}
	if strings.Contains(string(openAPI), "previous_binding") {
		t.Fatal("OpenAPI exposes the server-side reconnect binding")
	}
	for path := range document.Paths {
		if strings.Contains(strings.ToLower(path), "authenticated") ||
			strings.Contains(strings.ToLower(path), "provider-request") {
			t.Fatalf("OpenAPI mounts internal boundary at %q", path)
		}
	}
}

func TestDynamicNetworkErrorRedactionPreservesProviderFailureKind(t *testing.T) {
	err := redactDynamicRequestError(&ProviderFailure{
		Kind:      FailureTemporary,
		Code:      "secret_remote_code",
		Retryable: true,
		Cause:     fmt.Errorf("secret cause"),
	})
	var failure *ProviderFailure
	if !errors.As(err, &failure) ||
		failure.Kind != FailureTemporary ||
		!failure.Retryable ||
		failure.Code != "authenticated_provider_request_failed" ||
		failure.Cause != nil {
		t.Fatalf("redacted failure = %#v", failure)
	}
}
