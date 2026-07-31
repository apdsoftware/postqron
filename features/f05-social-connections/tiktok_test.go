package socialconnections

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestTikTokAdapterOfficialOAuthAndLifecycle(t *testing.T) {
	var creatorCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(
		response http.ResponseWriter,
		request *http.Request,
	) {
		switch request.URL.Path {
		case "/oauth/token":
			if request.Method != http.MethodPost {
				t.Errorf("token method = %s", request.Method)
			}
			_ = request.ParseForm()
			if request.Form.Get("client_key") != "fixture-client-key" ||
				request.Form.Get("client_secret") != "fixture-client-secret" {
				t.Errorf("token client credentials were not sent server-side")
			}
			switch request.Form.Get("grant_type") {
			case "authorization_code":
				if request.Form.Get("code") != "fixture-code" ||
					request.Form.Get("redirect_uri") !=
						"https://app.example.test/tiktok/callback" ||
					request.Form.Get("code_verifier") != "" {
					t.Errorf("exchange form = %v", request.Form)
				}
				writeVideoFixture(t, response, "tiktok_token.json")
			case "refresh_token":
				if request.Form.Get("refresh_token") != "fixture-tiktok-refresh" {
					t.Errorf("refresh form = %v", request.Form)
				}
				writeVideoFixture(t, response, "tiktok_refresh.json")
			default:
				t.Errorf("grant type = %q", request.Form.Get("grant_type"))
				http.Error(response, "unexpected grant", http.StatusBadRequest)
			}
		case "/creator":
			creatorCalls.Add(1)
			if request.Method != http.MethodPost ||
				request.Header.Get("Authorization") !=
					"Bearer fixture-tiktok-access" {
				t.Errorf(
					"creator request = %s %q",
					request.Method,
					request.Header.Get("Authorization"),
				)
			}
			writeVideoFixture(t, response, "tiktok_discovery.json")
		case "/revoke":
			_ = request.ParseForm()
			if request.Form.Get("token") != "fixture-tiktok-refreshed" ||
				request.Form.Get("client_secret") != "fixture-client-secret" {
				t.Errorf("revoke form = %v", request.Form)
			}
			writeVideoFixture(t, response, "tiktok_revoke.json")
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()

	adapter, err := NewTikTokAdapter(TikTokAdapterConfig{
		ClientKey:      "fixture-client-key",
		ClientSecret:   "fixture-client-secret",
		RedirectURL:    "https://app.example.test/tiktok/callback",
		HTTPClient:     server.Client(),
		TokenURL:       server.URL + "/oauth/token",
		CreatorInfoURL: server.URL + "/creator",
		RevokeURL:      server.URL + "/revoke",
		Now: func() time.Time {
			return time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	config := adapter.Config()
	if !slicesEqual(config.Scopes, []string{"video.publish"}) ||
		config.ScopeSeparator != OAuthScopeSeparatorComma ||
		config.SupportsPKCE {
		t.Fatalf("TikTok OAuth config = %#v", config)
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
	if query.Get("client_key") != "fixture-client-key" ||
		query.Get("client_id") != "" ||
		query.Get("scope") != "video.publish" ||
		query.Get("state") != "one-time-state" ||
		query.Get("code_challenge") != "" ||
		strings.Contains(authorizationURL, "fixture-client-secret") {
		t.Fatalf("TikTok authorization URL = %s", authorizationURL)
	}

	grant, err := adapter.Exchange(context.Background(), ExchangeRequest{
		Code:        "fixture-code",
		RedirectURL: "https://app.example.test/tiktok/callback",
	})
	if err != nil {
		t.Fatal(err)
	}
	if grant.AccessToken != "fixture-tiktok-access" ||
		grant.RefreshToken != "fixture-tiktok-refresh" ||
		!slicesEqual(grant.Scopes, []string{"video.publish"}) {
		t.Fatalf("TikTok grant = %#v", grant)
	}
	resources, err := adapter.Discover(context.Background(), grant)
	if err != nil {
		t.Fatal(err)
	}
	if len(resources) != 1 ||
		resources[0].Candidate.RemoteID != "fixture_creator" ||
		resources[0].Candidate.DisplayName != "Fixture Creator" ||
		resources[0].Candidate.ResourceType != ResourceTikTokProfile {
		t.Fatalf("TikTok resources = %#v", resources)
	}
	if err := adapter.Verify(
		context.Background(),
		"fixture_creator",
		grant,
	); err != nil {
		t.Fatal(err)
	}
	if creatorCalls.Load() != 2 {
		t.Fatalf("creator calls = %d", creatorCalls.Load())
	}
	refreshed, err := adapter.Refresh(context.Background(), grant)
	if err != nil {
		t.Fatal(err)
	}
	if refreshed.AccessToken != "fixture-tiktok-refreshed" ||
		refreshed.RefreshToken != "fixture-tiktok-refresh-rotated" {
		t.Fatalf("TikTok refresh = %#v", refreshed)
	}
	if err := adapter.Revoke(
		context.Background(),
		"fixture_creator",
		refreshed,
	); err != nil {
		t.Fatal(err)
	}
}

func TestTikTokRejectsIncompleteScopesAndMalformedPayloads(t *testing.T) {
	tests := []struct {
		name    string
		fixture string
	}{
		{name: "incomplete grant", fixture: "tiktok_incomplete_scope.json"},
		{name: "malformed JSON", fixture: "tiktok_malformed.json"},
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
			adapter := newTikTokFixtureAdapter(t, server, server.URL)
			_, err := adapter.Exchange(context.Background(), ExchangeRequest{
				Code:        "fixture-code",
				RedirectURL: "https://app.example.test/tiktok/callback",
			})
			assertProviderFailure(t, err, FailureInvalidResponse, false)
		})
	}
}

func TestTikTokFailureFixturesAreClientSafe(t *testing.T) {
	tests := []struct {
		name      string
		status    int
		fixture   string
		kind      ProviderFailureKind
		retryable bool
	}{
		{"denial", 400, "tiktok_denial.json", FailurePermissionMissing, false},
		{"provider quota", 200, "tiktok_quota.json", FailureTemporary, true},
		{"rate code", 200, "tiktok_rate_limit.json", FailureTemporary, true},
		{"processing", 200, "tiktok_processing_error.json", FailureTemporary, true},
		{"401", 401, "tiktok_401.json", FailureAuthentication, false},
		{"403", 403, "tiktok_403.json", FailurePermissionMissing, false},
		{"404", 404, "tiktok_404.json", FailureResourceGone, false},
		{"429", 429, "tiktok_429.json", FailureTemporary, true},
		{"5xx", 500, "tiktok_500.json", FailureTemporary, true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			payload := readVideoFixture(t, test.fixture)
			err := classifyTikTokError(test.status, payload)
			assertProviderFailure(t, err, test.kind, test.retryable)
			if strings.Contains(err.Error(), "message") ||
				strings.Contains(err.Error(), "fixture-client-secret") {
				t.Fatalf("provider error exposed payload details: %v", err)
			}
		})
	}
}

func TestTikTokRuntimeRequiresEveryExternalGate(t *testing.T) {
	key := []byte("0123456789abcdef0123456789abcdef")
	cipher, err := NewAESGCMCipher("fixture-key", key)
	if err != nil {
		t.Fatal(err)
	}
	complete := map[string]string{
		configTikTokEnabled:        "true",
		configTikTokClientKey:      "fixture-client-key",
		configTikTokClientSecret:   "fixture-client-secret",
		configTikTokRedirect:       "https://app.example.test/tiktok/callback",
		configTikTokReviewed:       "true",
		configTikTokAudited:        "true",
		configTikTokSmokeVerified:  "true",
		configTikTokAccessVerified: "true",
	}
	tests := []struct {
		name  string
		key   string
		value string
		state ProviderConfigurationState
	}{
		{"credentials", configTikTokClientSecret, "", ProviderNotConfigured},
		{"review", configTikTokReviewed, "", ProviderReviewRequired},
		{"audit", configTikTokAudited, "", ProviderAuditRequired},
		{"smoke", configTikTokSmokeVerified, "", ProviderAuditRequired},
		{"quota access", configTikTokAccessVerified, "", ProviderReviewRequired},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			values := cloneStringMap(complete)
			values[test.key] = test.value
			adapters, availability := videoRuntimeMaps()
			configureVideoNetworksRuntime(values, cipher, adapters, availability)
			got := availability[ProviderTikTok]
			if got.Status != ProviderUnavailable ||
				got.ConfigurationState != test.state ||
				adapters[ProviderTikTok] != nil {
				t.Fatalf("TikTok gate = %#v", got)
			}
		})
	}
	adapters, availability := videoRuntimeMaps()
	configureVideoNetworksRuntime(complete, cipher, adapters, availability)
	if adapters[ProviderTikTok] == nil ||
		availability[ProviderTikTok].Status != ProviderAvailable ||
		availability[ProviderTikTok].ConfigurationState != ProviderReady {
		t.Fatalf("ready TikTok runtime = %#v", availability[ProviderTikTok])
	}
}

func newTikTokFixtureAdapter(
	t *testing.T,
	server *httptest.Server,
	tokenURL string,
) *TikTokAdapter {
	t.Helper()
	adapter, err := NewTikTokAdapter(TikTokAdapterConfig{
		ClientKey:    "fixture-client-key",
		ClientSecret: "fixture-client-secret",
		RedirectURL:  "https://app.example.test/tiktok/callback",
		HTTPClient:   server.Client(),
		TokenURL:     tokenURL,
	})
	if err != nil {
		t.Fatal(err)
	}
	return adapter
}

func readVideoFixture(t *testing.T, name string) []byte {
	t.Helper()
	payload, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return payload
}

func writeVideoFixture(
	t *testing.T,
	response http.ResponseWriter,
	name string,
) {
	t.Helper()
	response.Header().Set("Content-Type", "application/json")
	if _, err := response.Write(readVideoFixture(t, name)); err != nil {
		t.Error(err)
	}
}

func parseVideoAuthorizationQuery(raw string) (url.Values, error) {
	parsed, err := url.Parse(raw)
	if err != nil {
		return nil, err
	}
	return parsed.Query(), nil
}

func assertProviderFailure(
	t *testing.T,
	err error,
	kind ProviderFailureKind,
	retryable bool,
) {
	t.Helper()
	var failure *ProviderFailure
	if !errors.As(err, &failure) ||
		failure.Kind != kind ||
		failure.Retryable != retryable {
		t.Fatalf(
			"provider failure = %#v (%v), want kind=%s retryable=%t",
			failure,
			err,
			kind,
			retryable,
		)
	}
}

func cloneStringMap(source map[string]string) map[string]string {
	result := make(map[string]string, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}

func videoRuntimeMaps() (
	map[Provider]Adapter,
	map[Provider]ProviderAvailability,
) {
	adapters := make(map[Provider]Adapter)
	availability := make(map[Provider]ProviderAvailability)
	for _, provider := range SupportedProviders {
		availability[provider] = ProviderAvailability{
			Provider:           provider,
			Status:             ProviderUnavailable,
			ConfigurationState: ProviderNotConfigured,
		}
	}
	return adapters, availability
}

func TestTikTokFixtureFilesContainNoClientSecrets(t *testing.T) {
	for _, name := range []string{
		"tiktok_token.json",
		"tiktok_discovery.json",
		"tiktok_refresh.json",
		"tiktok_revoke.json",
	} {
		var payload any
		if err := json.Unmarshal(readVideoFixture(t, name), &payload); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		serialized, _ := json.Marshal(payload)
		if strings.Contains(string(serialized), "client_secret") {
			t.Fatalf("%s contains a client secret field", name)
		}
	}
}
