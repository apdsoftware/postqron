package entitlements

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

type ChangeDirection string

const (
	ChangeUpgrade   ChangeDirection = "upgrade"
	ChangeDowngrade ChangeDirection = "downgrade"
)

type SubscriptionChangeRequest struct {
	WorkspaceID    string
	AccountID      string
	Plan           PlanCode
	Interval       BillingInterval
	Channels       *int64
	IdempotencyKey string
}

type ProviderSubscriptionChange struct {
	SubscriptionID   string
	Items            []PaddleItem
	ProrationMode    string
	OnPaymentFailure string
	IdempotencyKey   string
}

type SubscriptionChangePreview struct {
	Direction ChangeDirection `json:"direction"`
	Immediate bool            `json:"immediate"`
	Raw       json.RawMessage `json:"provider_preview"`
}

type SubscriptionChangeProvider interface {
	PreviewSubscriptionChange(
		context.Context,
		ProviderSubscriptionChange,
	) (json.RawMessage, error)
	UpdateSubscription(
		context.Context,
		ProviderSubscriptionChange,
	) error
	CancelSubscription(context.Context, string, string) error
}

type SubscriptionBindingStore interface {
	BillingBinding(context.Context, string) (BillingBinding, error)
}

type SubscriptionChangeService struct {
	authorizer OwnerAuthorizer
	provider   SubscriptionChangeProvider
	store      SubscriptionBindingStore
	catalog    PaddleCatalog
}

var ErrMixedSubscriptionChange = errors.New(
	"changes that combine upgrade and downgrade dimensions must be split",
)

func NewSubscriptionChangeService(
	authorizer OwnerAuthorizer,
	provider SubscriptionChangeProvider,
	store SubscriptionBindingStore,
	catalog PaddleCatalog,
) (*SubscriptionChangeService, error) {
	if err := catalog.Validate(); err != nil {
		return nil, err
	}
	return &SubscriptionChangeService{
		authorizer: authorizer,
		provider:   provider,
		store:      store,
		catalog:    catalog,
	}, nil
}

func (service *SubscriptionChangeService) Preview(
	ctx context.Context,
	request SubscriptionChangeRequest,
) (SubscriptionChangePreview, error) {
	change, direction, err := service.prepare(ctx, request)
	if err != nil {
		return SubscriptionChangePreview{}, err
	}
	raw, err := service.provider.PreviewSubscriptionChange(ctx, change)
	if err != nil {
		return SubscriptionChangePreview{}, fmt.Errorf("preview Paddle subscription change: %w", err)
	}
	return SubscriptionChangePreview{
		Direction: direction,
		Immediate: direction == ChangeUpgrade,
		Raw:       raw,
	}, nil
}

func (service *SubscriptionChangeService) Apply(
	ctx context.Context,
	request SubscriptionChangeRequest,
) error {
	change, _, err := service.prepare(ctx, request)
	if err != nil {
		return err
	}
	if err := service.provider.UpdateSubscription(ctx, change); err != nil {
		return fmt.Errorf("update Paddle subscription: %w", err)
	}
	return nil
}

func (service *SubscriptionChangeService) Cancel(
	ctx context.Context,
	workspaceID string,
	accountID string,
	idempotencyKey string,
) error {
	if workspaceID == "" {
		return ErrInvalidWorkspace
	}
	if accountID == "" {
		return ErrOwnerRequired
	}
	if idempotencyKey == "" || len(idempotencyKey) > 255 {
		return ErrInvalidIdempotencyKey
	}
	owner, err := service.authorizer.IsOwner(ctx, workspaceID, accountID)
	if err != nil {
		return fmt.Errorf("authorize billing owner: %w", err)
	}
	if !owner {
		return ErrOwnerRequired
	}
	binding, err := service.store.BillingBinding(ctx, workspaceID)
	if err != nil {
		return err
	}
	if err := service.provider.CancelSubscription(
		ctx,
		binding.SubscriptionID,
		idempotencyKey,
	); err != nil {
		return fmt.Errorf("cancel Paddle subscription: %w", err)
	}
	return nil
}

func (service *SubscriptionChangeService) prepare(
	ctx context.Context,
	request SubscriptionChangeRequest,
) (ProviderSubscriptionChange, ChangeDirection, error) {
	if request.WorkspaceID == "" {
		return ProviderSubscriptionChange{}, "", ErrInvalidWorkspace
	}
	if request.AccountID == "" {
		return ProviderSubscriptionChange{}, "", ErrOwnerRequired
	}
	if request.IdempotencyKey == "" || len(request.IdempotencyKey) > 255 {
		return ProviderSubscriptionChange{}, "", ErrInvalidIdempotencyKey
	}
	target, err := PublicPlanByCode(request.Plan)
	if err != nil {
		return ProviderSubscriptionChange{}, "", err
	}
	if !target.Purchasable {
		return ProviderSubscriptionChange{}, "", ErrFreePlan
	}
	if !validInterval(request.Interval) {
		return ProviderSubscriptionChange{}, "", ErrInvalidInterval
	}
	if err := validateChannelQuantity(target, request.Channels); err != nil {
		return ProviderSubscriptionChange{}, "", err
	}
	owner, err := service.authorizer.IsOwner(ctx, request.WorkspaceID, request.AccountID)
	if err != nil {
		return ProviderSubscriptionChange{}, "", fmt.Errorf("authorize billing owner: %w", err)
	}
	if !owner {
		return ProviderSubscriptionChange{}, "", ErrOwnerRequired
	}
	current, err := service.store.BillingBinding(ctx, request.WorkspaceID)
	if err != nil {
		return ProviderSubscriptionChange{}, "", err
	}
	direction, err := classifyChange(current, request)
	if err != nil {
		return ProviderSubscriptionChange{}, "", err
	}
	items, err := service.catalog.ExpectedItems(request.Plan, request.Interval, request.Channels)
	if err != nil {
		return ProviderSubscriptionChange{}, "", err
	}
	mode := "prorated_immediately"
	if direction == ChangeDowngrade {
		// Paddle receives the future recurring items without issuing a credit.
		// The local entitlement remains unchanged until the next verified
		// transaction.completed event contains this exact item list.
		mode = "do_not_bill"
	}
	return ProviderSubscriptionChange{
		SubscriptionID:   current.SubscriptionID,
		Items:            items,
		ProrationMode:    mode,
		OnPaymentFailure: "prevent_change",
		IdempotencyKey:   request.IdempotencyKey,
	}, direction, nil
}

func classifyChange(
	current BillingBinding,
	target SubscriptionChangeRequest,
) (ChangeDirection, error) {
	var upgrades, downgrades int
	if current.Plan != target.Plan {
		currentRank, currentOK := paidPlanRank(current.Plan)
		targetRank, targetOK := paidPlanRank(target.Plan)
		if !currentOK || !targetOK {
			return "", ErrUnknownPlan
		}
		if currentRank < targetRank {
			upgrades++
		} else {
			downgrades++
		}
	}
	if current.Interval != target.Interval {
		if current.Interval == IntervalMonthly && target.Interval == IntervalAnnual {
			upgrades++
		} else {
			downgrades++
		}
	}
	if current.Channels != nil && target.Channels != nil {
		if *current.Channels < *target.Channels {
			upgrades++
		} else if *current.Channels > *target.Channels {
			downgrades++
		}
	} else if current.Plan == target.Plan {
		if current.Plan != PlanUnlimited ||
			current.Channels != nil ||
			target.Channels != nil {
			return "", ErrEventConflict
		}
	}
	if upgrades > 0 && downgrades > 0 {
		return "", ErrMixedSubscriptionChange
	}
	if downgrades > 0 {
		return ChangeDowngrade, nil
	}
	return ChangeUpgrade, nil
}

func paidPlanRank(plan PlanCode) (int, bool) {
	switch plan {
	case PlanPro:
		return 1, true
	case PlanTeam:
		return 2, true
	case PlanUnlimited:
		return 3, true
	default:
		return 0, false
	}
}
