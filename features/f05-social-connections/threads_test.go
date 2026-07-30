package socialconnections

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

var threadsTestNow = time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)

func TestThreadsAdapterOfficialLifecycle(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(
		response http.ResponseWriter,
		request *http.Request,
	) {
		switch request.URL.Path {
		case "/oauth/access_token":
			query := request.URL.Query()
			switch query.Get("grant_type") {
			case "authorization_code":
				if request.Method != http.MethodPost {
					t.Errorf("short exchange method = %s", request.Method)
				}
				if query.Get("client_id") != "threads-client" ||
					query.Get("client_secret") != "fixture-threads-secret" ||
					query.Get("code") != "oauth-code" ||
					query.Get("redirect_uri") !=
						"https://app.example.test/social/callback" {
					t.Errorf("short exchange query = %v", query)
				}
				if query.Get("code_verifier") != "" {
					t.Error("undocumented PKCE verifier was sent")
				}
				writeThreadsJSON(t, response, map[string]any{
					"access_token": "short-threads-token",
					"user_id":      "123456789",
				})
			case "client_credentials":
				if request.Method != http.MethodGet ||
					query.Get("client_id") != "threads-client" ||
					query.Get("client_secret") != "fixture-threads-secret" {
					t.Errorf("app token request = %s %v", request.Method, query)
				}
				writeThreadsJSON(t, response, map[string]any{
					"access_token": "threads-app-token",
					"token_type":   "bearer",
				})
			default:
				t.Errorf("unexpected token grant = %q", query.Get("grant_type"))
				http.Error(response, "unexpected grant", http.StatusBadRequest)
			}
		case "/access_token":
			if request.Method != http.MethodGet ||
				request.Header.Get("Authorization") !=
					"Bearer short-threads-token" {
				t.Errorf(
					"long exchange = %s %q",
					request.Method,
					request.Header.Get("Authorization"),
				)
			}
			if request.URL.Query().Get("grant_type") != "th_exchange_token" ||
				request.URL.Query().Get("client_secret") !=
					"fixture-threads-secret" {
				t.Errorf("long exchange query = %v", request.URL.Query())
			}
			writeThreadsJSON(t, response, map[string]any{
				"access_token": "long-threads-token",
				"token_type":   "bearer",
				"expires_in":   5_184_000,
			})
		case "/debug_token":
			if request.Header.Get("Authorization") !=
				"Bearer threads-app-token" {
				t.Errorf(
					"debug bearer = %q",
					request.Header.Get("Authorization"),
				)
			}
			inputToken := request.URL.Query().Get("input_token")
			if inputToken != "long-threads-token" &&
				inputToken != "refreshed-threads-token" {
				t.Errorf("debug input token = %q", inputToken)
			}
			writeThreadsJSON(t, response, map[string]any{
				"data": map[string]any{
					"app_id":   "threads-client",
					"is_valid": true,
					"scopes":   threadsRequiredScopes,
					"user_id":  "123456789",
				},
			})
		case "/me":
			if request.Header.Get("Authorization") !=
				"Bearer long-threads-token" {
				t.Errorf(
					"discovery bearer = %q",
					request.Header.Get("Authorization"),
				)
			}
			if request.URL.Query().Get("fields") !=
				"id,username,name,threads_profile_picture_url" {
				t.Errorf("discovery fields = %q", request.URL.Query().Get("fields"))
			}
			writeThreadsJSON(t, response, map[string]any{
				"id":                          "123456789",
				"username":                    "postqron",
				"name":                        "Postqron",
				"threads_profile_picture_url": "https://cdn.example.test/profile.jpg",
			})
		case "/refresh_access_token":
			if request.URL.Query().Get("grant_type") != "th_refresh_token" ||
				request.Header.Get("Authorization") !=
					"Bearer long-threads-token" {
				t.Errorf(
					"refresh request = %v %q",
					request.URL.Query(),
					request.Header.Get("Authorization"),
				)
			}
			writeThreadsJSON(t, response, map[string]any{
				"access_token": "refreshed-threads-token",
				"token_type":   "bearer",
				"expires_in":   5_184_000,
			})
		case "/123456789":
			if request.URL.Query().Get("fields") != "id,username" ||
				request.Header.Get("Authorization") !=
					"Bearer refreshed-threads-token" {
				t.Errorf(
					"verify request = %v %q",
					request.URL.Query(),
					request.Header.Get("Authorization"),
				)
			}
			writeThreadsJSON(t, response, map[string]any{
				"id":       123456789,
				"username": "postqron",
			})
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()

	adapter := newThreadsFixtureAdapter(t, server)
	config := adapter.Config()
	if config.AuthorizationURL != "https://www.threads.com/oauth/authorize" ||
		config.SupportsPKCE ||
		!slices.Equal(config.Scopes, threadsRequiredScopes) {
		t.Fatalf("Threads OAuth config = %#v", config)
	}
	if capabilities := adapter.AdapterCapabilities(); capabilities !=
		(AdapterCapabilities{
			Authorization:     true,
			ResourceSelection: true,
			TokenRefresh:      true,
		}) {
		t.Fatalf("Threads capabilities = %#v", capabilities)
	}

	grant, err := adapter.Exchange(context.Background(), ExchangeRequest{
		Code:        "oauth-code",
		RedirectURL: "https://app.example.test/social/callback",
	})
	if err != nil {
		t.Fatal(err)
	}
	if grant.AccessToken != "long-threads-token" ||
		grant.ExpiresAt == nil ||
		!grant.ExpiresAt.Equal(threadsTestNow.Add(60*24*time.Hour)) {
		t.Fatalf("Threads grant = %#v", grant)
	}

	resources, err := adapter.Discover(context.Background(), grant)
	if err != nil {
		t.Fatal(err)
	}
	if len(resources) != 1 ||
		resources[0].Candidate.RemoteID != "123456789" ||
		resources[0].Candidate.ResourceType != ResourceThreadsProfile ||
		resources[0].Candidate.AccountType != AccountTypeProfile ||
		resources[0].Candidate.Handle != "postqron" ||
		resources[0].Credential.AccessToken != "long-threads-token" {
		t.Fatalf("Threads resources = %#v", resources)
	}

	refreshed, err := adapter.Refresh(
		context.Background(),
		resources[0].Credential,
	)
	if err != nil {
		t.Fatal(err)
	}
	if refreshed.AccessToken != "refreshed-threads-token" ||
		refreshed.ExpiresAt == nil ||
		!slices.Equal(refreshed.Scopes, threadsRequiredScopes) {
		t.Fatalf("Threads refreshed credential = %#v", refreshed)
	}
	if err = adapter.Verify(
		context.Background(),
		"123456789",
		refreshed,
	); err != nil {
		t.Fatal(err)
	}
	if err = adapter.Revoke(
		context.Background(),
		"123456789",
		refreshed,
	); !errors.Is(err, ErrExternalRevocationUnavailable) {
		t.Fatalf("Threads revoke error = %v", err)
	}
}

func TestThreadsLifecycleUsesOneTimeStateEncryptedSelectionAndLocalRevoke(
	t *testing.T,
) {
	var exchangeCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(
		response http.ResponseWriter,
		request *http.Request,
	) {
		switch request.URL.Path {
		case "/oauth/access_token":
			switch request.URL.Query().Get("grant_type") {
			case "authorization_code":
				exchangeCalls.Add(1)
				writeThreadsJSON(t, response, map[string]any{
					"access_token": "short-selection-token",
					"user_id":      "123456789",
				})
			case "client_credentials":
				writeThreadsJSON(t, response, map[string]any{
					"access_token": "selection-app-token",
				})
			default:
				http.Error(response, "unexpected grant", http.StatusBadRequest)
			}
		case "/access_token":
			writeThreadsJSON(t, response, map[string]any{
				"access_token": "long-selection-token",
				"expires_in":   5_184_000,
			})
		case "/debug_token":
			writeThreadsJSON(t, response, map[string]any{
				"data": map[string]any{
					"app_id":   "threads-client",
					"is_valid": true,
					"scopes":   threadsRequiredScopes,
					"user_id":  "123456789",
				},
			})
		case "/me":
			writeThreadsJSON(t, response, map[string]any{
				"id":       "123456789",
				"username": "postqron",
				"name":     "Postqron",
			})
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()

	adapter := newThreadsFixtureAdapter(t, server)
	repository := NewMemoryRepository()
	cipher, err := NewAESGCMCipher(
		"threads-fixture-key",
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
			ProviderThreads: adapter,
		},
		Availability: map[Provider]ProviderAvailability{
			ProviderThreads: {
				Provider:           ProviderThreads,
				Status:             ProviderAvailable,
				ConfigurationState: ProviderReady,
			},
		},
		Now: func() time.Time { return threadsTestNow },
	})
	if err != nil {
		t.Fatal(err)
	}

	deniedAuthorization, deniedState := beginThreadsAuthorization(t, service)
	if _, err = service.Callback(context.Background(), CallbackRequest{
		State:         deniedState,
		ProviderError: "access_denied",
	}); !errors.Is(err, ErrProviderDenied) {
		t.Fatalf("denied callback error = %v", err)
	}
	if exchangeCalls.Load() != 0 {
		t.Fatal("provider was called after authorization denial")
	}
	if _, err = service.Callback(context.Background(), CallbackRequest{
		State: deniedState,
		Code:  "replayed-code",
	}); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("denied state replay error = %v", err)
	}
	_ = deniedAuthorization

	authorization, state := beginThreadsAuthorization(t, service)
	parsed, err := url.Parse(authorization.URL)
	if err != nil {
		t.Fatal(err)
	}
	query := parsed.Query()
	if query.Get("state") != state ||
		query.Get("scope") != "threads_basic,threads_content_publish" ||
		query.Get("code_challenge") != "" ||
		query.Get("code_challenge_method") != "" ||
		query.Get("client_secret") != "" {
		t.Fatalf("Threads authorization query = %v", query)
	}

	selection, err := service.Callback(context.Background(), CallbackRequest{
		State: state,
		Code:  "selection-code",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.Callback(context.Background(), CallbackRequest{
		State: state,
		Code:  "replayed-code",
	}); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("successful state replay error = %v", err)
	}
	if len(selection.Resources) != 1 ||
		selection.Resources[0].RemoteID != "123456789" {
		t.Fatalf("Threads selection = %#v", selection)
	}
	serialized, err := json.Marshal(selection)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(serialized), "selection-token") {
		t.Fatalf("selection exposed a credential: %s", serialized)
	}

	connection, err := service.Select(context.Background(), SelectRequest{
		WorkspaceID: "workspace-threads",
		ActorID:     "owner-threads",
		SelectionID: selection.ID,
		RemoteID:    "123456789",
	})
	if err != nil {
		t.Fatal(err)
	}
	stored, err := repository.GetCredential(
		context.Background(),
		"workspace-threads",
		connection.ID,
	)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(
		string(stored.AccessTokenCiphertext.Data),
		"long-selection-token",
	) || len(stored.AccessTokenCiphertext.Data) == 0 {
		t.Fatal("Threads access token was not encrypted")
	}

	revoked, err := service.Revoke(
		context.Background(),
		"workspace-threads",
		"owner-threads",
		connection.ID,
	)
	if err != nil {
		t.Fatal(err)
	}
	if revoked.ProviderRevoked ||
		revoked.Connection.Status != StatusRevoked {
		t.Fatalf("Threads revocation = %#v", revoked)
	}
}

func TestThreadsFailuresClassifyReconnectRateLimitsAndMalformedResponses(
	t *testing.T,
) {
	tests := []struct {
		name      string
		status    int
		body      string
		kind      ProviderFailureKind
		retryable bool
	}{
		{
			name:   "authentication requires reconnect",
			status: http.StatusBadRequest,
			body:   `{"error":{"code":190,"error_subcode":463}}`,
			kind:   FailureAuthentication,
		},
		{
			name:   "permission denial",
			status: http.StatusForbidden,
			body:   `{"error":{"code":10}}`,
			kind:   FailurePermissionMissing,
		},
		{
			name:      "rate limit",
			status:    http.StatusTooManyRequests,
			body:      `{"error":{"code":4}}`,
			kind:      FailureTemporary,
			retryable: true,
		},
		{
			name:      "provider outage",
			status:    http.StatusServiceUnavailable,
			body:      `{"error":{"code":2}}`,
			kind:      FailureTemporary,
			retryable: true,
		},
		{
			name:   "malformed success",
			status: http.StatusOK,
			body:   `{`,
			kind:   FailureInvalidResponse,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(
				response http.ResponseWriter,
				_ *http.Request,
			) {
				response.WriteHeader(test.status)
				_, _ = response.Write([]byte(test.body))
			}))
			defer server.Close()
			adapter := newThreadsFixtureAdapter(t, server)
			err := adapter.Verify(
				context.Background(),
				"threads-user-1",
				Credential{AccessToken: "fixture-token"},
			)
			var failure *ProviderFailure
			if !errors.As(err, &failure) ||
				failure.Kind != test.kind ||
				failure.Retryable != test.retryable {
				t.Fatalf("Threads failure = %#v, %v", failure, err)
			}
		})
	}
}

func TestThreadsRejectsIncompleteTokenExchangeAndUndocumentedPKCE(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(
		response http.ResponseWriter,
		request *http.Request,
	) {
		switch request.URL.Path {
		case "/oauth/access_token":
			switch request.URL.Query().Get("grant_type") {
			case "authorization_code":
				writeThreadsJSON(t, response, map[string]any{
					"access_token": "short-token",
					"user_id":      "123",
				})
			default:
				http.Error(response, "unexpected grant", http.StatusBadRequest)
			}
		case "/access_token":
			writeThreadsJSON(t, response, map[string]any{
				"access_token": "long-token-without-expiry",
			})
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()
	adapter := newThreadsFixtureAdapter(t, server)

	_, err := adapter.Exchange(context.Background(), ExchangeRequest{
		Code:         "oauth-code",
		RedirectURL:  "https://app.example.test/social/callback",
		PKCEVerifier: "not-documented",
	})
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("PKCE error = %v", err)
	}
	_, err = adapter.Exchange(context.Background(), ExchangeRequest{
		Code:        "oauth-code",
		RedirectURL: "https://app.example.test/social/callback",
	})
	var failure *ProviderFailure
	if !errors.As(err, &failure) ||
		failure.Kind != FailureInvalidResponse {
		t.Fatalf("incomplete token failure = %#v, %v", failure, err)
	}
}

func TestThreadsTokenDebuggerRejectsMissingPublishingPermission(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(
		response http.ResponseWriter,
		request *http.Request,
	) {
		switch request.URL.Path {
		case "/oauth/access_token":
			switch request.URL.Query().Get("grant_type") {
			case "authorization_code":
				writeThreadsJSON(t, response, map[string]any{
					"access_token": "short-token",
					"user_id":      "123",
				})
			case "client_credentials":
				writeThreadsJSON(t, response, map[string]any{
					"access_token": "app-token",
				})
			}
		case "/access_token":
			writeThreadsJSON(t, response, map[string]any{
				"access_token": "long-token",
				"expires_in":   5_184_000,
			})
		case "/debug_token":
			writeThreadsJSON(t, response, map[string]any{
				"data": map[string]any{
					"app_id":   "threads-client",
					"is_valid": true,
					"scopes":   []string{"threads_basic"},
					"user_id":  "123",
				},
			})
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()
	adapter := newThreadsFixtureAdapter(t, server)

	_, err := adapter.Exchange(context.Background(), ExchangeRequest{
		Code:        "oauth-code",
		RedirectURL: "https://app.example.test/social/callback",
	})
	var failure *ProviderFailure
	if !errors.As(err, &failure) ||
		failure.Kind != FailurePermissionMissing ||
		failure.Code != "threads_required_scope_missing" {
		t.Fatalf("missing permission failure = %#v, %v", failure, err)
	}
}

func TestThreadsTransportErrorsNeverExposeSecretsOrTokens(t *testing.T) {
	client := &http.Client{Transport: threadsRoundTripperFunc(func(
		request *http.Request,
	) (*http.Response, error) {
		return nil, errors.New("failed request: " + request.URL.String())
	})}
	adapter, err := NewThreadsAdapter(ThreadsAdapterConfig{
		ClientID:     "threads-client",
		ClientSecret: "fixture-threads-secret",
		RedirectURL:  "https://app.example.test/social/callback",
		HTTPClient:   client,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = adapter.Exchange(context.Background(), ExchangeRequest{
		Code:        "fixture-authorization-code",
		RedirectURL: "https://app.example.test/social/callback",
	})
	for _, sensitive := range []string{
		"fixture-threads-secret",
		"fixture-authorization-code",
	} {
		if strings.Contains(err.Error(), sensitive) {
			t.Fatalf("transport error exposed %q: %v", sensitive, err)
		}
	}
}

func TestThreadsTokenExchangeNeverForwardsSecretBearingRedirects(t *testing.T) {
	var redirectedCalls atomic.Int32
	redirectTarget := httptest.NewServer(http.HandlerFunc(func(
		http.ResponseWriter,
		*http.Request,
	) {
		redirectedCalls.Add(1)
	}))
	defer redirectTarget.Close()
	tokenServer := httptest.NewServer(http.HandlerFunc(func(
		response http.ResponseWriter,
		_ *http.Request,
	) {
		response.Header().Set("Location", redirectTarget.URL)
		response.WriteHeader(http.StatusFound)
	}))
	defer tokenServer.Close()
	adapter := newThreadsFixtureAdapter(t, tokenServer)

	_, err := adapter.Exchange(context.Background(), ExchangeRequest{
		Code:        "fixture-authorization-code",
		RedirectURL: "https://app.example.test/social/callback",
	})
	var failure *ProviderFailure
	if !errors.As(err, &failure) ||
		failure.Kind != FailureInvalidResponse {
		t.Fatalf("redirect failure = %#v, %v", failure, err)
	}
	if redirectedCalls.Load() != 0 {
		t.Fatal("secret-bearing Threads redirect was followed")
	}
	for _, sensitive := range []string{
		"fixture-threads-secret",
		"fixture-authorization-code",
		tokenServer.URL,
		redirectTarget.URL,
	} {
		if strings.Contains(err.Error(), sensitive) {
			t.Fatalf("redirect error exposed %q: %v", sensitive, err)
		}
	}
}

func newThreadsFixtureAdapter(
	t *testing.T,
	server *httptest.Server,
) *ThreadsAdapter {
	t.Helper()
	adapter, err := NewThreadsAdapter(ThreadsAdapterConfig{
		ClientID:     "threads-client",
		ClientSecret: "fixture-threads-secret",
		RedirectURL:  "https://app.example.test/social/callback",
		HTTPClient:   server.Client(),
		APIBaseURL:   server.URL,
		Now:          func() time.Time { return threadsTestNow },
	})
	if err != nil {
		t.Fatal(err)
	}
	return adapter
}

func beginThreadsAuthorization(
	t *testing.T,
	service *Service,
) (Authorization, string) {
	t.Helper()
	authorization, err := service.Begin(context.Background(), BeginRequest{
		WorkspaceID: "workspace-threads",
		ActorID:     "owner-threads",
		Provider:    ProviderThreads,
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
		t.Fatal("Threads authorization URL has no state")
	}
	return authorization, state
}

type threadsRoundTripperFunc func(*http.Request) (*http.Response, error)

func (function threadsRoundTripperFunc) RoundTrip(
	request *http.Request,
) (*http.Response, error) {
	return function(request)
}

func writeThreadsJSON(
	t *testing.T,
	response http.ResponseWriter,
	value any,
) {
	t.Helper()
	response.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(response).Encode(value); err != nil {
		t.Error(err)
	}
}
