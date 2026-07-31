package socialconnections

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestBlueskyOAuthPARPKCEDPoPNonceRefreshPDSAndProfileFixtures(
	t *testing.T,
) {
	t.Parallel()
	var logical string
	var mu sync.Mutex
	var parCalls, tokenCalls, profileCalls int
	var callbackState string
	var proofs []string
	server := httptest.NewTLSServer(http.HandlerFunc(func(
		response http.ResponseWriter,
		request *http.Request,
	) {
		response.Header().Set("Content-Type", "application/json")
		if proof := request.Header.Get("DPoP"); proof != "" {
			mu.Lock()
			proofs = append(proofs, proof)
			mu.Unlock()
		}
		switch request.URL.Path {
		case "/.well-known/oauth-protected-resource":
			_ = json.NewEncoder(response).Encode(map[string]any{
				"authorization_servers": []string{logical},
			})
		case "/.well-known/oauth-authorization-server":
			_ = json.NewEncoder(response).Encode(blueskyMetadataFixture(logical))
		case "/par":
			_ = request.ParseForm()
			parCalls++
			callbackState = request.Form.Get("state")
			if request.Form.Get("code_challenge_method") != "S256" ||
				request.Form.Get("code_challenge") == "" ||
				request.Form.Get("scope") != strings.Join(blueskyScopes, " ") {
				t.Errorf("PAR form = %v", request.Form)
			}
			if parCalls == 1 {
				response.Header().Set("DPoP-Nonce", "as-nonce-1")
				response.WriteHeader(http.StatusBadRequest)
				_ = json.NewEncoder(response).Encode(map[string]string{
					"error": "use_dpop_nonce",
				})
				return
			}
			if !blueskyProofHasClaim(request.Header.Get("DPoP"), "nonce", "as-nonce-1") {
				t.Error("PAR retry omitted rotated nonce")
			}
			response.Header().Set("DPoP-Nonce", "as-nonce-2")
			response.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(response).Encode(map[string]any{
				"request_uri": "urn:ietf:params:oauth:request_uri:fixture",
				"expires_in":  90,
			})
		case "/token":
			_ = request.ParseForm()
			tokenCalls++
			if request.Form.Get("client_id") != "https://client.example.test/oauth.json" {
				t.Errorf("token form = %v", request.Form)
			}
			response.Header().Set("DPoP-Nonce", "as-nonce-"+string(rune('2'+tokenCalls)))
			token := map[string]any{
				"access_token":  "access-token",
				"refresh_token": "refresh-token",
				"token_type":    "DPoP",
				"scope":         strings.Join(blueskyScopes, " "),
				"sub":           "did:plc:alice",
				"expires_in":    300,
			}
			if request.Form.Get("grant_type") == "refresh_token" {
				if request.Form.Get("refresh_token") != "refresh-token" {
					t.Error("refresh did not use the current single-use token")
				}
				token["access_token"] = "access-token-rotated"
				token["refresh_token"] = "refresh-token-rotated"
			} else if request.Form.Get("code_verifier") == "" {
				t.Error("token exchange omitted PKCE verifier")
			}
			_ = json.NewEncoder(response).Encode(token)
		case "/did:plc:alice":
			_ = json.NewEncoder(response).Encode(map[string]any{
				"id":          "did:plc:alice",
				"alsoKnownAs": []string{"at://alice.example"},
				"service": []map[string]string{{
					"id":              "#atproto_pds",
					"type":            "AtprotoPersonalDataServer",
					"serviceEndpoint": logical,
				}},
			})
		case "/xrpc/app.bsky.actor.getProfile":
			profileCalls++
			if request.Header.Get("Authorization") != "DPoP access-token" {
				t.Errorf("profile authorization = %q", request.Header.Get("Authorization"))
			}
			if request.Header.Get("Atproto-Proxy") !=
				"did:web:api.bsky.app#bsky_appview" {
				t.Errorf("profile proxy = %q", request.Header.Get("Atproto-Proxy"))
			}
			if profileCalls == 1 {
				response.Header().Set("DPoP-Nonce", "rs-nonce-1")
				response.Header().Set(
					"WWW-Authenticate",
					`DPoP error="use_dpop_nonce"`,
				)
				response.WriteHeader(http.StatusUnauthorized)
				return
			}
			if !blueskyProofHasClaim(request.Header.Get("DPoP"), "nonce", "rs-nonce-1") ||
				!blueskyProofHasNonEmptyClaim(request.Header.Get("DPoP"), "ath") {
				t.Error("resource DPoP proof omitted nonce or ath")
			}
			response.Header().Set("DPoP-Nonce", "rs-nonce-2")
			_ = json.NewEncoder(response).Encode(map[string]any{
				"did":         "did:plc:alice",
				"handle":      "alice.example",
				"displayName": "Alice",
				"avatar":      logical + "/avatar.png",
			})
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
	cipher, err := NewAESGCMCipher(
		"fixture-key",
		[]byte("0123456789abcdef0123456789abcdef"),
	)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 30, 18, 0, 0, 0, time.UTC)
	client, err := NewBlueskyOAuthClient(BlueskyOAuthConfig{
		ClientID:           "https://client.example.test/oauth.json",
		RedirectURL:        "https://app.example.test/oauth/callback",
		Cipher:             cipher,
		HTTP:               safeHTTP,
		PLCDirectoryOrigin: origin,
		Now:                func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	authorization, err := client.Begin(
		context.Background(),
		origin,
		"alice.example",
	)
	if err != nil {
		t.Fatal(err)
	}
	parsed, _ := url.Parse(authorization.URL)
	if parsed.Query().Get("request_uri") == "" ||
		strings.Contains(authorization.URL, callbackState) {
		t.Fatalf("authorization URL = %s", authorization.URL)
	}
	if authorization.State != callbackState {
		t.Fatalf("authorization state = %q, callback state = %q", authorization.State, callbackState)
	}
	if bytes.Contains(authorization.ProviderState, []byte(callbackState)) ||
		bytes.Contains(authorization.ProviderState, []byte(`"dpop_key"`)) {
		t.Fatal("state or DPoP key stored in plaintext")
	}
	restartedClient, err := NewBlueskyOAuthClient(BlueskyOAuthConfig{
		ClientID:           "https://client.example.test/oauth.json",
		RedirectURL:        "https://app.example.test/oauth/callback",
		Cipher:             cipher,
		HTTP:               safeHTTP,
		PLCDirectoryOrigin: origin,
		Now:                func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	session, err := restartedClient.Callback(
		context.Background(),
		authorization.ProviderState,
		callbackState,
		"fixture-code",
		origin,
	)
	if err != nil {
		t.Fatal(err)
	}
	if session.SubjectDID != "did:plc:alice" ||
		session.Credential.AccessToken != "access-token" {
		t.Fatalf("session = %#v", session)
	}
	sealedSession, err := client.SealSession("session-1", session)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(sealedSession.Data, []byte("access-token")) ||
		bytes.Contains(sealedSession.Data, []byte(session.DPoPKey.D)) {
		t.Fatal("token or DPoP private key stored in plaintext")
	}
	openedSession, err := client.OpenSession("session-1", sealedSession)
	if err != nil ||
		openedSession.Credential.AccessToken != session.Credential.AccessToken ||
		openedSession.DPoPKey.D != session.DPoPKey.D {
		t.Fatalf("opened session = %#v, error = %v", openedSession, err)
	}
	if _, err = restartedClient.Callback(
		context.Background(),
		authorization.ProviderState,
		callbackState+"-wrong",
		"fixture-code",
		origin,
	); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("state mismatch error = %v", err)
	}
	resource, err := client.DiscoverProfile(context.Background(), &session)
	if err != nil {
		t.Fatal(err)
	}
	if resource.Candidate.RemoteID != "did:plc:alice" ||
		resource.Candidate.Handle != "@alice.example" ||
		session.RSNonce != "rs-nonce-2" {
		t.Fatalf("resource/session = %#v / %#v", resource, session)
	}
	refreshed, err := client.Refresh(context.Background(), session)
	if err != nil {
		t.Fatal(err)
	}
	if refreshed.Credential.AccessToken != "access-token-rotated" ||
		refreshed.Credential.RefreshToken != "refresh-token-rotated" {
		t.Fatalf("refresh = %#v", refreshed)
	}
	if !errors.Is(client.Revoke(refreshed), errBlueskyRemoteRevocationUnsupported) {
		t.Fatal("Bluesky remote revocation did not fail closed")
	}
	mu.Lock()
	defer mu.Unlock()
	seen := map[string]bool{}
	for _, proof := range proofs {
		if err := blueskyValidateDPoPProof(proof); err != nil {
			t.Fatal(err)
		}
		if seen[proof] {
			t.Fatal("DPoP proof was reused")
		}
		seen[proof] = true
	}
}

func TestBlueskyMetadataRejectsRedirectMalformedPrivateAndMissingRequirements(
	t *testing.T,
) {
	t.Parallel()
	var logical string
	server := httptest.NewTLSServer(http.HandlerFunc(func(
		response http.ResponseWriter,
		request *http.Request,
	) {
		switch request.URL.Path {
		case "/.well-known/oauth-protected-resource":
			response.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(response).Encode(map[string]any{
				"authorization_servers": []string{logical},
			})
		case "/.well-known/oauth-authorization-server":
			response.Header().Set("Content-Type", "application/json")
			document := blueskyMetadataFixture(logical)
			document["require_pushed_authorization_requests"] = false
			_ = json.NewEncoder(response).Encode(document)
		}
	}))
	defer server.Close()
	resolver := &mastodonFixtureResolver{addresses: [][]netip.Addr{{
		netip.MustParseAddr("1.1.1.1"),
	}}}
	safeHTTP, origin := mastodonFixtureHTTP(t, server, resolver)
	logical = origin
	cipher, _ := NewAESGCMCipher(
		"fixture-key",
		[]byte("0123456789abcdef0123456789abcdef"),
	)
	client, err := NewBlueskyOAuthClient(BlueskyOAuthConfig{
		ClientID:           "https://client.example.test/oauth.json",
		RedirectURL:        "https://app.example.test/oauth/callback",
		Cipher:             cipher,
		HTTP:               safeHTTP,
		PLCDirectoryOrigin: origin,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = client.DiscoverAuthorizationServer(
		context.Background(),
		origin,
	); err == nil {
		t.Fatal("incompatible authorization metadata accepted")
	}
}

func TestBlueskyTokenFixturesRejectMalformedAndMissingScopes(t *testing.T) {
	t.Parallel()
	if _, _, err := blueskyToken([]byte(`{"access_token":"secret"}`)); err == nil {
		t.Fatal("malformed token accepted")
	}
	body, _ := json.Marshal(map[string]any{
		"access_token":  "access",
		"refresh_token": "refresh",
		"token_type":    "DPoP",
		"scope":         "atproto",
		"sub":           "did:plc:alice",
	})
	var failure *ProviderFailure
	_, _, err := blueskyToken(body)
	if !errors.As(err, &failure) ||
		failure.Kind != FailurePermissionMissing {
		t.Fatalf("missing scope error = %v", err)
	}
}

func TestBlueskyDiscoversIndependentPDSOrigins(t *testing.T) {
	t.Parallel()
	for index := 0; index < 2; index++ {
		var logical string
		server := httptest.NewTLSServer(http.HandlerFunc(func(
			response http.ResponseWriter,
			request *http.Request,
		) {
			response.Header().Set("Content-Type", "application/json")
			if request.URL.Path == "/.well-known/oauth-protected-resource" {
				_ = json.NewEncoder(response).Encode(map[string]any{
					"authorization_servers": []string{logical},
				})
				return
			}
			_ = json.NewEncoder(response).Encode(blueskyMetadataFixture(logical))
		}))
		safeHTTP, origin := mastodonFixtureHTTP(
			t,
			server,
			&mastodonFixtureResolver{addresses: [][]netip.Addr{{
				netip.MustParseAddr("1.0.0.1"),
			}}},
		)
		logical = origin
		cipher, _ := NewAESGCMCipher(
			"fixture-key",
			[]byte("0123456789abcdef0123456789abcdef"),
		)
		client, err := NewBlueskyOAuthClient(BlueskyOAuthConfig{
			ClientID:           "https://client.example.test/oauth.json",
			RedirectURL:        "https://app.example.test/oauth/callback",
			Cipher:             cipher,
			HTTP:               safeHTTP,
			PLCDirectoryOrigin: origin,
		})
		if err != nil {
			server.Close()
			t.Fatal(err)
		}
		discovered, err := client.DiscoverAuthorizationServer(
			context.Background(),
			origin,
		)
		server.Close()
		if err != nil {
			t.Fatal(err)
		}
		if discovered.PDSOrigin != origin || discovered.Issuer != origin {
			t.Fatalf("discovered = %#v", discovered)
		}
	}
}

func blueskyMetadataFixture(origin string) map[string]any {
	return map[string]any{
		"issuer":                                           origin,
		"authorization_endpoint":                           origin + "/authorize",
		"token_endpoint":                                   origin + "/token",
		"pushed_authorization_request_endpoint":            origin + "/par",
		"response_types_supported":                         []string{"code"},
		"grant_types_supported":                            []string{"authorization_code", "refresh_token"},
		"code_challenge_methods_supported":                 []string{"S256"},
		"token_endpoint_auth_methods_supported":            []string{"none", "private_key_jwt"},
		"token_endpoint_auth_signing_alg_values_supported": []string{"ES256"},
		"scopes_supported":                                 append([]string(nil), blueskyScopes...),
		"dpop_signing_alg_values_supported":                []string{"ES256"},
		"authorization_response_iss_parameter_supported":   true,
		"require_pushed_authorization_requests":            true,
		"client_id_metadata_document_supported":            true,
	}
}

func blueskyProofHasClaim(proof, name, value string) bool {
	parts := strings.Split(proof, ".")
	if len(parts) != 3 {
		return false
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return false
	}
	var claims map[string]any
	return json.Unmarshal(payload, &claims) == nil && claims[name] == value
}

func blueskyProofHasNonEmptyClaim(proof, name string) bool {
	parts := strings.Split(proof, ".")
	if len(parts) != 3 {
		return false
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return false
	}
	var claims map[string]any
	if json.Unmarshal(payload, &claims) != nil {
		return false
	}
	value, ok := claims[name].(string)
	return ok && value != ""
}
