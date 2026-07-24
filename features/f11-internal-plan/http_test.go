package internalplan

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type authenticatorStub struct {
	principal     Principal
	authenticated bool
}

func (authenticator authenticatorStub) Principal(*http.Request) (Principal, bool) {
	return authenticator.principal, authenticator.authenticated
}

func TestInternalHandlerRejectsPayloadPrivilegeEscalation(t *testing.T) {
	tests := []string{
		`{"target_account_id":"` + targetID + `","actor_account_id":"` + adminID + `"}`,
		`{"target_account_id":"` + targetID + `","allowlisted":true}`,
		`{"target_account_id":"` + targetID + `","plan_code":"internal"}`,
		`{"target_account_id":"` + targetID + `"}{"target_account_id":"` + targetID + `"}`,
	}
	for _, payload := range tests {
		repository := newMemoryRepository()
		repository.allowlist[workspaceID+":"+targetID] = true
		service := newTestService(t, repository, &adminAuthorizerStub{
			admins: map[string]bool{memberID: false},
		})
		handler := NewInternalHTTPHandler(service, authenticatorStub{
			principal: Principal{
				AccountID:             memberID,
				StronglyAuthenticated: true,
			},
			authenticated: true,
		})

		request := httptest.NewRequest(
			http.MethodPut,
			"/internal/v1/workspaces/"+workspaceID+"/entitlement-override",
			strings.NewReader(payload),
		)
		request.Header.Set("X-Correlation-ID", "case-17-altered-payload")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)

		if response.Code != http.StatusBadRequest {
			t.Fatalf("payload %s status = %d, want 400", payload, response.Code)
		}
		if repository.active[workspaceID] || len(repository.audits) != 1 {
			t.Fatalf("malformed payload reached domain: %#v", repository)
		}
		if audit := repository.audits[0]; audit.Outcome != OutcomeDenied ||
			audit.ActorAccountID != memberID ||
			audit.TargetAccountID != "" {
			t.Fatalf("malformed payload audit = %#v", audit)
		}
	}
}

func TestInternalHandlerTakesActorOnlyFromTrustedAuthentication(t *testing.T) {
	repository := newMemoryRepository()
	repository.allowlist[workspaceID+":"+targetID] = true
	service := newTestService(t, repository, &adminAuthorizerStub{
		admins: map[string]bool{memberID: false, adminID: true},
	})
	handler := NewInternalHTTPHandler(service, authenticatorStub{
		principal: Principal{
			AccountID:             memberID,
			StronglyAuthenticated: true,
		},
		authenticated: true,
	})
	request := httptest.NewRequest(
		http.MethodPut,
		"/internal/v1/workspaces/"+workspaceID+"/entitlement-override",
		strings.NewReader(`{"target_account_id":"`+targetID+`"}`),
	)
	request.Header.Set("X-Correlation-ID", "case-17-server-actor")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", response.Code)
	}
	if repository.active[workspaceID] {
		t.Fatal("non-admin session activated the override")
	}
	if len(repository.audits) != 1 ||
		repository.audits[0].ActorAccountID != memberID {
		t.Fatalf("audit actor was not authenticated principal: %#v", repository.audits)
	}
}

func TestInternalHandlerIsSeparateFromPublicFlows(t *testing.T) {
	repository := newMemoryRepository()
	service := newTestService(t, repository, &adminAuthorizerStub{
		admins: map[string]bool{adminID: true},
	})
	handler := NewInternalHTTPHandler(service, authenticatorStub{})

	for _, path := range []string{
		"/api/v1/billing/plans",
		"/api/v1/workspaces/" + workspaceID + "/billing",
		"/pricing",
	} {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusNotFound {
			t.Fatalf("%s status = %d, want 404", path, response.Code)
		}
		body, err := io.ReadAll(response.Body)
		if err != nil {
			t.Fatal(err)
		}
		for _, forbidden := range []string{"internal", "unlimited", "override"} {
			if bytes.Contains(bytes.ToLower(body), []byte(forbidden)) {
				t.Fatalf("%s exposed %q: %s", path, forbidden, body)
			}
		}
	}
}

func TestUnauthenticatedRequestIsRejectedBeforePayload(t *testing.T) {
	repository := newMemoryRepository()
	service, err := NewService(repository, &adminAuthorizerStub{})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewInternalHTTPHandler(service, authenticatorStub{})
	request := httptest.NewRequest(
		http.MethodPut,
		"/internal/v1/workspaces/"+workspaceID+"/entitlement-override",
		strings.NewReader(`{"target_account_id":"`+targetID+`"}`),
	)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", response.Code)
	}
}

var _ = context.Background
