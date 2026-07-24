package entitlements

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

type DB interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
	QueryRow(context.Context, string, ...any) pgx.Row
}

type SQLStore struct {
	db DB
}

func NewSQLStore(db DB) *SQLStore {
	return &SQLStore{db: db}
}

func (store *SQLStore) Overview(ctx context.Context, workspaceID string) (Overview, error) {
	rows, err := store.db.Query(ctx, `
		SELECT plan_code, plan_name, monthly_price_cents, annual_price_cents,
		       billing_interval, billing_state, period_start, period_end,
		       resource, used, quota_limit, remaining, over_limit
		  FROM f10_public_entitlement_usage
		 WHERE workspace_id = $1
		 ORDER BY resource
	`, workspaceID)
	if err != nil {
		return Overview{}, fmt.Errorf("query entitlement overview: %w", err)
	}
	defer rows.Close()

	var result Overview
	found := false
	for rows.Next() {
		var (
			code         PlanCode
			name         string
			monthlyPrice int64
			annualPrice  int64
			usage        Usage
			periodStart  time.Time
			periodEnd    time.Time
		)
		if err := rows.Scan(
			&code,
			&name,
			&monthlyPrice,
			&annualPrice,
			&result.Interval,
			&result.State,
			&periodStart,
			&periodEnd,
			&usage.Resource,
			&usage.Used,
			&usage.Limit,
			&usage.Remaining,
			&usage.OverLimit,
		); err != nil {
			return Overview{}, fmt.Errorf("scan entitlement overview: %w", err)
		}

		if !found {
			plan, err := PublicPlanByCode(code)
			if err != nil {
				return Overview{}, fmt.Errorf("catalog drift for %q: %w", code, err)
			}
			if plan.Name != name ||
				plan.Prices.Monthly.AmountCents != monthlyPrice ||
				plan.Prices.Annual.AmountCents != annualPrice {
				return Overview{}, fmt.Errorf("catalog drift for %q", code)
			}
			result.Plan = plan
			result.Period = Period{Start: periodStart, End: periodEnd}
			found = true
		}
		result.Usage = append(result.Usage, usage)
	}
	if err := rows.Err(); err != nil {
		return Overview{}, fmt.Errorf("iterate entitlement overview: %w", err)
	}
	if !found {
		return Overview{}, ErrEntitlementUnavailable
	}
	return result, nil
}

func (store *SQLStore) ApplyUsage(
	ctx context.Context,
	command UsageCommand,
) (UsageDecision, error) {
	var (
		decision  UsageDecision
		limit     *int64
		remaining *int64
	)
	err := store.db.QueryRow(ctx, `
		SELECT accepted, decision_code, retryable, used, quota_limit, remaining, over_limit
		  FROM f10_apply_usage($1, $2, $3, $4, $5)
	`,
		command.WorkspaceID,
		command.Resource,
		command.Delta,
		command.IdempotencyKey,
		command.OccurredAt,
	).Scan(
		&decision.Accepted,
		&decision.Code,
		&decision.Retryable,
		&decision.Usage.Used,
		&limit,
		&remaining,
		&decision.Usage.OverLimit,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return UsageDecision{}, ErrEntitlementUnavailable
		}
		return UsageDecision{}, fmt.Errorf("execute atomic quota command: %w", err)
	}
	decision.Usage.Resource = command.Resource
	if limit != nil {
		decision.Usage.Limit = *limit
	}
	if remaining != nil {
		decision.Usage.Remaining = *remaining
	}
	return decision, nil
}
