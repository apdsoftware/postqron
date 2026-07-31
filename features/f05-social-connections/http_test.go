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

const testSocialOrigin = "https://postqron.com"

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
		testSocialOrigin,
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
				Retryable: false,
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	handler, err := NewHTTPHandler(
		service,
		fixedRequestAuthenticator{accountID: "owner-1"},
		testSocialOrigin,
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
		!strings.Contains(begin.Body.String(), `"code":"provider_not_configured"`) ||
		!strings.Contains(begin.Body.String(), `"retryable":false`) {
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
		testSocialOrigin,
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

func TestHTTPRuntimeDistinguishesProviderConfigurationGates(t *testing.T) {
	tests := []struct {
		name  string
		state ProviderConfigurationState
		code  string
	}{
		{
			name:  "not configured",
			state: ProviderNotConfigured,
			code:  "provider_not_configured",
		},
		{
			name:  "review required",
			state: ProviderReviewRequired,
			code:  "provider_review_required",
		},
		{
			name:  "audit required",
			state: ProviderAuditRequired,
			code:  "provider_audit_required",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service, err := NewService(Config{
				Repository: NewMemoryRepository(),
				Authorizer: &fakeAuthorizer{permissions: map[Permission]bool{
					PermissionViewWorkspace:  true,
					PermissionManageChannels: true,
				}},
				Quota: newFakeChannelQuota(),
				Availability: map[Provider]ProviderAvailability{
					ProviderX: {
						Status:             ProviderUnavailable,
						ConfigurationState: test.state,
					},
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			handler, err := NewHTTPHandler(
				service,
				fixedRequestAuthenticator{accountID: "owner-1"},
				testSocialOrigin,
			)
			if err != nil {
				t.Fatal(err)
			}
			response := performSocialRequest(
				t,
				handler,
				http.MethodPost,
				"/api/v1/workspaces/workspace-1/social-authorizations",
				`{"provider":"x"}`,
			)
			if response.Code != http.StatusServiceUnavailable ||
				!strings.Contains(
					response.Body.String(),
					`"code":"`+test.code+`"`,
				) {
				t.Fatalf(
					"configuration gate = %d %s",
					response.Code,
					response.Body.String(),
				)
			}
		})
	}
}

func TestHTTPRuntimeDistinguishesDeniedAndTemporaryProviderFailures(t *testing.T) {
	tests := []struct {
		name          string
		err           error
		status        int
		code          string
		retryable     bool
		privateDetail string
	}{
		{
			name:      "workspace access denied",
			err:       ErrUnauthorized,
			status:    http.StatusForbidden,
			code:      "forbidden",
			retryable: false,
		},
		{
			name: "Meta access denied",
			err: &ProviderFailure{
				Kind: FailurePermissionMissing,
				Code: "meta_error_200",
			},
			status:        http.StatusUnprocessableEntity,
			code:          "provider_access_denied",
			retryable:     false,
			privateDetail: "meta_error_200",
		},
		{
			name: "temporary Meta failure",
			err: &ProviderFailure{
				Kind:      FailureTemporary,
				Code:      "meta_error_2",
				Retryable: true,
			},
			status:        http.StatusBadGateway,
			code:          "provider_temporary",
			retryable:     true,
			privateDetail: "meta_error_2",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response := httptest.NewRecorder()
			writeSocialServiceError(response, test.err)
			if response.Code != test.status {
				t.Fatalf("status = %d, want %d", response.Code, test.status)
			}
			var payload struct {
				Code      string `json:"code"`
				Retryable bool   `json:"retryable"`
			}
			if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
				t.Fatal(err)
			}
			if payload.Code != test.code || payload.Retryable != test.retryable {
				t.Fatalf("payload = %#v", payload)
			}
			if test.privateDetail != "" &&
				strings.Contains(response.Body.String(), test.privateDetail) {
				t.Fatalf("response exposed private provider detail %q", test.privateDetail)
			}
		})
	}
}

func TestHTTPRuntimeRequiresAuthenticationAndRejectsHostileOrigin(
	t *testing.T,
) {
	fixture := newServiceFixture(t)
	handler, err := NewHTTPHandler(
		fixture.service,
		fixedRequestAuthenticator{},
		testSocialOrigin,
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
		testSocialOrigin,
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
	request.Header.Set("Origin", "https://postqron.com.evil.example")
	request.Header.Set("Sec-Fetch-Site", "cross-site")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("hostile-origin mutation status = %d", response.Code)
	}
	if len(fixture.repository.attempts) != 0 {
		t.Fatal("hostile-origin request created an OAuth attempt")
	}
}

func TestHTTPRuntimeAppliesCredentialedCORSAndRejectsHostileReads(
	t *testing.T,
) {
	fixture := newServiceFixture(t)
	handler, err := NewHTTPHandler(
		fixture.service,
		fixedRequestAuthenticator{accountID: "owner-1"},
		testSocialOrigin,
	)
	if err != nil {
		t.Fatal(err)
	}

	allowed := httptest.NewRequest(
		http.MethodGet,
		"/api/v1/workspaces/workspace-1/social-connections/bootstrap",
		nil,
	)
	allowed.Header.Set("Origin", testSocialOrigin)
	allowedResponse := httptest.NewRecorder()
	handler.ServeHTTP(allowedResponse, allowed)
	if allowedResponse.Code != http.StatusOK {
		t.Fatalf(
			"allowed read = %d %s",
			allowedResponse.Code,
			allowedResponse.Body.String(),
		)
	}
	assertCredentialedSocialCORS(t, allowedResponse.Header(), testSocialOrigin)

	callback := httptest.NewRequest(
		http.MethodGet,
		"/api/v1/social-authorizations/callback",
		nil,
	)
	callback.Header.Set("Origin", testSocialOrigin)
	callbackResponse := httptest.NewRecorder()
	handler.ServeHTTP(callbackResponse, callback)
	if callbackResponse.Code != http.StatusConflict {
		t.Fatalf(
			"callback error = %d %s",
			callbackResponse.Code,
			callbackResponse.Body.String(),
		)
	}
	assertCredentialedSocialCORS(t, callbackResponse.Header(), testSocialOrigin)

	hostile := httptest.NewRequest(
		http.MethodGet,
		"/api/v1/workspaces/workspace-1/social-connections/bootstrap",
		nil,
	)
	hostile.Header.Set("Origin", "https://postqron.com.evil.example")
	hostileResponse := httptest.NewRecorder()
	handler.ServeHTTP(hostileResponse, hostile)
	if hostileResponse.Code != http.StatusForbidden ||
		!strings.Contains(hostileResponse.Body.String(), `"code":"origin_forbidden"`) {
		t.Fatalf(
			"hostile read = %d %s",
			hostileResponse.Code,
			hostileResponse.Body.String(),
		)
	}
	if hostileResponse.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatal("hostile origin received an Access-Control-Allow-Origin header")
	}
}

func TestHTTPRuntimeServesCredentialedPreflight(t *testing.T) {
	fixture := newServiceFixture(t)
	handler, err := NewHTTPHandler(
		fixture.service,
		fixedRequestAuthenticator{accountID: "owner-1"},
		testSocialOrigin,
	)
	if err != nil {
		t.Fatal(err)
	}

	request := httptest.NewRequest(
		http.MethodOptions,
		"/api/v1/workspaces/workspace-1/social-authorizations",
		nil,
	)
	request.Header.Set("Origin", testSocialOrigin)
	request.Header.Set("Access-Control-Request-Method", http.MethodPost)
	request.Header.Set("Access-Control-Request-Headers", "content-type")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatalf("preflight = %d %s", response.Code, response.Body.String())
	}
	assertCredentialedSocialCORS(t, response.Header(), testSocialOrigin)
	if response.Header().Get("Access-Control-Allow-Methods") !=
		"GET, POST, DELETE, OPTIONS" {
		t.Fatalf(
			"allow methods = %q",
			response.Header().Get("Access-Control-Allow-Methods"),
		)
	}
	if response.Header().Get("Access-Control-Allow-Headers") != "Content-Type" {
		t.Fatalf(
			"allow headers = %q",
			response.Header().Get("Access-Control-Allow-Headers"),
		)
	}

	hostile := httptest.NewRequest(
		http.MethodOptions,
		"/api/v1/workspaces/workspace-1/social-connections",
		nil,
	)
	hostile.Header.Set("Origin", "https://evil.example")
	hostile.Header.Set("Access-Control-Request-Method", http.MethodPost)
	hostileResponse := httptest.NewRecorder()
	handler.ServeHTTP(hostileResponse, hostile)
	if hostileResponse.Code != http.StatusForbidden {
		t.Fatalf("hostile preflight status = %d", hostileResponse.Code)
	}
	if hostileResponse.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatal("hostile preflight received an allow-origin header")
	}
}

func TestHTTPRuntimeAcceptsProductionCrossOriginMutation(t *testing.T) {
	fixture := newServiceFixture(t)
	handler, err := NewHTTPHandler(
		fixture.service,
		fixedRequestAuthenticator{accountID: "owner-1"},
		testSocialOrigin,
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
	request.Header.Set("Origin", testSocialOrigin)
	request.Header.Set("Sec-Fetch-Site", "same-site")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf(
			"production cross-origin mutation = %d %s",
			response.Code,
			response.Body.String(),
		)
	}
	assertCredentialedSocialCORS(t, response.Header(), testSocialOrigin)
	if len(fixture.repository.attempts) != 1 {
		t.Fatalf(
			"OAuth attempts = %d, want 1",
			len(fixture.repository.attempts),
		)
	}

	missingOrigin := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/workspaces/workspace-1/social-authorizations",
		bytes.NewBufferString(`{"provider":"facebook_pages"}`),
	)
	missingOrigin.Header.Set("Content-Type", "application/json")
	missingResponse := httptest.NewRecorder()
	handler.ServeHTTP(missingResponse, missingOrigin)
	if missingResponse.Code != http.StatusForbidden {
		t.Fatalf("missing-origin mutation status = %d", missingResponse.Code)
	}
	if len(fixture.repository.attempts) != 1 {
		t.Fatal("missing-origin request created an OAuth attempt")
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
	if method == http.MethodPost || method == http.MethodDelete {
		request.Header.Set("Origin", testSocialOrigin)
		request.Header.Set("Sec-Fetch-Site", "same-site")
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func assertCredentialedSocialCORS(
	t *testing.T,
	header http.Header,
	origin string,
) {
	t.Helper()
	if header.Get("Access-Control-Allow-Origin") != origin {
		t.Fatalf(
			"allow origin = %q, want %q",
			header.Get("Access-Control-Allow-Origin"),
			origin,
		)
	}
	if header.Get("Access-Control-Allow-Credentials") != "true" {
		t.Fatalf(
			"allow credentials = %q",
			header.Get("Access-Control-Allow-Credentials"),
		)
	}
	if !strings.Contains(header.Get("Vary"), "Origin") {
		t.Fatalf("Vary = %q, want Origin", header.Get("Vary"))
	}
}
