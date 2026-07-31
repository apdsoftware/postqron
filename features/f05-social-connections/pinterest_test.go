package socialconnections

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

var pinterestFixtureNow = time.Date(
	2026,
	time.July,
	30,
	12,
	0,
	0,
	0,
	time.UTC,
)

func TestPinterestOAuthUsesOfficialAuthorizationCodeContract(t *testing.T) {
	adapter := newPinterestFixtureAdapter(t, "https://api.example.test/v5", nil)
	config := adapter.Config()
	if config.AuthorizationURL != pinterestAuthorizationURL ||
		config.RedirectURL != "https://app.example.test/social/callback" ||
		config.ScopeSeparator != OAuthScopeSeparatorSpace ||
		config.SupportsPKCE {
		t.Fatalf("Pinterest OAuth config = %#v", config)
	}
	if !slicesEqual(config.Scopes, pinterestRequiredScopes) {
		t.Fatalf("Pinterest scopes = %v", config.Scopes)
	}
	for _, scope := range config.Scopes {
		if len(strings.Fields(scope)) != 1 {
			t.Fatalf("Pinterest scope is not an individual entry: %q", scope)
		}
	}
	authorizationURL, err := buildAuthorizationURL(
		config,
		"fixture-one-time-state",
		"",
	)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(authorizationURL)
	if err != nil {
		t.Fatal(err)
	}
	query := parsed.Query()
	if query.Get("scope") != strings.Join(
		pinterestRequiredScopes,
		string(OAuthScopeSeparatorSpace),
	) || query.Get("state") != "fixture-one-time-state" ||
		query.Get("response_type") != "code" ||
		query.Has("code_challenge") {
		t.Fatalf("Pinterest authorization query = %v", query)
	}
	capabilities := adapter.AdapterCapabilities()
	if !capabilities.Authorization || !capabilities.ResourceSelection ||
		!capabilities.TokenRefresh || capabilities.PKCE ||
		capabilities.RemoteRevocation {
		t.Fatalf("Pinterest adapter capabilities = %#v", capabilities)
	}
}

func TestPinterestTokenExchangeAndContinuousRefreshUseBasicAuth(
	t *testing.T,
) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(
		response http.ResponseWriter,
		request *http.Request,
	) {
		calls.Add(1)
		if request.Method != http.MethodPost ||
			request.URL.Path != "/v5/oauth/token" {
			t.Errorf("token request = %s %s", request.Method, request.URL.Path)
		}
		clientID, clientSecret, ok := request.BasicAuth()
		if !ok || clientID != "fixture-client-id" ||
			clientSecret != "fixture-client-secret" {
			t.Errorf("Pinterest Basic auth = %q %q %t", clientID, clientSecret, ok)
		}
		if request.Header.Get("Content-Type") !=
			"application/x-www-form-urlencoded" {
			t.Errorf("content type = %q", request.Header.Get("Content-Type"))
		}
		if err := request.ParseForm(); err != nil {
			t.Error(err)
		}
		switch request.Form.Get("grant_type") {
		case "authorization_code":
			assertPinterestFormFixture(
				t,
				request.Form,
				"token_exchange_form.json",
			)
			writePinterestFixture(t, response, "token_authorization_code.json")
		case "refresh_token":
			assertPinterestFormFixture(
				t,
				request.Form,
				"token_refresh_form.json",
			)
			writePinterestFixture(t, response, "token_refresh.json")
		default:
			t.Errorf("unexpected token form = %v", request.Form)
		}
	}))
	defer server.Close()

	adapter := newPinterestFixtureAdapter(t, server.URL+"/v5", server.Client())
	grant, err := adapter.Exchange(context.Background(), ExchangeRequest{
		Code:        "fixture-code",
		RedirectURL: "https://app.example.test/social/callback",
	})
	if err != nil {
		t.Fatal(err)
	}
	wantExpiresAt := pinterestFixtureNow.Add(30 * 24 * time.Hour)
	if grant.AccessToken != "fixture-access-token" ||
		grant.RefreshToken != "fixture-refresh-token" ||
		grant.ExpiresAt == nil || !grant.ExpiresAt.Equal(wantExpiresAt) ||
		!slicesEqual(grant.Scopes, pinterestRequiredScopes) {
		t.Fatalf("Pinterest grant = %#v", grant)
	}
	refreshed, err := adapter.Refresh(context.Background(), grant)
	if err != nil {
		t.Fatal(err)
	}
	if refreshed.AccessToken != "fixture-refreshed-access-token" ||
		refreshed.RefreshToken != "fixture-rotated-refresh-token" ||
		!slicesEqual(refreshed.Scopes, pinterestRequiredScopes) {
		t.Fatalf("Pinterest refreshed credential = %#v", refreshed)
	}
	if calls.Load() != 2 {
		t.Fatalf("token calls = %d, want 2", calls.Load())
	}
}

func TestPinterestBoardDiscoveryFollowsBookmarksAndRequiresExplicitSelection(
	t *testing.T,
) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(
		response http.ResponseWriter,
		request *http.Request,
	) {
		call := calls.Add(1)
		if request.Method != http.MethodGet || request.URL.Path != "/v5/boards" ||
			request.Header.Get("Authorization") !=
				"Bearer fixture-access-token" ||
			request.URL.Query().Get("page_size") != "250" {
			t.Errorf(
				"board request = %s %s headers=%v query=%v",
				request.Method,
				request.URL.Path,
				request.Header,
				request.URL.Query(),
			)
		}
		if call == 1 {
			if request.URL.Query().Has("bookmark") {
				t.Errorf("first board page has bookmark: %v", request.URL.Query())
			}
			writePinterestFixture(t, response, "boards_page_1.json")
			return
		}
		if call == 2 {
			if request.URL.Query().Get("bookmark") != "fixture-bookmark-2" {
				t.Errorf("second board bookmark = %v", request.URL.Query())
			}
			writePinterestFixture(t, response, "boards_page_2.json")
			return
		}
		http.Error(response, "unexpected page", http.StatusInternalServerError)
	}))
	defer server.Close()

	adapter := newPinterestFixtureAdapter(t, server.URL+"/v5", server.Client())
	resources, err := adapter.Discover(
		context.Background(),
		pinterestFixtureCredential(),
	)
	if err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 2 || len(resources) != 2 {
		t.Fatalf("board calls=%d resources=%#v", calls.Load(), resources)
	}
	if resources[0].Candidate.RemoteID != "111111111111" ||
		resources[1].Candidate.RemoteID != "222222222222" {
		t.Fatalf("Pinterest board candidates = %#v", resources)
	}
	for _, resource := range resources {
		if resource.Candidate.ResourceType != ResourcePinterestBoard ||
			resource.Candidate.AccountType != AccountTypeBoard ||
			resource.Credential.AccessToken != "fixture-access-token" ||
			resource.Credential.RefreshToken != "fixture-refresh-token" {
			t.Fatalf("Pinterest discovered resource = %#v", resource)
		}
		encoded, err := json.Marshal(resource.Candidate)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(encoded), "fixture-access-token") ||
			strings.Contains(string(encoded), "fixture-refresh-token") {
			t.Fatalf("candidate exposed token: %s", encoded)
		}
	}
}

func TestPinterestVerifyAndUserTokenRevokeAreFailClosed(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(
		response http.ResponseWriter,
		request *http.Request,
	) {
		calls.Add(1)
		if request.Method != http.MethodGet ||
			request.URL.Path != "/v5/boards/111111111111" ||
			request.Header.Get("Authorization") !=
				"Bearer fixture-access-token" {
			t.Errorf("verify request = %s %s", request.Method, request.URL.Path)
		}
		writePinterestFixture(t, response, "board_verify.json")
	}))
	defer server.Close()

	adapter := newPinterestFixtureAdapter(t, server.URL+"/v5", server.Client())
	credential := pinterestFixtureCredential()
	if err := adapter.Verify(
		context.Background(),
		"111111111111",
		credential,
	); err != nil {
		t.Fatal(err)
	}
	if err := adapter.Revoke(
		context.Background(),
		"111111111111",
		credential,
	); !errors.Is(err, ErrExternalRevocationUnavailable) {
		t.Fatalf("Pinterest Revoke() error = %v", err)
	}
	if calls.Load() != 1 {
		t.Fatalf("remote calls after unsupported revoke = %d, want 1", calls.Load())
	}
}

func TestPinterestServiceUsesOneTimeStateExplicitSelectionAndEncryptedTokens(
	t *testing.T,
) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(
		response http.ResponseWriter,
		request *http.Request,
	) {
		calls.Add(1)
		switch request.URL.Path {
		case "/v5/oauth/token":
			writePinterestFixture(t, response, "token_authorization_code.json")
		case "/v5/boards":
			if request.URL.Query().Get("bookmark") == "" {
				writePinterestFixture(t, response, "boards_page_1.json")
				return
			}
			writePinterestFixture(t, response, "boards_page_2.json")
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()
	adapter := newPinterestFixtureAdapter(t, server.URL+"/v5", server.Client())
	repository := NewMemoryRepository()
	cipher, err := NewAESGCMCipher("fixture-key", make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewService(Config{
		Repository: repository,
		Authorizer: &fakeAuthorizer{permissions: map[Permission]bool{
			PermissionViewWorkspace:  true,
			PermissionManageChannels: true,
		}},
		Cipher: cipher,
		Quota:  newFakeChannelQuota(),
		Adapters: map[Provider]Adapter{
			ProviderPinterest: adapter,
		},
		Availability: map[Provider]ProviderAvailability{
			ProviderPinterest: {
				Provider:           ProviderPinterest,
				Status:             ProviderAvailable,
				ConfigurationState: ProviderReady,
			},
		},
		Now: func() time.Time { return pinterestFixtureNow },
	})
	if err != nil {
		t.Fatal(err)
	}
	authorization, err := service.Begin(context.Background(), BeginRequest{
		WorkspaceID: "workspace-pinterest",
		ActorID:     "owner-pinterest",
		Provider:    ProviderPinterest,
	})
	if err != nil {
		t.Fatal(err)
	}
	authorizationURL, err := url.Parse(authorization.URL)
	if err != nil {
		t.Fatal(err)
	}
	state := authorizationURL.Query().Get("state")
	if state == "" {
		t.Fatal("Pinterest authorization omitted one-time state")
	}
	selection, err := service.Callback(context.Background(), CallbackRequest{
		State: state,
		Code:  "fixture-code",
	})
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(selection)
	if err != nil {
		t.Fatal(err)
	}
	for _, token := range []string{
		"fixture-access-token",
		"fixture-refresh-token",
	} {
		if bytes.Contains(encoded, []byte(token)) {
			t.Fatalf("Pinterest selection exposed token: %s", encoded)
		}
	}
	if _, err = service.Callback(
		context.Background(),
		CallbackRequest{State: state, Code: "fixture-code"},
	); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("replayed Pinterest state error = %v", err)
	}
	connections, err := service.List(
		context.Background(),
		"workspace-pinterest",
		"owner-pinterest",
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(connections) != 0 {
		t.Fatalf("board connected without explicit selection: %#v", connections)
	}
	repository.mu.Lock()
	storedSelection := repository.selections[selection.ID]
	repository.mu.Unlock()
	if len(storedSelection.Resources) != 2 {
		t.Fatalf("stored Pinterest selection = %#v", storedSelection)
	}
	for _, resource := range storedSelection.Resources {
		if len(resource.AccessTokenCiphertext.Data) == 0 ||
			len(resource.RefreshTokenCiphertext.Data) == 0 ||
			bytes.Contains(
				resource.AccessTokenCiphertext.Data,
				[]byte("fixture-access-token"),
			) ||
			bytes.Contains(
				resource.RefreshTokenCiphertext.Data,
				[]byte("fixture-refresh-token"),
			) {
			t.Fatal("Pinterest selection did not encrypt both tokens")
		}
	}
	connection, err := service.Select(context.Background(), SelectRequest{
		WorkspaceID: "workspace-pinterest",
		ActorID:     "owner-pinterest",
		SelectionID: selection.ID,
		RemoteID:    "111111111111",
	})
	if err != nil {
		t.Fatal(err)
	}
	stored, err := repository.GetCredential(
		context.Background(),
		"workspace-pinterest",
		connection.ID,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(stored.AccessTokenCiphertext.Data) == 0 ||
		len(stored.RefreshTokenCiphertext.Data) == 0 {
		t.Fatal("connected Pinterest tokens are not encrypted")
	}
	revocation, err := service.Revoke(
		context.Background(),
		"workspace-pinterest",
		"owner-pinterest",
		connection.ID,
	)
	if err != nil {
		t.Fatal(err)
	}
	if revocation.ProviderRevoked ||
		revocation.Connection.Status != StatusRevoked {
		t.Fatalf("Pinterest revocation = %#v", revocation)
	}
	revoked, err := repository.GetCredential(
		context.Background(),
		"workspace-pinterest",
		connection.ID,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(revoked.AccessTokenCiphertext.Data) != 0 ||
		len(revoked.RefreshTokenCiphertext.Data) != 0 {
		t.Fatal("local Pinterest revoke retained encrypted tokens")
	}
	if calls.Load() != 3 {
		t.Fatalf("Pinterest remote calls = %d, want 3", calls.Load())
	}
}

func TestPinterestHTTPFailuresAreClientSafe(t *testing.T) {
	tests := []struct {
		status    int
		fixture   string
		kind      ProviderFailureKind
		retryable bool
	}{
		{http.StatusUnauthorized, "error_401.json", FailureAuthentication, false},
		{http.StatusForbidden, "error_403.json", FailurePermissionMissing, false},
		{http.StatusNotFound, "error_404.json", FailureResourceGone, false},
		{http.StatusTooManyRequests, "error_429.json", FailureTemporary, true},
		{http.StatusServiceUnavailable, "error_503.json", FailureTemporary, true},
	}
	for _, test := range tests {
		t.Run(http.StatusText(test.status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(
				response http.ResponseWriter,
				_ *http.Request,
			) {
				response.WriteHeader(test.status)
				writePinterestFixture(t, response, test.fixture)
			}))
			defer server.Close()
			adapter := newPinterestFixtureAdapter(
				t,
				server.URL+"/v5",
				server.Client(),
			)
			err := adapter.Verify(
				context.Background(),
				"111111111111",
				pinterestFixtureCredential(),
			)
			var failure *ProviderFailure
			if !errors.As(err, &failure) || failure.Kind != test.kind ||
				failure.Retryable != test.retryable {
				t.Fatalf("Pinterest failure = %#v error=%v", failure, err)
			}
			payload := string(pinterestFixture(t, test.fixture))
			var envelope struct {
				Message string `json:"message"`
			}
			if json.Unmarshal([]byte(payload), &envelope) != nil {
				t.Fatal("error fixture is invalid")
			}
			if strings.Contains(err.Error(), envelope.Message) {
				t.Fatalf("provider message leaked through error: %v", err)
			}
		})
	}
}

func TestPinterestMalformedPayloadsFailClosed(t *testing.T) {
	tests := []struct {
		name    string
		fixture string
		action  func(*PinterestAdapter) error
	}{
		{
			name:    "malformed token JSON",
			fixture: "malformed_response.txt",
			action: func(adapter *PinterestAdapter) error {
				_, err := adapter.Exchange(context.Background(), ExchangeRequest{
					Code:        "fixture-code",
					RedirectURL: "https://app.example.test/social/callback",
				})
				return err
			},
		},
		{
			name:    "incomplete token",
			fixture: "token_incomplete.json",
			action: func(adapter *PinterestAdapter) error {
				_, err := adapter.Exchange(context.Background(), ExchangeRequest{
					Code:        "fixture-code",
					RedirectURL: "https://app.example.test/social/callback",
				})
				return err
			},
		},
		{
			name:    "boards omit items",
			fixture: "boards_missing_items.json",
			action: func(adapter *PinterestAdapter) error {
				_, err := adapter.Discover(
					context.Background(),
					pinterestFixtureCredential(),
				)
				return err
			},
		},
		{
			name:    "verify returns another board",
			fixture: "board_mismatch.json",
			action: func(adapter *PinterestAdapter) error {
				return adapter.Verify(
					context.Background(),
					"111111111111",
					pinterestFixtureCredential(),
				)
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(
				response http.ResponseWriter,
				_ *http.Request,
			) {
				writePinterestFixture(t, response, test.fixture)
			}))
			defer server.Close()
			err := test.action(newPinterestFixtureAdapter(
				t,
				server.URL+"/v5",
				server.Client(),
			))
			var failure *ProviderFailure
			if !errors.As(err, &failure) ||
				(failure.Kind != FailureInvalidResponse &&
					failure.Kind != FailureResourceGone) ||
				failure.Retryable {
				t.Fatalf("malformed Pinterest failure = %#v error=%v", failure, err)
			}
		})
	}
}

func newPinterestFixtureAdapter(
	t *testing.T,
	apiBaseURL string,
	client *http.Client,
) *PinterestAdapter {
	t.Helper()
	adapter, err := NewPinterestAdapter(PinterestAdapterConfig{
		ClientID:     "fixture-client-id",
		ClientSecret: "fixture-client-secret",
		RedirectURL:  "https://app.example.test/social/callback",
		HTTPClient:   client,
		Now:          func() time.Time { return pinterestFixtureNow },
		APIBaseURL:   apiBaseURL,
	})
	if err != nil {
		t.Fatal(err)
	}
	return adapter
}

func pinterestFixtureCredential() Credential {
	expiresAt := pinterestFixtureNow.Add(30 * 24 * time.Hour)
	return Credential{
		AccessToken:  "fixture-access-token",
		RefreshToken: "fixture-refresh-token",
		ExpiresAt:    &expiresAt,
		Scopes:       append([]string(nil), pinterestRequiredScopes...),
	}
}

func pinterestFixture(t *testing.T, name string) []byte {
	t.Helper()
	payload, err := os.ReadFile("test/fixtures/pinterest/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return payload
}

func writePinterestFixture(
	t *testing.T,
	response http.ResponseWriter,
	name string,
) {
	t.Helper()
	response.Header().Set("Content-Type", "application/json")
	if _, err := io.Copy(response, strings.NewReader(string(
		pinterestFixture(t, name),
	))); err != nil {
		t.Error(err)
	}
}

func assertPinterestFormFixture(
	t *testing.T,
	actual url.Values,
	name string,
) {
	t.Helper()
	var expected url.Values
	if err := json.Unmarshal(pinterestFixture(t, name), &expected); err != nil {
		t.Fatal(err)
	}
	if len(actual) != len(expected) {
		t.Fatalf("Pinterest form = %v, want %v", actual, expected)
	}
	for key, values := range expected {
		if !slicesEqual(actual[key], values) {
			t.Fatalf("Pinterest form[%q] = %v, want %v", key, actual[key], values)
		}
	}
}
