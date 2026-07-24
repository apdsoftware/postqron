package entitlements

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

func (store *SQLStore) ProvisionTrial(
	ctx context.Context,
	workspaceID string,
	startedAt time.Time,
) (bool, error) {
	if workspaceID == "" {
		return false, ErrInvalidWorkspace
	}
	var created bool
	err := store.db.QueryRow(
		ctx,
		"SELECT f10_provision_trial($1, $2)",
		workspaceID,
		startedAt.UTC(),
	).Scan(&created)
	if err != nil {
		return false, fmt.Errorf("provision one-time Pro trial: %w", err)
	}
	return created, nil
}

func (store *SQLStore) RegisterCheckout(
	ctx context.Context,
	registration CheckoutRegistration,
) error {
	var matches bool
	err := store.db.QueryRow(ctx, `
		SELECT f10_register_checkout($1, $2, $3, $4, $5, $6)
	`,
		registration.SessionID,
		registration.WorkspaceID,
		registration.Plan,
		registration.Interval,
		registration.ExpiresAt,
		registration.CreatedAt,
	).Scan(&matches)
	if err != nil {
		return fmt.Errorf("register checkout: %w", err)
	}
	if !matches {
		return ErrEventConflict
	}
	return nil
}

func (store *SQLStore) CompleteCheckout(
	ctx context.Context,
	eventID string,
	createdAt time.Time,
	sessionID string,
	customerID string,
	subscriptionID string,
) (bool, error) {
	var firstDelivery bool
	err := store.db.QueryRow(ctx, `
		SELECT f10_complete_checkout($1, $2, $3, $4, $5)
	`,
		eventID,
		createdAt,
		sessionID,
		customerID,
		subscriptionID,
	).Scan(&firstDelivery)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, ErrInvalidCheckout
		}
		return false, fmt.Errorf("complete checkout: %w", err)
	}
	return firstDelivery, nil
}

func (store *SQLStore) ResolveSubscription(
	ctx context.Context,
	subscriptionID string,
) (BillingBinding, error) {
	var binding BillingBinding
	err := store.db.QueryRow(ctx, `
		SELECT checkout.workspace_id,
		       checkout.plan_code,
		       checkout.billing_interval,
		       checkout.customer_id,
		       checkout.subscription_id,
		       coalesce(billing.provider_period_start, checkout.completed_at),
		       coalesce(billing.provider_period_end, checkout.expires_at)
		  FROM f10_checkout_sessions AS checkout
		  LEFT JOIN f10_workspace_billing AS billing
		    ON billing.workspace_id = checkout.workspace_id
		 WHERE checkout.subscription_id = $1
		   AND checkout.completed_at IS NOT NULL
	`, subscriptionID).Scan(
		&binding.WorkspaceID,
		&binding.Plan,
		&binding.Interval,
		&binding.CustomerID,
		&binding.SubscriptionID,
		&binding.Period.Start,
		&binding.Period.End,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return BillingBinding{}, ErrUnknownSubscription
	}
	if err != nil {
		return BillingBinding{}, fmt.Errorf("resolve Stripe subscription: %w", err)
	}
	return binding, nil
}

func (store *SQLStore) ApplyBillingEvent(
	ctx context.Context,
	event BillingEvent,
) (BillingEventResult, error) {
	var result BillingEventResult
	err := store.db.QueryRow(ctx, `
		SELECT first_delivery, state_changed
		  FROM f10_apply_billing_event(
		       $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11
		  )
	`,
		event.ID,
		event.Type,
		event.CreatedAt,
		event.WorkspaceID,
		event.Plan,
		event.Interval,
		event.State,
		event.CustomerID,
		event.SubscriptionID,
		event.Period.Start,
		event.Period.End,
	).Scan(&result.FirstDelivery, &result.StateChanged)
	if err != nil {
		return BillingEventResult{}, fmt.Errorf("apply billing event: %w", err)
	}
	return result, nil
}

func (store *SQLStore) BillingCustomerID(
	ctx context.Context,
	workspaceID string,
) (string, error) {
	var customerID string
	err := store.db.QueryRow(ctx, `
		SELECT stripe_customer_id
		  FROM f10_workspace_billing
		 WHERE workspace_id = $1
		   AND stripe_customer_id IS NOT NULL
	`, workspaceID).Scan(&customerID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrEntitlementUnavailable
	}
	if err != nil {
		return "", fmt.Errorf("resolve Stripe customer: %w", err)
	}
	return customerID, nil
}
