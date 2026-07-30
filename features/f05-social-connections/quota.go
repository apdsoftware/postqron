package socialconnections

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

const f10ChannelsResource = "channels"

// PostgresChannelQuota invokes F10's authoritative atomic quota function.
// Resource, delta, timestamp, and idempotency key are constructed server-side.
type PostgresChannelQuota struct {
	database *sql.DB
	now      func() time.Time
}

func NewPostgresChannelQuota(
	database *sql.DB,
	clock func() time.Time,
) (*PostgresChannelQuota, error) {
	if database == nil {
		return nil, fmt.Errorf("%w: F10 database is required", ErrInvalidArgument)
	}
	if clock == nil {
		clock = time.Now
	}
	return &PostgresChannelQuota{database: database, now: clock}, nil
}

func (quota *PostgresChannelQuota) ReserveChannel(
	ctx context.Context,
	workspaceID, idempotencyKey string,
) (ChannelQuotaDecision, error) {
	return quota.apply(ctx, workspaceID, 1, idempotencyKey)
}

func (quota *PostgresChannelQuota) ReleaseChannel(
	ctx context.Context,
	workspaceID, idempotencyKey string,
) (ChannelQuotaDecision, error) {
	return quota.apply(ctx, workspaceID, -1, idempotencyKey)
}

func (quota *PostgresChannelQuota) apply(
	ctx context.Context,
	workspaceID string,
	delta int64,
	idempotencyKey string,
) (ChannelQuotaDecision, error) {
	if strings.TrimSpace(workspaceID) == "" ||
		strings.TrimSpace(idempotencyKey) == "" ||
		(delta != 1 && delta != -1) {
		return ChannelQuotaDecision{}, ErrInvalidArgument
	}
	var decision ChannelQuotaDecision
	err := quota.database.QueryRowContext(ctx, `
		SELECT accepted, decision_code, retryable
		  FROM f10_apply_usage($1::text, $2, $3, $4, $5)`,
		workspaceID,
		f10ChannelsResource,
		delta,
		idempotencyKey,
		quota.now().UTC(),
	).Scan(
		&decision.Accepted,
		&decision.Code,
		&decision.Retryable,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return ChannelQuotaDecision{}, ErrChannelQuotaUnavailable
	}
	if err != nil {
		return ChannelQuotaDecision{}, fmt.Errorf("apply F10 channel quota: %w", err)
	}
	return decision, nil
}
