package entitlements

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
)

func loadTestPaddleManifest(t *testing.T) PaddleCatalogManifest {
	t.Helper()
	file, err := os.Open("../../infra/paddle/catalog-d09-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	manifest, err := DecodePaddleCatalogManifest(file)
	if err != nil {
		t.Fatal(err)
	}
	return manifest
}

func TestPaddleCatalogManifestExactlyMatchesD09(t *testing.T) {
	manifest := loadTestPaddleManifest(t)
	if manifest.Version != PaddleCatalogVersion || len(manifest.Products) != 3 {
		t.Fatalf("manifest = %#v", manifest)
	}
	manifest.Products[2].Prices[0].UnitAmountCents++
	if err := manifest.Validate(); err == nil {
		t.Fatal("manifest drift was accepted")
	}
	if _, err := DecodePaddleCatalogManifest(strings.NewReader(
		`{"version":"d09-v1","unexpected":true}`,
	)); err == nil {
		t.Fatal("unknown manifest field was accepted")
	}
}

type fakePaddleCatalogAPI struct {
	t         *testing.T
	mu        sync.Mutex
	products  []map[string]any
	prices    []map[string]any
	mutations int
}

func (api *fakePaddleCatalogAPI) ServeHTTP(
	writer http.ResponseWriter,
	request *http.Request,
) {
	api.mu.Lock()
	defer api.mu.Unlock()
	if request.Header.Get("Paddle-Version") != "1" ||
		!strings.HasPrefix(request.Header.Get("Authorization"), "Bearer pdl_sdbx_") {
		api.t.Errorf("unexpected Paddle headers")
	}
	writer.Header().Set("Content-Type", "application/json")
	switch {
	case request.Method == http.MethodGet && request.URL.Path == "/products":
		writeFakePaddleList(api.t, writer, api.products)
	case request.Method == http.MethodPost && request.URL.Path == "/products":
		product := decodeFakePaddlePayload(api.t, request)
		product["id"] = fmt.Sprintf("pro_%026d", len(api.products)+1)
		product["status"] = "active"
		api.products = append(api.products, product)
		api.mutations++
		writeFakePaddleEntity(api.t, writer, http.StatusCreated, product)
	case request.Method == http.MethodGet && request.URL.Path == "/prices":
		productID := request.URL.Query().Get("product_id")
		var matches []map[string]any
		for _, price := range api.prices {
			if price["product_id"] == productID {
				matches = append(matches, price)
			}
		}
		writeFakePaddleList(api.t, writer, matches)
	case request.Method == http.MethodPost && request.URL.Path == "/prices":
		price := decodeFakePaddlePayload(api.t, request)
		price["id"] = fmt.Sprintf("pri_%026d", len(api.prices)+1)
		price["status"] = "active"
		price["trial_period"] = nil
		api.prices = append(api.prices, price)
		api.mutations++
		writeFakePaddleEntity(api.t, writer, http.StatusCreated, price)
	default:
		http.Error(writer, "not found", http.StatusNotFound)
	}
}

func decodeFakePaddlePayload(t *testing.T, request *http.Request) map[string]any {
	t.Helper()
	var payload map[string]any
	if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	return payload
}

func writeFakePaddleList(
	t *testing.T,
	writer http.ResponseWriter,
	data []map[string]any,
) {
	t.Helper()
	if err := json.NewEncoder(writer).Encode(map[string]any{
		"data": data,
		"meta": map[string]any{
			"pagination": map[string]any{"has_more": false, "next": nil},
		},
	}); err != nil {
		t.Fatal(err)
	}
}

func writeFakePaddleEntity(
	t *testing.T,
	writer http.ResponseWriter,
	status int,
	data map[string]any,
) {
	t.Helper()
	writer.WriteHeader(status)
	if err := json.NewEncoder(writer).Encode(map[string]any{"data": data}); err != nil {
		t.Fatal(err)
	}
}

func TestPaddleCatalogProvisionPlansAppliesAndReusesWithoutInventedIDs(t *testing.T) {
	manifest := loadTestPaddleManifest(t)
	fakeAPI := &fakePaddleCatalogAPI{t: t}
	server := httptest.NewServer(fakeAPI)
	defer server.Close()
	client, err := NewPaddleCatalogClient(
		PaddleSandbox,
		testAPIKey(PaddleSandbox),
		server.Client(),
	)
	if err != nil {
		t.Fatal(err)
	}
	client.apiBase = server.URL

	plan, err := client.ProvisionCatalog(context.Background(), manifest, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Actions) != 17 || len(plan.Catalog) != 0 || fakeAPI.mutations != 0 {
		t.Fatalf(
			"plan actions=%d catalog=%d mutations=%d",
			len(plan.Actions),
			len(plan.Catalog),
			fakeAPI.mutations,
		)
	}
	for _, action := range plan.Actions {
		if action.Action != "create" {
			t.Fatalf("plan action = %#v", action)
		}
	}

	applied, err := client.ProvisionCatalog(context.Background(), manifest, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(applied.Catalog) != 14 || fakeAPI.mutations != 17 {
		t.Fatalf("applied catalog=%d mutations=%d", len(applied.Catalog), fakeAPI.mutations)
	}
	if err := applied.Catalog.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, mapping := range applied.Catalog {
		if !paddleIDPattern.MatchString(mapping.ProductID) ||
			!paddleIDPattern.MatchString(mapping.PriceID) {
			t.Fatalf("mapping contains a non-provider ID: %#v", mapping)
		}
	}

	reused, err := client.ProvisionCatalog(context.Background(), manifest, true)
	if err != nil {
		t.Fatal(err)
	}
	if fakeAPI.mutations != 17 || len(reused.Actions) != 17 {
		t.Fatalf("rerun mutated catalog: mutations=%d", fakeAPI.mutations)
	}
	for _, action := range reused.Actions {
		if action.Action != "reuse" {
			t.Fatalf("rerun action = %#v", action)
		}
	}
	var report bytes.Buffer
	if err := WriteCatalogProvisionReport(&report, reused); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(report.String(), testAPIKey(PaddleSandbox)) ||
		strings.Contains(report.String(), "pri_") ||
		strings.Contains(report.String(), "pro_") {
		t.Fatalf("provision report exposed credentials or IDs: %s", report.String())
	}
	var mapping bytes.Buffer
	if err := WritePaddleCatalogMapping(&mapping, reused.Catalog); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(mapping.String(), testAPIKey(PaddleSandbox)) {
		t.Fatal("runtime mapping exposed API key")
	}
}

func TestCatalogDryRunValidatesTaxModeTrialAndQuantity(t *testing.T) {
	catalog := testPaddleCatalog()
	wrongQuantity := false
	server := httptest.NewServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		request *http.Request,
	) {
		priceID := strings.TrimPrefix(request.URL.Path, "/prices/")
		var key PaddlePriceKey
		var mapping PaddlePriceMapping
		found := false
		for candidateKey, candidate := range catalog {
			if candidate.PriceID == priceID {
				key, mapping, found = candidateKey, candidate, true
				break
			}
		}
		if !found {
			http.NotFound(writer, request)
			return
		}
		minimum, maximum, _ := ExpectedPaddleQuantity(key)
		if wrongQuantity && key.Plan == PlanPro &&
			key.Interval == IntervalMonthly && key.Tier == TierOne {
			maximum++
		}
		interval := "month"
		if key.Interval == IntervalAnnual {
			interval = "year"
		}
		_ = json.NewEncoder(writer).Encode(map[string]any{
			"data": map[string]any{
				"id":         mapping.PriceID,
				"product_id": mapping.ProductID,
				"status":     "active",
				"tax_mode":   "location",
				"unit_price": map[string]any{
					"amount":        fmt.Sprintf("%d", mapping.UnitAmountCents),
					"currency_code": "EUR",
				},
				"billing_cycle": map[string]any{"interval": interval, "frequency": 1},
				"trial_period":  nil,
				"quantity": map[string]any{
					"minimum": minimum,
					"maximum": maximum,
				},
			},
		})
	}))
	defer server.Close()
	config := PaddleConfig{
		Environment:   PaddleSandbox,
		APIKey:        testAPIKey(PaddleSandbox),
		WebhookSecret: "endpoint_secret",
		Catalog:       catalog,
	}
	client, err := NewPaddleClient(config, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	client.apiBase = server.URL
	checks, err := client.DryRunCatalog(context.Background(), catalog)
	if err != nil {
		t.Fatal(err)
	}
	for _, check := range checks {
		if !check.OK {
			t.Fatalf("dry-run check = %#v", check)
		}
	}
	wrongQuantity = true
	checks, err = client.DryRunCatalog(context.Background(), catalog)
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err := WriteCatalogDryRun(&output, checks); err == nil ||
		!strings.Contains(output.String(), "quantity limits differ") {
		t.Fatalf("quantity drift was not rejected: %s", output.String())
	}
}
