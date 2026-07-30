package entitlements

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

func (store *SQLStore) SubscriptionChange(
	ctx context.Context,
	workspaceID string,
	idempotencyKey string,
) (SubscriptionChangeResult, bool, error) {
	var result SubscriptionChangeResult
	err := store.db.QueryRow(ctx, `
		SELECT status,
		       direction,
		       action,
		       target_plan_code,
		       target_billing_interval,
		       target_channel_quantity
		  FROM f10_subscription_changes
		 WHERE workspace_id = $1
		   AND idempotency_key = $2
	`, workspaceID, idempotencyKey).Scan(
		&result.Status,
		&result.Direction,
		&result.Action,
		&result.Target.Plan,
		&result.Target.Interval,
		&result.Target.Channels,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return SubscriptionChangeResult{}, false, nil
	}
	if err != nil {
		return SubscriptionChangeResult{}, false, fmt.Errorf(
			"query subscription change: %w",
			err,
		)
	}
	result.IdempotencyKey = idempotencyKey
	return result, true, nil
}

func (store *SQLStore) PlanChangeSnapshot(
	ctx context.Context,
	workspaceID string,
) (BillingBinding, []Usage, error) {
	rows, err := store.db.Query(ctx, `
		WITH resources(resource) AS (
			VALUES
				('members'::text),
				('channels'::text),
				('scheduled_publications'::text)
		)
		SELECT billing.workspace_id,
		       billing.plan_code,
		       billing.billing_interval,
		       billing.channel_quantity,
		       coalesce(billing.paddle_customer_id, ''),
		       coalesce(billing.paddle_subscription_id, ''),
		       billing.provider_period_start,
		       billing.provider_period_end,
		       resources.resource,
		       coalesce(counters.used, 0)
		  FROM f10_workspace_billing AS billing
		 CROSS JOIN resources
		  LEFT JOIN f10_usage_counters AS counters
		    ON counters.workspace_id = billing.workspace_id
		   AND counters.resource = resources.resource
		   AND counters.window_start =
		       '1970-01-01 00:00:00+00'::timestamptz
		 WHERE billing.workspace_id = $1
		 ORDER BY resources.resource
	`, workspaceID)
	if err != nil {
		return BillingBinding{}, nil, fmt.Errorf(
			"query plan change snapshot: %w",
			err,
		)
	}
	defer rows.Close()

	var binding BillingBinding
	var usage []Usage
	for rows.Next() {
		var current Usage
		if err := rows.Scan(
			&binding.WorkspaceID,
			&binding.Plan,
			&binding.Interval,
			&binding.Channels,
			&binding.CustomerID,
			&binding.SubscriptionID,
			&binding.Period.Start,
			&binding.Period.End,
			&current.Resource,
			&current.Used,
		); err != nil {
			return BillingBinding{}, nil, fmt.Errorf(
				"scan plan change snapshot: %w",
				err,
			)
		}
		usage = append(usage, current)
	}
	if err := rows.Err(); err != nil {
		return BillingBinding{}, nil, fmt.Errorf(
			"iterate plan change snapshot: %w",
			err,
		)
	}
	if binding.WorkspaceID == "" {
		return BillingBinding{}, nil, ErrEntitlementUnavailable
	}
	return binding, usage, nil
}

func (store *SQLStore) BeginSubscriptionChange(
	ctx context.Context,
	registration SubscriptionChangeRegistration,
) (SubscriptionChangeBeginResult, error) {
	items, err := json.Marshal(registration.ExpectedItems)
	if err != nil {
		return SubscriptionChangeBeginResult{}, fmt.Errorf(
			"encode pending Paddle items: %w",
			err,
		)
	}
	var (
		code         string
		status       ChangeStatus
		subscription string
		overagesJSON []byte
	)
	err = store.db.QueryRow(ctx, `
		SELECT result_code, change_status, subscription_id, overages
		  FROM f10_begin_subscription_change(
		       $1::text, $2, $3, $4, $5, $6, $7, $8,
		       $9, $10, $11, $12, $13
		  )
	`,
		registration.WorkspaceID,
		registration.IdempotencyKey,
		registration.Source.Plan,
		registration.Source.Interval,
		registration.Source.Channels,
		registration.Source.SubscriptionID,
		registration.Target.Plan,
		registration.Target.Interval,
		registration.Target.Channels,
		registration.Direction,
		registration.Action,
		items,
		CatalogVersion,
	).Scan(&code, &status, &subscription, &overagesJSON)
	if errors.Is(err, pgx.ErrNoRows) {
		return SubscriptionChangeBeginResult{}, ErrEntitlementUnavailable
	}
	if err != nil {
		return SubscriptionChangeBeginResult{}, fmt.Errorf(
			"begin atomic subscription change: %w",
			err,
		)
	}
	var overages []DowngradeOverage
	if len(overagesJSON) != 0 {
		if err := json.Unmarshal(overagesJSON, &overages); err != nil {
			return SubscriptionChangeBeginResult{}, fmt.Errorf(
				"decode downgrade overages: %w",
				err,
			)
		}
	}
	switch code {
	case "dispatch":
		return SubscriptionChangeBeginResult{
			Dispatch: true,
			Status:   status,
		}, nil
	case "replay":
		return SubscriptionChangeBeginResult{Status: status}, nil
	case "downgrade_blocked":
		return SubscriptionChangeBeginResult{
			Status:   status,
			Overages: overages,
		}, nil
	case "change_in_progress":
		return SubscriptionChangeBeginResult{}, ErrChangeInProgress
	case "idempotency_conflict":
		return SubscriptionChangeBeginResult{}, ErrIdempotencyConflict
	case "state_conflict":
		return SubscriptionChangeBeginResult{}, ErrChangeConflict
	case "entitlement_unavailable":
		return SubscriptionChangeBeginResult{}, ErrEntitlementUnavailable
	default:
		return SubscriptionChangeBeginResult{}, fmt.Errorf(
			"%w: unknown begin result %q",
			ErrChangeConflict,
			code,
		)
	}
}

func (store *SQLStore) MarkSubscriptionChangePending(
	ctx context.Context,
	workspaceID string,
	idempotencyKey string,
) (ChangeStatus, error) {
	var status ChangeStatus
	err := store.db.QueryRow(
		ctx,
		`SELECT f10_mark_subscription_change_pending($1::text, $2)`,
		workspaceID,
		idempotencyKey,
	).Scan(&status)
	if err != nil {
		return "", fmt.Errorf("mark subscription change pending: %w", err)
	}
	if status != ChangePending && status != ChangeApplied {
		return "", ErrChangeConflict
	}
	return status, nil
}

func (store *SQLStore) MarkSubscriptionChangeFailed(
	ctx context.Context,
	workspaceID string,
	idempotencyKey string,
) error {
	var changed bool
	err := store.db.QueryRow(
		ctx,
		`SELECT f10_mark_subscription_change_failed($1::text, $2)`,
		workspaceID,
		idempotencyKey,
	).Scan(&changed)
	if err != nil {
		return fmt.Errorf("mark subscription change failed: %w", err)
	}
	if !changed {
		return ErrChangeConflict
	}
	return nil
}
