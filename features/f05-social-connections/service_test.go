package socialconnections

import (
	"context"
	"errors"
	"net/url"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

var serviceTestNow = time.Date(2026, 7, 24, 16, 0, 0, 0, time.UTC)

type fakeAuthorizer struct {
	permissions map[Permission]bool
	calls       []Permission
}

func (authorizer *fakeAuthorizer) Authorize(
	_ context.Context,
	_, _ string,
	permission Permission,
) error {
	authorizer.calls = append(authorizer.calls, permission)
	if !authorizer.permissions[permission] {
		return errors.New("denied")
	}
	return nil
}

type fakeAdapter struct {
	mu            sync.Mutex
	config        OAuthConfig
	grant         Credential
	resources     []DiscoveredResource
	exchange      ExchangeRequest
	exchangeCalls int
	refreshResult Credential
	refreshErr    error
	refreshCalls  int
	verifyErr     error
	verifyCalls   int
	revokeErr     error
	revokeCalls   int
}

func (adapter *fakeAdapter) Config() OAuthConfig {
	return adapter.config
}

func (adapter *fakeAdapter) Exchange(
	_ context.Context,
	request ExchangeRequest,
) (Credential, error) {
	adapter.mu.Lock()
	defer adapter.mu.Unlock()
	adapter.exchange = request
	adapter.exchangeCalls++
	return cloneCredential(adapter.grant), nil
}

func (adapter *fakeAdapter) Discover(
	context.Context,
	Credential,
) ([]DiscoveredResource, error) {
	adapter.mu.Lock()
	defer adapter.mu.Unlock()
	resources := make([]DiscoveredResource, len(adapter.resources))
	for index, resource := range adapter.resources {
		resources[index] = DiscoveredResource{
			Candidate:  cloneCandidate(resource.Candidate),
			Credential: cloneCredential(resource.Credential),
		}
	}
	return resources, nil
}

func (adapter *fakeAdapter) Refresh(
	context.Context,
	Credential,
) (Credential, error) {
	adapter.mu.Lock()
	defer adapter.mu.Unlock()
	adapter.refreshCalls++
	return cloneCredential(adapter.refreshResult), adapter.refreshErr
}

func (adapter *fakeAdapter) Verify(
	context.Context,
	string,
	Credential,
) error {
	adapter.mu.Lock()
	defer adapter.mu.Unlock()
	adapter.verifyCalls++
	return adapter.verifyErr
}

func (adapter *fakeAdapter) Revoke(
	context.Context,
	string,
	Credential,
) error {
	adapter.mu.Lock()
	defer adapter.mu.Unlock()
	adapter.revokeCalls++
	return adapter.revokeErr
}

func (adapter *fakeAdapter) counts() (refresh, verify, revoke int) {
	adapter.mu.Lock()
	defer adapter.mu.Unlock()
	return adapter.refreshCalls, adapter.verifyCalls, adapter.revokeCalls
}

type serviceFixture struct {
	service    *Service
	repository *MemoryRepository
	authorizer *fakeAuthorizer
	facebook   *fakeAdapter
	instagram  *fakeAdapter
}

func newServiceFixture(t *testing.T) serviceFixture {
	t.Helper()
	repository := NewMemoryRepository()
	authorizer := &fakeAuthorizer{permissions: map[Permission]bool{
		PermissionViewWorkspace:  true,
		PermissionManageChannels: true,
	}}
	cipher, err := NewAESGCMCipher(
		"test-key",
		[]byte("0123456789abcdef0123456789abcdef"),
	)
	if err != nil {
		t.Fatal(err)
	}
	expires := serviceTestNow.Add(time.Hour)
	facebook := &fakeAdapter{
		config: OAuthConfig{
			ClientID:         "facebook-client",
			AuthorizationURL: "https://www.facebook.com/v25.0/dialog/oauth",
			RedirectURL:      "https://app.example.test/social/callback",
			Scopes: append(
				[]string(nil),
				requiredScopes[ProviderFacebookPages]...,
			),
			SupportsPKCE: true,
		},
		grant: Credential{
			AccessToken: "facebook-user-token",
			ExpiresAt:   &expires,
			Scopes: append(
				[]string(nil),
				requiredScopes[ProviderFacebookPages]...,
			),
		},
		resources: []DiscoveredResource{
			facebookResource("page-1", "Page one", "page-token-1", &expires),
			facebookResource("page-2", "Page two", "page-token-2", &expires),
		},
	}
	instagram := &fakeAdapter{
		config: OAuthConfig{
			ClientID:         "instagram-client",
			AuthorizationURL: "https://www.instagram.com/oauth/authorize",
			RedirectURL:      "https://app.example.test/social/callback",
			Scopes: append(
				[]string(nil),
				requiredScopes[ProviderInstagramProfessional]...,
			),
		},
		grant: Credential{
			AccessToken: "instagram-token",
			ExpiresAt:   &expires,
			Scopes: append(
				[]string(nil),
				requiredScopes[ProviderInstagramProfessional]...,
			),
		},
		resources: []DiscoveredResource{instagramResource(
			"ig-1",
			"postqron",
			"instagram-token",
			&expires,
		)},
	}
	service, err := NewService(Config{
		Repository: repository,
		Authorizer: authorizer,
		Cipher:     cipher,
		Adapters: map[Provider]Adapter{
			ProviderFacebookPages:         facebook,
			ProviderInstagramProfessional: instagram,
		},
		Now: func() time.Time { return serviceTestNow },
	})
	if err != nil {
		t.Fatal(err)
	}
	return serviceFixture{
		service:    service,
		repository: repository,
		authorizer: authorizer,
		facebook:   facebook,
		instagram:  instagram,
	}
}

func TestConnectionRequiresOwnerAuthorizationAndExplicitResourceSelection(
	t *testing.T,
) {
	fixture := newServiceFixture(t)
	fixture.authorizer.permissions[PermissionManageChannels] = false
	if _, err := fixture.service.Begin(context.Background(), BeginRequest{
		WorkspaceID: "workspace-1",
		ActorID:     "member-1",
		Provider:    ProviderFacebookPages,
	}); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("member Begin() error = %v, want unauthorized", err)
	}
	if fixture.facebook.exchangeCalls != 0 {
		t.Fatal("provider called before authorization")
	}

	fixture.authorizer.permissions[PermissionManageChannels] = true
	selection := authorizeAndDiscover(
		t,
		fixture.service,
		ProviderFacebookPages,
	)
	connections, err := fixture.service.List(
		context.Background(),
		"workspace-1",
		"owner-1",
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(connections) != 0 {
		t.Fatal("resource connected before explicit selection")
	}
	if len(selection.Resources) != 2 {
		t.Fatalf("candidate count = %d, want 2", len(selection.Resources))
	}
	if _, err = fixture.service.Select(context.Background(), SelectRequest{
		WorkspaceID: "workspace-1",
		ActorID:     "owner-1",
		SelectionID: selection.ID,
		RemoteID:    "not-offered",
	}); !errors.Is(err, ErrResourceNotFound) {
		t.Fatalf("unoffered Select() error = %v", err)
	}
	connection, err := fixture.service.Select(context.Background(), SelectRequest{
		WorkspaceID: "workspace-1",
		ActorID:     "owner-1",
		SelectionID: selection.ID,
		RemoteID:    "page-2",
	})
	if err != nil {
		t.Fatal(err)
	}
	if connection.RemoteID != "page-2" || connection.Status != StatusConnected {
		t.Fatalf("connection = %#v", connection)
	}
	if !slices.Equal(connection.Scopes, requiredScopes[ProviderFacebookPages]) {
		t.Fatalf("connection scopes = %v", connection.Scopes)
	}
	if _, err = fixture.service.Select(context.Background(), SelectRequest{
		WorkspaceID: "workspace-1",
		ActorID:     "owner-1",
		SelectionID: selection.ID,
		RemoteID:    "page-2",
	}); !errors.Is(err, ErrResourceNotFound) {
		t.Fatalf("reused selection error = %v", err)
	}
}

func TestOAuthStatePKCEAndCredentialsAreNeverStoredInPlaintext(t *testing.T) {
	fixture := newServiceFixture(t)
	authorization, state := beginAuthorization(
		t,
		fixture.service,
		ProviderFacebookPages,
	)
	parsed, err := url.Parse(authorization.URL)
	if err != nil {
		t.Fatal(err)
	}
	query := parsed.Query()
	if query.Get("state") != state ||
		query.Get("code_challenge") == "" ||
		query.Get("code_challenge_method") != "S256" {
		t.Fatalf("authorization query = %v", query)
	}
	selection, err := fixture.service.Callback(context.Background(), CallbackRequest{
		State: state,
		Code:  "provider-code",
	})
	if err != nil {
		t.Fatal(err)
	}
	if fixture.facebook.exchange.PKCEVerifier == "" {
		t.Fatal("PKCE verifier was not supplied to token exchange")
	}
	if strings.Contains(
		fixture.facebook.exchange.PKCEVerifier,
		query.Get("code_challenge"),
	) {
		t.Fatal("PKCE verifier and challenge should differ")
	}
	if _, err = fixture.service.Callback(context.Background(), CallbackRequest{
		State: state,
		Code:  "provider-code",
	}); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("replayed state error = %v", err)
	}
	connection, err := fixture.service.Select(context.Background(), SelectRequest{
		WorkspaceID: "workspace-1",
		ActorID:     "owner-1",
		SelectionID: selection.ID,
		RemoteID:    "page-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	stored, err := fixture.repository.GetCredential(
		context.Background(),
		"workspace-1",
		connection.ID,
	)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(stored.AccessTokenCiphertext.Data), "page-token-1") {
		t.Fatal("stored credential contains plaintext access token")
	}
	listed, err := fixture.service.List(
		context.Background(),
		"workspace-1",
		"owner-1",
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 1 {
		t.Fatalf("listed connections = %d", len(listed))
	}
}

func TestExpiredCredentialRefreshesOnceAndEmitsEvent(t *testing.T) {
	fixture := newServiceFixture(t)
	expired := serviceTestNow.Add(-time.Minute)
	fixture.instagram.resources = []DiscoveredResource{instagramResource(
		"ig-1",
		"postqron",
		"old-token",
		&expired,
	)}
	refreshedExpiry := serviceTestNow.Add(50 * 24 * time.Hour)
	fixture.instagram.refreshResult = Credential{
		AccessToken: "new-token",
		ExpiresAt:   &refreshedExpiry,
		Scopes: append(
			[]string(nil),
			requiredScopes[ProviderInstagramProfessional]...,
		),
	}
	connection := connectResource(
		t,
		fixture.service,
		ProviderInstagramProfessional,
		"ig-1",
	)
	token, err := fixture.service.AccessToken(
		context.Background(),
		"workspace-1",
		connection.ID,
	)
	if err != nil {
		t.Fatal(err)
	}
	if token != "new-token" {
		t.Fatalf("access token = %q", token)
	}
	token, err = fixture.service.AccessToken(
		context.Background(),
		"workspace-1",
		connection.ID,
	)
	if err != nil {
		t.Fatal(err)
	}
	if token != "new-token" {
		t.Fatalf("second access token = %q", token)
	}
	refreshCalls, _, _ := fixture.instagram.counts()
	if refreshCalls != 1 {
		t.Fatalf("refresh calls = %d, want 1", refreshCalls)
	}
	if countEvents(fixture.repository.Events(), EventTokenRefreshed) != 1 {
		t.Fatalf("events = %#v", fixture.repository.Events())
	}
}

func TestRevokedPermissionTransitionsOnceWithoutRefreshLoopAndReconnects(
	t *testing.T,
) {
	fixture := newServiceFixture(t)
	expired := serviceTestNow.Add(-time.Minute)
	fixture.instagram.resources = []DiscoveredResource{instagramResource(
		"ig-1",
		"postqron",
		"revoked-token",
		&expired,
	)}
	fixture.instagram.refreshErr = &ProviderFailure{
		Kind: FailureAuthentication,
		Code: "meta_error_190",
	}
	connection := connectResource(
		t,
		fixture.service,
		ProviderInstagramProfessional,
		"ig-1",
	)
	if _, err := fixture.service.AccessToken(
		context.Background(),
		"workspace-1",
		connection.ID,
	); !errors.Is(err, ErrReconnectRequired) {
		t.Fatalf("first AccessToken() error = %v", err)
	}
	if _, err := fixture.service.AccessToken(
		context.Background(),
		"workspace-1",
		connection.ID,
	); !errors.Is(err, ErrReconnectRequired) {
		t.Fatalf("second AccessToken() error = %v", err)
	}
	refreshCalls, _, _ := fixture.instagram.counts()
	if refreshCalls != 1 {
		t.Fatalf("refresh calls = %d, want exactly 1", refreshCalls)
	}
	stored, err := fixture.repository.GetCredential(
		context.Background(),
		"workspace-1",
		connection.ID,
	)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != StatusReconnectRequired ||
		len(stored.AccessTokenCiphertext.Data) != 0 {
		t.Fatalf("reconnect state = %#v", stored)
	}
	if countEvents(fixture.repository.Events(), EventReconnectRequired) != 1 {
		t.Fatalf("events = %#v", fixture.repository.Events())
	}

	newExpiry := serviceTestNow.Add(50 * 24 * time.Hour)
	fixture.instagram.refreshErr = nil
	fixture.instagram.resources = []DiscoveredResource{instagramResource(
		"ig-1",
		"postqron",
		"reconnected-token",
		&newExpiry,
	)}
	reconnected := connectResource(
		t,
		fixture.service,
		ProviderInstagramProfessional,
		"ig-1",
	)
	if reconnected.ID != connection.ID ||
		reconnected.Status != StatusConnected ||
		reconnected.ReconnectReason != "" {
		t.Fatalf("reconnected = %#v", reconnected)
	}
	token, err := fixture.service.AccessToken(
		context.Background(),
		"workspace-1",
		connection.ID,
	)
	if err != nil {
		t.Fatal(err)
	}
	if token != "reconnected-token" {
		t.Fatalf("reconnected token = %q", token)
	}
	if countEvents(fixture.repository.Events(), EventReconnected) != 1 {
		t.Fatalf("events = %#v", fixture.repository.Events())
	}
}

func TestRevocationIsOwnerOnlyWipesTokensAndIsIdempotent(t *testing.T) {
	fixture := newServiceFixture(t)
	connection := connectResource(
		t,
		fixture.service,
		ProviderFacebookPages,
		"page-1",
	)
	fixture.authorizer.permissions[PermissionManageChannels] = false
	if _, err := fixture.service.Revoke(
		context.Background(),
		"workspace-1",
		"member-1",
		connection.ID,
	); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("member Revoke() error = %v", err)
	}
	_, _, revokeCalls := fixture.facebook.counts()
	if revokeCalls != 0 {
		t.Fatal("provider revoke called for unauthorized member")
	}

	fixture.authorizer.permissions[PermissionManageChannels] = true
	fixture.facebook.revokeErr = ErrExternalRevocationUnavailable
	result, err := fixture.service.Revoke(
		context.Background(),
		"workspace-1",
		"owner-1",
		connection.ID,
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.ProviderRevoked || result.Connection.Status != StatusRevoked {
		t.Fatalf("revocation result = %#v", result)
	}
	stored, err := fixture.repository.GetCredential(
		context.Background(),
		"workspace-1",
		connection.ID,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(stored.AccessTokenCiphertext.Data) != 0 ||
		len(stored.RefreshTokenCiphertext.Data) != 0 {
		t.Fatal("local revocation retained credential ciphertext")
	}
	if _, err = fixture.service.Revoke(
		context.Background(),
		"workspace-1",
		"owner-1",
		connection.ID,
	); err != nil {
		t.Fatal(err)
	}
	_, _, revokeCalls = fixture.facebook.counts()
	if revokeCalls != 1 {
		t.Fatalf("provider revoke calls = %d, want 1", revokeCalls)
	}
	if countEvents(fixture.repository.Events(), EventDisconnected) != 1 {
		t.Fatalf("events = %#v", fixture.repository.Events())
	}
}

func authorizeAndDiscover(
	t *testing.T,
	service *Service,
	provider Provider,
) Selection {
	t.Helper()
	_, state := beginAuthorization(t, service, provider)
	selection, err := service.Callback(context.Background(), CallbackRequest{
		State: state,
		Code:  "provider-code",
	})
	if err != nil {
		t.Fatal(err)
	}
	return selection
}

func beginAuthorization(
	t *testing.T,
	service *Service,
	provider Provider,
) (Authorization, string) {
	t.Helper()
	authorization, err := service.Begin(context.Background(), BeginRequest{
		WorkspaceID: "workspace-1",
		ActorID:     "owner-1",
		Provider:    provider,
	})
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(authorization.URL)
	if err != nil {
		t.Fatal(err)
	}
	state := parsed.Query().Get("state")
	if state == "" {
		t.Fatal("authorization URL has no state")
	}
	return authorization, state
}

func connectResource(
	t *testing.T,
	service *Service,
	provider Provider,
	remoteID string,
) Connection {
	t.Helper()
	selection := authorizeAndDiscover(t, service, provider)
	connection, err := service.Select(context.Background(), SelectRequest{
		WorkspaceID: "workspace-1",
		ActorID:     "owner-1",
		SelectionID: selection.ID,
		RemoteID:    remoteID,
	})
	if err != nil {
		t.Fatal(err)
	}
	return connection
}

func facebookResource(
	id, name, token string,
	expiresAt *time.Time,
) DiscoveredResource {
	scopes := append([]string(nil), requiredScopes[ProviderFacebookPages]...)
	return DiscoveredResource{
		Candidate: Candidate{
			RemoteID:     id,
			ResourceType: ResourceFacebookPage,
			AccountType:  AccountTypePage,
			DisplayName:  name,
			Scopes:       scopes,
		},
		Credential: Credential{
			AccessToken: token,
			ExpiresAt:   cloneTimePointer(expiresAt),
			Scopes:      scopes,
		},
	}
}

func instagramResource(
	id, handle, token string,
	expiresAt *time.Time,
) DiscoveredResource {
	scopes := append(
		[]string(nil),
		requiredScopes[ProviderInstagramProfessional]...,
	)
	return DiscoveredResource{
		Candidate: Candidate{
			RemoteID:     id,
			ResourceType: ResourceInstagramProfessional,
			AccountType:  AccountTypeBusiness,
			DisplayName:  handle,
			Handle:       handle,
			Scopes:       scopes,
		},
		Credential: Credential{
			AccessToken: token,
			ExpiresAt:   cloneTimePointer(expiresAt),
			Scopes:      scopes,
		},
	}
}

func cloneCredential(credential Credential) Credential {
	credential.ExpiresAt = cloneTimePointer(credential.ExpiresAt)
	credential.Scopes = append([]string(nil), credential.Scopes...)
	return credential
}

func countEvents(events []Event, eventType string) int {
	count := 0
	for _, event := range events {
		if event.Type == eventType {
			count++
		}
	}
	return count
}
