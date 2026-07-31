package socialconnections

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"
)

func TestBlueskyRuntimeCallbackSurvivesAdapterRestartAndReplayRemainsOneTime(
	t *testing.T,
) {
	t.Parallel()
	cipher, err := NewAESGCMCipher(
		"runtime-key",
		[]byte("0123456789abcdef0123456789abcdef"),
	)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 31, 9, 0, 0, 0, time.UTC)
	adapterA, _, callbackState := newBlueskyRuntimeFixture(
		t,
		cipher,
		func() time.Time { return now },
	)
	repository := NewMemoryRepository()
	serviceA := newBlueskyRuntimeService(t, repository, cipher, adapterA, now)
	if _, err = serviceA.Begin(context.Background(), BeginRequest{
		WorkspaceID: "workspace-1",
		ActorID:     "owner-1",
		Provider:    ProviderBluesky,
		Discovery: DiscoveryInput{
			Kind:  DiscoveryPDSOrigin,
			Value: adapterA.client.plcOrigin,
		},
	}); err != nil {
		t.Fatal(err)
	}
	if callbackState == nil || *callbackState == "" {
		t.Fatal("callback state was not captured from PAR")
	}
	if _, ok := repository.attemptByState[digest(*callbackState)]; !ok {
		t.Fatalf("callback state %q was not persisted in the central attempt store", *callbackState)
	}
	restartedClient, err := NewBlueskyOAuthClient(BlueskyOAuthConfig{
		ClientID:           adapterA.client.clientID,
		RedirectURL:        adapterA.client.redirectURL,
		Cipher:             cipher,
		HTTP:               adapterA.client.http,
		PLCDirectoryOrigin: adapterA.client.plcOrigin,
		Now:                func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	adapterB := &blueskyRuntimeDynamicAdapter{client: restartedClient}
	serviceB := newBlueskyRuntimeService(t, repository, cipher, adapterB, now)
	selection, err := serviceB.Callback(context.Background(), CallbackRequest{
		State:  *callbackState,
		Code:   "fixture-code",
		Issuer: adapterA.client.plcOrigin,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(selection.Resources) != 1 ||
		selection.Resources[0].RemoteID != dynamicTestDID {
		t.Fatalf("selection = %#v", selection)
	}
	if _, err = serviceB.Callback(context.Background(), CallbackRequest{
		State:  *callbackState,
		Code:   "fixture-code",
		Issuer: adapterA.client.plcOrigin,
	}); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("replayed callback error = %v", err)
	}
}

func TestBlueskyRuntimeAttemptStateExpiresAcrossAdapterRestart(t *testing.T) {
	t.Parallel()
	cipher, err := NewAESGCMCipher(
		"runtime-key",
		[]byte("0123456789abcdef0123456789abcdef"),
	)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 31, 10, 0, 0, 0, time.UTC)
	adapterA, origin, _ := newBlueskyRuntimeFixture(
		t,
		cipher,
		func() time.Time { return now },
	)
	authorization, err := adapterA.BeginDynamic(
		context.Background(),
		DynamicBeginRequest{
			Discovery: DiscoveryInput{Kind: DiscoveryPDSOrigin, Value: origin},
			State:     "runtime-state",
			ExpiresAt: now.Add(30 * time.Second),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	expiredClient, err := NewBlueskyOAuthClient(BlueskyOAuthConfig{
		ClientID:           adapterA.client.clientID,
		RedirectURL:        adapterA.client.redirectURL,
		Cipher:             cipher,
		HTTP:               adapterA.client.http,
		PLCDirectoryOrigin: origin,
		Now:                func() time.Time { return now.Add(time.Minute) },
	})
	if err != nil {
		t.Fatal(err)
	}
	expiredAdapter := &blueskyRuntimeDynamicAdapter{client: expiredClient}
	if _, err = expiredAdapter.CompleteDynamic(
		context.Background(),
		DynamicCallbackRequest{
			Code:          "fixture-code",
			Issuer:        origin,
			ProviderState: authorization.ProviderState,
		},
	); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("expired attempt error = %v", err)
	}
}

func newBlueskyRuntimeFixture(
	t *testing.T,
	cipher CredentialCipher,
	now func() time.Time,
) (*blueskyRuntimeDynamicAdapter, string, *string) {
	t.Helper()
	var logical string
	var callbackState string
	server := httptest.NewTLSServer(http.HandlerFunc(func(
		response http.ResponseWriter,
		request *http.Request,
	) {
		response.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/.well-known/oauth-protected-resource":
			_ = json.NewEncoder(response).Encode(map[string]any{
				"authorization_servers": []string{logical},
			})
		case "/.well-known/oauth-authorization-server":
			_ = json.NewEncoder(response).Encode(blueskyMetadataFixture(logical))
		case "/par":
			_ = request.ParseForm()
			callbackState = request.Form.Get("state")
			response.Header().Set("DPoP-Nonce", "as-nonce-1")
			response.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(response).Encode(map[string]any{
				"request_uri": "urn:ietf:params:oauth:request_uri:fixture",
				"expires_in":  90,
			})
		case "/token":
			response.Header().Set("DPoP-Nonce", "as-nonce-2")
			_ = json.NewEncoder(response).Encode(map[string]any{
				"access_token":  "access-token",
				"refresh_token": "refresh-token",
				"token_type":    "DPoP",
				"scope":         strings.Join(blueskyScopes, " "),
				"sub":           dynamicTestDID,
				"expires_in":    300,
			})
		case "/" + dynamicTestDID:
			_ = json.NewEncoder(response).Encode(map[string]any{
				"id": dynamicTestDID,
				"service": []map[string]string{{
					"id":              "#atproto_pds",
					"type":            "AtprotoPersonalDataServer",
					"serviceEndpoint": logical,
				}},
			})
		case "/xrpc/app.bsky.actor.getProfile":
			response.Header().Set("DPoP-Nonce", "rs-nonce-1")
			_ = json.NewEncoder(response).Encode(map[string]any{
				"did":         dynamicTestDID,
				"handle":      "alice.example",
				"displayName": "Alice",
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
	client, err := NewBlueskyOAuthClient(BlueskyOAuthConfig{
		ClientID:           "https://client.example.test/oauth.json",
		RedirectURL:        "https://app.example.test/oauth/callback",
		Cipher:             cipher,
		HTTP:               safeHTTP,
		PLCDirectoryOrigin: origin,
		Now:                now,
	})
	if err != nil {
		t.Fatal(err)
	}
	return &blueskyRuntimeDynamicAdapter{client: client}, origin, &callbackState
}

func newBlueskyRuntimeService(
	t *testing.T,
	repository Repository,
	cipher CredentialCipher,
	adapter DynamicAdapter,
	now time.Time,
) *Service {
	t.Helper()
	service, err := NewService(Config{
		Repository: repository,
		Authorizer: &fakeAuthorizer{permissions: map[Permission]bool{
			PermissionViewWorkspace:  true,
			PermissionManageChannels: true,
		}},
		Cipher: cipher,
		Quota:  newFakeChannelQuota(),
		DynamicAdapters: map[Provider]DynamicAdapter{
			ProviderBluesky: adapter,
		},
		Availability: map[Provider]ProviderAvailability{
			ProviderBluesky: {
				Provider:           ProviderBluesky,
				Status:             ProviderAvailable,
				ConfigurationState: ProviderReady,
			},
		},
		Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	return service
}
