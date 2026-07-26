package entitlements

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
