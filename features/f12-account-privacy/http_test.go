package accountprivacy

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestHTTPHandlerRejectsCrossSiteMutationsAndMissingConfirmation(t *testing.T) {
	now := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	repository := NewMemoryRepository()
	repository.PutProfile(Profile{AccountID: "account-1", DisplayName: "Carlo"})
	adapters := defaultAdapters(now)
	service := newTestService(t, repository, adapters, func() time.Time { return now })
	handler, err := NewHTTPHandler(service, fixedAuthenticator{principal: recentPrincipal(now)})
	if err != nil {
		t.Fatalf("create handler: %v", err)
	}

	crossSite := httptest.NewRequest(
		http.MethodPatch,
		"/api/v1/account/profile",
		strings.NewReader(`{"display_name":"Carlo","locale":"it-IT","timezone":"Europe/Rome"}`),
	)
	crossSite.Header.Set("Sec-Fetch-Site", "cross-site")
	crossSiteResponse := httptest.NewRecorder()
	handler.ServeHTTP(crossSiteResponse, crossSite)
	if crossSiteResponse.Code != http.StatusBadRequest {
		t.Fatalf("expected cross-site rejection, got %d", crossSiteResponse.Code)
	}

	unconfirmed := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/account/exports",
		strings.NewReader(`{"scope":"account","confirmation":"no"}`),
	)
	unconfirmedResponse := httptest.NewRecorder()
	handler.ServeHTTP(unconfirmedResponse, unconfirmed)
	if unconfirmedResponse.Code != http.StatusBadRequest {
		t.Fatalf("expected explicit confirmation rejection, got %d", unconfirmedResponse.Code)
	}
}

func TestHTTPAccountAreaIsPrivateAndNotCacheable(t *testing.T) {
	now := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	repository := NewMemoryRepository()
	repository.PutProfile(Profile{
		AccountID:   "account-1",
		DisplayName: "Carlo",
		Locale:      "it-IT",
		Timezone:    "Europe/Rome",
	})
	adapters := defaultAdapters(now)
	service := newTestService(t, repository, adapters, func() time.Time { return now })
	handler, err := NewHTTPHandler(service, fixedAuthenticator{principal: recentPrincipal(now)})
	if err != nil {
		t.Fatalf("create handler: %v", err)
	}

	request := httptest.NewRequest(http.MethodGet, "/api/v1/account", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("expected account area, got %d: %s", response.Code, response.Body.String())
	}
	if response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("private response can be cached: %#v", response.Header())
	}
}

type fixedAuthenticator struct {
	principal Principal
	ok        bool
}

func (authenticator fixedAuthenticator) Principal(*http.Request) (Principal, bool) {
	if !authenticator.ok && authenticator.principal.AccountID == "" {
		return Principal{}, false
	}
	return authenticator.principal, true
}
