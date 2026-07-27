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
	Channels       *int64
	IdempotencyKey string
}

type ProviderCheckoutRequest struct {
	WorkspaceID    string
	Items          []PaddleItem
	CheckoutURL    string
	CatalogVersion string
	Plan           PlanCode
	Interval       BillingInterval
	Channels       *int64
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
	SessionID      string
	WorkspaceID    string
	Plan           PlanCode
	Interval       BillingInterval
	Channels       *int64
	CatalogVersion string
	Items          []PaddleItem
	ExpiresAt      time.Time
	CreatedAt      time.Time
}

type CheckoutStore interface {
	RegisterCheckout(context.Context, CheckoutRegistration) error
}

type CheckoutService struct {
	authorizer  OwnerAuthorizer
	provider    CheckoutProvider
	store       CheckoutStore
	catalog     PaddleCatalog
	checkoutURL string
	now         func() time.Time
}

var (
	ErrOwnerRequired       = errors.New("workspace owner permission is required")
	ErrInvalidReturnURL    = errors.New("billing URL must be absolute HTTPS")
	ErrInvalidCheckout     = errors.New("invalid Paddle transaction")
	ErrUnknownSubscription = errors.New("unknown Paddle subscription")
	ErrEventConflict       = errors.New("provider event conflicts with recorded data")
)

func NewCheckoutService(
	authorizer OwnerAuthorizer,
	provider CheckoutProvider,
	store CheckoutStore,
	catalog PaddleCatalog,
	checkoutURL string,
) (*CheckoutService, error) {
	if !absoluteHTTPS(checkoutURL) {
		return nil, ErrInvalidReturnURL
	}
	if err := catalog.Validate(); err != nil {
		return nil, err
	}
	return &CheckoutService{
		authorizer:  authorizer,
		provider:    provider,
		store:       store,
		catalog:     catalog,
		checkoutURL: checkoutURL,
		now:         time.Now,
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
	plan, err := PublicPlanByCode(request.Plan)
	if err != nil {
		return CheckoutSession{}, err
	}
	if !plan.Purchasable {
		return CheckoutSession{}, ErrFreePlan
	}
	if !validInterval(request.Interval) {
		return CheckoutSession{}, ErrInvalidInterval
	}
	if err := validateChannelQuantity(plan, request.Channels); err != nil {
		return CheckoutSession{}, err
	}
	owner, err := service.authorizer.IsOwner(ctx, request.WorkspaceID, request.AccountID)
	if err != nil {
		return CheckoutSession{}, fmt.Errorf("authorize billing owner: %w", err)
	}
	if !owner {
		return CheckoutSession{}, ErrOwnerRequired
	}
	items, err := service.catalog.ExpectedItems(request.Plan, request.Interval, request.Channels)
	if err != nil {
		return CheckoutSession{}, err
	}
	session, err := service.provider.CreateCheckout(ctx, ProviderCheckoutRequest{
		WorkspaceID:    request.WorkspaceID,
		Items:          items,
		CheckoutURL:    service.checkoutURL,
		CatalogVersion: CatalogVersion,
		Plan:           request.Plan,
		Interval:       request.Interval,
		Channels:       request.Channels,
		IdempotencyKey: request.IdempotencyKey,
	})
	if err != nil {
		return CheckoutSession{}, fmt.Errorf("create Paddle transaction: %w", err)
	}
	if session.ID == "" || !absoluteHTTPS(session.URL) || session.ExpiresAt.IsZero() {
		return CheckoutSession{}, ErrInvalidCheckout
	}
	if err := service.store.RegisterCheckout(ctx, CheckoutRegistration{
		SessionID:      session.ID,
		WorkspaceID:    request.WorkspaceID,
		Plan:           request.Plan,
		Interval:       request.Interval,
		Channels:       request.Channels,
		CatalogVersion: CatalogVersion,
		Items:          items,
		ExpiresAt:      session.ExpiresAt.UTC(),
		CreatedAt:      service.now().UTC(),
	}); err != nil {
		return CheckoutSession{}, fmt.Errorf("register Paddle transaction: %w", err)
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
	Channels       *int64
	CustomerID     string
	SubscriptionID string
	TransactionID  string
	ExpectedItems  []PaddleItem
	Period         Period
}

type BillingEvent struct {
	ID              string
	Type            string
	OccurredAt      time.Time
	WorkspaceID     string
	Plan            PlanCode
	Interval        BillingInterval
	Channels        *int64
	State           BillingState
	CustomerID      string
	SubscriptionID  string
	TransactionID   string
	Period          Period
	ApplyState      bool
	PaymentFailedAt *time.Time
}

type BillingEventResult struct {
	FirstDelivery bool
	StateChanged  bool
}

type BillingStore interface {
	ResolveTransaction(context.Context, string) (BillingBinding, error)
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
	SubscriptionID string
	IdempotencyKey string
}

type PortalSession struct {
	URL string `json:"url"`
}

type PortalProvider interface {
	CreatePortal(context.Context, ProviderPortalRequest) (PortalSession, error)
}

type PortalStore interface {
	BillingBinding(context.Context, string) (BillingBinding, error)
}

type PortalService struct {
	authorizer OwnerAuthorizer
	provider   PortalProvider
	store      PortalStore
}

func NewPortalService(
	authorizer OwnerAuthorizer,
	provider PortalProvider,
	store PortalStore,
) *PortalService {
	return &PortalService{
		authorizer: authorizer,
		provider:   provider,
		store:      store,
	}
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
	binding, err := service.store.BillingBinding(ctx, request.WorkspaceID)
	if err != nil {
		return PortalSession{}, err
	}
	session, err := service.provider.CreatePortal(ctx, ProviderPortalRequest{
		CustomerID:     binding.CustomerID,
		SubscriptionID: binding.SubscriptionID,
		IdempotencyKey: request.IdempotencyKey,
	})
	if err != nil {
		return PortalSession{}, fmt.Errorf("create Paddle customer portal session: %w", err)
	}
	if !absoluteHTTPS(session.URL) {
		return PortalSession{}, ErrInvalidCheckout
	}
	return session, nil
}
