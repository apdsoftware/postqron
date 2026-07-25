package entitlements

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func testAPIKey(environment PaddleEnvironment) string {
	prefix := "pdl_sdbx_apikey_"
	if environment == PaddleProduction {
		prefix = "pdl_live_apikey_"
	}
	return prefix + strings.Repeat("a", 26) + "_" +
		strings.Repeat("B", 22) + "_" + strings.Repeat("c", 3)
}

func TestPaddleConfigValidatesEnvironmentAndNeverAcceptsDrift(t *testing.T) {
	config := PaddleConfig{
		Environment: PaddleSandbox, APIKey: testAPIKey(PaddleSandbox),
		WebhookSecret: "endpoint_secret", Catalog: testPaddleCatalog(),
	}
	if err := config.Validate(); err != nil {
		t.Fatal(err)
	}
	config.Environment = PaddleProduction
	if err := config.Validate(); err == nil {
		t.Fatal("sandbox key was accepted for production")
	}
	config.Environment = PaddleSandbox
	key := PaddlePriceKey{Plan: PlanPro, Interval: IntervalMonthly, Tier: TierOne}
	mapping := config.Catalog[key]
	mapping.UnitAmountCents++
	config.Catalog[key] = mapping
	if err := config.Validate(); err == nil {
		t.Fatal("catalog amount drift was accepted")
	}
}

func TestPaddleClientUsesVersionedServerAPIAndComposedItems(t *testing.T) {
	var received map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		request *http.Request,
	) {
		if request.URL.Path != "/transactions" ||
			request.Header.Get("Paddle-Version") != "1" ||
			!strings.HasPrefix(request.Header.Get("Authorization"), "Bearer pdl_sdbx_") {
			t.Errorf("request = %s headers=%v", request.URL.Path, request.Header)
		}
		body, _ := io.ReadAll(request.Body)
		if err := json.Unmarshal(body, &received); err != nil {
			t.Error(err)
		}
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(http.StatusCreated)
		_, _ = writer.Write([]byte(`{
			"data": {
				"id": "txn_00000000000000000000000001",
				"checkout": {"url": "https://pay.paddle.io/test"}
			}
		}`))
	}))
	defer server.Close()
	config := PaddleConfig{
		Environment: PaddleSandbox, APIKey: testAPIKey(PaddleSandbox),
		WebhookSecret: "endpoint_secret", Catalog: testPaddleCatalog(),
	}
	client, err := NewPaddleClient(config, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	client.apiBase = server.URL
	client.now = func() time.Time {
		return time.Date(2026, time.July, 24, 12, 0, 0, 0, time.UTC)
	}
	session, err := client.CreateCheckout(context.Background(), ProviderCheckoutRequest{
		WorkspaceID:    "workspace",
		Items:          []PaddleItem{{PriceID: "pri_test", Quantity: 10}},
		CheckoutURL:    "https://app.postqron.test/checkout",
		CatalogVersion: CatalogVersion,
		Plan:           PlanPro, Interval: IntervalMonthly, Channels: 10,
		IdempotencyKey: "checkout-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if session.ID == "" || session.URL != "https://pay.paddle.io/test" ||
		session.ExpiresAt.IsZero() {
		t.Fatalf("session = %#v", session)
	}
	custom := received["custom_data"].(map[string]any)
	if custom["postqron_workspace_id"] != "workspace" ||
		custom["catalog_version"] != CatalogVersion {
		t.Fatalf("custom data = %#v", custom)
	}
	if _, exists := received["api_key"]; exists {
		t.Fatal("request body exposed API key")
	}
}

type changeProviderStub struct {
	change   ProviderSubscriptionChange
	canceled string
}

func (stub *changeProviderStub) PreviewSubscriptionChange(
	_ context.Context,
	change ProviderSubscriptionChange,
) (json.RawMessage, error) {
	stub.change = change
	return json.RawMessage(`{"data":{"immediate_transaction":null}}`), nil
}

func (stub *changeProviderStub) UpdateSubscription(
	_ context.Context,
	change ProviderSubscriptionChange,
) error {
	stub.change = change
	return nil
}

func (stub *changeProviderStub) CancelSubscription(
	_ context.Context,
	subscriptionID string,
	_ string,
) error {
	stub.canceled = subscriptionID
	return nil
}

func TestSubscriptionChangesApplyD07ProrationAndCancellation(t *testing.T) {
	provider := &changeProviderStub{}
	store := portalStoreStub{binding: BillingBinding{
		Plan: PlanPro, Interval: IntervalMonthly, Channels: 10,
		SubscriptionID: "sub_server",
	}}
	service, err := NewSubscriptionChangeService(
		ownerStub{owner: true}, provider, store, testPaddleCatalog(),
	)
	if err != nil {
		t.Fatal(err)
	}
	upgrade := SubscriptionChangeRequest{
		WorkspaceID: "workspace", AccountID: "owner", Plan: PlanTeam,
		Interval: IntervalMonthly, Channels: 10, IdempotencyKey: "upgrade",
	}
	preview, err := service.Preview(context.Background(), upgrade)
	if err != nil {
		t.Fatal(err)
	}
	if preview.Direction != ChangeUpgrade || !preview.Immediate ||
		provider.change.ProrationMode != "prorated_immediately" ||
		provider.change.OnPaymentFailure != "prevent_change" {
		t.Fatalf("upgrade preview=%#v change=%#v", preview, provider.change)
	}
	store.binding = BillingBinding{
		Plan: PlanTeam, Interval: IntervalAnnual, Channels: 25,
		SubscriptionID: "sub_server",
	}
	service.store = store
	downgrade := SubscriptionChangeRequest{
		WorkspaceID: "workspace", AccountID: "owner", Plan: PlanPro,
		Interval: IntervalAnnual, Channels: 25, IdempotencyKey: "downgrade",
	}
	if err := service.Apply(context.Background(), downgrade); err != nil {
		t.Fatal(err)
	}
	if provider.change.ProrationMode != "do_not_bill" {
		t.Fatalf("downgrade change = %#v", provider.change)
	}
	if err := service.Cancel(
		context.Background(), "workspace", "owner", "cancel",
	); err != nil {
		t.Fatal(err)
	}
	if provider.canceled != "sub_server" {
		t.Fatalf("canceled %q", provider.canceled)
	}
}
