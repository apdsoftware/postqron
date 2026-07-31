package socialconnections

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strings"
	"sync"
	"testing"
)

type mastodonFixtureResolver struct {
	mu        sync.Mutex
	addresses [][]netip.Addr
	calls     int
}

func (resolver *mastodonFixtureResolver) LookupNetIP(
	context.Context,
	string,
	string,
) ([]netip.Addr, error) {
	resolver.mu.Lock()
	defer resolver.mu.Unlock()
	index := resolver.calls
	if index >= len(resolver.addresses) {
		index = len(resolver.addresses) - 1
	}
	resolver.calls++
	return append([]netip.Addr(nil), resolver.addresses[index]...), nil
}

func mastodonFixtureHTTP(
	t *testing.T,
	server *httptest.Server,
	resolver *mastodonFixtureResolver,
) (*mastodonSafeHTTP, string) {
	t.Helper()
	actual, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	logical := "https://example.com:" + actual.Port()
	transport := server.Client().Transport.(*http.Transport).Clone()
	dialer := &net.Dialer{}
	return &mastodonSafeHTTP{
		resolver:  resolver,
		transport: transport,
		timeout:   decentralizedRequestTimeout,
		dialer: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return dialer.DialContext(ctx, network, actual.Host)
		},
	}, logical
}

func TestMastodonMultiInstanceDiscoveryExchangeRefreshProfileAndRevoke(
	t *testing.T,
) {
	t.Parallel()
	var logical string
	var tokenCalls int
	server := httptest.NewTLSServer(http.HandlerFunc(func(
		response http.ResponseWriter,
		request *http.Request,
	) {
		response.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/api/v2/instance":
			_ = json.NewEncoder(response).Encode(map[string]any{
				"domain":       "example.com",
				"version":      "4.4.8+fixture",
				"api_versions": map[string]int{"mastodon": 4},
			})
		case "/.well-known/oauth-authorization-server":
			_ = json.NewEncoder(response).Encode(map[string]any{
				"issuer":                    logical + "/",
				"authorization_endpoint":    logical + "/oauth/authorize",
				"token_endpoint":            logical + "/oauth/token",
				"revocation_endpoint":       logical + "/oauth/revoke",
				"app_registration_endpoint": logical + "/api/v1/apps",
				"grant_types_supported": []string{
					"authorization_code",
					"refresh_token",
				},
			})
		case "/oauth/token":
			if request.Method != http.MethodPost {
				t.Errorf("token method = %s", request.Method)
			}
			_ = request.ParseForm()
			if request.Form.Get("client_secret") != "fixture-client-secret" {
				t.Error("token request omitted client authentication")
			}
			tokenCalls++
			if tokenCalls == 1 {
				if request.Form.Get("code_verifier") != "fixture-verifier" {
					t.Error("PKCE verifier missing")
				}
				_ = json.NewEncoder(response).Encode(map[string]any{
					"access_token":  "fixture-access-token",
					"refresh_token": "fixture-refresh-token",
					"token_type":    "Bearer",
					"scope":         strings.Join(mastodonScopes, " "),
					"expires_in":    300,
				})
			} else {
				if request.Form.Get("grant_type") != "refresh_token" {
					t.Errorf("refresh form = %v", request.Form)
				}
				_ = json.NewEncoder(response).Encode(map[string]any{
					"access_token":  "rotated-access-token",
					"refresh_token": "rotated-refresh-token",
					"token_type":    "Bearer",
					"scope":         strings.Join(mastodonScopes, " "),
					"expires_in":    300,
				})
			}
		case "/api/v1/accounts/verify_credentials":
			if !strings.HasPrefix(request.Header.Get("Authorization"), "Bearer ") {
				t.Error("profile request omitted bearer token")
			}
			_ = json.NewEncoder(response).Encode(map[string]any{
				"id":           "42",
				"username":     "alice",
				"display_name": "Alice",
				"url":          logical + "/@alice",
				"avatar":       logical + "/avatar.png",
			})
		case "/oauth/revoke":
			_ = request.ParseForm()
			if request.Form.Get("token") != "rotated-access-token" {
				t.Errorf("revoked token = %q", request.Form.Get("token"))
			}
			_ = json.NewEncoder(response).Encode(map[string]any{})
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()
	resolver := &mastodonFixtureResolver{addresses: [][]netip.Addr{{
		netip.MustParseAddr("8.8.8.8"),
	}}}
	safeHTTP, origin := mastodonFixtureHTTP(t, server, resolver)
	logical = origin
	discovery := &MastodonDiscovery{http: safeHTTP}
	instance, err := discovery.Discover(context.Background(), origin)
	if err != nil {
		t.Fatal(err)
	}
	if !instance.SupportsPKCE || !instance.SupportsRefresh ||
		instance.APIVersion != 4 {
		t.Fatalf("instance = %#v", instance)
	}
	adapter, err := NewMastodonAdapter(MastodonAdapterConfig{
		Instance:     instance,
		ClientID:     "fixture-client",
		ClientSecret: "fixture-client-secret",
		RedirectURL:  "https://app.example.test/social/callback",
		HTTP:         safeHTTP,
	})
	if err != nil {
		t.Fatal(err)
	}
	grant, err := adapter.Exchange(context.Background(), ExchangeRequest{
		Code:         "fixture-code",
		RedirectURL:  "https://app.example.test/social/callback",
		PKCEVerifier: "fixture-verifier",
	})
	if err != nil {
		t.Fatal(err)
	}
	resources, err := adapter.Discover(context.Background(), grant)
	if err != nil {
		t.Fatal(err)
	}
	if len(resources) != 1 ||
		resources[0].Candidate.RemoteID != origin+"/api/v1/accounts/42" ||
		resources[0].Candidate.Handle != "@alice@example.com" {
		t.Fatalf("resources = %#v", resources)
	}
	refreshed, err := adapter.Refresh(context.Background(), grant)
	if err != nil {
		t.Fatal(err)
	}
	if refreshed.AccessToken != "rotated-access-token" ||
		refreshed.RefreshToken != "rotated-refresh-token" {
		t.Fatalf("refresh = %#v", refreshed)
	}
	if err = adapter.Verify(
		context.Background(),
		origin+"/api/v1/accounts/42",
		refreshed,
	); err != nil {
		t.Fatal(err)
	}
	if err = adapter.Revoke(
		context.Background(),
		origin+"/api/v1/accounts/42",
		refreshed,
	); err != nil {
		t.Fatal(err)
	}
}

func TestMastodonDiscoveryRejectsRedirectMalformedPrivateAndIncompatible(
	t *testing.T,
) {
	t.Parallel()
	var redirected bool
	server := httptest.NewTLSServer(http.HandlerFunc(func(
		response http.ResponseWriter,
		request *http.Request,
	) {
		switch request.URL.Path {
		case "/api/v2/instance":
			http.Redirect(response, request, "/private", http.StatusFound)
		case "/private":
			redirected = true
			_, _ = response.Write([]byte(`{"version":"4.4.0"}`))
		}
	}))
	defer server.Close()
	resolver := &mastodonFixtureResolver{addresses: [][]netip.Addr{{
		netip.MustParseAddr("8.8.4.4"),
	}}}
	safeHTTP, origin := mastodonFixtureHTTP(t, server, resolver)
	_, err := (&MastodonDiscovery{http: safeHTTP}).Discover(
		context.Background(),
		origin,
	)
	if err == nil || redirected {
		t.Fatalf("redirect error = %v, redirected = %v", err, redirected)
	}

	privateResolver := &mastodonFixtureResolver{addresses: [][]netip.Addr{{
		netip.MustParseAddr("127.0.0.1"),
	}}}
	privateHTTP, _ := mastodonFixtureHTTP(t, server, privateResolver)
	_, err = (&MastodonDiscovery{http: privateHTTP}).Discover(
		context.Background(),
		origin,
	)
	if !errors.Is(err, errUnsafeProviderURL) {
		t.Fatalf("private address error = %v", err)
	}

	if _, err = (&MastodonDiscovery{http: safeHTTP}).Discover(
		context.Background(),
		"http://example.com",
	); !errors.Is(err, errUnsafeProviderURL) {
		t.Fatalf("HTTP origin error = %v", err)
	}
}

func TestMastodonDNSIsPinnedAndResponseSizeIsLimited(t *testing.T) {
	t.Parallel()
	payload := bytes.Repeat([]byte("x"), decentralizedResponseLimit+1)
	server := httptest.NewTLSServer(http.HandlerFunc(func(
		response http.ResponseWriter,
		_ *http.Request,
	) {
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write(payload)
	}))
	defer server.Close()
	resolver := &mastodonFixtureResolver{addresses: [][]netip.Addr{
		{netip.MustParseAddr("8.8.8.8")},
		{netip.MustParseAddr("127.0.0.1")},
	}}
	safeHTTP, origin := mastodonFixtureHTTP(t, server, resolver)
	_, err := (&MastodonDiscovery{http: safeHTTP}).Discover(
		context.Background(),
		origin,
	)
	if err == nil {
		t.Fatal("oversized response accepted")
	}
	resolver.mu.Lock()
	calls := resolver.calls
	resolver.mu.Unlock()
	if calls != 1 {
		t.Fatalf("DNS resolver calls = %d; dial must use the pinned address", calls)
	}
}

func TestMastodonRevalidatesDNSBeforeEveryDiscoveryHop(t *testing.T) {
	t.Parallel()
	var logical string
	var metadataCalled bool
	server := httptest.NewTLSServer(http.HandlerFunc(func(
		response http.ResponseWriter,
		request *http.Request,
	) {
		response.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/api/v2/instance":
			_ = json.NewEncoder(response).Encode(map[string]any{
				"domain":  "example.com",
				"version": "4.4.0",
			})
		case "/.well-known/oauth-authorization-server":
			metadataCalled = true
			_ = json.NewEncoder(response).Encode(map[string]any{
				"issuer":                 logical + "/",
				"authorization_endpoint": logical + "/oauth/authorize",
				"token_endpoint":         logical + "/oauth/token",
			})
		}
	}))
	defer server.Close()
	resolver := &mastodonFixtureResolver{addresses: [][]netip.Addr{
		{netip.MustParseAddr("8.8.8.8")},
		{netip.MustParseAddr("127.0.0.1")},
	}}
	safeHTTP, origin := mastodonFixtureHTTP(t, server, resolver)
	logical = origin
	_, err := (&MastodonDiscovery{http: safeHTTP}).Discover(
		context.Background(),
		origin,
	)
	if !errors.Is(err, errUnsafeProviderURL) || metadataCalled {
		t.Fatalf("rebinding error = %v, metadata called = %v", err, metadataCalled)
	}
}

func TestMastodonDiscoversIndependentPKCECompatibilityVersions(t *testing.T) {
	t.Parallel()
	for _, fixture := range []struct {
		version       string
		wantPKCE      bool
		wantTokenURL  string
		metadataFound bool
	}{
		{"4.2.13", false, "/oauth/token", false},
		{"4.3.0", true, "/oauth/token", true},
	} {
		fixture := fixture
		t.Run(fixture.version, func(t *testing.T) {
			var logical string
			server := httptest.NewTLSServer(http.HandlerFunc(func(
				response http.ResponseWriter,
				request *http.Request,
			) {
				response.Header().Set("Content-Type", "application/json")
				if request.URL.Path == "/api/v2/instance" {
					_ = json.NewEncoder(response).Encode(map[string]any{
						"domain":  "example.com",
						"version": fixture.version,
					})
					return
				}
				if !fixture.metadataFound {
					http.NotFound(response, request)
					return
				}
				_ = json.NewEncoder(response).Encode(map[string]any{
					"issuer":                 logical + "/",
					"authorization_endpoint": logical + "/oauth/authorize",
					"token_endpoint":         logical + "/oauth/token",
				})
			}))
			defer server.Close()
			safeHTTP, origin := mastodonFixtureHTTP(
				t,
				server,
				&mastodonFixtureResolver{addresses: [][]netip.Addr{{
					netip.MustParseAddr("9.9.9.9"),
				}}},
			)
			logical = origin
			instance, err := (&MastodonDiscovery{http: safeHTTP}).Discover(
				context.Background(),
				origin,
			)
			if err != nil {
				t.Fatal(err)
			}
			if instance.SupportsPKCE != fixture.wantPKCE ||
				!strings.HasSuffix(instance.TokenURL, fixture.wantTokenURL) {
				t.Fatalf("instance = %#v", instance)
			}
		})
	}
}

func TestMastodonAndBlueskyStatusClassificationFixtures(t *testing.T) {
	t.Parallel()
	tests := []struct {
		status    int
		kind      ProviderFailureKind
		retryable bool
	}{
		{http.StatusUnauthorized, FailureAuthentication, false},
		{http.StatusForbidden, FailurePermissionMissing, false},
		{http.StatusNotFound, FailureResourceGone, false},
		{http.StatusTooManyRequests, FailureTemporary, true},
		{http.StatusInternalServerError, FailureTemporary, true},
		{http.StatusBadGateway, FailureTemporary, true},
	}
	for _, test := range tests {
		err := mastodonStatusFailure("fixture", test.status)
		var failure *ProviderFailure
		if !errors.As(err, &failure) ||
			failure.Kind != test.kind ||
			failure.Retryable != test.retryable {
			t.Fatalf("status %d failure = %#v", test.status, failure)
		}
	}
}
