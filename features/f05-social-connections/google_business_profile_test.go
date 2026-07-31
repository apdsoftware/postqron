package socialconnections

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestGoogleBusinessProfileAdapterCompleteOfflineFlow(t *testing.T) {
	var tokenExchanges int
	client := &http.Client{Transport: professionalRoundTripFunc(
		func(request *http.Request) (*http.Response, error) {
			if request.URL.Path == "/token" {
				tokenExchanges++
				body, err := io.ReadAll(request.Body)
				if err != nil {
					t.Fatal(err)
				}
				values, err := url.ParseQuery(string(body))
				if err != nil {
					t.Fatal(err)
				}
				if values.Get("client_secret") != "fixture-secret" {
					t.Fatal("Google token exchange omitted server-side secret")
				}
				refresh := `"refresh_token":"fixture-refresh",`
				if values.Get("grant_type") == "refresh_token" {
					refresh = ""
				}
				return fixtureJSONResponse(http.StatusOK, `{
					"access_token":"fixture-access",
					"expires_in":3600,
					`+refresh+`
					"scope":"`+googleBusinessScope+`",
					"token_type":"Bearer"
				}`), nil
			}
			if request.Header.Get("Authorization") != "Bearer fixture-access" {
				t.Fatalf("Google bearer header = %q", request.Header.Get("Authorization"))
			}
			if request.URL.Path == "/v1/accounts" {
				if request.URL.Query().Get("pageSize") != "20" {
					t.Fatalf("Google account query = %s", request.URL.RawQuery)
				}
				if request.URL.Query().Get("pageToken") == "" {
					return fixtureJSONResponse(http.StatusOK, `{
						"accounts":[{
							"name":"accounts/one",
							"accountName":"Ada Stores",
							"type":"PERSONAL",
							"role":"PRIMARY_OWNER",
							"permissionLevel":"OWNER_LEVEL"
						}],
						"nextPageToken":"accounts-page-2"
					}`), nil
				}
				return fixtureJSONResponse(http.StatusOK, `{
					"accounts":[{
						"name":"accounts/two",
						"accountName":"Analytical Group",
						"type":"LOCATION_GROUP",
						"role":"MANAGER",
						"permissionLevel":"MEMBER_LEVEL"
					}]
				}`), nil
			}
			if !strings.Contains(request.URL.Query().Get("readMask"), "title") ||
				request.URL.Query().Get("pageSize") != "100" {
				t.Fatalf("Google location query = %s", request.URL.RawQuery)
			}
			switch request.URL.Path {
			case "/v1/accounts/one/locations":
				if request.URL.Query().Get("pageToken") == "" {
					return fixtureJSONResponse(http.StatusOK, `{
						"locations":[{
							"name":"locations/101",
							"title":"Ada Books",
							"storefrontAddress":{"locality":"London","regionCode":"GB"}
						}],
						"nextPageToken":"locations-page-2"
					}`), nil
				}
				return fixtureJSONResponse(http.StatusOK, `{
					"locations":[{
						"name":"locations/102",
						"title":"Ada Machines",
						"storefrontAddress":{"locality":"Paris","regionCode":"FR"}
					}]
				}`), nil
			case "/v1/accounts/two/locations":
				return fixtureJSONResponse(http.StatusOK, `{
					"locations":[{
						"name":"locations/202",
						"title":"Difference Engine",
						"storefrontAddress":{"locality":"Turin","regionCode":"IT"}
					}]
				}`), nil
			default:
				t.Fatalf("unexpected Google request %s", request.URL.String())
				return nil, nil
			}
		},
	)}
	adapter, err := NewGoogleBusinessProfileAdapter(
		GoogleBusinessProfileAdapterConfig{
			ClientID:           "fixture-client",
			ClientSecret:       "fixture-secret",
			RedirectURL:        "https://api.example.test/social/callback",
			HTTPClient:         client,
			AuthorizationURL:   "https://google.fixture/auth",
			TokenURL:           "https://google.fixture/token",
			AccountAPIBaseURL:  "https://google.fixture",
			BusinessAPIBaseURL: "https://google.fixture",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	config := adapter.Config()
	if len(config.Scopes) != 1 || config.Scopes[0] != googleBusinessScope ||
		config.SupportsPKCE ||
		config.ExtraParameters["access_type"] != "offline" ||
		config.ExtraParameters["prompt"] != "consent" {
		t.Fatalf("Google OAuth config = %#v", config)
	}
	if strings.Contains(config.AuthorizationURL, "fixture-secret") {
		t.Fatal("Google secret leaked into browser configuration")
	}
	authorizationURL, err := buildAuthorizationURL(config, "fixture-state", "")
	if err != nil {
		t.Fatal(err)
	}
	parsedAuthorizationURL, err := url.Parse(authorizationURL)
	if err != nil {
		t.Fatal(err)
	}
	if parsedAuthorizationURL.Query().Get("scope") != googleBusinessScope ||
		parsedAuthorizationURL.Query().Get("access_type") != "offline" ||
		parsedAuthorizationURL.Query().Get("prompt") != "consent" {
		t.Fatalf("Google authorization URL = %s", authorizationURL)
	}
	capabilities := adapter.AdapterCapabilities()
	if !capabilities.Authorization || !capabilities.ResourceSelection ||
		!capabilities.TokenRefresh || capabilities.PKCE ||
		capabilities.RemoteRevocation {
		t.Fatalf("Google capabilities = %#v", capabilities)
	}
	grant, err := adapter.Exchange(context.Background(), ExchangeRequest{
		Code:        "fixture-code",
		RedirectURL: config.RedirectURL,
	})
	if err != nil {
		t.Fatal(err)
	}
	resources, err := adapter.Discover(context.Background(), grant)
	if err != nil {
		t.Fatal(err)
	}
	if len(resources) != 3 {
		t.Fatalf("Google resources = %#v", resources)
	}
	got := make(map[string]Candidate)
	for _, resource := range resources {
		got[resource.Candidate.RemoteID] = resource.Candidate
		if resource.Credential.RefreshToken != "fixture-refresh" {
			t.Fatal("Google refresh token was not propagated for encrypted storage")
		}
	}
	if got["accounts/one/locations/101"].Handle != "London, GB" ||
		got["accounts/two/locations/202"].DisplayName != "Difference Engine" {
		t.Fatalf("Google normalized resources = %#v", got)
	}
	if err := adapter.Verify(
		context.Background(),
		"accounts/one/locations/102",
		grant,
	); err != nil {
		t.Fatal(err)
	}
	refreshed, err := adapter.Refresh(context.Background(), grant)
	if err != nil {
		t.Fatal(err)
	}
	if refreshed.AccessToken != "fixture-access" ||
		refreshed.RefreshToken != "fixture-refresh" {
		t.Fatalf("Google refreshed credential = %#v", refreshed)
	}
	if err := adapter.Revoke(
		context.Background(),
		"accounts/one/locations/101",
		grant,
	); !errors.Is(err, ErrExternalRevocationUnavailable) {
		t.Fatalf("Google revoke error = %v", err)
	}
}

func TestGoogleBusinessProfileFailureFixtures(t *testing.T) {
	tests := []struct {
		name      string
		status    int
		body      string
		kind      ProviderFailureKind
		retryable bool
	}{
		{"invalid_grant", 400, `{"error":"invalid_grant"}`, FailureAuthentication, false},
		{"unauthorized", 401, `{}`, FailureAuthentication, false},
		{"forbidden", 403, `{}`, FailurePermissionMissing, false},
		{
			"quota",
			403,
			`{"error":{"errors":[{"reason":"quotaExceeded"}]}}`,
			FailureTemporary,
			true,
		},
		{"not_found", 404, `{}`, FailureResourceGone, false},
		{"rate_limit", 429, `{}`, FailureTemporary, true},
		{"server", 503, `{}`, FailureTemporary, true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			adapter := googleBusinessFailureFixture(t, test.status, test.body)
			_, err := adapter.Discover(
				context.Background(),
				Credential{AccessToken: "fixture-token"},
			)
			var failure *ProviderFailure
			if !errors.As(err, &failure) ||
				failure.Kind != test.kind ||
				failure.Retryable != test.retryable {
				t.Fatalf("Google failure = %#v, err=%v", failure, err)
			}
		})
	}
	t.Run("malformed", func(t *testing.T) {
		adapter := googleBusinessFailureFixture(t, 200, `{`)
		_, err := adapter.Discover(
			context.Background(),
			Credential{AccessToken: "fixture-token"},
		)
		var failure *ProviderFailure
		if !errors.As(err, &failure) ||
			failure.Kind != FailureInvalidResponse {
			t.Fatalf("Google malformed failure = %#v, err=%v", failure, err)
		}
	})
}

func googleBusinessFailureFixture(
	t *testing.T,
	status int,
	body string,
) *GoogleBusinessProfileAdapter {
	t.Helper()
	adapter, err := NewGoogleBusinessProfileAdapter(
		GoogleBusinessProfileAdapterConfig{
			ClientID:     "fixture-client",
			ClientSecret: "fixture-secret",
			RedirectURL:  "https://api.example.test/social/callback",
			HTTPClient: &http.Client{Transport: professionalRoundTripFunc(
				func(*http.Request) (*http.Response, error) {
					return fixtureJSONResponse(status, body), nil
				},
			)},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	return adapter
}

func TestGoogleBusinessProfileRejectsUnsafeConfigAndMissingOfflineToken(
	t *testing.T,
) {
	if _, err := NewGoogleBusinessProfileAdapter(
		GoogleBusinessProfileAdapterConfig{
			ClientID:     "id",
			ClientSecret: "secret",
			RedirectURL:  "http://api.example.test/callback",
		},
	); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("Google invalid config error = %v", err)
	}
	adapter, err := NewGoogleBusinessProfileAdapter(
		GoogleBusinessProfileAdapterConfig{
			ClientID:     "id",
			ClientSecret: "secret",
			RedirectURL:  "https://api.example.test/callback",
			TokenURL:     "https://google.fixture/token",
			HTTPClient: &http.Client{Transport: professionalRoundTripFunc(
				func(*http.Request) (*http.Response, error) {
					return fixtureJSONResponse(http.StatusOK, `{
						"access_token":"fixture-access",
						"expires_in":3600,
						"scope":"`+googleBusinessScope+`",
						"token_type":"Bearer"
					}`), nil
				},
			)},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = adapter.Exchange(context.Background(), ExchangeRequest{
		Code:        "fixture-code",
		RedirectURL: adapter.Config().RedirectURL,
	}); err == nil {
		t.Fatal("Google accepted an offline grant without refresh token")
	}
	if _, err = adapter.Refresh(
		context.Background(),
		Credential{},
	); !errors.Is(err, ErrNotRefreshable) {
		t.Fatalf("Google missing refresh error = %v", err)
	}
}
