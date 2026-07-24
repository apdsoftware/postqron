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

type portalStoreStub struct {
	customerID string
	err        error
}

func (stub portalStoreStub) BillingCustomerID(context.Context, string) (string, error) {
	return stub.customerID, stub.err
}

type portalProviderStub struct {
	calls   int
	request ProviderPortalRequest
	result  PortalSession
	err     error
}

func (stub *portalProviderStub) CreatePortal(
	_ context.Context,
	request ProviderPortalRequest,
) (PortalSession, error) {
	stub.calls++
	stub.request = request
	return stub.result, stub.err
}

func (stub *checkoutStoreStub) RegisterCheckout(
	_ context.Context,
	registration CheckoutRegistration,
) error {
	stub.registration = registration
	return stub.err
}

func testPrices() StripePrices {
	return StripePrices{
		{Plan: PlanStart, Interval: IntervalMonthly}: "price_start_monthly",
		{Plan: PlanStart, Interval: IntervalAnnual}:  "price_start_annual",
		{Plan: PlanPro, Interval: IntervalMonthly}:   "price_pro_monthly",
		{Plan: PlanPro, Interval: IntervalAnnual}:    "price_pro_annual",
		{Plan: PlanTeam, Interval: IntervalMonthly}:  "price_team_monthly",
		{Plan: PlanTeam, Interval: IntervalAnnual}:   "price_team_annual",
	}
}

func TestCheckoutUsesOwnerAuthorizationAndServerPrice(t *testing.T) {
	expiresAt := time.Date(2026, time.July, 24, 13, 0, 0, 0, time.UTC)
	provider := &checkoutProviderStub{result: CheckoutSession{
		ID:        "cs_test_123",
		URL:       "https://checkout.stripe.com/c/pay/cs_test_123",
		ExpiresAt: expiresAt,
	}}
	store := &checkoutStoreStub{}
	service, err := NewCheckoutService(
		ownerStub{owner: true},
		provider,
		store,
		testPrices(),
		"https://app.postqron.test/billing/success",
		"https://app.postqron.test/billing/cancel",
	)
	if err != nil {
		t.Fatal(err)
	}
	now := expiresAt.Add(-time.Hour)
	service.now = func() time.Time { return now }

	session, err := service.Create(context.Background(), CheckoutRequest{
		WorkspaceID:    "46c847c5-621f-4c2a-a672-bdfeb2f9aa29",
		AccountID:      "account-1",
		Plan:           PlanTeam,
		Interval:       IntervalAnnual,
		IdempotencyKey: "upgrade-team-annual",
	})
	if err != nil {
		t.Fatal(err)
	}
	if session.ID != "cs_test_123" {
		t.Fatalf("session = %#v", session)
	}
	if provider.request.PriceID != "price_team_annual" ||
		provider.request.IdempotencyKey != "upgrade-team-annual" {
		t.Fatalf("provider request = %#v", provider.request)
	}
	if store.registration.Plan != PlanTeam ||
		store.registration.Interval != IntervalAnnual ||
		!store.registration.CreatedAt.Equal(now) {
		t.Fatalf("registration = %#v", store.registration)
	}
}

func TestCheckoutCannotSelectPrivateEntitlement(t *testing.T) {
	provider := &checkoutProviderStub{}
	service, err := NewCheckoutService(
		ownerStub{owner: true},
		provider,
		&checkoutStoreStub{},
		testPrices(),
		"https://app.postqron.test/success",
		"https://app.postqron.test/cancel",
	)
	if err != nil {
		t.Fatal(err)
	}

	_, err = service.Create(context.Background(), CheckoutRequest{
		WorkspaceID:    "workspace",
		AccountID:      "account",
		Plan:           PlanCode("internal"),
		Interval:       IntervalMonthly,
		IdempotencyKey: "forbidden",
	})
	if !errors.Is(err, ErrUnknownPlan) {
		t.Fatalf("error = %v, want ErrUnknownPlan", err)
	}
	if provider.calls != 0 {
		t.Fatal("provider called for a non-public plan")
	}
}

func TestStripePricesRejectPrivateMapping(t *testing.T) {
	prices := testPrices()
	prices[PriceKey{
		Plan:     PlanCode("internal"),
		Interval: IntervalMonthly,
	}] = "price_private"
	if err := prices.Validate(); err == nil {
		t.Fatal("Stripe price configuration accepted a non-public plan")
	}
}

func TestCheckoutRequiresOwner(t *testing.T) {
	provider := &checkoutProviderStub{}
	service, err := NewCheckoutService(
		ownerStub{owner: false},
		provider,
		&checkoutStoreStub{},
		testPrices(),
		"https://app.postqron.test/success",
		"https://app.postqron.test/cancel",
	)
	if err != nil {
		t.Fatal(err)
	}

	_, err = service.Create(context.Background(), CheckoutRequest{
		WorkspaceID:    "workspace",
		AccountID:      "account",
		Plan:           PlanStart,
		Interval:       IntervalMonthly,
		IdempotencyKey: "start",
	})
	if !errors.Is(err, ErrOwnerRequired) {
		t.Fatalf("error = %v, want ErrOwnerRequired", err)
	}
	if provider.calls != 0 {
		t.Fatal("provider called for a non-owner")
	}
}

func TestCustomerPortalUsesStoredCustomerAndOwnerAuthorization(t *testing.T) {
	provider := &portalProviderStub{result: PortalSession{
		URL: "https://billing.stripe.com/p/session/test",
	}}
	service, err := NewPortalService(
		ownerStub{owner: true},
		provider,
		portalStoreStub{customerID: "cus_server_recorded"},
		"https://app.postqron.test/settings/billing",
	)
	if err != nil {
		t.Fatal(err)
	}
	session, err := service.Create(context.Background(), PortalRequest{
		WorkspaceID:    "46c847c5-621f-4c2a-a672-bdfeb2f9aa29",
		AccountID:      "account-1",
		IdempotencyKey: "portal:account-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if session.URL != "https://billing.stripe.com/p/session/test" {
		t.Fatalf("session = %#v", session)
	}
	if provider.request.CustomerID != "cus_server_recorded" ||
		provider.request.IdempotencyKey != "portal:account-1" {
		t.Fatalf("provider request = %#v", provider.request)
	}
}

func TestCustomerPortalRequiresOwnerBeforeCustomerLookup(t *testing.T) {
	provider := &portalProviderStub{}
	service, err := NewPortalService(
		ownerStub{owner: false},
		provider,
		portalStoreStub{customerID: "cus_hidden"},
		"https://app.postqron.test/settings/billing",
	)
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.Create(context.Background(), PortalRequest{
		WorkspaceID:    "workspace",
		AccountID:      "account",
		IdempotencyKey: "portal",
	})
	if !errors.Is(err, ErrOwnerRequired) {
		t.Fatalf("error = %v, want ErrOwnerRequired", err)
	}
	if provider.calls != 0 {
		t.Fatal("Stripe portal called for a non-owner")
	}
}
