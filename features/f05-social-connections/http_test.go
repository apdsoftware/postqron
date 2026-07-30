package socialconnections

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

type fixedRequestAuthenticator struct {
	accountID string
}

func (authenticator fixedRequestAuthenticator) AccountID(
	*http.Request,
) (string, bool) {
	return authenticator.accountID, authenticator.accountID != ""
}

func TestHTTPRuntimeExposesClientSafeConnectionLifecycle(t *testing.T) {
	fixture := newServiceFixture(t)
	handler, err := NewHTTPHandler(
		fixture.service,
		fixedRequestAuthenticator{accountID: "owner-1"},
	)
	if err != nil {
		t.Fatal(err)
	}

	bootstrap := performSocialRequest(
		t,
		handler,
		http.MethodGet,
		"/api/v1/workspaces/workspace-1/social-connections/bootstrap",
		"",
	)
	if bootstrap.Code != http.StatusOK ||
		!strings.Contains(bootstrap.Body.String(), `"status":"available"`) {
		t.Fatalf("bootstrap = %d %s", bootstrap.Code, bootstrap.Body.String())
	}

	begin := performSocialRequest(
		t,
		handler,
		http.MethodPost,
		"/api/v1/workspaces/workspace-1/social-authorizations",
		`{"provider":"facebook_pages"}`,
	)
	if begin.Code != http.StatusCreated {
		t.Fatalf("begin = %d %s", begin.Code, begin.Body.String())
	}
	var authorization Authorization
	if err = json.Unmarshal(begin.Body.Bytes(), &authorization); err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(authorization.URL)
	if err != nil {
		t.Fatal(err)
	}
	state := parsed.Query().Get("state")

	callback := performSocialRequest(
		t,
		handler,
		http.MethodGet,
		"/api/v1/social-authorizations/callback?state="+
			url.QueryEscape(state)+"&code=fixture-code",
		"",
	)
	if callback.Code != http.StatusOK {
		t.Fatalf("callback = %d %s", callback.Code, callback.Body.String())
	}
	var selection Selection
	if err = json.Unmarshal(callback.Body.Bytes(), &selection); err != nil {
		t.Fatal(err)
	}
	if len(selection.Resources) != 2 {
		t.Fatalf("selection = %#v", selection)
	}
	if strings.Contains(callback.Body.String(), "page-token") ||
		strings.Contains(callback.Body.String(), "facebook-user-token") {
		t.Fatal("callback exposed token material")
	}

	selectResponse := performSocialRequest(
		t,
		handler,
		http.MethodPost,
		"/api/v1/workspaces/workspace-1/social-connections",
		`{"selection_id":"`+selection.ID+`","remote_id":"page-1"}`,
	)
	if selectResponse.Code != http.StatusCreated ||
		strings.Contains(selectResponse.Body.String(), "page-token") {
		t.Fatalf(
			"select = %d %s",
			selectResponse.Code,
			selectResponse.Body.String(),
		)
	}
	var connection Connection
	if err = json.Unmarshal(selectResponse.Body.Bytes(), &connection); err != nil {
		t.Fatal(err)
	}

	list := performSocialRequest(
		t,
		handler,
		http.MethodGet,
		"/api/v1/workspaces/workspace-1/social-connections",
		"",
	)
	if list.Code != http.StatusOK ||
		!strings.Contains(list.Body.String(), `"remote_id":"page-1"`) ||
		strings.Contains(list.Body.String(), "ConnectedByActorID") {
		t.Fatalf("list = %d %s", list.Code, list.Body.String())
	}

	revoke := performSocialRequest(
		t,
		handler,
		http.MethodDelete,
		"/api/v1/workspaces/workspace-1/social-connections/"+connection.ID,
		"",
	)
	if revoke.Code != http.StatusOK ||
		!strings.Contains(revoke.Body.String(), `"status":"revoked"`) {
		t.Fatalf("revoke = %d %s", revoke.Code, revoke.Body.String())
	}

	reconnect := performSocialRequest(
		t,
		handler,
		http.MethodPost,
		"/api/v1/workspaces/workspace-1/social-connections/"+
			connection.ID+"/reconnect",
		"",
	)
	if reconnect.Code != http.StatusCreated ||
		!strings.Contains(reconnect.Body.String(), `"authorization_url"`) {
		t.Fatalf("reconnect = %d %s", reconnect.Code, reconnect.Body.String())
	}
}

func TestHTTPRuntimeFailsClosedForUnavailableProviderAndQuota(t *testing.T) {
	repository := NewMemoryRepository()
	authorizer := &fakeAuthorizer{permissions: map[Permission]bool{
		PermissionViewWorkspace:  true,
		PermissionManageChannels: true,
	}}
	service, err := NewService(Config{
		Repository: repository,
		Authorizer: authorizer,
		Quota:      newFakeChannelQuota(),
		Availability: map[Provider]ProviderAvailability{
			ProviderFacebookPages: {
				Status:    ProviderUnavailable,
				Retryable: true,
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	handler, err := NewHTTPHandler(
		service,
		fixedRequestAuthenticator{accountID: "owner-1"},
	)
	if err != nil {
		t.Fatal(err)
	}
	begin := performSocialRequest(
		t,
		handler,
		http.MethodPost,
		"/api/v1/workspaces/workspace-1/social-authorizations",
		`{"provider":"facebook_pages"}`,
	)
	if begin.Code != http.StatusServiceUnavailable ||
		!strings.Contains(begin.Body.String(), `"code":"provider_unavailable"`) ||
		!strings.Contains(begin.Body.String(), `"retryable":true`) {
		t.Fatalf("unavailable begin = %d %s", begin.Code, begin.Body.String())
	}

	fixture := newServiceFixture(t)
	fixture.quota.reserveDecision = ChannelQuotaDecision{
		Accepted: false,
		Code:     "quota_exceeded",
	}
	selection := authorizeAndDiscover(
		t,
		fixture.service,
		ProviderFacebookPages,
	)
	handler, err = NewHTTPHandler(
		fixture.service,
		fixedRequestAuthenticator{accountID: "owner-1"},
	)
	if err != nil {
		t.Fatal(err)
	}
	response := performSocialRequest(
		t,
		handler,
		http.MethodPost,
		"/api/v1/workspaces/workspace-1/social-connections",
		`{"selection_id":"`+selection.ID+`","remote_id":"page-1"}`,
	)
	if response.Code != http.StatusConflict ||
		!strings.Contains(response.Body.String(), `"code":"channel_quota_exceeded"`) {
		t.Fatalf("quota response = %d %s", response.Code, response.Body.String())
	}
}

func TestHTTPRuntimeRequiresAuthenticationAndRejectsCrossSiteMutation(
	t *testing.T,
) {
	fixture := newServiceFixture(t)
	handler, err := NewHTTPHandler(
		fixture.service,
		fixedRequestAuthenticator{},
	)
	if err != nil {
		t.Fatal(err)
	}
	response := performSocialRequest(
		t,
		handler,
		http.MethodGet,
		"/api/v1/workspaces/workspace-1/social-connections",
		"",
	)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated list status = %d", response.Code)
	}

	handler, err = NewHTTPHandler(
		fixture.service,
		fixedRequestAuthenticator{accountID: "owner-1"},
	)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/workspaces/workspace-1/social-authorizations",
		bytes.NewBufferString(`{"provider":"facebook_pages"}`),
	)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Sec-Fetch-Site", "cross-site")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("cross-site mutation status = %d", response.Code)
	}
	if len(fixture.repository.attempts) != 0 {
		t.Fatal("cross-site request created an OAuth attempt")
	}
}

func performSocialRequest(
	t *testing.T,
	handler http.Handler,
	method, target, body string,
) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(
		method,
		target,
		bytes.NewBufferString(body),
	).WithContext(context.Background())
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}
