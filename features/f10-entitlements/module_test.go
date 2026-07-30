package entitlements

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestModuleExposesAuthoritativePublicCatalog(t *testing.T) {
	module, err := NewPostgresModule(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := module.Start(context.Background()); err != nil {
		t.Fatal(err)
	}

	handler, ok := module.Handler("PublicPlans")
	if !ok {
		t.Fatal("PublicPlans handler is not exposed")
	}
	request := httptest.NewRequest(
		http.MethodGet,
		"/api/v1/billing/plans",
		nil,
	)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d", response.Code)
	}
	var payload struct {
		Provider       string       `json:"provider"`
		CatalogVersion string       `json:"catalog_version"`
		Currency       string       `json:"currency"`
		Plans          []PublicPlan `json:"plans"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	if payload.Provider != "paddle" {
		t.Fatalf("provider = %q", payload.Provider)
	}
	if payload.CatalogVersion != CatalogVersion {
		t.Fatalf("catalog_version = %q", payload.CatalogVersion)
	}
	if payload.Currency != "EUR" {
		t.Fatalf("currency = %q", payload.Currency)
	}
	expected := PublicPlans()
	if len(payload.Plans) != len(expected) {
		t.Fatalf("plans = %d, want %d", len(payload.Plans), len(expected))
	}
	for index := range expected {
		if payload.Plans[index].Code != expected[index].Code {
			t.Fatalf(
				"plans[%d].code = %q, want %q",
				index,
				payload.Plans[index].Code,
				expected[index].Code,
			)
		}
	}
}

func TestModuleRejectsUnknownHandlers(t *testing.T) {
	module, err := NewPostgresModule(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if handler, ok := module.Handler("BillingPrivate"); ok || handler != nil {
		t.Fatal("unexpected handler exposed")
	}
}

func TestModuleAllowsTrustedBrowserOriginForBillingRoutes(t *testing.T) {
	t.Setenv(
		billingAllowedOriginsEnv,
		"https://app.example.test, https://other.example.test/",
	)
	module, err := NewPostgresModule(nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	plans, ok := module.Handler("PublicPlans")
	if !ok {
		t.Fatal("PublicPlans handler is not exposed")
	}
	request := httptest.NewRequest(
		http.MethodGet,
		"/api/v1/billing/plans",
		nil,
	)
	request.Header.Set("Origin", "https://app.example.test")
	response := httptest.NewRecorder()
	plans.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", response.Code)
	}
	if got := response.Header().Get("Access-Control-Allow-Origin"); got !=
		"https://app.example.test" {
		t.Fatalf("allow origin = %q", got)
	}
	if got := response.Header().Get("Access-Control-Allow-Credentials"); got !=
		"true" {
		t.Fatalf("allow credentials = %q", got)
	}
	if got := response.Header().Values("Vary"); !slicesContain(got, "Origin") {
		t.Fatalf("Vary = %q, want Origin", got)
	}

	overview, ok := module.Handler("BillingOverview")
	if !ok {
		t.Fatal("BillingOverview handler is not exposed")
	}
	request = httptest.NewRequest(
		http.MethodGet,
		"/api/v1/workspaces/workspace/billing",
		nil,
	)
	request.Header.Set("Origin", "https://other.example.test")
	response = httptest.NewRecorder()
	overview.ServeHTTP(response, request)
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("catalog-only overview status = %d, want 503", response.Code)
	}
	if got := response.Header().Get("Access-Control-Allow-Origin"); got !=
		"https://other.example.test" {
		t.Fatalf("catalog-only overview allow origin = %q", got)
	}
}

func TestModuleBillingPreflightIsPublicAndCredentialed(t *testing.T) {
	t.Setenv(billingAllowedOriginsEnv, "https://app.example.test")
	module, err := NewPostgresModule(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	handler, ok := module.Handler("BillingPreflight")
	if !ok {
		t.Fatal("BillingPreflight handler is not exposed")
	}
	request := httptest.NewRequest(
		http.MethodOptions,
		"/api/v1/workspaces/workspace/billing/subscription",
		nil,
	)
	request.Header.Set("Origin", "https://app.example.test")
	request.Header.Set("Access-Control-Request-Method", http.MethodPatch)
	request.Header.Set("Access-Control-Request-Headers", "content-type")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", response.Code)
	}
	if got := response.Header().Get("Access-Control-Allow-Origin"); got !=
		"https://app.example.test" {
		t.Fatalf("allow origin = %q", got)
	}
	if got := response.Header().Get("Access-Control-Allow-Methods"); !strings.Contains(got, http.MethodPatch) {
		t.Fatalf("allow methods = %q, want PATCH", got)
	}
	if got := response.Header().Get("Access-Control-Allow-Headers"); got !=
		"Content-Type" {
		t.Fatalf("allow headers = %q", got)
	}
}

func TestModuleRejectsUntrustedBrowserOrigin(t *testing.T) {
	t.Setenv(billingAllowedOriginsEnv, "https://app.example.test")
	module, err := NewPostgresModule(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	handler, ok := module.Handler("PublicPlans")
	if !ok {
		t.Fatal("PublicPlans handler is not exposed")
	}
	request := httptest.NewRequest(
		http.MethodGet,
		"/api/v1/billing/plans",
		nil,
	)
	request.Header.Set("Origin", "https://evil.example")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", response.Code)
	}
	if got := response.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("allow origin = %q, want empty", got)
	}
	if !strings.Contains(response.Body.String(), `"error":"origin_forbidden"`) {
		t.Fatalf("body = %q", response.Body.String())
	}
}

func TestModuleRejectsInvalidBillingOriginConfiguration(t *testing.T) {
	t.Setenv(billingAllowedOriginsEnv, "https://app.example.test/path")
	if _, err := NewPostgresModule(nil, nil); err == nil ||
		!strings.Contains(err.Error(), "billing allowed origin") {
		t.Fatalf("NewPostgresModule() error = %v", err)
	}
}

func slicesContain(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}
