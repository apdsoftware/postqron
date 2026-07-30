package entitlements

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type requestAuthenticatorStub struct{}

func (requestAuthenticatorStub) AccountID(*http.Request) (string, bool) {
	return "", false
}

type authenticatedRequestStub struct{}

func (authenticatedRequestStub) AccountID(*http.Request) (string, bool) {
	return "account", true
}

type workspaceViewerStub struct{}

func (workspaceViewerStub) CanViewBilling(
	context.Context,
	string,
	string,
) (bool, error) {
	return false, nil
}

func TestPublicPlansEndpointOnlyContainsPublicCatalog(t *testing.T) {
	handler := NewHTTPHandler(
		NewService(&serviceStoreStub{}),
		nil,
		nil,
		nil,
		http.NotFoundHandler(),
		requestAuthenticatorStub{},
		workspaceViewerStub{},
	)
	request := httptest.NewRequest(http.MethodGet, "/api/v1/billing/plans", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d", response.Code)
	}
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	payload := strings.ToLower(string(body))
	for _, required := range []string{
		`"provider":"paddle"`,
		`"catalog_version":"d09-v2"`,
		`"start"`,
		`"pro"`,
		`"team"`,
		`"unlimited"`,
		`"price_tiers"`,
	} {
		if !strings.Contains(payload, required) {
			t.Fatalf("public catalog is missing %q: %s", required, body)
		}
	}
	for _, forbidden := range []string{
		"internal_unlimited",
		"override",
		"allowlist",
		"assignment_reason",
	} {
		if strings.Contains(payload, forbidden) {
			t.Fatalf("public catalog contains %q: %s", forbidden, body)
		}
	}
	if cacheControl := response.Header().Get("Cache-Control"); cacheControl != "no-store" {
		t.Fatalf("Cache-Control = %q", cacheControl)
	}
}

func TestPlanChangeHTTPReturnsStructuredNonRetryableOverages(t *testing.T) {
	changes, err := NewSubscriptionChangeService(
		ownerStub{owner: true},
		&changeProviderStub{},
		&changeStoreStub{
			binding: BillingBinding{
				Plan: PlanUnlimited, Interval: IntervalMonthly,
				SubscriptionID: "sub_server",
			},
			usage: []Usage{
				{Resource: ResourceMembers, Used: 4},
				{Resource: ResourceChannels, Used: 7},
				{Resource: ResourceScheduledPublications, Used: 251},
			},
		},
		testPaddleCatalog(),
	)
	if err != nil {
		t.Fatal(err)
	}
	handler := NewHTTPHandler(
		nil,
		nil,
		nil,
		changes,
		http.NotFoundHandler(),
		authenticatedRequestStub{},
		workspaceViewerStub{},
	)
	request := httptest.NewRequest(
		http.MethodPatch,
		"/api/v1/workspaces/workspace/billing/subscription",
		strings.NewReader(`{
			"plan":"pro",
			"interval":"monthly",
			"channels":6,
			"idempotency_key":"blocked"
		}`),
	)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusConflict {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var payload struct {
		Error     string             `json:"error"`
		Retryable bool               `json:"retryable"`
		Overages  []DowngradeOverage `json:"overages"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Error != "downgrade_limit_exceeded" ||
		payload.Retryable ||
		len(payload.Overages) != 3 {
		t.Fatalf("payload = %#v", payload)
	}
}

func TestPlanChangeHTTPIsOwnerOnly(t *testing.T) {
	changes, err := NewSubscriptionChangeService(
		ownerStub{owner: false},
		&changeProviderStub{},
		&changeStoreStub{},
		testPaddleCatalog(),
	)
	if err != nil {
		t.Fatal(err)
	}
	handler := NewHTTPHandler(
		nil,
		nil,
		nil,
		changes,
		http.NotFoundHandler(),
		authenticatedRequestStub{},
		workspaceViewerStub{},
	)
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/workspaces/workspace/billing/subscription/preview",
		strings.NewReader(`{
			"plan":"team",
			"interval":"monthly",
			"channels":9,
			"idempotency_key":"member-attempt"
		}`),
	)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden ||
		!strings.Contains(response.Body.String(), `"owner_required"`) {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}
