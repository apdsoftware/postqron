package medialibrary

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// F10QuotaCommand is the internal server-to-server command used to extend
// F10's quota ledger with media storage. It is never decoded from an HTTP
// request handled by this slice.
type F10QuotaCommand struct {
	WorkspaceID    string    `json:"workspace_id"`
	Resource       string    `json:"resource"`
	Delta          int64     `json:"delta"`
	IdempotencyKey string    `json:"idempotency_key"`
	OccurredAt     time.Time `json:"occurred_at"`
}

type F10QuotaDecision struct {
	Accepted  bool   `json:"accepted"`
	Code      string `json:"code"`
	Retryable bool   `json:"retryable"`
}

type F10QuotaCommands interface {
	ApplyQuota(context.Context, F10QuotaCommand) (F10QuotaDecision, error)
}

type F10MediaQuota struct {
	commands F10QuotaCommands
	now      func() time.Time
}

func NewF10MediaQuota(commands F10QuotaCommands) (*F10MediaQuota, error) {
	if commands == nil {
		return nil, fmt.Errorf("%w: F10 quota commands are required", ErrInvalidArgument)
	}
	return &F10MediaQuota{commands: commands, now: time.Now}, nil
}

func (quota *F10MediaQuota) ReserveMediaBytes(
	ctx context.Context,
	workspaceID string,
	amount int64,
	idempotencyKey string,
) (bool, error) {
	decision, err := quota.apply(ctx, workspaceID, amount, idempotencyKey)
	if err != nil {
		return false, err
	}
	return decision.Accepted, nil
}

func (quota *F10MediaQuota) ReleaseMediaBytes(
	ctx context.Context,
	workspaceID string,
	amount int64,
	idempotencyKey string,
) error {
	decision, err := quota.apply(ctx, workspaceID, -amount, idempotencyKey)
	if err != nil {
		return err
	}
	if !decision.Accepted {
		return fmt.Errorf("F10 rejected media quota release: %s", decision.Code)
	}
	return nil
}

func (quota *F10MediaQuota) apply(
	ctx context.Context,
	workspaceID string,
	delta int64,
	idempotencyKey string,
) (F10QuotaDecision, error) {
	if strings.TrimSpace(workspaceID) == "" || delta == 0 ||
		strings.TrimSpace(idempotencyKey) == "" {
		return F10QuotaDecision{}, ErrInvalidArgument
	}
	decision, err := quota.commands.ApplyQuota(ctx, F10QuotaCommand{
		WorkspaceID:    workspaceID,
		Resource:       QuotaResource,
		Delta:          delta,
		IdempotencyKey: idempotencyKey,
		OccurredAt:     quota.now().UTC(),
	})
	if err != nil {
		return F10QuotaDecision{}, fmt.Errorf("apply F10 media quota: %w", err)
	}
	return decision, nil
}
