package entitlements

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"time"
)

type OwnerAuthorizer interface {
	IsOwner(context.Context, string, string) (bool, error)
}

type CheckoutRequest struct {
	WorkspaceID    string
	AccountID      string
	Plan           PlanCode
	Interval       BillingInterval
	IdempotencyKey string
}

type ProviderCheckoutRequest struct {
	WorkspaceID    string
	PriceID        string
	SuccessURL     string
	CancelURL      string
	IdempotencyKey string
}

type CheckoutSession struct {
	ID        string    `json:"id"`
	URL       string    `json:"url"`
	ExpiresAt time.Time `json:"expires_at"`
}

type CheckoutProvider interface {
	CreateCheckout(context.Context, ProviderCheckoutRequest) (CheckoutSession, error)
}

type CheckoutRegistration struct {
	SessionID   string
	WorkspaceID string
	Plan        PlanCode
	Interval    BillingInterval
	ExpiresAt   time.Time
	CreatedAt   time.Time
}

type CheckoutStore interface {
	RegisterCheckout(context.Context, CheckoutRegistration) error
}

type CheckoutService struct {
	authorizer OwnerAuthorizer
	provider   CheckoutProvider
	store      CheckoutStore
	prices     StripePrices
	successURL string
	cancelURL  string
	now        func() time.Time
}

var (
	ErrOwnerRequired       = errors.New("workspace owner permission is required")
	ErrInvalidReturnURL    = errors.New("checkout return URL must be absolute HTTPS")
	ErrMissingStripePrice  = errors.New("Stripe price is not configured")
	ErrInvalidCheckout     = errors.New("invalid checkout session")
	ErrUnknownSubscription = errors.New("unknown Stripe subscription")
	ErrEventConflict       = errors.New("provider event conflicts with recorded data")
)

func NewCheckoutService(
	authorizer OwnerAuthorizer,
	provider CheckoutProvider,
	store CheckoutStore,
	prices StripePrices,
	successURL string,
	cancelURL string,
) (*CheckoutService, error) {
	if !absoluteHTTPS(successURL) || !absoluteHTTPS(cancelURL) {
		return nil, ErrInvalidReturnURL
	}
	if err := prices.Validate(); err != nil {
		return nil, err
	}
	return &CheckoutService{
		authorizer: authorizer,
		provider:   provider,
		store:      store,
		prices:     prices,
		successURL: successURL,
		cancelURL:  cancelURL,
		now:        time.Now,
	}, nil
}

func (service *CheckoutService) Create(
	ctx context.Context,
	request CheckoutRequest,
) (CheckoutSession, error) {
	if request.WorkspaceID == "" {
		return CheckoutSession{}, ErrInvalidWorkspace
	}
	if request.AccountID == "" {
		return CheckoutSession{}, ErrOwnerRequired
	}
	if request.IdempotencyKey == "" || len(request.IdempotencyKey) > 255 {
		return CheckoutSession{}, ErrInvalidIdempotencyKey
	}
	if _, err := PublicPlanByCode(request.Plan); err != nil {
		return CheckoutSession{}, err
	}
	if !validInterval(request.Interval) {
		return CheckoutSession{}, ErrInvalidInterval
	}
	owner, err := service.authorizer.IsOwner(ctx, request.WorkspaceID, request.AccountID)
	if err != nil {
		return CheckoutSession{}, fmt.Errorf("authorize billing owner: %w", err)
	}
	if !owner {
		return CheckoutSession{}, ErrOwnerRequired
	}

	priceID, ok := service.prices.PriceID(request.Plan, request.Interval)
	if !ok {
		return CheckoutSession{}, ErrMissingStripePrice
	}
	session, err := service.provider.CreateCheckout(ctx, ProviderCheckoutRequest{
		WorkspaceID:    request.WorkspaceID,
		PriceID:        priceID,
		SuccessURL:     service.successURL,
		CancelURL:      service.cancelURL,
		IdempotencyKey: request.IdempotencyKey,
	})
	if err != nil {
		return CheckoutSession{}, fmt.Errorf("create Stripe Checkout session: %w", err)
	}
	if session.ID == "" || !absoluteHTTPS(session.URL) || session.ExpiresAt.IsZero() {
		return CheckoutSession{}, ErrInvalidCheckout
	}
	if err := service.store.RegisterCheckout(ctx, CheckoutRegistration{
		SessionID:   session.ID,
		WorkspaceID: request.WorkspaceID,
		Plan:        request.Plan,
		Interval:    request.Interval,
		ExpiresAt:   session.ExpiresAt.UTC(),
		CreatedAt:   service.now().UTC(),
	}); err != nil {
		return CheckoutSession{}, fmt.Errorf("register Stripe Checkout session: %w", err)
	}
	return session, nil
}

func absoluteHTTPS(value string) bool {
	parsed, err := url.Parse(value)
	return err == nil && parsed.Scheme == "https" && parsed.Host != ""
}

type BillingBinding struct {
	WorkspaceID    string
	Plan           PlanCode
	Interval       BillingInterval
	CustomerID     string
	SubscriptionID string
	Period         Period
}

type BillingEvent struct {
	ID             string
	Type           string
	CreatedAt      time.Time
	WorkspaceID    string
	Plan           PlanCode
	Interval       BillingInterval
	State          BillingState
	CustomerID     string
	SubscriptionID string
	Period         Period
}

type BillingEventResult struct {
	FirstDelivery bool
	StateChanged  bool
}

type BillingStore interface {
	CompleteCheckout(
		context.Context,
		string,
		time.Time,
		string,
		string,
		string,
	) (bool, error)
	ResolveSubscription(context.Context, string) (BillingBinding, error)
	ApplyBillingEvent(context.Context, BillingEvent) (BillingEventResult, error)
}

type PortalRequest struct {
	WorkspaceID    string
	AccountID      string
	IdempotencyKey string
}

type ProviderPortalRequest struct {
	CustomerID     string
	ReturnURL      string
	IdempotencyKey string
}

type PortalSession struct {
	URL string `json:"url"`
}

type PortalProvider interface {
	CreatePortal(context.Context, ProviderPortalRequest) (PortalSession, error)
}

type PortalStore interface {
	BillingCustomerID(context.Context, string) (string, error)
}

type PortalService struct {
	authorizer OwnerAuthorizer
	provider   PortalProvider
	store      PortalStore
	returnURL  string
}

func NewPortalService(
	authorizer OwnerAuthorizer,
	provider PortalProvider,
	store PortalStore,
	returnURL string,
) (*PortalService, error) {
	if !absoluteHTTPS(returnURL) {
		return nil, ErrInvalidReturnURL
	}
	return &PortalService{
		authorizer: authorizer,
		provider:   provider,
		store:      store,
		returnURL:  returnURL,
	}, nil
}

func (service *PortalService) Create(
	ctx context.Context,
	request PortalRequest,
) (PortalSession, error) {
	if request.WorkspaceID == "" {
		return PortalSession{}, ErrInvalidWorkspace
	}
	if request.AccountID == "" {
		return PortalSession{}, ErrOwnerRequired
	}
	if request.IdempotencyKey == "" || len(request.IdempotencyKey) > 255 {
		return PortalSession{}, ErrInvalidIdempotencyKey
	}
	owner, err := service.authorizer.IsOwner(ctx, request.WorkspaceID, request.AccountID)
	if err != nil {
		return PortalSession{}, fmt.Errorf("authorize billing owner: %w", err)
	}
	if !owner {
		return PortalSession{}, ErrOwnerRequired
	}
	customerID, err := service.store.BillingCustomerID(ctx, request.WorkspaceID)
	if err != nil {
		return PortalSession{}, err
	}
	session, err := service.provider.CreatePortal(ctx, ProviderPortalRequest{
		CustomerID:     customerID,
		ReturnURL:      service.returnURL,
		IdempotencyKey: request.IdempotencyKey,
	})
	if err != nil {
		return PortalSession{}, fmt.Errorf("create Stripe Customer Portal session: %w", err)
	}
	if !absoluteHTTPS(session.URL) {
		return PortalSession{}, ErrInvalidCheckout
	}
	return session, nil
}
