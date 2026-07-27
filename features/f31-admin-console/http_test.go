package adminconsole

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"
)

func testHandler(t *testing.T, fixture serviceFixture, sessions map[string]Session) http.Handler {
	t.Helper()
	handler, err := NewHandler(
		fixture.service,
		NewMemoryAuthenticator(sessions),
		"https://postqron.com",
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

func TestAllowedOriginsAreRequiredAndNormalized(t *testing.T) {
	origins, err := AllowedOriginsFromEnvironment(func(key string) (string, bool) {
		if key != AdminAllowedOriginsEnvName {
			return "", false
		}
		return " HTTPS://POSTQRON.COM:443/, https://app.postqron.com, https://app.postqron.com ", true
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(origins) != 2 ||
		origins[0] != "https://app.postqron.com" ||
		origins[1] != "https://postqron.com" {
		t.Fatalf("origins = %v", origins)
	}
	if _, err := AllowedOriginsFromEnvironment(func(string) (string, bool) {
		return "", false
	}); err == nil {
		t.Fatal("missing allowed origins did not fail closed")
	}
	for _, raw := range []string{
		" ",
		"https://postqron.com/admin",
		"https://*.postqron.com",
		"*",
	} {
		if _, err := ParseAllowedOrigins(raw); err == nil {
			t.Fatalf("invalid allowed origins %q did not fail closed", raw)
		}
	}
}

func TestCredentialedCORSForAllowedBrowserOrigin(t *testing.T) {
	fixture := newServiceFixture(t, initialAdminEmail)
	normal := Session{
		AccountID:       "account-user",
		Email:           "user@example.com",
		EmailVerified:   true,
		AuthenticatedAt: testNow,
		ExpiresAt:       testNow.Add(time.Hour),
	}
	handler := testHandler(t, fixture, map[string]Session{
		"admin":  validSession(),
		"normal": normal,
	})
	headers := map[string]string{"Origin": "https://postqron.com"}

	anonymous := request(
		t, handler, http.MethodGet, "/api/v1/admin/session", "", nil, headers,
	)
	if anonymous.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous status = %d", anonymous.Code)
	}

	forbidden := request(
		t, handler, http.MethodGet, "/api/v1/admin/session", "normal", nil, headers,
	)
	if forbidden.Code != http.StatusForbidden {
		t.Fatalf("non-admin status = %d", forbidden.Code)
	}

	admin := request(
		t, handler, http.MethodGet, "/api/v1/admin/session", "admin", nil, headers,
	)
	if admin.Code != http.StatusOK {
		t.Fatalf("admin status = %d, body = %s", admin.Code, admin.Body)
	}
	for name, response := range map[string]*httptest.ResponseRecorder{
		"anonymous": anonymous,
		"non-admin": forbidden,
		"admin":     admin,
	} {
		if response.Header().Get("Access-Control-Allow-Origin") != "https://postqron.com" ||
			response.Header().Get("Access-Control-Allow-Credentials") != "true" ||
			response.Header().Get("Vary") != "Origin" {
			t.Fatalf("%s CORS headers = %v", name, response.Header())
		}
	}
}

func TestAllowedPreflightReturnsBeforeAuthentication(t *testing.T) {
	fixture := newServiceFixture(t, initialAdminEmail)
	handler := testHandler(t, fixture, nil)
	preflight := request(
		t,
		handler,
		http.MethodOptions,
		"/api/v1/admin/workspaces/workspace-1/internal-plan",
		"",
		nil,
		map[string]string{
			"Origin":                         "https://postqron.com",
			"Access-Control-Request-Method":  http.MethodPut,
			"Access-Control-Request-Headers": "content-type,x-csrf-token,idempotency-key",
		},
	)
	if preflight.Code != http.StatusNoContent || preflight.Body.Len() != 0 {
		t.Fatalf("preflight = %d %q", preflight.Code, preflight.Body.String())
	}
	if preflight.Header().Get("Access-Control-Allow-Methods") != "GET, PUT, DELETE" {
		t.Fatalf("allowed methods = %q",
			preflight.Header().Get("Access-Control-Allow-Methods"))
	}
	if preflight.Header().Get("Access-Control-Allow-Headers") !=
		"Content-Type, X-CSRF-Token, Idempotency-Key" {
		t.Fatalf("allowed headers = %q",
			preflight.Header().Get("Access-Control-Allow-Headers"))
	}
	if preflight.Header().Get("Access-Control-Allow-Credentials") != "true" {
		t.Fatalf("allow credentials = %q",
			preflight.Header().Get("Access-Control-Allow-Credentials"))
	}
}

func TestManifestRoutesOnlySessionAndPreflightsThroughPublicChannel(t *testing.T) {
	manifest, err := os.ReadFile("feature.yaml")
	if err != nil {
		t.Fatal(err)
	}
	source := string(manifest)
	if strings.Count(source, "methods: [OPTIONS]") != 10 ||
		strings.Count(source, `visibility: "public"`) != 11 {
		t.Fatalf("public session/preflight routes are incomplete:\n%s", source)
	}
	if !strings.Contains(source, "methods: [GET]\n      visibility: \"public\"") {
		t.Fatalf("session GET does not bypass global private authentication:\n%s", source)
	}
	for _, route := range []struct {
		path    string
		methods string
	}{
		{"/admin/dashboard", "[GET]"},
		{"/admin/search", "[GET]"},
		{"/admin/plans", "[GET]"},
		{"/admin/plans/export", "[GET]"},
		{"/admin/audit", "[GET]"},
		{"/admin/audit/export", "[GET]"},
		{"/admin/audit/{event_id}", "[GET]"},
		{"/admin/workspaces/{workspace_id}/internal-plan", "[PUT, DELETE]"},
		{"/admin/admins/{account_id}", "[PUT, DELETE]"},
	} {
		expected := "    - path: " + route.path +
			"\n      handler: Admin" +
			"\n      methods: " + route.methods +
			"\n      visibility: private"
		if !strings.Contains(source, expected) {
			t.Fatalf("admin data route left the private channel: %s", route.path)
		}
	}
	// Nine server data routes and all six admin web routes must remain private.
	if strings.Count(source, "\n      visibility: private") != 15 {
		t.Fatalf("admin data and web routes no longer remain private:\n%s", source)
	}
	for _, methods := range []string{
		"methods: [GET, OPTIONS]",
		"methods: [PUT, DELETE, OPTIONS]",
	} {
		if strings.Contains(source, methods) {
			t.Fatalf("preflight still crosses private authentication: %s", methods)
		}
	}
}

func TestDisallowedBrowserOriginIsRejectedBeforeAdminDataIsRead(t *testing.T) {
	fixture := newServiceFixture(t, initialAdminEmail)
	handler := testHandler(t, fixture, map[string]Session{"admin": validSession()})
	response := request(
		t,
		handler,
		http.MethodGet,
		"/api/v1/admin/dashboard",
		"admin",
		nil,
		map[string]string{"Origin": "https://attacker.example"},
	)
	if response.Code != http.StatusForbidden ||
		!strings.Contains(response.Body.String(), "ADMIN_ORIGIN_FORBIDDEN") {
		t.Fatalf("response = %d %s", response.Code, response.Body.String())
	}
	if response.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatalf("disallowed origin received CORS headers: %v", response.Header())
	}
	if fixture.reader.dashboardCalls != 0 {
		t.Fatalf("disallowed origin read admin data %d times", fixture.reader.dashboardCalls)
	}
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

func TestPlanAndAuditHTTPListsUseCombinedServerSideQueries(t *testing.T) {
	fixture := newServiceFixture(t, initialAdminEmail)
	fixture.reader.plans = []PlanRow{{
		WorkspaceID: "workspace-1", WorkspaceName: "Studio",
		OwnerEmail: "owner@example.test", PlanCode: "pro",
		Status: "active", PlanUpdatedAt: testNow,
		WorkspaceCreatedAt: testNow, PeriodStart: testNow,
		PeriodEnd: testNow.Add(30 * 24 * time.Hour),
	}}
	fixture.reader.auditEvents = []AuditEvent{{
		ID: "audit-event-1", Code: "internal_plan.assign",
		ActorID: "account-admin", SubjectID: "workspace-1",
		Reason: "Approved operation", Outcome: "succeeded",
		CorrelationID: "correlation-1", OccurredAt: testNow,
	}}
	handler := testHandler(t, fixture, map[string]Session{"admin": validSession()})

	anonymous := request(
		t, handler, http.MethodGet, "/api/v1/admin/plans", "", nil, nil,
	)
	if anonymous.Code != http.StatusUnauthorized || fixture.reader.planCalls != 0 {
		t.Fatalf("anonymous plans = %d, calls = %d", anonymous.Code, fixture.reader.planCalls)
	}

	from := testNow.Add(-time.Hour).Format(time.RFC3339)
	to := testNow.Add(time.Hour).Format(time.RFC3339)
	plansPath := "/api/v1/admin/plans?q=Studio&plan=pro&status=active" +
		"&type=internal&from=" + url.QueryEscape(from) +
		"&to=" + url.QueryEscape(to) +
		"&sort=owner&direction=asc&page=1&page_size=10"
	plans := request(t, handler, http.MethodGet, plansPath, "admin", nil, nil)
	if plans.Code != http.StatusOK ||
		!strings.Contains(plans.Body.String(), `"total":1`) {
		t.Fatalf("plans = %d %s", plans.Code, plans.Body.String())
	}
	if fixture.reader.lastPlanQuery.Search != "Studio" ||
		fixture.reader.lastPlanQuery.Status != "active" ||
		fixture.reader.lastPlanQuery.Type != "internal" ||
		fixture.reader.lastPlanQuery.Sort != "owner" ||
		fixture.reader.lastPlanPage.PageSize != 10 {
		t.Fatalf(
			"plan query/page = %+v / %+v",
			fixture.reader.lastPlanQuery,
			fixture.reader.lastPlanPage,
		)
	}

	auditPath := "/api/v1/admin/audit?action=internal_plan.assign" +
		"&actor=account&subject=workspace&outcome=succeeded" +
		"&sort=code&direction=asc&page=1&page_size=25"
	audit := request(t, handler, http.MethodGet, auditPath, "admin", nil, nil)
	if audit.Code != http.StatusOK ||
		!strings.Contains(audit.Body.String(), "internal_plan.assign") {
		t.Fatalf("audit = %d %s", audit.Code, audit.Body.String())
	}
	detail := request(
		t,
		handler,
		http.MethodGet,
		"/api/v1/admin/audit/audit-event-1",
		"admin",
		nil,
		nil,
	)
	if detail.Code != http.StatusOK ||
		!strings.Contains(detail.Body.String(), "correlation-1") {
		t.Fatalf("detail = %d %s", detail.Code, detail.Body.String())
	}

	beforeInvalid := fixture.reader.planCalls
	invalid := request(
		t,
		handler,
		http.MethodGet,
		"/api/v1/admin/plans?sort=secret&unexpected=value",
		"admin",
		nil,
		nil,
	)
	if invalid.Code != http.StatusBadRequest ||
		fixture.reader.planCalls != beforeInvalid {
		t.Fatalf("invalid filter = %d, calls = %d", invalid.Code, fixture.reader.planCalls)
	}
}

func TestAdminExportsAreAuthorizedLimitedAndFormulaSafe(t *testing.T) {
	fixture := newServiceFixture(t, initialAdminEmail)
	fixture.reader.plans = []PlanRow{{
		WorkspaceID:   "workspace-1",
		WorkspaceName: "=HYPERLINK(\"https://attacker.example\")",
		OwnerEmail:    "+owner@example.test",
		PlanCode:      "pro", Status: "active",
		WorkspaceCreatedAt: testNow, PlanUpdatedAt: testNow,
		PeriodStart: testNow, PeriodEnd: testNow.Add(30 * 24 * time.Hour),
		Usage: EntitlementUsage{
			Members: UsageSummary{
				Used: 1, Limit: int64Pointer(5), Remaining: int64Pointer(4),
			},
			Channels: UsageSummary{
				Used: 2, Limit: int64Pointer(10), Remaining: int64Pointer(8),
			},
			ScheduledPublications: UsageSummary{
				Used: 3, Limit: int64Pointer(100), Remaining: int64Pointer(97),
			},
		},
	}}
	handler := testHandler(t, fixture, map[string]Session{"admin": validSession()})

	anonymous := request(
		t,
		handler,
		http.MethodGet,
		"/api/v1/admin/plans/export?format=csv",
		"",
		nil,
		nil,
	)
	if anonymous.Code != http.StatusUnauthorized || fixture.reader.planCalls != 0 {
		t.Fatalf("anonymous export = %d, calls = %d", anonymous.Code, fixture.reader.planCalls)
	}
	csvExport := request(
		t,
		handler,
		http.MethodGet,
		"/api/v1/admin/plans/export?format=csv&type=public",
		"admin",
		nil,
		nil,
	)
	if csvExport.Code != http.StatusOK ||
		!strings.HasPrefix(csvExport.Header().Get("Content-Type"), "text/csv") ||
		!strings.Contains(csvExport.Body.String(), `'=HYPERLINK`) ||
		!strings.Contains(csvExport.Body.String(), `'+owner@example.test`) {
		t.Fatalf(
			"CSV export = %d %v %q",
			csvExport.Code,
			csvExport.Header(),
			csvExport.Body.String(),
		)
	}
	xlsxExport := request(
		t,
		handler,
		http.MethodGet,
		"/api/v1/admin/plans/export?format=xlsx",
		"admin",
		nil,
		nil,
	)
	if xlsxExport.Code != http.StatusOK ||
		!bytes.HasPrefix(xlsxExport.Body.Bytes(), []byte("PK")) ||
		!strings.Contains(
			xlsxExport.Header().Get("Content-Disposition"),
			"postqron-admin-plans.xlsx",
		) {
		t.Fatalf("XLSX export = %d %v", xlsxExport.Code, xlsxExport.Header())
	}
	invalid := request(
		t,
		handler,
		http.MethodGet,
		"/api/v1/admin/plans/export?format=pdf",
		"admin",
		nil,
		nil,
	)
	if invalid.Code != http.StatusBadRequest ||
		!strings.Contains(invalid.Body.String(), "ADMIN_INVALID_EXPORT_FORMAT") {
		t.Fatalf("invalid export = %d %s", invalid.Code, invalid.Body.String())
	}
	fixture.reader.planTotal = maxAdminExportRows + 1
	limited := request(
		t,
		handler,
		http.MethodGet,
		"/api/v1/admin/plans/export?format=csv",
		"admin",
		nil,
		nil,
	)
	if limited.Code != http.StatusRequestEntityTooLarge ||
		!strings.Contains(limited.Body.String(), "ADMIN_EXPORT_LIMIT_EXCEEDED") {
		t.Fatalf("limited export = %d %s", limited.Code, limited.Body.String())
	}
}

func int64Pointer(value int64) *int64 {
	return &value
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
