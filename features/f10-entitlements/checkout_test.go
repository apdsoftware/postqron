package entitlements

import (
	"context"
	"errors"
	"testing"
	"time"
)

type ownerStub struct {
	owner bool
	err   error
}

func (stub ownerStub) IsOwner(context.Context, string, string) (bool, error) {
	return stub.owner, stub.err
}

type checkoutProviderStub struct {
	calls   int
	request ProviderCheckoutRequest
	result  CheckoutSession
	err     error
}

func (stub *checkoutProviderStub) CreateCheckout(
	_ context.Context,
	request ProviderCheckoutRequest,
) (CheckoutSession, error) {
	stub.calls++
	stub.request = request
	return stub.result, stub.err
}

type checkoutStoreStub struct {
	registration CheckoutRegistration
	err          error
}

func (stub *checkoutStoreStub) RegisterCheckout(
	_ context.Context,
	registration CheckoutRegistration,
) error {
	stub.registration = registration
	return stub.err
}

func TestCheckoutUsesOwnerAndServerComposedPaddleItems(t *testing.T) {
	expiresAt := time.Date(2026, time.July, 24, 13, 0, 0, 0, time.UTC)
	provider := &checkoutProviderStub{result: CheckoutSession{
		ID:        "txn_00000000000000000000000001",
		URL:       "https://pay.paddle.io/checkout/test",
		ExpiresAt: expiresAt,
	}}
	store := &checkoutStoreStub{}
	service, err := NewCheckoutService(
		ownerStub{owner: true},
		provider,
		store,
		testPaddleCatalog(),
		"https://app.postqron.test/billing/checkout",
	)
	if err != nil {
		t.Fatal(err)
	}
	now := expiresAt.Add(-time.Hour)
	service.now = func() time.Time { return now }
	_, err = service.Create(context.Background(), CheckoutRequest{
		WorkspaceID:    "46c847c5-621f-4c2a-a672-bdfeb2f9aa29",
		AccountID:      "account-1",
		Plan:           PlanTeam,
		Interval:       IntervalAnnual,
		Channels:       25,
		IdempotencyKey: "upgrade-team-annual",
	})
	if err != nil {
		t.Fatal(err)
	}
	if provider.request.CatalogVersion != CatalogVersion ||
		provider.request.Channels != 25 ||
		len(provider.request.Items) != 2 ||
		provider.request.Items[0].Quantity != 10 ||
		provider.request.Items[1].Quantity != 15 {
		t.Fatalf("provider request = %#v", provider.request)
	}
	if store.registration.Plan != PlanTeam ||
		store.registration.Channels != 25 ||
		!SamePaddleItems(store.registration.Items, provider.request.Items) {
		t.Fatalf("registration = %#v", store.registration)
	}
}

func TestCheckoutRejectsFreePlanAndNonOwner(t *testing.T) {
	provider := &checkoutProviderStub{}
	service, err := NewCheckoutService(
		ownerStub{owner: true},
		provider,
		&checkoutStoreStub{},
		testPaddleCatalog(),
		"https://app.postqron.test/checkout",
	)
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.Create(context.Background(), CheckoutRequest{
		WorkspaceID: "workspace", AccountID: "account", Plan: PlanStart,
		Interval: IntervalMonthly, Channels: 3, IdempotencyKey: "free",
	})
	if !errors.Is(err, ErrFreePlan) || provider.calls != 0 {
		t.Fatalf("free checkout = %v, calls %d", err, provider.calls)
	}
	service.authorizer = ownerStub{owner: false}
	_, err = service.Create(context.Background(), CheckoutRequest{
		WorkspaceID: "workspace", AccountID: "account", Plan: PlanPro,
		Interval: IntervalMonthly, Channels: 3, IdempotencyKey: "paid",
	})
	if !errors.Is(err, ErrOwnerRequired) || provider.calls != 0 {
		t.Fatalf("non-owner checkout = %v, calls %d", err, provider.calls)
	}
}

type portalStoreStub struct {
	binding BillingBinding
	err     error
}

func (stub portalStoreStub) BillingBinding(context.Context, string) (BillingBinding, error) {
	return stub.binding, stub.err
}

type portalProviderStub struct {
	calls   int
	request ProviderPortalRequest
	result  PortalSession
}

func (stub *portalProviderStub) CreatePortal(
	_ context.Context,
	request ProviderPortalRequest,
) (PortalSession, error) {
	stub.calls++
	stub.request = request
	return stub.result, nil
}

func TestCustomerPortalIsGeneratedOnDemandFromStoredBinding(t *testing.T) {
	provider := &portalProviderStub{result: PortalSession{
		URL: "https://customer-portal.paddle.com/cpl_test?token=temporary",
	}}
	service := NewPortalService(
		ownerStub{owner: true},
		provider,
		portalStoreStub{binding: BillingBinding{
			CustomerID: "ctm_server", SubscriptionID: "sub_server",
		}},
	)
	first, err := service.Create(context.Background(), PortalRequest{
		WorkspaceID: "workspace", AccountID: "account", IdempotencyKey: "portal-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.Create(context.Background(), PortalRequest{
		WorkspaceID: "workspace", AccountID: "account", IdempotencyKey: "portal-2",
	})
	if err != nil {
		t.Fatal(err)
	}
	if first.URL == "" || second.URL == "" || provider.calls != 2 ||
		provider.request.CustomerID != "ctm_server" ||
		provider.request.SubscriptionID != "sub_server" {
		t.Fatalf("portal calls=%d request=%#v", provider.calls, provider.request)
	}
}
