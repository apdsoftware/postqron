package socialconnections

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"
)

func TestDecentralizedRuntimeRequiresAuditSmokeVersionAndCentralContract(
	t *testing.T,
) {
	t.Parallel()
	tests := []struct {
		name  string
		input map[string]string
		want  ProviderConfigurationState
	}{
		{
			name:  "disabled",
			input: map[string]string{},
			want:  ProviderNotConfigured,
		},
		{
			name: "audit required",
			input: map[string]string{
				"social.mastodon.enabled":                "true",
				"social.mastodon.runtime_audit_verified": "false",
				mastodonRuntimeRedirectURLKey:            "https://app.example.test/api/v1/social-authorizations/callback",
			},
			want: ProviderAuditRequired,
		},
		{
			name: "smoke required",
			input: map[string]string{
				"social.mastodon.enabled":                "true",
				"social.mastodon.runtime_audit_verified": "true",
				mastodonRuntimeRedirectURLKey:            "https://app.example.test/api/v1/social-authorizations/callback",
			},
			want: ProviderAuditRequired,
		},
		{
			name: "version required",
			input: map[string]string{
				"social.mastodon.enabled":                "true",
				"social.mastodon.runtime_audit_verified": "true",
				"social.mastodon.runtime_smoke_verified": "true",
				mastodonRuntimeRedirectURLKey:            "https://app.example.test/api/v1/social-authorizations/callback",
			},
			want: ProviderAuditRequired,
		},
		{
			name: "central contract version enables activation",
			input: map[string]string{
				"social.mastodon.enabled":                "true",
				"social.mastodon.runtime_audit_verified": "true",
				"social.mastodon.runtime_smoke_verified": "true",
				"social.mastodon.compatibility_version":  RuntimeDynamicProviderCompatibilityVersion,
				mastodonRuntimeRedirectURLKey:            "https://app.example.test/api/v1/social-authorizations/callback",
			},
			want: ProviderReady,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			registry, err := configureRuntimeProviderFamilies(test.input, nil)
			if err != nil {
				t.Fatal(err)
			}
			if len(registry.staticAdapters) != 0 {
				t.Fatalf("static adapters = %#v, want none", registry.staticAdapters)
			}
			if test.want == ProviderReady &&
				registry.dynamicAdapters[ProviderMastodon] == nil {
				t.Fatal("Mastodon dynamic adapter was not registered")
			}
			if got := registry.availability[ProviderMastodon]; got.Status != runtimeStatusForState(test.want) ||
				got.ConfigurationState != test.want {
				t.Fatalf("Mastodon availability = %#v", got)
			}
			if got := registry.availability[ProviderBluesky]; got.Status != ProviderUnavailable {
				t.Fatalf("Bluesky availability = %#v", got)
			}
		})
	}
}

func runtimeStatusForState(
	state ProviderConfigurationState,
) ProviderAvailabilityStatus {
	if state == ProviderReady {
		return ProviderAvailable
	}
	return ProviderUnavailable
}

func TestMastodonRuntimeRegistersPerOriginAndRejectsCrossOriginReuse(
	t *testing.T,
) {
	t.Parallel()
	newFixture := func(
		t *testing.T,
		clientID, clientSecret string,
	) (*mastodonRuntimeDynamicAdapter, string) {
		t.Helper()
		var logical string
		server := httptest.NewTLSServer(http.HandlerFunc(func(
			response http.ResponseWriter,
			request *http.Request,
		) {
			response.Header().Set("Content-Type", "application/json")
			switch request.URL.Path {
			case "/api/v2/instance":
				_ = json.NewEncoder(response).Encode(map[string]any{
					"domain":       "example.com",
					"version":      "4.4.0",
					"api_versions": map[string]int{"mastodon": 4},
				})
			case "/.well-known/oauth-authorization-server":
				_ = json.NewEncoder(response).Encode(map[string]any{
					"issuer":                    logical + "/",
					"authorization_endpoint":    logical + "/oauth/authorize",
					"token_endpoint":            logical + "/oauth/token",
					"revocation_endpoint":       logical + "/oauth/revoke",
					"app_registration_endpoint": logical + "/api/v1/apps",
					"grant_types_supported":     []string{"authorization_code"},
				})
			case "/api/v1/apps":
				_ = request.ParseForm()
				if request.Form.Get("redirect_uris") !=
					"https://app.example.test/social/callback" {
					t.Errorf("redirect_uris = %q", request.Form.Get("redirect_uris"))
				}
				_ = json.NewEncoder(response).Encode(map[string]any{
					"client_id":     clientID,
					"client_secret": clientSecret,
				})
			default:
				http.NotFound(response, request)
			}
		}))
		t.Cleanup(server.Close)
		safeHTTP, origin := mastodonFixtureHTTP(
			t,
			server,
			&mastodonFixtureResolver{addresses: [][]netip.Addr{{
				netip.MustParseAddr("8.8.8.8"),
			}}},
		)
		logical = origin
		return &mastodonRuntimeDynamicAdapter{
			redirectURL: "https://app.example.test/social/callback",
			discovery:   &MastodonDiscovery{http: safeHTTP},
			http:        safeHTTP,
		}, origin
	}

	adapterA, originA := newFixture(t, "client-a", "secret-a")
	adapterB, originB := newFixture(t, "client-b", "secret-b")

	authorizationA, err := adapterA.BeginDynamic(context.Background(), DynamicBeginRequest{
		Discovery: DiscoveryInput{Kind: DiscoveryInstanceOrigin, Value: originA},
		State:     "state-a",
	})
	if err != nil {
		t.Fatal(err)
	}
	authorizationB, err := adapterB.BeginDynamic(context.Background(), DynamicBeginRequest{
		Discovery: DiscoveryInput{Kind: DiscoveryInstanceOrigin, Value: originB},
		State:     "state-b",
	})
	if err != nil {
		t.Fatal(err)
	}

	stateA, err := openMastodonRuntimeAttemptState(authorizationA.ProviderState)
	if err != nil {
		t.Fatal(err)
	}
	stateB, err := openMastodonRuntimeAttemptState(authorizationB.ProviderState)
	if err != nil {
		t.Fatal(err)
	}
	if stateA.App.ClientID != "client-a" ||
		stateB.App.ClientID != "client-b" ||
		stateA.App.ClientID == stateB.App.ClientID ||
		stateA.App.Origin != originA ||
		stateB.App.Origin != originB {
		t.Fatalf("runtime apps = %#v / %#v", stateA.App, stateB.App)
	}

	tampered := stateA
	tampered.App = stateB.App
	tamperedState, err := json.Marshal(tampered)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = adapterA.CompleteDynamic(context.Background(), DynamicCallbackRequest{
		Code:          "fixture-code",
		RedirectURL:   "https://app.example.test/social/callback",
		ProviderState: tamperedState,
	}); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("cross-origin app reuse error = %v", err)
	}
}
