package scheduling

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSchedulingCredentialedCORSAndPreflight(t *testing.T) {
	origins, err := parseSchedulingAllowedOrigins(
		"https://postqron.com, https://app.postqron.com",
	)
	if err != nil {
		t.Fatal(err)
	}
	handler := credentialedSchedulingCORS(
		newSchedulingHTTPHandler(
			mustSchedulingTestService(t),
			authenticatorStub{accountID: "account-1"},
			origins,
		),
		origins,
	)
	request := httptest.NewRequest(
		http.MethodOptions,
		"/api/v1/workspaces/workspace-1/scheduled-posts/post-1/reschedule",
		nil,
	)
	request.Header.Set("Origin", "https://postqron.com")
	request.Header.Set("Access-Control-Request-Method", http.MethodPost)
	request.Header.Set("Access-Control-Request-Headers", "content-type")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusNoContent ||
		response.Header().Get("Access-Control-Allow-Origin") !=
			"https://postqron.com" ||
		response.Header().Get("Access-Control-Allow-Credentials") != "true" ||
		!strings.Contains(
			response.Header().Get("Access-Control-Allow-Methods"),
			http.MethodPost,
		) ||
		response.Header().Get("Access-Control-Allow-Headers") != "Content-Type" ||
		response.Header().Get("Access-Control-Max-Age") != "600" {
		t.Fatalf(
			"preflight status=%d headers=%v body=%q",
			response.Code,
			response.Header(),
			response.Body.String(),
		)
	}
}

func TestSchedulingCORSRejectsHostileOriginAndCrossSiteMutation(t *testing.T) {
	origins, err := parseSchedulingAllowedOrigins("https://postqron.com")
	if err != nil {
		t.Fatal(err)
	}
	core := newSchedulingHTTPHandler(
		mustSchedulingTestService(t),
		authenticatorStub{accountID: "account-1"},
		origins,
	)
	handler := credentialedSchedulingCORS(core, origins)

	hostile := httptest.NewRequest(
		http.MethodOptions,
		"/api/v1/workspaces/workspace-1/calendar",
		nil,
	)
	hostile.Header.Set("Origin", "https://attacker.example")
	hostileResponse := httptest.NewRecorder()
	handler.ServeHTTP(hostileResponse, hostile)
	if hostileResponse.Code != http.StatusForbidden ||
		hostileResponse.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatalf(
			"hostile preflight status=%d headers=%v body=%q",
			hostileResponse.Code,
			hostileResponse.Header(),
			hostileResponse.Body.String(),
		)
	}

	crossSite := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/workspaces/workspace-1/scheduled-posts",
		strings.NewReader(`{}`),
	)
	crossSite.Header.Set("Sec-Fetch-Site", "cross-site")
	crossSiteResponse := httptest.NewRecorder()
	core.ServeHTTP(crossSiteResponse, crossSite)
	if crossSiteResponse.Code != http.StatusForbidden ||
		!strings.Contains(crossSiteResponse.Body.String(), `"code":"origin_forbidden"`) {
		t.Fatalf(
			"cross-site mutation status=%d body=%q",
			crossSiteResponse.Code,
			crossSiteResponse.Body.String(),
		)
	}
}

func mustSchedulingTestService(t *testing.T) *Service {
	t.Helper()
	service, _, _ := newTestService(t)
	return service
}
