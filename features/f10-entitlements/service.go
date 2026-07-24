package entitlements

import (
	"context"
	"errors"
	"fmt"
	"time"
)

type BillingState string

const (
	StateTrialing          BillingState = "trialing"
	StateActive            BillingState = "active"
	StatePastDue           BillingState = "past_due"
	StateTrialExpired      BillingState = "trial_expired"
	StatePaymentRestricted BillingState = "payment_restricted"
	StateCanceled          BillingState = "canceled"
)

type Usage struct {
	Resource  Resource `json:"resource"`
	Used      int64    `json:"used"`
	Limit     int64    `json:"limit"`
	Remaining int64    `json:"remaining"`
	OverLimit bool     `json:"over_limit"`
}

type Period struct {
	Start time.Time `json:"start"`
	End   time.Time `json:"end"`
}

type Overview struct {
	Plan     PublicPlan      `json:"plan"`
	Interval BillingInterval `json:"interval"`
	State    BillingState    `json:"state"`
	Period   Period          `json:"period"`
	Usage    []Usage         `json:"usage"`
}

type UsageCommand struct {
	WorkspaceID    string
	Resource       Resource
	Delta          int64
	IdempotencyKey string
	OccurredAt     time.Time
}

type UsageDecision struct {
	Accepted  bool   `json:"accepted"`
	Code      string `json:"code"`
	Retryable bool   `json:"retryable"`
	Usage     Usage  `json:"usage"`
}

type Store interface {
	Overview(context.Context, string) (Overview, error)
	ApplyUsage(context.Context, UsageCommand) (UsageDecision, error)
}

type Service struct {
	store Store
	now   func() time.Time
}

var (
	ErrInvalidWorkspace       = errors.New("workspace id is required")
	ErrInvalidIdempotencyKey  = errors.New("idempotency key is required")
	ErrInvalidAmount          = errors.New("amount must be greater than zero")
	ErrUnknownResource        = errors.New("unknown quota resource")
	ErrPublicationRelease     = errors.New("scheduled publication quota is never released")
	ErrEntitlementUnavailable = errors.New("entitlement is unavailable")
)

func NewService(store Store) *Service {
	return &Service{store: store, now: time.Now}
}

func (service *Service) GetOverview(ctx context.Context, workspaceID string) (Overview, error) {
	if workspaceID == "" {
		return Overview{}, ErrInvalidWorkspace
	}
	return service.store.Overview(ctx, workspaceID)
}

func (service *Service) Reserve(
	ctx context.Context,
	workspaceID string,
	resource Resource,
	amount int64,
	idempotencyKey string,
) (UsageDecision, error) {
	if amount <= 0 {
		return UsageDecision{}, ErrInvalidAmount
	}
	return service.apply(ctx, workspaceID, resource, amount, idempotencyKey)
}

func (service *Service) Release(
	ctx context.Context,
	workspaceID string,
	resource Resource,
	amount int64,
	idempotencyKey string,
) (UsageDecision, error) {
	if amount <= 0 {
		return UsageDecision{}, ErrInvalidAmount
	}
	if resource == ResourceScheduledPublications {
		return UsageDecision{}, ErrPublicationRelease
	}
	return service.apply(ctx, workspaceID, resource, -amount, idempotencyKey)
}

func (service *Service) apply(
	ctx context.Context,
	workspaceID string,
	resource Resource,
	delta int64,
	idempotencyKey string,
) (UsageDecision, error) {
	if workspaceID == "" {
		return UsageDecision{}, ErrInvalidWorkspace
	}
	if !validResource(resource) {
		return UsageDecision{}, ErrUnknownResource
	}
	if idempotencyKey == "" || len(idempotencyKey) > 255 {
		return UsageDecision{}, ErrInvalidIdempotencyKey
	}

	decision, err := service.store.ApplyUsage(ctx, UsageCommand{
		WorkspaceID:    workspaceID,
		Resource:       resource,
		Delta:          delta,
		IdempotencyKey: idempotencyKey,
		OccurredAt:     service.now().UTC(),
	})
	if err != nil {
		return UsageDecision{}, fmt.Errorf("apply quota usage: %w", err)
	}
	return decision, nil
}
