package adminconsole

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func testHandler(t *testing.T, fixture serviceFixture, sessions map[string]Session) http.Handler {
	t.Helper()
	handler, err := NewHandler(
		fixture.service,
		NewMemoryAuthenticator(sessions),
	)
	if err != nil {
		t.Fatal(err)
	}
	return handler
}

func request(
	t *testing.T,
	handler http.Handler,
	method, path, token string,
	body any,
	headers map[string]string,
) *httptest.ResponseRecorder {
	t.Helper()
	var payload *bytes.Reader
	if body == nil {
		payload = bytes.NewReader(nil)
	} else {
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		payload = bytes.NewReader(encoded)
	}
	httpRequest := httptest.NewRequest(method, path, payload)
	if body != nil {
		httpRequest.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		httpRequest.AddCookie(&http.Cookie{Name: sessionCookieName, Value: token})
	}
	for key, value := range headers {
		httpRequest.Header.Set(key, value)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httpRequest)
	return response
}

func TestUnauthorizedRequestsReturnBeforeAdminDataIsRead(t *testing.T) {
	fixture := newServiceFixture(t, initialAdminEmail)
	normal := Session{
		AccountID:       "account-user",
		Email:           "user@example.com",
		EmailVerified:   true,
		AuthenticatedAt: testNow,
		ExpiresAt:       testNow.Add(time.Hour),
	}
	handler := testHandler(t, fixture, map[string]Session{"normal": normal})

	unauthenticated := request(t, handler, http.MethodGet, "/api/v1/admin/dashboard", "", nil, nil)
	if unauthenticated.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status = %d", unauthenticated.Code)
	}
	forbidden := request(t, handler, http.MethodGet, "/api/v1/admin/dashboard", "normal", nil, nil)
	if forbidden.Code != http.StatusForbidden {
		t.Fatalf("forbidden status = %d", forbidden.Code)
	}
	if fixture.reader.dashboardCalls != 0 {
		t.Fatalf("unauthorized request read admin data %d times", fixture.reader.dashboardCalls)
	}
	if strings.Contains(forbidden.Body.String(), "allowlist") ||
		strings.Contains(forbidden.Body.String(), initialAdminEmail) {
		t.Fatalf("forbidden response leaked authorization detail: %s", forbidden.Body.String())
	}
}

func TestAdminEndToEndLoginAssignAndRevokeInternalPlan(t *testing.T) {
	fixture := newServiceFixture(t, initialAdminEmail)
	handler := testHandler(t, fixture, map[string]Session{"admin-session": validSession()})

	login := request(t, handler, http.MethodGet, "/api/v1/admin/session", "admin-session", nil, nil)
	if login.Code != http.StatusOK ||
		!strings.Contains(login.Body.String(), initialAdminEmail) {
		t.Fatalf("login = %d %s", login.Code, login.Body.String())
	}
	dashboard := request(t, handler, http.MethodGet, "/api/v1/admin/dashboard", "admin-session", nil, nil)
	if dashboard.Code != http.StatusOK || fixture.reader.dashboardCalls != 1 {
		t.Fatalf("dashboard = %d %s", dashboard.Code, dashboard.Body.String())
	}

	headers := map[string]string{
		"X-CSRF-Token":    validSession().CSRFToken,
		"Idempotency-Key": "e2e-request-assign",
	}
	assign := request(
		t,
		handler,
		http.MethodPut,
		"/api/v1/admin/workspaces/workspace-1/internal-plan",
		"admin-session",
		map[string]any{
			"confirmed": true,
			"reason":    "Approved by operations owner",
		},
		headers,
	)
	if assign.Code != http.StatusOK {
		t.Fatalf("assign = %d %s", assign.Code, assign.Body.String())
	}
	headers["Idempotency-Key"] = "e2e-request-revoke"
	revoke := request(
		t,
		handler,
		http.MethodDelete,
		"/api/v1/admin/workspaces/workspace-1/internal-plan",
		"admin-session",
		map[string]any{
			"confirmed": true,
			"reason":    "Internal access no longer required",
		},
		headers,
	)
	if revoke.Code != http.StatusOK {
		t.Fatalf("revoke = %d %s", revoke.Code, revoke.Body.String())
	}
	if len(fixture.plan.changes) != 2 ||
		fixture.plan.changes[0].Action != "internal_plan.assign" ||
		fixture.plan.changes[1].Action != "internal_plan.revoke" {
		t.Fatalf("F11 changes = %+v", fixture.plan.changes)
	}
}

func TestHTTPRejectsCSRFStaleReauthAndPayloadManipulation(t *testing.T) {
	fixture := newServiceFixture(t, initialAdminEmail)
	stale := validSession()
	stale.AuthenticatedAt = testNow.Add(-10 * time.Minute)
	handler := testHandler(t, fixture, map[string]Session{
		"admin": validSession(),
		"stale": stale,
	})
	path := "/api/v1/admin/workspaces/workspace-1/internal-plan"
	body := map[string]any{
		"confirmed": true,
		"reason":    "Approved for controlled test",
	}
	csrf := request(t, handler, http.MethodPut, path, "admin", body, map[string]string{
		"X-CSRF-Token":    "forged",
		"Idempotency-Key": "http-request-csrf",
	})
	if csrf.Code != http.StatusForbidden ||
		!strings.Contains(csrf.Body.String(), "ADMIN_CSRF_INVALID") {
		t.Fatalf("csrf = %d %s", csrf.Code, csrf.Body.String())
	}
	reauth := request(t, handler, http.MethodPut, path, "stale", body, map[string]string{
		"X-CSRF-Token":    stale.CSRFToken,
		"Idempotency-Key": "http-request-reauth",
	})
	if reauth.Code != http.StatusUnauthorized ||
		!strings.Contains(reauth.Body.String(), "ADMIN_REAUTH_REQUIRED") {
		t.Fatalf("reauth = %d %s", reauth.Code, reauth.Body.String())
	}
	manipulated := request(
		t,
		handler,
		http.MethodPut,
		path,
		"admin",
		map[string]any{
			"confirmed":   true,
			"reason":      "Approved for controlled test",
			"admin_email": "attacker@example.com",
		},
		map[string]string{
			"X-CSRF-Token":    validSession().CSRFToken,
			"Idempotency-Key": "http-request-manipulated",
		},
	)
	if manipulated.Code != http.StatusBadRequest {
		t.Fatalf("manipulated = %d %s", manipulated.Code, manipulated.Body.String())
	}
	if len(fixture.plan.changes) != 0 {
		t.Fatalf("rejected HTTP requests reached F11: %+v", fixture.plan.changes)
	}
}
