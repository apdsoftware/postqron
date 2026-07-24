package socialconnections

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

func TestFacebookPagesAdapterUsesOfficialScopesAndFiltersPublishablePages(
	t *testing.T,
) {
	var mu sync.Mutex
	var tokenCalls int
	server := httptest.NewServer(http.HandlerFunc(func(
		response http.ResponseWriter,
		request *http.Request,
	) {
		switch request.URL.Path {
		case "/v25.0/oauth/access_token":
			mu.Lock()
			tokenCalls++
			call := tokenCalls
			mu.Unlock()
			if request.URL.Query().Get("client_secret") != "client-secret" {
				t.Error("token exchange omitted client secret")
			}
			if call == 1 {
				if request.URL.Query().Get("code") != "oauth-code" ||
					request.URL.Query().Get("code_verifier") != "verifier" {
					t.Errorf("short token query = %v", request.URL.Query())
				}
				writeJSON(t, response, map[string]any{
					"access_token": "short-user-token",
					"expires_in":   3600,
				})
				return
			}
			if request.URL.Query().Get("grant_type") != "fb_exchange_token" ||
				request.URL.Query().Get("fb_exchange_token") != "short-user-token" {
				t.Errorf("long token query = %v", request.URL.Query())
			}
			writeJSON(t, response, map[string]any{
				"access_token": "long-user-token",
				"expires_in":   5_000_000,
			})
		case "/v25.0/me/accounts":
			if request.Header.Get("Authorization") != "Bearer long-user-token" {
				t.Errorf("authorization header = %q", request.Header.Get("Authorization"))
			}
			if request.URL.Query().Get("fields") !=
				"id,name,picture,tasks,access_token" {
				t.Errorf("fields = %q", request.URL.Query().Get("fields"))
			}
			writeJSON(t, response, map[string]any{"data": []map[string]any{
				{
					"id":           "page-1",
					"name":         "Publishable",
					"access_token": "page-token",
					"tasks":        []string{"ANALYZE", "CREATE_CONTENT"},
					"picture": map[string]any{
						"data": map[string]any{"url": "https://cdn.example.test/page.jpg"},
					},
				},
				{
					"id":           "page-2",
					"name":         "Read only",
					"access_token": "page-token-2",
					"tasks":        []string{"ANALYZE"},
				},
			}})
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()

	adapter, err := NewMetaAdapter(MetaAdapterConfig{
		Provider:         ProviderFacebookPages,
		ClientID:         "client-id",
		ClientSecret:     "client-secret",
		RedirectURL:      "https://app.example.test/callback",
		GraphVersion:     "v25.0",
		SupportsPKCE:     true,
		HTTPClient:       server.Client(),
		FacebookGraphURL: server.URL,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := adapter.Config().Scopes; !slicesEqual(
		got,
		[]string{
			"pages_show_list",
			"pages_read_engagement",
			"pages_manage_posts",
		},
	) {
		t.Fatalf("Facebook scopes = %v", got)
	}
	grant, err := adapter.Exchange(context.Background(), ExchangeRequest{
		Code:         "oauth-code",
		RedirectURL:  "https://app.example.test/callback",
		PKCEVerifier: "verifier",
	})
	if err != nil {
		t.Fatal(err)
	}
	resources, err := adapter.Discover(context.Background(), grant)
	if err != nil {
		t.Fatal(err)
	}
	if len(resources) != 1 ||
		resources[0].Candidate.RemoteID != "page-1" ||
		resources[0].Credential.AccessToken != "page-token" {
		t.Fatalf("resources = %#v", resources)
	}
}

func TestInstagramProfessionalAdapterExchangeDiscoveryRefreshAndRevoke(
	t *testing.T,
) {
	server := httptest.NewServer(http.HandlerFunc(func(
		response http.ResponseWriter,
		request *http.Request,
	) {
		switch request.URL.Path {
		case "/oauth/access_token":
			if request.Method != http.MethodPost {
				t.Errorf("short exchange method = %s", request.Method)
			}
			_ = request.ParseForm()
			if request.Form.Get("grant_type") != "authorization_code" ||
				request.Form.Get("code") != "oauth-code" {
				t.Errorf("short exchange form = %v", request.Form)
			}
			writeJSON(t, response, map[string]any{
				"access_token": "short-instagram-token",
				"user_id":      42,
			})
		case "/access_token":
			if request.URL.Query().Get("grant_type") != "ig_exchange_token" {
				t.Errorf("long exchange query = %v", request.URL.Query())
			}
			writeJSON(t, response, map[string]any{
				"access_token": "long-instagram-token",
				"expires_in":   5_000_000,
			})
		case "/v25.0/me":
			if request.Header.Get("Authorization") !=
				"Bearer long-instagram-token" {
				t.Errorf("discovery bearer = %q", request.Header.Get("Authorization"))
			}
			writeJSON(t, response, map[string]any{
				"user_id":             "ig-42",
				"username":            "postqron",
				"name":                "Postqron",
				"account_type":        "CREATOR",
				"profile_picture_url": "https://cdn.example.test/ig.jpg",
			})
		case "/refresh_access_token":
			if request.URL.Query().Get("grant_type") != "ig_refresh_token" {
				t.Errorf("refresh query = %v", request.URL.Query())
			}
			writeJSON(t, response, map[string]any{
				"access_token": "refreshed-instagram-token",
				"expires_in":   5_000_000,
			})
		case "/v25.0/ig-42/permissions":
			if request.Method != http.MethodDelete ||
				request.Header.Get("Authorization") !=
					"Bearer refreshed-instagram-token" {
				t.Errorf(
					"revoke request = %s %q",
					request.Method,
					request.Header.Get("Authorization"),
				)
			}
			writeJSON(t, response, map[string]any{"success": true})
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()

	adapter, err := NewMetaAdapter(MetaAdapterConfig{
		Provider:          ProviderInstagramProfessional,
		ClientID:          "client-id",
		ClientSecret:      "client-secret",
		RedirectURL:       "https://app.example.test/callback",
		GraphVersion:      "v25.0",
		HTTPClient:        server.Client(),
		InstagramGraphURL: server.URL,
		InstagramTokenURL: server.URL,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := adapter.Config().Scopes; !slicesEqual(
		got,
		[]string{
			"instagram_business_basic",
			"instagram_business_content_publish",
		},
	) {
		t.Fatalf("Instagram scopes = %v", got)
	}
	grant, err := adapter.Exchange(context.Background(), ExchangeRequest{
		Code:        "oauth-code",
		RedirectURL: "https://app.example.test/callback",
	})
	if err != nil {
		t.Fatal(err)
	}
	resources, err := adapter.Discover(context.Background(), grant)
	if err != nil {
		t.Fatal(err)
	}
	if len(resources) != 1 ||
		resources[0].Candidate.AccountType != AccountTypeCreator ||
		resources[0].Candidate.RemoteID != "ig-42" {
		t.Fatalf("resources = %#v", resources)
	}
	refreshed, err := adapter.Refresh(context.Background(), resources[0].Credential)
	if err != nil {
		t.Fatal(err)
	}
	if refreshed.AccessToken != "refreshed-instagram-token" {
		t.Fatalf("refreshed credential = %#v", refreshed)
	}
	if err = adapter.Revoke(context.Background(), "ig-42", refreshed); err != nil {
		t.Fatal(err)
	}
}

func TestMetaAuthenticationErrorsAreClassifiedForReconnection(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(
		response http.ResponseWriter,
		_ *http.Request,
	) {
		response.WriteHeader(http.StatusBadRequest)
		writeJSON(t, response, map[string]any{
			"error": map[string]any{"code": 190, "error_subcode": 463},
		})
	}))
	defer server.Close()
	adapter, err := NewMetaAdapter(MetaAdapterConfig{
		Provider:         ProviderFacebookPages,
		ClientID:         "client-id",
		ClientSecret:     "client-secret",
		RedirectURL:      "https://app.example.test/callback",
		GraphVersion:     "v25.0",
		HTTPClient:       server.Client(),
		FacebookGraphURL: server.URL,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = adapter.Exchange(context.Background(), ExchangeRequest{
		Code:        "expired-code",
		RedirectURL: "https://app.example.test/callback",
	})
	var failure *ProviderFailure
	if !errors.As(err, &failure) ||
		failure.Kind != FailureAuthentication ||
		failure.Retryable {
		t.Fatalf("provider failure = %#v, %v", failure, err)
	}
}

func writeJSON(t *testing.T, response http.ResponseWriter, value any) {
	t.Helper()
	response.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(response).Encode(value); err != nil {
		t.Error(err)
	}
}

func slicesEqual(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
