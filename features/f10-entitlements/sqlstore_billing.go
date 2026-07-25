package entitlements

import (
	"context"
	"encoding/json"
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
		return false, fmt.Errorf("provision one-time Team trial: %w", err)
	}
	return created, nil
}

func (store *SQLStore) RegisterCheckout(
	ctx context.Context,
	registration CheckoutRegistration,
) error {
	items, err := json.Marshal(registration.Items)
	if err != nil {
		return fmt.Errorf("encode Paddle checkout items: %w", err)
	}
	var matches bool
	err = store.db.QueryRow(ctx, `
		SELECT f10_register_checkout($1, $2, $3, $4, $5, $6, $7, $8)
	`,
		registration.SessionID,
		registration.WorkspaceID,
		registration.Plan,
		registration.Interval,
		registration.Channels,
		registration.CatalogVersion,
		items,
		registration.ExpiresAt,
	).Scan(&matches)
	if err != nil {
		return fmt.Errorf("register Paddle transaction: %w", err)
	}
	if !matches {
		return ErrEventConflict
	}
	return nil
}

func (store *SQLStore) ResolveTransaction(
	ctx context.Context,
	transactionID string,
) (BillingBinding, error) {
	var (
		binding BillingBinding
		items   []byte
	)
	err := store.db.QueryRow(ctx, `
		SELECT checkout.workspace_id,
		       checkout.plan_code,
		       checkout.billing_interval,
		       checkout.channel_quantity,
		       coalesce(checkout.customer_id, ''),
		       coalesce(checkout.subscription_id, ''),
		       checkout.session_id,
		       checkout.expected_items,
		       coalesce(billing.provider_period_start, checkout.created_at),
		       coalesce(billing.provider_period_end, checkout.expires_at)
		  FROM f10_checkout_sessions AS checkout
		  LEFT JOIN f10_workspace_billing AS billing
		    ON billing.workspace_id = checkout.workspace_id
		 WHERE checkout.session_id = $1
		   AND checkout.expires_at > now()
	`, transactionID).Scan(
		&binding.WorkspaceID,
		&binding.Plan,
		&binding.Interval,
		&binding.Channels,
		&binding.CustomerID,
		&binding.SubscriptionID,
		&binding.TransactionID,
		&items,
		&binding.Period.Start,
		&binding.Period.End,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return BillingBinding{}, ErrInvalidCheckout
	}
	if err != nil {
		return BillingBinding{}, fmt.Errorf("resolve Paddle transaction: %w", err)
	}
	if err := json.Unmarshal(items, &binding.ExpectedItems); err != nil {
		return BillingBinding{}, fmt.Errorf("decode expected Paddle items: %w", err)
	}
	return binding, nil
}

func (store *SQLStore) ResolveSubscription(
	ctx context.Context,
	subscriptionID string,
) (BillingBinding, error) {
	var (
		binding BillingBinding
		items   []byte
	)
	err := store.db.QueryRow(ctx, `
		SELECT billing.workspace_id,
		       billing.plan_code,
		       billing.billing_interval,
		       billing.channel_quantity,
		       billing.paddle_customer_id,
		       billing.paddle_subscription_id,
		       coalesce(checkout.session_id, ''),
		       coalesce(checkout.expected_items, '[]'::jsonb),
		       billing.provider_period_start,
		       billing.provider_period_end
		  FROM f10_workspace_billing AS billing
		  LEFT JOIN LATERAL (
		      SELECT session_id, expected_items
		        FROM f10_checkout_sessions
		       WHERE subscription_id = billing.paddle_subscription_id
		       ORDER BY completed_at DESC NULLS LAST, created_at DESC
		       LIMIT 1
		  ) AS checkout ON true
		 WHERE billing.paddle_subscription_id = $1
	`, subscriptionID).Scan(
		&binding.WorkspaceID,
		&binding.Plan,
		&binding.Interval,
		&binding.Channels,
		&binding.CustomerID,
		&binding.SubscriptionID,
		&binding.TransactionID,
		&items,
		&binding.Period.Start,
		&binding.Period.End,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return BillingBinding{}, ErrUnknownSubscription
	}
	if err != nil {
		return BillingBinding{}, fmt.Errorf("resolve Paddle subscription: %w", err)
	}
	if err := json.Unmarshal(items, &binding.ExpectedItems); err != nil {
		return BillingBinding{}, fmt.Errorf("decode expected Paddle items: %w", err)
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
		       $1, $2, $3, $4, $5, $6, $7, $8,
		       $9, $10, $11, $12, $13, $14
		  )
	`,
		event.ID,
		event.Type,
		event.OccurredAt,
		event.WorkspaceID,
		event.Plan,
		event.Interval,
		event.Channels,
		event.State,
		event.CustomerID,
		event.SubscriptionID,
		event.TransactionID,
		event.Period.Start,
		event.Period.End,
		event.ApplyState,
	).Scan(&result.FirstDelivery, &result.StateChanged)
	if err != nil {
		return BillingEventResult{}, fmt.Errorf("apply Paddle billing event: %w", err)
	}
	return result, nil
}

func (store *SQLStore) BillingBinding(
	ctx context.Context,
	workspaceID string,
) (BillingBinding, error) {
	var binding BillingBinding
	err := store.db.QueryRow(ctx, `
		SELECT workspace_id,
		       plan_code,
		       billing_interval,
		       channel_quantity,
		       paddle_customer_id,
		       paddle_subscription_id,
		       provider_period_start,
		       provider_period_end
		  FROM f10_workspace_billing
		 WHERE workspace_id = $1
		   AND paddle_customer_id IS NOT NULL
		   AND paddle_subscription_id IS NOT NULL
	`, workspaceID).Scan(
		&binding.WorkspaceID,
		&binding.Plan,
		&binding.Interval,
		&binding.Channels,
		&binding.CustomerID,
		&binding.SubscriptionID,
		&binding.Period.Start,
		&binding.Period.End,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return BillingBinding{}, ErrEntitlementUnavailable
	}
	if err != nil {
		return BillingBinding{}, fmt.Errorf("resolve Paddle customer: %w", err)
	}
	return binding, nil
}
