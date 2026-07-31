package socialconnections

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

var xFixtureNow = time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)

func TestXAdapterOfficialOAuthLifecycle(t *testing.T) {
	var tokenCalls atomic.Int32
	var userCalls atomic.Int32
	var revokeMu sync.Mutex
	var revokedTokens []string
	server := httptest.NewServer(http.HandlerFunc(func(
		response http.ResponseWriter,
		request *http.Request,
	) {
		switch request.URL.Path {
		case xOAuthTokenPath:
			if request.Method != http.MethodPost {
				t.Errorf("token method = %s", request.Method)
			}
			clientID, clientSecret, ok := request.BasicAuth()
			if !ok ||
				clientID != "fixture-x-client" ||
				clientSecret != "fixture-x-client-secret" {
				t.Errorf("token Basic auth = %q, %q, %v", clientID, clientSecret, ok)
			}
			if request.URL.RawQuery != "" {
				t.Errorf("token query exposed values: %q", request.URL.RawQuery)
			}
			if err := request.ParseForm(); err != nil {
				t.Error(err)
			}
			call := tokenCalls.Add(1)
			switch call {
			case 1:
				if request.Form.Get("grant_type") != "authorization_code" ||
					request.Form.Get("code") != "fixture-code" ||
					request.Form.Get("code_verifier") != "fixture-verifier" ||
					request.Form.Get("redirect_uri") !=
						"https://app.example.test/api/v1/social-authorizations/callback" ||
					request.Form.Get("client_id") != "" {
					t.Errorf("exchange form = %v", request.Form)
				}
				writeXFixture(t, response, "token_exchange.json")
			case 2:
				if request.Form.Get("grant_type") != "refresh_token" ||
					request.Form.Get("refresh_token") !=
						"fixture-x-refresh-token" ||
					request.Form.Get("client_id") != "" {
					t.Errorf("refresh form = %v", request.Form)
				}
				writeXFixture(t, response, "token_refresh.json")
			default:
				t.Errorf("unexpected token call %d", call)
				http.Error(response, "unexpected", http.StatusInternalServerError)
			}
		case xAuthenticatedUserPath:
			userCalls.Add(1)
			if request.Method != http.MethodGet ||
				request.Header.Get("Authorization") !=
					"Bearer fixture-x-access-token" &&
					request.Header.Get("Authorization") !=
						"Bearer fixture-x-refreshed-access-token" {
				t.Errorf(
					"user request = %s %q",
					request.Method,
					request.Header.Get("Authorization"),
				)
			}
			if request.URL.Query().Get("user.fields") != "profile_image_url" {
				t.Errorf("user fields = %q", request.URL.Query().Get("user.fields"))
			}
			writeXFixture(t, response, "user.json")
		case xOAuthRevokePath:
			if request.Method != http.MethodPost {
				t.Errorf("revoke method = %s", request.Method)
			}
			clientID, clientSecret, ok := request.BasicAuth()
			if !ok ||
				clientID != "fixture-x-client" ||
				clientSecret != "fixture-x-client-secret" {
				t.Errorf("revoke Basic auth = %q, %q, %v", clientID, clientSecret, ok)
			}
			if err := request.ParseForm(); err != nil {
				t.Error(err)
			}
			revokeMu.Lock()
			revokedTokens = append(revokedTokens, request.Form.Get("token"))
			revokeMu.Unlock()
			writeXFixture(t, response, "revoke.json")
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()

	adapter := newFixtureXAdapter(t, server)
	config := adapter.Config()
	if config.AuthorizationURL != xOfficialAuthorizationURL ||
		!config.SupportsPKCE ||
		config.ScopeSeparator != OAuthScopeSeparatorSpace ||
		!slices.Equal(config.Scopes, xRequiredScopes) {
		t.Fatalf("OAuth config = %#v", config)
	}
	capabilities := adapter.AdapterCapabilities()
	if capabilities != (AdapterCapabilities{
		Authorization:     true,
		PKCE:              true,
		ResourceSelection: true,
		TokenRefresh:      true,
		RemoteRevocation:  true,
	}) {
		t.Fatalf("capabilities = %#v", capabilities)
	}
	authorizationURL, err := buildAuthorizationURL(
		config,
		"fixture-state",
		"fixture-verifier",
	)
	if err != nil {
		t.Fatal(err)
	}
	parsedAuthorizationURL, err := url.Parse(authorizationURL)
	if err != nil {
		t.Fatal(err)
	}
	query := parsedAuthorizationURL.Query()
	if query.Get("scope") != strings.Join(xRequiredScopes, " ") ||
		query.Get("state") != "fixture-state" ||
		query.Get("code_challenge_method") != "S256" ||
		query.Get("code_challenge") == "" ||
		query.Get("code_challenge") == "fixture-verifier" ||
		query.Get("client_secret") != "" {
		t.Fatalf("authorization query = %v", query)
	}

	grant, err := adapter.Exchange(context.Background(), ExchangeRequest{
		Code:         "fixture-code",
		RedirectURL:  config.RedirectURL,
		PKCEVerifier: "fixture-verifier",
	})
	if err != nil {
		t.Fatal(err)
	}
	if grant.AccessToken != "fixture-x-access-token" ||
		grant.RefreshToken != "fixture-x-refresh-token" ||
		grant.ExpiresAt == nil ||
		!grant.ExpiresAt.Equal(xFixtureNow.Add(2*time.Hour)) ||
		!slices.Equal(grant.Scopes, xRequiredScopes) {
		t.Fatalf("exchange credential = %#v", grant)
	}
	resources, err := adapter.Discover(context.Background(), grant)
	if err != nil {
		t.Fatal(err)
	}
	if len(resources) != 1 ||
		resources[0].Candidate.RemoteID != "2244994945" ||
		resources[0].Candidate.ResourceType != ResourceXProfile ||
		resources[0].Candidate.AccountType != AccountTypeProfile ||
		resources[0].Candidate.Handle != "postqron_fixture" ||
		resources[0].Credential.RefreshToken != "fixture-x-refresh-token" {
		t.Fatalf("resources = %#v", resources)
	}
	refreshed, err := adapter.Refresh(
		context.Background(),
		resources[0].Credential,
	)
	if err != nil {
		t.Fatal(err)
	}
	if refreshed.AccessToken != "fixture-x-refreshed-access-token" ||
		refreshed.RefreshToken != "fixture-x-rotated-refresh-token" ||
		refreshed.ExpiresAt == nil ||
		!refreshed.ExpiresAt.Equal(xFixtureNow.Add(2*time.Hour)) {
		t.Fatalf("refreshed credential = %#v", refreshed)
	}
	if err = adapter.Verify(
		context.Background(),
		"2244994945",
		refreshed,
	); err != nil {
		t.Fatal(err)
	}
	if err = adapter.Revoke(
		context.Background(),
		"2244994945",
		refreshed,
	); err != nil {
		t.Fatal(err)
	}
	revokeMu.Lock()
	gotRevokedTokens := append([]string(nil), revokedTokens...)
	revokeMu.Unlock()
	if !slices.Equal(gotRevokedTokens, []string{
		"fixture-x-refreshed-access-token",
		"fixture-x-rotated-refresh-token",
	}) {
		t.Fatalf("revoked tokens = %v", gotRevokedTokens)
	}
	if tokenCalls.Load() != 2 || userCalls.Load() != 2 {
		t.Fatalf(
			"provider calls: token=%d user=%d",
			tokenCalls.Load(),
			userCalls.Load(),
		)
	}
}

func TestXGrantScopesAreValidatedAsAnIndividualSet(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(
		response http.ResponseWriter,
		request *http.Request,
	) {
		if request.URL.Path != xOAuthTokenPath {
			http.NotFound(response, request)
			return
		}
		writeXFixture(t, response, "token_exchange_reordered_scopes.json")
	}))
	defer server.Close()
	adapter := newFixtureXAdapter(t, server)
	credential, err := adapter.Exchange(context.Background(), ExchangeRequest{
		Code:         "fixture-code",
		RedirectURL:  adapter.redirectURL,
		PKCEVerifier: "fixture-verifier",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(credential.Scopes, xRequiredScopes) {
		t.Fatalf("canonical stored scopes = %v", credential.Scopes)
	}
}

func TestXRevokeIsIdempotentWithoutHidingRetryableFailures(t *testing.T) {
	tests := []struct {
		name        string
		firstCode   int
		firstFile   string
		secondCode  int
		secondFile  string
		wantFailure bool
	}{
		{
			name:       "second token already invalid",
			secondCode: http.StatusBadRequest,
			secondFile: "revoke_invalid_token.json",
		},
		{
			name:        "second token server failure",
			secondCode:  http.StatusServiceUnavailable,
			secondFile:  "error_500.json",
			wantFailure: true,
		},
		{
			name:        "first token server failure is preserved",
			firstCode:   http.StatusServiceUnavailable,
			firstFile:   "error_500.json",
			secondCode:  http.StatusOK,
			secondFile:  "revoke.json",
			wantFailure: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var calls atomic.Int32
			var mu sync.Mutex
			var tokens []string
			server := httptest.NewServer(http.HandlerFunc(func(
				response http.ResponseWriter,
				request *http.Request,
			) {
				if request.URL.Path != xOAuthRevokePath {
					http.NotFound(response, request)
					return
				}
				if err := request.ParseForm(); err != nil {
					t.Error(err)
				}
				mu.Lock()
				tokens = append(tokens, request.Form.Get("token"))
				mu.Unlock()
				if calls.Add(1) == 1 {
					status := test.firstCode
					fixture := test.firstFile
					if status == 0 {
						status = http.StatusOK
						fixture = "revoke.json"
					}
					response.WriteHeader(status)
					writeXFixture(t, response, fixture)
					return
				}
				response.WriteHeader(test.secondCode)
				writeXFixture(t, response, test.secondFile)
			}))
			defer server.Close()
			adapter := newFixtureXAdapter(t, server)
			err := adapter.Revoke(
				context.Background(),
				"2244994945",
				Credential{
					AccessToken:  "fixture-access",
					RefreshToken: "fixture-refresh",
				},
			)
			if test.wantFailure {
				var failure *ProviderFailure
				if !errors.As(err, &failure) ||
					failure.Kind != FailureTemporary ||
					!failure.Retryable {
					t.Fatalf("retryable revoke failure = %#v, %v", failure, err)
				}
			} else if err != nil {
				t.Fatalf("idempotent revoke error = %v", err)
			}
			mu.Lock()
			gotTokens := append([]string(nil), tokens...)
			mu.Unlock()
			if calls.Load() != 2 ||
				!slices.Equal(gotTokens, []string{
					"fixture-access",
					"fixture-refresh",
				}) {
				t.Fatalf("revoke calls = %d, tokens = %v", calls.Load(), gotTokens)
			}
		})
	}
}

func TestXProviderErrorsAreClientSafeAndClassified(t *testing.T) {
	tests := []struct {
		name      string
		status    int
		fixture   string
		kind      ProviderFailureKind
		retryable bool
	}{
		{"unauthorized", http.StatusUnauthorized, "error_401.json", FailureAuthentication, false},
		{"forbidden", http.StatusForbidden, "error_403.json", FailurePermissionMissing, false},
		{"rate limit", http.StatusTooManyRequests, "error_429.json", FailureTemporary, true},
		{"server error", http.StatusInternalServerError, "error_500.json", FailureTemporary, true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(
				response http.ResponseWriter,
				_ *http.Request,
			) {
				response.WriteHeader(test.status)
				writeXFixture(t, response, test.fixture)
			}))
			defer server.Close()
			adapter := newFixtureXAdapter(t, server)
			err := adapter.Verify(
				context.Background(),
				"2244994945",
				Credential{AccessToken: "fixture-invalid-token"},
			)
			var failure *ProviderFailure
			if !errors.As(err, &failure) ||
				failure.Kind != test.kind ||
				failure.Retryable != test.retryable ||
				!stringsHasPrefix(failure.Code, "x_") ||
				bytes.Contains([]byte(failure.Error()), []byte("Fixture")) {
				t.Fatalf("failure = %#v, error = %v", failure, err)
			}
		})
	}
}

func TestXAdapterRejectsMalformedOrOverScopedResponses(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		payload    []byte
		operation  func(*XAdapter) error
	}{
		{
			name:       "malformed token JSON",
			statusCode: http.StatusOK,
			payload:    readXFixture(t, "malformed.json"),
			operation: func(adapter *XAdapter) error {
				_, err := adapter.Exchange(context.Background(), ExchangeRequest{
					Code:         "fixture-code",
					RedirectURL:  adapter.redirectURL,
					PKCEVerifier: "fixture-verifier",
				})
				return err
			},
		},
		{
			name:       "incomplete token",
			statusCode: http.StatusOK,
			payload:    []byte(`{"token_type":"bearer","expires_in":7200}`),
			operation: func(adapter *XAdapter) error {
				_, err := adapter.Exchange(context.Background(), ExchangeRequest{
					Code:         "fixture-code",
					RedirectURL:  adapter.redirectURL,
					PKCEVerifier: "fixture-verifier",
				})
				return err
			},
		},
		{
			name:       "exchange additional scope",
			statusCode: http.StatusOK,
			payload: []byte(`{
				"token_type":"bearer",
				"expires_in":7200,
				"access_token":"fixture-token",
				"refresh_token":"fixture-refresh",
				"scope":"tweet.read tweet.write users.read offline.access dm.read"
			}`),
			operation: func(adapter *XAdapter) error {
				_, err := adapter.Exchange(context.Background(), ExchangeRequest{
					Code:         "fixture-code",
					RedirectURL:  adapter.redirectURL,
					PKCEVerifier: "fixture-verifier",
				})
				return err
			},
		},
		{
			name:       "exchange missing individual scope",
			statusCode: http.StatusOK,
			payload: []byte(`{
				"token_type":"bearer",
				"expires_in":7200,
				"access_token":"fixture-token",
				"refresh_token":"fixture-refresh",
				"scope":"tweet.read tweet.write users.read"
			}`),
			operation: func(adapter *XAdapter) error {
				_, err := adapter.Exchange(context.Background(), ExchangeRequest{
					Code:         "fixture-code",
					RedirectURL:  adapter.redirectURL,
					PKCEVerifier: "fixture-verifier",
				})
				return err
			},
		},
		{
			name:       "exchange duplicate individual scope",
			statusCode: http.StatusOK,
			payload: []byte(`{
				"token_type":"bearer",
				"expires_in":7200,
				"access_token":"fixture-token",
				"refresh_token":"fixture-refresh",
				"scope":"tweet.read tweet.write users.read users.read"
			}`),
			operation: func(adapter *XAdapter) error {
				_, err := adapter.Exchange(context.Background(), ExchangeRequest{
					Code:         "fixture-code",
					RedirectURL:  adapter.redirectURL,
					PKCEVerifier: "fixture-verifier",
				})
				return err
			},
		},
		{
			name:       "malformed user JSON",
			statusCode: http.StatusOK,
			payload:    readXFixture(t, "malformed.json"),
			operation: func(adapter *XAdapter) error {
				_, err := adapter.Discover(context.Background(), Credential{
					AccessToken:  "fixture-token",
					RefreshToken: "fixture-refresh",
					Scopes:       xCredentialScopes(),
				})
				return err
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(
				response http.ResponseWriter,
				_ *http.Request,
			) {
				response.WriteHeader(test.statusCode)
				_, _ = response.Write(test.payload)
			}))
			defer server.Close()
			err := test.operation(newFixtureXAdapter(t, server))
			var failure *ProviderFailure
			if !errors.As(err, &failure) ||
				failure.Kind != FailureInvalidResponse {
				t.Fatalf("failure = %#v, error = %v", failure, err)
			}
		})
	}
}

func TestXRefreshScopeSetMismatchRequiresReconnect(t *testing.T) {
	tests := []struct {
		name    string
		payload []byte
	}{
		{
			name: "additional scope",
			payload: []byte(`{
				"token_type":"bearer",
				"expires_in":7200,
				"access_token":"fixture-token",
				"refresh_token":"fixture-refresh-2",
				"scope":"tweet.read tweet.write users.read offline.access dm.read"
			}`),
		},
		{
			name: "missing scope",
			payload: []byte(`{
				"token_type":"bearer",
				"expires_in":7200,
				"access_token":"fixture-token",
				"refresh_token":"fixture-refresh-2",
				"scope":"tweet.read tweet.write users.read"
			}`),
		},
		{
			name: "duplicate scope",
			payload: []byte(`{
				"token_type":"bearer",
				"expires_in":7200,
				"access_token":"fixture-token",
				"refresh_token":"fixture-refresh-2",
				"scope":"tweet.read tweet.write users.read users.read"
			}`),
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(
				response http.ResponseWriter,
				_ *http.Request,
			) {
				response.WriteHeader(http.StatusOK)
				_, _ = response.Write(test.payload)
			}))
			defer server.Close()
			adapter := newFixtureXAdapter(t, server)
			_, err := adapter.Refresh(context.Background(), Credential{
				RefreshToken: "fixture-refresh-1",
			})
			var failure *ProviderFailure
			if !errors.As(err, &failure) ||
				failure.Kind != FailurePermissionMissing ||
				failure.Code != "x_required_scope_missing" {
				t.Fatalf("failure = %#v, error = %v", failure, err)
			}
		})
	}
}

func TestXOAuthStateIsOneTimeAndCredentialsRemainEncrypted(t *testing.T) {
	var rejectProfile atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(
		response http.ResponseWriter,
		request *http.Request,
	) {
		switch request.URL.Path {
		case xOAuthTokenPath:
			writeXFixture(t, response, "token_exchange.json")
		case xAuthenticatedUserPath:
			if rejectProfile.Load() {
				response.WriteHeader(http.StatusUnauthorized)
				writeXFixture(t, response, "error_401.json")
				return
			}
			writeXFixture(t, response, "user.json")
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()
	adapter := newFixtureXAdapter(t, server)
	repository := NewMemoryRepository()
	cipher, err := NewAESGCMCipher(
		"fixture-key",
		[]byte("0123456789abcdef0123456789abcdef"),
	)
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
			ProviderX: adapter,
		},
		Availability: map[Provider]ProviderAvailability{
			ProviderX: {
				Provider:           ProviderX,
				Status:             ProviderAvailable,
				ConfigurationState: ProviderReady,
			},
		},
		Now: func() time.Time { return xFixtureNow },
	})
	if err != nil {
		t.Fatal(err)
	}
	authorization, err := service.Begin(context.Background(), BeginRequest{
		WorkspaceID: "workspace-x",
		ActorID:     "owner-x",
		Provider:    ProviderX,
	})
	if err != nil {
		t.Fatal(err)
	}
	authorizationURL, err := url.Parse(authorization.URL)
	if err != nil {
		t.Fatal(err)
	}
	state := authorizationURL.Query().Get("state")
	if state == "" ||
		authorizationURL.Query().Get("code_challenge_method") != "S256" {
		t.Fatalf("authorization URL = %s", authorization.URL)
	}
	selection, err := service.Callback(context.Background(), CallbackRequest{
		State: state,
		Code:  "fixture-code",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.Callback(context.Background(), CallbackRequest{
		State: state,
		Code:  "fixture-code",
	}); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("replayed callback error = %v", err)
	}
	connection, err := service.Select(context.Background(), SelectRequest{
		WorkspaceID: "workspace-x",
		ActorID:     "owner-x",
		SelectionID: selection.ID,
		RemoteID:    "2244994945",
	})
	if err != nil {
		t.Fatal(err)
	}
	stored, err := repository.GetCredential(
		context.Background(),
		"workspace-x",
		connection.ID,
	)
	if err != nil {
		t.Fatal(err)
	}
	for _, plaintext := range [][]byte{
		[]byte("fixture-x-access-token"),
		[]byte("fixture-x-refresh-token"),
	} {
		if bytes.Contains(stored.AccessTokenCiphertext.Data, plaintext) ||
			bytes.Contains(stored.RefreshTokenCiphertext.Data, plaintext) {
			t.Fatal("stored credential contains plaintext fixture token")
		}
	}
	rejectProfile.Store(true)
	if err = service.Verify(
		context.Background(),
		"workspace-x",
		connection.ID,
	); !errors.Is(err, ErrReconnectRequired) {
		t.Fatalf("verify error = %v", err)
	}
	stored, err = repository.GetCredential(
		context.Background(),
		"workspace-x",
		connection.ID,
	)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != StatusReconnectRequired ||
		len(stored.AccessTokenCiphertext.Data) != 0 ||
		len(stored.RefreshTokenCiphertext.Data) != 0 {
		t.Fatalf("reconnect credential = %#v", stored)
	}
	if _, err = service.BeginReconnect(context.Background(), ReconnectRequest{
		WorkspaceID:  "workspace-x",
		ActorID:      "owner-x",
		ConnectionID: connection.ID,
	}); err != nil {
		t.Fatal(err)
	}
}

func TestXDisconnectDeletesLocalCiphertextWhenRefreshIsAlreadyInvalid(
	t *testing.T,
) {
	var revokeCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(
		response http.ResponseWriter,
		request *http.Request,
	) {
		switch request.URL.Path {
		case xOAuthTokenPath:
			writeXFixture(t, response, "token_exchange.json")
		case xAuthenticatedUserPath:
			writeXFixture(t, response, "user.json")
		case xOAuthRevokePath:
			if revokeCalls.Add(1) == 1 {
				writeXFixture(t, response, "revoke.json")
				return
			}
			response.WriteHeader(http.StatusBadRequest)
			writeXFixture(t, response, "revoke_invalid_token.json")
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()
	adapter := newFixtureXAdapter(t, server)
	repository := NewMemoryRepository()
	cipher, err := NewAESGCMCipher(
		"fixture-key",
		[]byte("0123456789abcdef0123456789abcdef"),
	)
	if err != nil {
		t.Fatal(err)
	}
	quota := newFakeChannelQuota()
	service, err := NewService(Config{
		Repository: repository,
		Authorizer: &fakeAuthorizer{permissions: map[Permission]bool{
			PermissionViewWorkspace:  true,
			PermissionManageChannels: true,
		}},
		Cipher: cipher,
		Quota:  quota,
		Adapters: map[Provider]Adapter{
			ProviderX: adapter,
		},
		Availability: map[Provider]ProviderAvailability{
			ProviderX: {
				Provider:           ProviderX,
				Status:             ProviderAvailable,
				ConfigurationState: ProviderReady,
			},
		},
		Now: func() time.Time { return xFixtureNow },
	})
	if err != nil {
		t.Fatal(err)
	}
	authorization, err := service.Begin(context.Background(), BeginRequest{
		WorkspaceID: "workspace-revoke",
		ActorID:     "owner-revoke",
		Provider:    ProviderX,
	})
	if err != nil {
		t.Fatal(err)
	}
	authorizationURL, err := url.Parse(authorization.URL)
	if err != nil {
		t.Fatal(err)
	}
	selection, err := service.Callback(context.Background(), CallbackRequest{
		State: authorizationURL.Query().Get("state"),
		Code:  "fixture-code",
	})
	if err != nil {
		t.Fatal(err)
	}
	connection, err := service.Select(context.Background(), SelectRequest{
		WorkspaceID: "workspace-revoke",
		ActorID:     "owner-revoke",
		SelectionID: selection.ID,
		RemoteID:    "2244994945",
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.Revoke(
		context.Background(),
		"workspace-revoke",
		"owner-revoke",
		connection.ID,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !result.ProviderRevoked ||
		result.Connection.Status != StatusRevoked ||
		revokeCalls.Load() != 2 {
		t.Fatalf("revocation result = %#v, calls = %d", result, revokeCalls.Load())
	}
	stored, err := repository.GetCredential(
		context.Background(),
		"workspace-revoke",
		connection.ID,
	)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != StatusRevoked ||
		len(stored.AccessTokenCiphertext.Data) != 0 ||
		len(stored.RefreshTokenCiphertext.Data) != 0 {
		t.Fatalf("revoked stored credential = %#v", stored)
	}
	if _, err = service.Revoke(
		context.Background(),
		"workspace-revoke",
		"owner-revoke",
		connection.ID,
	); err != nil {
		t.Fatal(err)
	}
	if revokeCalls.Load() != 2 {
		t.Fatalf("idempotent disconnect made %d remote calls", revokeCalls.Load())
	}
	reserved, released := quota.counts()
	if reserved != 1 || released != 2 {
		t.Fatalf("quota calls: reserved=%d released=%d", reserved, released)
	}
}

func TestXAdapterRequiresConfidentialHTTPSConfiguration(t *testing.T) {
	tests := []XAdapterConfig{
		{
			ClientID:    "fixture-client",
			RedirectURL: "https://app.example.test/callback",
		},
		{
			ClientID:     "fixture-client",
			ClientSecret: "fixture-secret",
			RedirectURL:  "http://app.example.test/callback",
		},
		{
			ClientID:         "fixture-client",
			ClientSecret:     "fixture-secret",
			RedirectURL:      "https://app.example.test/callback",
			AuthorizationURL: "http://attacker.example.test/oauth",
		},
		{
			ClientID:     "fixture-client",
			ClientSecret: "fixture-secret",
			RedirectURL:  "https://app.example.test/callback",
			APIBaseURL:   "https://api.x.com/unsafe",
		},
	}
	for index, config := range tests {
		if _, err := NewXAdapter(config); !errors.Is(err, ErrInvalidArgument) {
			t.Fatalf("config %d error = %v", index, err)
		}
	}
}

func newFixtureXAdapter(t *testing.T, server *httptest.Server) *XAdapter {
	t.Helper()
	adapter, err := NewXAdapter(XAdapterConfig{
		ClientID:         "fixture-x-client",
		ClientSecret:     "fixture-x-client-secret",
		RedirectURL:      "https://app.example.test/api/v1/social-authorizations/callback",
		HTTPClient:       server.Client(),
		Now:              func() time.Time { return xFixtureNow },
		AuthorizationURL: xOfficialAuthorizationURL,
		APIBaseURL:       server.URL,
	})
	if err != nil {
		t.Fatal(err)
	}
	return adapter
}

func readXFixture(t *testing.T, name string) []byte {
	t.Helper()
	payload, err := os.ReadFile("testdata/x/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return payload
}

func writeXFixture(
	t *testing.T,
	response http.ResponseWriter,
	name string,
) {
	t.Helper()
	response.Header().Set("Content-Type", "application/json")
	payload, err := os.ReadFile("testdata/x/" + name)
	if err != nil {
		t.Error(err)
		http.Error(response, "fixture unavailable", http.StatusInternalServerError)
		return
	}
	if _, err := io.Copy(
		response,
		bytes.NewReader(payload),
	); err != nil {
		t.Error(err)
	}
}

func stringsHasPrefix(value, prefix string) bool {
	return len(value) >= len(prefix) && value[:len(prefix)] == prefix
}
