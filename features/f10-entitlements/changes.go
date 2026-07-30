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

type ChangeAction string

const (
	ChangeSubscription ChangeAction = "update_subscription"
	ChangeCancel       ChangeAction = "cancel_subscription"
)

type ChangeStatus string

const (
	ChangeDispatching    ChangeStatus = "dispatching"
	ChangePending        ChangeStatus = "pending"
	ChangeApplied        ChangeStatus = "applied"
	ChangeProviderFailed ChangeStatus = "provider_failed"
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

type SubscriptionChangeTarget struct {
	Plan     PlanCode        `json:"plan"`
	Interval BillingInterval `json:"interval"`
	Channels *int64          `json:"channels"`
}

type SubscriptionChangePreview struct {
	Direction ChangeDirection          `json:"direction"`
	Action    ChangeAction             `json:"action"`
	Immediate bool                     `json:"immediate"`
	Target    SubscriptionChangeTarget `json:"target"`
	Raw       json.RawMessage          `json:"provider_preview"`
}

type SubscriptionChangeResult struct {
	Status         ChangeStatus             `json:"status"`
	Direction      ChangeDirection          `json:"direction"`
	Action         ChangeAction             `json:"action"`
	Target         SubscriptionChangeTarget `json:"target"`
	IdempotencyKey string                   `json:"idempotency_key"`
}

type DowngradeOverage struct {
	Resource Resource `json:"resource"`
	Used     int64    `json:"used"`
	Limit    int64    `json:"limit"`
	Excess   int64    `json:"excess"`
}

type DowngradeBlockedError struct {
	Overages []DowngradeOverage
}

func (err *DowngradeBlockedError) Error() string {
	return "target plan limits are below current workspace usage"
}

func (err *DowngradeBlockedError) Is(target error) bool {
	return target == ErrDowngradeBlocked
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

type SubscriptionChangeRegistration struct {
	WorkspaceID    string
	IdempotencyKey string
	Source         BillingBinding
	Target         SubscriptionChangeTarget
	Direction      ChangeDirection
	Action         ChangeAction
	ExpectedItems  []PaddleItem
}

type SubscriptionChangeBeginResult struct {
	Dispatch bool
	Status   ChangeStatus
	Overages []DowngradeOverage
}

type SubscriptionChangeStore interface {
	SubscriptionChange(
		context.Context,
		string,
		string,
	) (SubscriptionChangeResult, bool, error)
	PlanChangeSnapshot(
		context.Context,
		string,
	) (BillingBinding, []Usage, error)
	BeginSubscriptionChange(
		context.Context,
		SubscriptionChangeRegistration,
	) (SubscriptionChangeBeginResult, error)
	MarkSubscriptionChangePending(
		context.Context,
		string,
		string,
	) (ChangeStatus, error)
	MarkSubscriptionChangeFailed(
		context.Context,
		string,
		string,
	) error
}

type SubscriptionChangeService struct {
	authorizer OwnerAuthorizer
	provider   SubscriptionChangeProvider
	store      SubscriptionChangeStore
	catalog    PaddleCatalog
}

var (
	ErrMixedSubscriptionChange = errors.New(
		"changes that combine upgrade and downgrade dimensions must be split",
	)
	ErrNoSubscriptionChange = errors.New("subscription target is already active")
	ErrCheckoutRequired     = errors.New("a Paddle checkout is required")
	ErrDowngradeBlocked     = errors.New("downgrade exceeds target limits")
	ErrChangeInProgress     = errors.New("another plan change is pending")
	ErrChangeConflict       = errors.New("plan change state changed concurrently")
	ErrIdempotencyConflict  = errors.New(
		"idempotency key was reused for a different plan change",
	)
)

func NewSubscriptionChangeService(
	authorizer OwnerAuthorizer,
	provider SubscriptionChangeProvider,
	store SubscriptionChangeStore,
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
	prepared, err := service.prepare(ctx, request)
	if err != nil {
		return SubscriptionChangePreview{}, err
	}

	var raw json.RawMessage
	if prepared.action == ChangeSubscription {
		raw, err = service.provider.PreviewSubscriptionChange(
			ctx,
			prepared.providerChange,
		)
		if err != nil {
			return SubscriptionChangePreview{}, fmt.Errorf(
				"preview Paddle subscription change: %w",
				err,
			)
		}
	}
	return SubscriptionChangePreview{
		Direction: prepared.direction,
		Action:    prepared.action,
		Immediate: prepared.direction == ChangeUpgrade,
		Target:    prepared.target,
		Raw:       raw,
	}, nil
}

func (service *SubscriptionChangeService) Apply(
	ctx context.Context,
	request SubscriptionChangeRequest,
) (SubscriptionChangeResult, error) {
	target, err := service.validateAndAuthorize(ctx, request)
	if err != nil {
		return SubscriptionChangeResult{}, err
	}
	existing, found, err := service.store.SubscriptionChange(
		ctx,
		request.WorkspaceID,
		request.IdempotencyKey,
	)
	if err != nil {
		return SubscriptionChangeResult{}, err
	}
	if found {
		if !sameSubscriptionTarget(existing.Target, target) {
			return SubscriptionChangeResult{}, ErrIdempotencyConflict
		}
		if existing.Status == ChangeProviderFailed {
			return SubscriptionChangeResult{}, ErrChangeConflict
		}
		return existing, nil
	}
	prepared, err := service.prepareTarget(ctx, request, target)
	if err != nil {
		return SubscriptionChangeResult{}, err
	}
	begin, err := service.store.BeginSubscriptionChange(
		ctx,
		SubscriptionChangeRegistration{
			WorkspaceID:    request.WorkspaceID,
			IdempotencyKey: request.IdempotencyKey,
			Source:         prepared.source,
			Target:         prepared.target,
			Direction:      prepared.direction,
			Action:         prepared.action,
			ExpectedItems:  prepared.providerChange.Items,
		},
	)
	if err != nil {
		return SubscriptionChangeResult{}, err
	}
	if len(begin.Overages) != 0 {
		return SubscriptionChangeResult{}, &DowngradeBlockedError{
			Overages: begin.Overages,
		}
	}

	result := SubscriptionChangeResult{
		Status:         begin.Status,
		Direction:      prepared.direction,
		Action:         prepared.action,
		Target:         prepared.target,
		IdempotencyKey: request.IdempotencyKey,
	}
	if !begin.Dispatch {
		return result, nil
	}

	switch prepared.action {
	case ChangeSubscription:
		err = service.provider.UpdateSubscription(ctx, prepared.providerChange)
	case ChangeCancel:
		err = service.provider.CancelSubscription(
			ctx,
			prepared.source.SubscriptionID,
			request.IdempotencyKey,
		)
	default:
		err = ErrChangeConflict
	}
	if err != nil {
		_ = service.store.MarkSubscriptionChangeFailed(
			ctx,
			request.WorkspaceID,
			request.IdempotencyKey,
		)
		return SubscriptionChangeResult{}, fmt.Errorf(
			"request Paddle subscription change: %w",
			err,
		)
	}
	status, err := service.store.MarkSubscriptionChangePending(
		ctx,
		request.WorkspaceID,
		request.IdempotencyKey,
	)
	if err != nil {
		return SubscriptionChangeResult{}, fmt.Errorf(
			"record pending Paddle subscription change: %w",
			err,
		)
	}
	result.Status = status
	return result, nil
}

func (service *SubscriptionChangeService) Cancel(
	ctx context.Context,
	workspaceID string,
	accountID string,
	idempotencyKey string,
) (SubscriptionChangeResult, error) {
	return service.Apply(ctx, SubscriptionChangeRequest{
		WorkspaceID:    workspaceID,
		AccountID:      accountID,
		Plan:           PlanStart,
		IdempotencyKey: idempotencyKey,
	})
}

type preparedSubscriptionChange struct {
	source         BillingBinding
	target         SubscriptionChangeTarget
	direction      ChangeDirection
	action         ChangeAction
	providerChange ProviderSubscriptionChange
}

func (service *SubscriptionChangeService) prepare(
	ctx context.Context,
	request SubscriptionChangeRequest,
) (preparedSubscriptionChange, error) {
	target, err := service.validateAndAuthorize(ctx, request)
	if err != nil {
		return preparedSubscriptionChange{}, err
	}
	return service.prepareTarget(ctx, request, target)
}

func (service *SubscriptionChangeService) validateAndAuthorize(
	ctx context.Context,
	request SubscriptionChangeRequest,
) (SubscriptionChangeTarget, error) {
	if request.WorkspaceID == "" {
		return SubscriptionChangeTarget{}, ErrInvalidWorkspace
	}
	if request.AccountID == "" {
		return SubscriptionChangeTarget{}, ErrOwnerRequired
	}
	if request.IdempotencyKey == "" || len(request.IdempotencyKey) > 255 {
		return SubscriptionChangeTarget{}, ErrInvalidIdempotencyKey
	}
	owner, err := service.authorizer.IsOwner(
		ctx,
		request.WorkspaceID,
		request.AccountID,
	)
	if err != nil {
		return SubscriptionChangeTarget{}, fmt.Errorf(
			"authorize billing owner: %w",
			err,
		)
	}
	if !owner {
		return SubscriptionChangeTarget{}, ErrOwnerRequired
	}
	target, err := normalizeSubscriptionTarget(request)
	if err != nil {
		return SubscriptionChangeTarget{}, err
	}
	return target, nil
}

func (service *SubscriptionChangeService) prepareTarget(
	ctx context.Context,
	request SubscriptionChangeRequest,
	target SubscriptionChangeTarget,
) (preparedSubscriptionChange, error) {
	source, usage, err := service.store.PlanChangeSnapshot(
		ctx,
		request.WorkspaceID,
	)
	if err != nil {
		return preparedSubscriptionChange{}, err
	}
	if source.SubscriptionID == "" && target.Plan != PlanStart {
		return preparedSubscriptionChange{}, ErrCheckoutRequired
	}
	direction, err := classifyChange(source, SubscriptionChangeRequest{
		Plan:     target.Plan,
		Interval: target.Interval,
		Channels: target.Channels,
	})
	if err != nil {
		return preparedSubscriptionChange{}, err
	}
	if direction == ChangeDowngrade {
		overages, err := downgradeOverages(target, usage)
		if err != nil {
			return preparedSubscriptionChange{}, err
		}
		if len(overages) != 0 {
			return preparedSubscriptionChange{}, &DowngradeBlockedError{
				Overages: overages,
			}
		}
	}

	action := ChangeSubscription
	var items []PaddleItem
	if target.Plan == PlanStart {
		action = ChangeCancel
	} else {
		items, err = service.catalog.ExpectedItems(
			target.Plan,
			target.Interval,
			target.Channels,
		)
		if err != nil {
			return preparedSubscriptionChange{}, err
		}
	}
	mode := "prorated_immediately"
	if direction == ChangeDowngrade {
		mode = "do_not_bill"
	}
	return preparedSubscriptionChange{
		source:    source,
		target:    target,
		direction: direction,
		action:    action,
		providerChange: ProviderSubscriptionChange{
			SubscriptionID:   source.SubscriptionID,
			Items:            items,
			ProrationMode:    mode,
			OnPaymentFailure: "prevent_change",
			IdempotencyKey:   request.IdempotencyKey,
		},
	}, nil
}

func sameSubscriptionTarget(
	left SubscriptionChangeTarget,
	right SubscriptionChangeTarget,
) bool {
	if left.Plan != right.Plan || left.Interval != right.Interval {
		return false
	}
	if left.Channels == nil || right.Channels == nil {
		return left.Channels == nil && right.Channels == nil
	}
	return *left.Channels == *right.Channels
}

func normalizeSubscriptionTarget(
	request SubscriptionChangeRequest,
) (SubscriptionChangeTarget, error) {
	plan, err := PublicPlanByCode(request.Plan)
	if err != nil {
		return SubscriptionChangeTarget{}, err
	}
	if request.Plan == PlanStart {
		if request.Interval != "" && request.Interval != IntervalMonthly {
			return SubscriptionChangeTarget{}, ErrInvalidInterval
		}
		if request.Channels != nil && *request.Channels != 3 {
			return SubscriptionChangeTarget{}, ErrInvalidChannels
		}
		return SubscriptionChangeTarget{
			Plan: PlanStart, Interval: IntervalMonthly, Channels: limit(3),
		}, nil
	}
	if !plan.Purchasable {
		return SubscriptionChangeTarget{}, ErrFreePlan
	}
	if !validInterval(request.Interval) {
		return SubscriptionChangeTarget{}, ErrInvalidInterval
	}
	if err := validateChannelQuantity(plan, request.Channels); err != nil {
		return SubscriptionChangeTarget{}, err
	}
	return SubscriptionChangeTarget{
		Plan: request.Plan, Interval: request.Interval, Channels: request.Channels,
	}, nil
}

func downgradeOverages(
	target SubscriptionChangeTarget,
	usage []Usage,
) ([]DowngradeOverage, error) {
	plan, err := PublicPlanByCode(target.Plan)
	if err != nil {
		return nil, err
	}
	limits := map[Resource]*int64{
		ResourceMembers:               plan.Limits.Members,
		ResourceChannels:              target.Channels,
		ResourceScheduledPublications: plan.Limits.ScheduledPublications,
	}
	var overages []DowngradeOverage
	for _, current := range usage {
		targetLimit, known := limits[current.Resource]
		if !known {
			return nil, ErrUnknownResource
		}
		if targetLimit != nil && current.Used > *targetLimit {
			overages = append(overages, DowngradeOverage{
				Resource: current.Resource,
				Used:     current.Used,
				Limit:    *targetLimit,
				Excess:   current.Used - *targetLimit,
			})
		}
	}
	return overages, nil
}

func classifyChange(
	current BillingBinding,
	target SubscriptionChangeRequest,
) (ChangeDirection, error) {
	var upgrades, downgrades int
	if current.Plan != target.Plan {
		currentRank, currentOK := publicPlanRank(current.Plan)
		targetRank, targetOK := publicPlanRank(target.Plan)
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
		if current.Interval == IntervalMonthly &&
			target.Interval == IntervalAnnual {
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
	if upgrades == 0 {
		return "", ErrNoSubscriptionChange
	}
	return ChangeUpgrade, nil
}

func publicPlanRank(plan PlanCode) (int, bool) {
	switch plan {
	case PlanStart:
		return 0, true
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
