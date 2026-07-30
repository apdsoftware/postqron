package workspaces

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestRuntimeHTTPCompleteOnboardingReturnsCreatedSession(t *testing.T) {
	repository := NewMemoryRepository()
	seedRuntimeDocuments(repository)
	service := newRuntimeService(t, repository)
	handler, err := NewRuntimeHTTPHandler(
		service,
		fixedSessionAuthenticator{account: runtimeAccount("account-http")},
	)
	if err != nil {
		t.Fatal(err)
	}

	body, err := json.Marshal(map[string]any{
		"consents":  runtimeConsents(),
		"workspace": map[string]any{"mode": "create", "name": "HTTP Workspace"},
	})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/app/onboarding", bytes.NewReader(body))
	request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "opaque"})
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusCreated)
	}
	var session AppSession
	if err := json.Unmarshal(response.Body.Bytes(), &session); err != nil {
		t.Fatal(err)
	}
	if session.Account.ID != "account-http" || session.CurrentWorkspace == nil {
		t.Fatalf("session = %#v", session)
	}
}

func TestRuntimeHTTPCrossSiteMutationIsRejected(t *testing.T) {
	repository := NewMemoryRepository()
	seedRuntimeDocuments(repository)
	service := newRuntimeService(t, repository)
	handler, err := NewRuntimeHTTPHandler(
		service,
		fixedSessionAuthenticator{account: runtimeAccount("account-http")},
	)
	if err != nil {
		t.Fatal(err)
	}

	request := httptest.NewRequest(http.MethodPost, "/api/v1/app/workspaces/select", bytes.NewReader([]byte(`{"workspace_id":"workspace-1"}`)))
	request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "opaque"})
	request.Header.Set("Sec-Fetch-Site", "cross-site")
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusForbidden)
	}
}

func TestRuntimeHTTPCurrentWorkspaceAndMembersUseStoredSelection(t *testing.T) {
	repository := NewMemoryRepository()
	service := newRuntimeService(t, repository)
	domainService := newTestService(t, repository, 10)
	workspace := createPersonal(t, domainService, "owner")
	inviteAndAccept(
		t,
		domainService,
		workspace.ID,
		"owner",
		"member",
		"member@example.com",
	)
	repository.selections["member"] = workspace.ID

	handler, err := NewRuntimeHTTPHandler(
		service,
		fixedSessionAuthenticator{
			account: AppSessionAccount{
				ID:              "member",
				DisplayName:     "Member",
				Email:           "member@example.com",
				Locale:          LocaleEN,
				ContractCountry: "IT",
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	workspaceRequest := httptest.NewRequest(http.MethodGet, "/api/v1/app/workspaces/current", nil)
	workspaceRequest.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "opaque"})
	workspaceResponse := httptest.NewRecorder()
	handler.ServeHTTP(workspaceResponse, workspaceRequest)
	if workspaceResponse.Code != http.StatusOK {
		t.Fatalf("current workspace status = %d, want %d", workspaceResponse.Code, http.StatusOK)
	}

	membersRequest := httptest.NewRequest(http.MethodGet, "/api/v1/app/workspaces/current/members", nil)
	membersRequest.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "opaque"})
	membersResponse := httptest.NewRecorder()
	handler.ServeHTTP(membersResponse, membersRequest)
	if membersResponse.Code != http.StatusOK {
		t.Fatalf("members status = %d, want %d", membersResponse.Code, http.StatusOK)
	}
	var memberships []RuntimeMember
	if err := json.Unmarshal(membersResponse.Body.Bytes(), &memberships); err != nil {
		t.Fatal(err)
	}
	if len(memberships) != 2 {
		t.Fatalf("memberships count = %d, want 2", len(memberships))
	}
	for _, membership := range memberships {
		if membership.ID == "" ||
			membership.AccountID == "" ||
			membership.Email == "" ||
			membership.CreatedAt.IsZero() {
			t.Fatalf("runtime membership = %#v", membership)
		}
	}
	var wireMembers []map[string]any
	if err := json.Unmarshal(membersResponse.Body.Bytes(), &wireMembers); err != nil {
		t.Fatal(err)
	}
	if _, legacyField := wireMembers[0]["AccountID"]; legacyField {
		t.Fatalf("members response leaked legacy Go field names: %s", membersResponse.Body.String())
	}
}

func TestRuntimeHTTPCurrentWorkspaceOwnerMutations(t *testing.T) {
	repository := NewMemoryRepository()
	manager := newTestService(t, repository, 10)
	workspace := createPersonal(t, manager, "owner")
	inviteAndAccept(
		t,
		manager,
		workspace.ID,
		"owner",
		"member",
		"member@example.com",
	)
	runtimeService, err := NewRuntimeServiceWithManager(
		repository,
		manager,
		func() time.Time { return testNow },
	)
	if err != nil {
		t.Fatal(err)
	}
	ownerHandler, err := NewRuntimeHTTPHandler(
		runtimeService,
		fixedSessionAuthenticator{account: runtimeAccount("owner")},
	)
	if err != nil {
		t.Fatal(err)
	}
	memberHandler, err := NewRuntimeHTTPHandler(
		runtimeService,
		fixedSessionAuthenticator{account: runtimeAccount("member")},
	)
	if err != nil {
		t.Fatal(err)
	}

	memberRename := runtimeHTTPRequest(
		t,
		memberHandler,
		http.MethodPatch,
		"/api/v1/app/workspaces/current",
		`{"name":"Forbidden"}`,
	)
	if memberRename.Code != http.StatusForbidden {
		t.Fatalf("Member rename status = %d, want %d", memberRename.Code, http.StatusForbidden)
	}

	rename := runtimeHTTPRequest(
		t,
		ownerHandler,
		http.MethodPatch,
		"/api/v1/app/workspaces/current",
		`{"name":"Renamed workspace"}`,
	)
	if rename.Code != http.StatusOK {
		t.Fatalf("rename status = %d, body = %s", rename.Code, rename.Body.String())
	}
	var renamed RuntimeWorkspace
	if err := json.Unmarshal(rename.Body.Bytes(), &renamed); err != nil {
		t.Fatal(err)
	}
	if renamed.Name != "Renamed workspace" || renamed.Role != RoleOwner {
		t.Fatalf("renamed workspace = %#v", renamed)
	}

	invite := runtimeHTTPRequest(
		t,
		ownerHandler,
		http.MethodPost,
		"/api/v1/app/workspaces/current/invitations",
		`{"email":"next@example.com"}`,
	)
	if invite.Code != http.StatusCreated {
		t.Fatalf("invite status = %d, body = %s", invite.Code, invite.Body.String())
	}
	var invitation RuntimeInvitation
	if err := json.Unmarshal(invite.Body.Bytes(), &invitation); err != nil {
		t.Fatal(err)
	}
	if invitation.Token == "" || invitation.Status != InvitationPending {
		t.Fatalf("invitation = %#v", invitation)
	}
	if invite.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("invite Cache-Control = %q, want no-store", invite.Header().Get("Cache-Control"))
	}

	promote := runtimeHTTPRequest(
		t,
		ownerHandler,
		http.MethodPut,
		"/api/v1/app/workspaces/current/members/member/role",
		`{"role":"owner"}`,
	)
	if promote.Code != http.StatusNoContent {
		t.Fatalf("promote status = %d, body = %s", promote.Code, promote.Body.String())
	}
	remove := runtimeHTTPRequest(
		t,
		ownerHandler,
		http.MethodDelete,
		"/api/v1/app/workspaces/current/members/member",
		"",
	)
	if remove.Code != http.StatusNoContent {
		t.Fatalf("remove status = %d, body = %s", remove.Code, remove.Body.String())
	}

	lastOwner := runtimeHTTPRequest(
		t,
		ownerHandler,
		http.MethodDelete,
		"/api/v1/app/workspaces/current/members/owner",
		"",
	)
	if lastOwner.Code != http.StatusConflict {
		t.Fatalf("last Owner status = %d, body = %s", lastOwner.Code, lastOwner.Body.String())
	}
	assertRuntimeErrorCode(t, lastOwner, "APP_LAST_OWNER", false)
}

func TestRuntimeHTTPErrorContract(t *testing.T) {
	repository := NewMemoryRepository()
	manager := newTestService(t, repository, 5)
	createPersonal(t, manager, "owner")
	runtimeService, err := NewRuntimeServiceWithManager(
		repository,
		manager,
		func() time.Time { return testNow },
	)
	if err != nil {
		t.Fatal(err)
	}
	handler, err := NewRuntimeHTTPHandler(
		runtimeService,
		fixedSessionAuthenticator{account: runtimeAccount("owner")},
	)
	if err != nil {
		t.Fatal(err)
	}

	malformed := runtimeHTTPRequest(
		t,
		handler,
		http.MethodPatch,
		"/api/v1/app/workspaces/current",
		`{"name":`,
	)
	if malformed.Code != http.StatusBadRequest {
		t.Fatalf("malformed status = %d, body = %s", malformed.Code, malformed.Body.String())
	}
	assertRuntimeErrorCode(t, malformed, "APP_INVALID_REQUEST", false)

	request := httptest.NewRequest(http.MethodGet, "/api/v1/app/workspaces/current", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status = %d, want %d", response.Code, http.StatusUnauthorized)
	}
	assertRuntimeErrorCode(t, response, "APP_UNAUTHENTICATED", false)

	unavailableHandler, err := NewRuntimeHTTPHandler(
		runtimeService,
		fixedSessionAuthenticator{err: errors.New("database unavailable")},
	)
	if err != nil {
		t.Fatal(err)
	}
	unavailable := runtimeHTTPRequest(
		t,
		unavailableHandler,
		http.MethodGet,
		"/api/v1/app/workspaces/current",
		"",
	)
	if unavailable.Code != http.StatusServiceUnavailable {
		t.Fatalf("unavailable status = %d, body = %s", unavailable.Code, unavailable.Body.String())
	}
	assertRuntimeErrorCode(t, unavailable, "APP_RUNTIME_UNAVAILABLE", true)

	concurrent := httptest.NewRecorder()
	writeRuntimeError(concurrent, testSQLStateError{})
	if concurrent.Code != http.StatusConflict {
		t.Fatalf("concurrent status = %d, body = %s", concurrent.Code, concurrent.Body.String())
	}
	assertRuntimeErrorCode(t, concurrent, "APP_CONCURRENT_UPDATE", true)
}

func runtimeHTTPRequest(
	t *testing.T,
	handler http.Handler,
	method, path, body string,
) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "opaque"})
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func assertRuntimeErrorCode(
	t *testing.T,
	response *httptest.ResponseRecorder,
	code string,
	retryable bool,
) {
	t.Helper()
	var payload struct {
		Error struct {
			Code      string `json:"code"`
			Retryable bool   `json:"retryable"`
		} `json:"error"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Error.Code != code || payload.Error.Retryable != retryable {
		t.Fatalf("error payload = %#v, want code %q retryable %v", payload, code, retryable)
	}
}

type testSQLStateError struct{}

func (testSQLStateError) Error() string {
	return "serialization failure"
}

func (testSQLStateError) SQLState() string {
	return "40001"
}
