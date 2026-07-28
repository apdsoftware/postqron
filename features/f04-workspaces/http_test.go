package workspaces

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
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
	var memberships []Membership
	if err := json.Unmarshal(membersResponse.Body.Bytes(), &memberships); err != nil {
		t.Fatal(err)
	}
	if len(memberships) != 2 {
		t.Fatalf("memberships count = %d, want 2", len(memberships))
	}
}
