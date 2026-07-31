package socialconnections

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestYouTubeAdapterUsesAtomicMinimumScopesAndLifecycle(t *testing.T) {
	var channelCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(
		response http.ResponseWriter,
		request *http.Request,
	) {
		switch request.URL.Path {
		case "/token":
			_ = request.ParseForm()
			if request.Form.Get("client_id") != "fixture-client-id" ||
				request.Form.Get("client_secret") != "fixture-client-secret" {
				t.Errorf("token client credentials were not sent server-side")
			}
			switch request.Form.Get("grant_type") {
			case "authorization_code":
				if request.Form.Get("code") != "fixture-code" ||
					request.Form.Get("redirect_uri") !=
						"https://app.example.test/youtube/callback" ||
					request.Form.Get("code_verifier") != "" {
					t.Errorf("exchange form = %v", request.Form)
				}
				writeVideoFixture(t, response, "youtube_token.json")
			case "refresh_token":
				if request.Form.Get("refresh_token") != "fixture-youtube-refresh" {
					t.Errorf("refresh form = %v", request.Form)
				}
				writeVideoFixture(t, response, "youtube_refresh.json")
			default:
				t.Errorf("grant type = %q", request.Form.Get("grant_type"))
				http.Error(response, "unexpected grant", http.StatusBadRequest)
			}
		case "/youtube/v3/channels":
			channelCalls.Add(1)
			if request.Method != http.MethodGet ||
				request.Header.Get("Authorization") !=
					"Bearer fixture-youtube-access" ||
				request.URL.Query().Get("part") != "id,snippet" ||
				request.URL.Query().Get("mine") != "true" {
				t.Errorf(
					"channels request = %s %q %v",
					request.Method,
					request.Header.Get("Authorization"),
					request.URL.Query(),
				)
			}
			writeVideoFixture(t, response, "youtube_discovery.json")
		case "/revoke":
			_ = request.ParseForm()
			if request.Form.Get("token") != "fixture-youtube-refresh" {
				t.Errorf("revoke form = %v", request.Form)
			}
			writeVideoFixture(t, response, "youtube_revoke.json")
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()

	adapter, err := NewYouTubeAdapter(YouTubeAdapterConfig{
		ClientID:     "fixture-client-id",
		ClientSecret: "fixture-client-secret",
		RedirectURL:  "https://app.example.test/youtube/callback",
		HTTPClient:   server.Client(),
		TokenURL:     server.URL + "/token",
		APIBaseURL:   server.URL + "/youtube/v3",
		RevokeURL:    server.URL + "/revoke",
		Now: func() time.Time {
			return time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	config := adapter.Config()
	if !slicesEqual(config.Scopes, []string{
		"https://www.googleapis.com/auth/youtube.readonly",
		"https://www.googleapis.com/auth/youtube.upload",
	}) ||
		config.ScopeSeparator != OAuthScopeSeparatorSpace ||
		config.SupportsPKCE ||
		config.ExtraParameters["access_type"] != "offline" ||
		config.ExtraParameters["prompt"] != "consent" {
		t.Fatalf("YouTube OAuth config = %#v", config)
	}
	authorizationURL, err := buildAuthorizationURL(
		config,
		"one-time-state",
		"",
	)
	if err != nil {
		t.Fatal(err)
	}
	query, err := parseVideoAuthorizationQuery(authorizationURL)
	if err != nil {
		t.Fatal(err)
	}
	if query.Get("scope") != strings.Join(config.Scopes, " ") ||
		query.Get("access_type") != "offline" ||
		query.Get("prompt") != "consent" ||
		query.Get("code_challenge") != "" ||
		strings.Contains(authorizationURL, "fixture-client-secret") {
		t.Fatalf("YouTube authorization URL = %s", authorizationURL)
	}

	grant, err := adapter.Exchange(context.Background(), ExchangeRequest{
		Code:        "fixture-code",
		RedirectURL: "https://app.example.test/youtube/callback",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !slicesEqual(grant.Scopes, config.Scopes) ||
		grant.AccessToken != "fixture-youtube-access" ||
		grant.RefreshToken != "fixture-youtube-refresh" {
		t.Fatalf("YouTube grant = %#v", grant)
	}
	resources, err := adapter.Discover(context.Background(), grant)
	if err != nil {
		t.Fatal(err)
	}
	if len(resources) != 1 ||
		resources[0].Candidate.RemoteID != "UCfixture" ||
		resources[0].Candidate.Handle != "@fixture" ||
		resources[0].Candidate.ResourceType != ResourceYouTubeChannel {
		t.Fatalf("YouTube resources = %#v", resources)
	}
	if err := adapter.Verify(
		context.Background(),
		"UCfixture",
		grant,
	); err != nil {
		t.Fatal(err)
	}
	if channelCalls.Load() != 2 {
		t.Fatalf("channel calls = %d", channelCalls.Load())
	}
	refreshed, err := adapter.Refresh(context.Background(), grant)
	if err != nil {
		t.Fatal(err)
	}
	if refreshed.AccessToken != "fixture-youtube-refreshed" ||
		refreshed.RefreshToken != "fixture-youtube-refresh" ||
		!slicesEqual(refreshed.Scopes, config.Scopes) {
		t.Fatalf("YouTube refresh = %#v", refreshed)
	}
	if err := adapter.Revoke(
		context.Background(),
		"UCfixture",
		refreshed,
	); err != nil {
		t.Fatal(err)
	}
}

func TestYouTubeRejectsPartialGrantAndMalformedPayloads(t *testing.T) {
	tests := []struct {
		name    string
		fixture string
	}{
		{name: "upload without readonly", fixture: "youtube_incomplete_scope.json"},
		{name: "malformed JSON", fixture: "youtube_malformed.json"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(
				response http.ResponseWriter,
				_ *http.Request,
			) {
				writeVideoFixture(t, response, test.fixture)
			}))
			defer server.Close()
			adapter := newYouTubeFixtureAdapter(t, server, server.URL)
			_, err := adapter.Exchange(context.Background(), ExchangeRequest{
				Code:        "fixture-code",
				RedirectURL: "https://app.example.test/youtube/callback",
			})
			assertProviderFailure(t, err, FailureInvalidResponse, false)
		})
	}
}

func TestYouTubeFailureFixturesAreClientSafe(t *testing.T) {
	tests := []struct {
		name      string
		status    int
		fixture   string
		kind      ProviderFailureKind
		retryable bool
	}{
		{"denial", 400, "youtube_denial.json", FailurePermissionMissing, false},
		{"provider quota", 403, "youtube_quota.json", FailureTemporary, true},
		{"rate code", 429, "youtube_rate_limit.json", FailureTemporary, true},
		{"processing", 503, "youtube_processing_error.json", FailureTemporary, true},
		{"401", 401, "youtube_401.json", FailureAuthentication, false},
		{"403", 403, "youtube_403.json", FailurePermissionMissing, false},
		{"404", 404, "youtube_404.json", FailureResourceGone, false},
		{"429", 429, "youtube_429.json", FailureTemporary, true},
		{"5xx", 500, "youtube_500.json", FailureTemporary, true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := classifyYouTubeError(
				test.status,
				readVideoFixture(t, test.fixture),
			)
			assertProviderFailure(t, err, test.kind, test.retryable)
			if strings.Contains(err.Error(), "message") ||
				strings.Contains(err.Error(), "fixture-client-secret") {
				t.Fatalf("provider error exposed payload details: %v", err)
			}
		})
	}
}

func TestYouTubeRuntimeRequiresEveryExternalGate(t *testing.T) {
	cipher, err := NewAESGCMCipher(
		"fixture-key",
		[]byte("0123456789abcdef0123456789abcdef"),
	)
	if err != nil {
		t.Fatal(err)
	}
	complete := map[string]string{
		configYouTubeEnabled:        "true",
		configYouTubeClientID:       "fixture-client-id",
		configYouTubeClientSecret:   "fixture-client-secret",
		configYouTubeRedirect:       "https://app.example.test/youtube/callback",
		configYouTubeVerified:       "true",
		configYouTubeAudited:        "true",
		configYouTubeSmokeVerified:  "true",
		configYouTubeAccessVerified: "true",
	}
	tests := []struct {
		name  string
		key   string
		value string
		state ProviderConfigurationState
	}{
		{"credentials", configYouTubeClientSecret, "", ProviderNotConfigured},
		{"OAuth verification", configYouTubeVerified, "", ProviderReviewRequired},
		{"API audit", configYouTubeAudited, "", ProviderAuditRequired},
		{"smoke", configYouTubeSmokeVerified, "", ProviderAuditRequired},
		{"quota access", configYouTubeAccessVerified, "", ProviderReviewRequired},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			values := cloneStringMap(complete)
			values[test.key] = test.value
			adapters, availability := videoRuntimeMaps()
			configureVideoNetworksRuntime(values, cipher, adapters, availability)
			got := availability[ProviderYouTube]
			if got.Status != ProviderUnavailable ||
				got.ConfigurationState != test.state ||
				adapters[ProviderYouTube] != nil {
				t.Fatalf("YouTube gate = %#v", got)
			}
		})
	}
	adapters, availability := videoRuntimeMaps()
	configureVideoNetworksRuntime(complete, cipher, adapters, availability)
	if adapters[ProviderYouTube] == nil ||
		availability[ProviderYouTube].Status != ProviderAvailable ||
		availability[ProviderYouTube].ConfigurationState != ProviderReady {
		t.Fatalf("ready YouTube runtime = %#v", availability[ProviderYouTube])
	}
}

func TestYouTubeVerifyReportsAChangedChannelAsGone(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(
		response http.ResponseWriter,
		_ *http.Request,
	) {
		writeVideoFixture(t, response, "youtube_discovery.json")
	}))
	defer server.Close()
	adapter, err := NewYouTubeAdapter(YouTubeAdapterConfig{
		ClientID:     "fixture-client-id",
		ClientSecret: "fixture-client-secret",
		RedirectURL:  "https://app.example.test/youtube/callback",
		HTTPClient:   server.Client(),
		APIBaseURL:   server.URL,
	})
	if err != nil {
		t.Fatal(err)
	}
	err = adapter.Verify(context.Background(), "UCmissing", Credential{
		AccessToken: "fixture-youtube-access",
		Scopes:      append([]string(nil), youTubeRequiredScopes...),
	})
	assertProviderFailure(t, err, FailureResourceGone, false)
}

func newYouTubeFixtureAdapter(
	t *testing.T,
	server *httptest.Server,
	tokenURL string,
) *YouTubeAdapter {
	t.Helper()
	adapter, err := NewYouTubeAdapter(YouTubeAdapterConfig{
		ClientID:     "fixture-client-id",
		ClientSecret: "fixture-client-secret",
		RedirectURL:  "https://app.example.test/youtube/callback",
		HTTPClient:   server.Client(),
		TokenURL:     tokenURL,
	})
	if err != nil {
		t.Fatal(err)
	}
	return adapter
}
