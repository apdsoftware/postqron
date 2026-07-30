package socialconnections

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
)

type professionalRoundTripFunc func(*http.Request) (*http.Response, error)

func (function professionalRoundTripFunc) RoundTrip(
	request *http.Request,
) (*http.Response, error) {
	return function(request)
}

func fixtureJSONResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func TestLinkedInAdapterCompleteOfflineFlow(t *testing.T) {
	t.Helper()
	var tokenExchanges int
	client := &http.Client{Transport: professionalRoundTripFunc(
		func(request *http.Request) (*http.Response, error) {
			if request.URL.Path == "/oauth/v2/accessToken" {
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
					t.Fatal("LinkedIn token exchange omitted server-side secret")
				}
				refresh := "fixture-refresh"
				if values.Get("grant_type") == "refresh_token" {
					refresh = "fixture-refresh-rotated"
				}
				return fixtureJSONResponse(http.StatusOK, `{
					"access_token":"fixture-access-`+strconv.Itoa(tokenExchanges)+`",
					"expires_in":5184000,
					"refresh_token":"`+refresh+`",
					"refresh_token_expires_in":31536000,
					"scope":"`+strings.Join(linkedInAtomicScopes, " ")+`"
				}`), nil
			}
			if request.Header.Get("Authorization") == "" {
				t.Fatal("LinkedIn API request omitted bearer token")
			}
			if request.URL.Path == "/v2/userinfo" {
				if request.Header.Get("Linkedin-Version") != "" {
					t.Fatal("OIDC userinfo unexpectedly received marketing headers")
				}
				return fixtureJSONResponse(http.StatusOK, `{
					"sub":"member42",
					"name":"Ada Lovelace",
					"picture":"https://media.example.test/member42.jpg"
				}`), nil
			}
			if request.Header.Get("Linkedin-Version") != "202607" ||
				request.Header.Get("X-Restli-Protocol-Version") != "2.0.0" {
				t.Fatal("LinkedIn marketing headers are incomplete")
			}
			if request.URL.Path == "/rest/organizationAcls" {
				start := request.URL.Query().Get("start")
				if request.URL.Query().Get("q") != "roleAssignee" ||
					request.URL.Query().Get("state") != "APPROVED" {
					t.Fatalf("unexpected ACL query: %s", request.URL.RawQuery)
				}
				if start == "0" {
					elements := make([]map[string]string, 100)
					for index := range elements {
						elements[index] = map[string]string{
							"organization": "urn:li:organization:999",
							"role":         "ANALYST",
							"state":        "APPROVED",
						}
					}
					elements[0] = map[string]string{
						"organization": "urn:li:organization:101",
						"role":         "ADMINISTRATOR",
						"state":        "APPROVED",
					}
					payload, _ := json.Marshal(map[string]any{
						"elements": elements,
						"paging": map[string]any{
							"start": 0,
							"count": 100,
							"total": 101,
							"links": []map[string]string{{
								"rel":  "next",
								"href": "/rest/organizationAcls?q=roleAssignee&state=APPROVED&start=100&count=100",
							}},
						},
					})
					return fixtureJSONResponse(http.StatusOK, string(payload)), nil
				}
				if start != "100" {
					t.Fatalf("unexpected LinkedIn page start %q", start)
				}
				return fixtureJSONResponse(http.StatusOK, `{
					"elements":[{
						"organizationTarget":"urn:li:organization:202",
						"role":"CONTENT_ADMINISTRATOR",
						"state":"APPROVED"
					}],
					"paging":{"start":100,"count":1,"total":101}
				}`), nil
			}
			switch request.URL.Path {
			case "/rest/organizations/101":
				return fixtureJSONResponse(
					http.StatusOK,
					`{"id":101,"localizedName":"Analytical Engines","vanityName":"engines"}`,
				), nil
			case "/rest/organizations/202":
				return fixtureJSONResponse(
					http.StatusOK,
					`{"id":"202","localizedName":"Difference Labs","vanityName":"difference"}`,
				), nil
			default:
				t.Fatalf("unexpected LinkedIn request %s", request.URL.String())
				return nil, nil
			}
		},
	)}
	adapter, err := NewLinkedInAdapter(LinkedInAdapterConfig{
		ClientID:                    "fixture-client",
		ClientSecret:                "fixture-secret",
		RedirectURL:                 "https://api.example.test/social/callback",
		APIVersion:                  "202607",
		ProgrammaticRefreshApproved: true,
		HTTPClient:                  client,
		AuthorizationURL:            "https://linkedin.fixture/oauth/v2/authorization",
		TokenURL:                    "https://linkedin.fixture/oauth/v2/accessToken",
		UserInfoURL:                 "https://linkedin.fixture/v2/userinfo",
		APIBaseURL:                  "https://linkedin.fixture",
	})
	if err != nil {
		t.Fatal(err)
	}
	config := adapter.Config()
	if strings.Join(config.Scopes, " ") != strings.Join(linkedInAtomicScopes, " ") ||
		config.ScopeSeparator != OAuthScopeSeparatorSpace ||
		config.SupportsPKCE {
		t.Fatalf("LinkedIn OAuth config = %#v", config)
	}
	if strings.Contains(config.AuthorizationURL, "fixture-secret") {
		t.Fatal("LinkedIn secret leaked into browser configuration")
	}
	authorizationURL, err := buildAuthorizationURL(config, "fixture-state", "")
	if err != nil {
		t.Fatal(err)
	}
	parsedAuthorizationURL, err := url.Parse(authorizationURL)
	if err != nil {
		t.Fatal(err)
	}
	if parsedAuthorizationURL.Query().Get("scope") !=
		strings.Join(linkedInAtomicScopes, " ") ||
		parsedAuthorizationURL.Query().Get("state") != "fixture-state" {
		t.Fatalf("LinkedIn authorization URL = %s", authorizationURL)
	}
	capabilities := adapter.AdapterCapabilities()
	if !capabilities.Authorization || !capabilities.ResourceSelection ||
		!capabilities.TokenRefresh || capabilities.PKCE ||
		capabilities.RemoteRevocation {
		t.Fatalf("LinkedIn capabilities = %#v", capabilities)
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
		t.Fatalf("LinkedIn resources = %#v", resources)
	}
	got := make(map[string]Candidate)
	for _, resource := range resources {
		got[resource.Candidate.RemoteID] = resource.Candidate
		if resource.Credential.AccessToken == "" ||
			resource.Credential.RefreshToken == "" {
			t.Fatal("LinkedIn discovered resource lacks encrypted-store credential input")
		}
		if strings.Join(resource.Credential.Scopes, " ") !=
			strings.Join(linkedInAtomicScopes, " ") {
			t.Fatalf("LinkedIn credential scopes = %#v", resource.Credential.Scopes)
		}
	}
	if got["urn:li:person:member42"].ResourceType != ResourceLinkedInProfile ||
		got["urn:li:organization:101"].AccountType != AccountTypeOrganization ||
		got["urn:li:organization:202"].DisplayName != "Difference Labs" {
		t.Fatalf("LinkedIn normalized resources = %#v", got)
	}
	if err := adapter.Verify(
		context.Background(),
		"urn:li:organization:101",
		grant,
	); err != nil {
		t.Fatal(err)
	}
	refreshed, err := adapter.Refresh(context.Background(), grant)
	if err != nil {
		t.Fatal(err)
	}
	if refreshed.AccessToken != "fixture-access-2" ||
		refreshed.RefreshToken != "fixture-refresh-rotated" {
		t.Fatalf("LinkedIn refreshed credential = %#v", refreshed)
	}
	if err := adapter.Revoke(
		context.Background(),
		"urn:li:person:member42",
		grant,
	); !errors.Is(err, ErrExternalRevocationUnavailable) {
		t.Fatalf("LinkedIn revoke error = %v", err)
	}
}

func TestLinkedInRefreshRequiresExplicitPartnerApproval(t *testing.T) {
	adapter, err := NewLinkedInAdapter(LinkedInAdapterConfig{
		ClientID:     "fixture-client",
		ClientSecret: "fixture-secret",
		RedirectURL:  "https://api.example.test/social/callback",
		APIVersion:   "202607",
	})
	if err != nil {
		t.Fatal(err)
	}
	if adapter.AdapterCapabilities().TokenRefresh {
		t.Fatal("LinkedIn claimed unapproved programmatic refresh")
	}
	if _, err = adapter.Refresh(
		context.Background(),
		Credential{RefreshToken: "fixture-refresh"},
	); !errors.Is(err, ErrNotRefreshable) {
		t.Fatalf("LinkedIn refresh error = %v", err)
	}
}

func TestLinkedInTokenScopeFixtures(t *testing.T) {
	tests := []struct {
		name      string
		scopeJSON string
		wantKind  ProviderFailureKind
	}{
		{
			name: "omitted_means_requested_scope",
		},
		{
			name:      "reduced_scope_fails_closed",
			scopeJSON: `,"scope":"openid profile"`,
			wantKind:  FailurePermissionMissing,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			adapter, err := NewLinkedInAdapter(LinkedInAdapterConfig{
				ClientID:     "fixture-client",
				ClientSecret: "fixture-secret",
				RedirectURL:  "https://api.example.test/social/callback",
				APIVersion:   "202607",
				HTTPClient: &http.Client{Transport: professionalRoundTripFunc(
					func(*http.Request) (*http.Response, error) {
						return fixtureJSONResponse(http.StatusOK,
							`{"access_token":"fixture-access","expires_in":3600`+
								test.scopeJSON+`}`,
						), nil
					},
				)},
			})
			if err != nil {
				t.Fatal(err)
			}
			credential, err := adapter.Exchange(
				context.Background(),
				ExchangeRequest{
					Code:        "fixture-code",
					RedirectURL: adapter.Config().RedirectURL,
				},
			)
			if test.wantKind == "" {
				if err != nil {
					t.Fatal(err)
				}
				if strings.Join(credential.Scopes, " ") !=
					strings.Join(linkedInAtomicScopes, " ") {
					t.Fatalf("LinkedIn credential scopes = %#v", credential.Scopes)
				}
				return
			}
			var failure *ProviderFailure
			if !errors.As(err, &failure) || failure.Kind != test.wantKind {
				t.Fatalf("LinkedIn scope failure = %#v, err=%v", failure, err)
			}
		})
	}
}

func TestLinkedInFailureFixtures(t *testing.T) {
	tests := []struct {
		name      string
		status    int
		body      string
		kind      ProviderFailureKind
		retryable bool
	}{
		{"unauthorized", 401, `{}`, FailureAuthentication, false},
		{"forbidden", 403, `{}`, FailurePermissionMissing, false},
		{"not_found", 404, `{}`, FailureResourceGone, false},
		{"rate_limit", 429, `{}`, FailureTemporary, true},
		{"server", 503, `{}`, FailureTemporary, true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			adapter := linkedInFailureFixture(t, test.status, test.body)
			_, err := adapter.Discover(
				context.Background(),
				Credential{AccessToken: "fixture-token"},
			)
			var failure *ProviderFailure
			if !errors.As(err, &failure) ||
				failure.Kind != test.kind ||
				failure.Retryable != test.retryable {
				t.Fatalf("LinkedIn failure = %#v, err=%v", failure, err)
			}
		})
	}
	t.Run("malformed", func(t *testing.T) {
		adapter := linkedInFailureFixture(t, 200, `{`)
		_, err := adapter.Discover(
			context.Background(),
			Credential{AccessToken: "fixture-token"},
		)
		var failure *ProviderFailure
		if !errors.As(err, &failure) ||
			failure.Kind != FailureInvalidResponse {
			t.Fatalf("LinkedIn malformed failure = %#v, err=%v", failure, err)
		}
	})
}

func TestLinkedInRejectsUnsafePaginationLink(t *testing.T) {
	adapter, err := NewLinkedInAdapter(LinkedInAdapterConfig{
		ClientID:     "fixture-client",
		ClientSecret: "fixture-secret",
		RedirectURL:  "https://api.example.test/social/callback",
		APIVersion:   "202607",
		HTTPClient: &http.Client{Transport: professionalRoundTripFunc(
			func(request *http.Request) (*http.Response, error) {
				if request.URL.Path != "/rest/organizationAcls" {
					t.Fatalf("unexpected LinkedIn request %s", request.URL)
				}
				return fixtureJSONResponse(http.StatusOK, `{
					"elements":[],
					"paging":{
						"start":0,
						"count":100,
						"links":[{
							"rel":"next",
							"href":"https://attacker.example/steal?q=roleAssignee&state=APPROVED&start=100"
						}]
					}
				}`), nil
			},
		)},
		APIBaseURL: "https://linkedin.fixture",
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = adapter.organizationURNs(context.Background(), "fixture-token")
	var failure *ProviderFailure
	if !errors.As(err, &failure) ||
		failure.Kind != FailureInvalidResponse {
		t.Fatalf("LinkedIn unsafe pagination failure = %#v, err=%v", failure, err)
	}
}

func linkedInFailureFixture(
	t *testing.T,
	status int,
	body string,
) *LinkedInAdapter {
	t.Helper()
	adapter, err := NewLinkedInAdapter(LinkedInAdapterConfig{
		ClientID:     "fixture-client",
		ClientSecret: "fixture-secret",
		RedirectURL:  "https://api.example.test/social/callback",
		APIVersion:   "202607",
		HTTPClient: &http.Client{Transport: professionalRoundTripFunc(
			func(*http.Request) (*http.Response, error) {
				return fixtureJSONResponse(status, body), nil
			},
		)},
	})
	if err != nil {
		t.Fatal(err)
	}
	return adapter
}

func TestLinkedInConstructorRejectsUnsafeOrUnversionedConfiguration(t *testing.T) {
	tests := []LinkedInAdapterConfig{
		{},
		{
			ClientID:     "id",
			ClientSecret: "secret",
			RedirectURL:  "http://api.example.test/callback",
			APIVersion:   "202607",
		},
		{
			ClientID:     "id",
			ClientSecret: "secret",
			RedirectURL:  "https://api.example.test/callback",
			APIVersion:   "latest",
		},
	}
	for _, config := range tests {
		if _, err := NewLinkedInAdapter(config); !errors.Is(err, ErrInvalidArgument) {
			t.Fatalf("LinkedIn invalid config error = %v", err)
		}
	}
}
