package auth

import (
	"context"
	"errors"
	"time"
)

// AccountAccessState is the authoritative F3 access state consumed by F12.
type AccountAccessState string

const (
	AccountAccessActive    AccountAccessState = "active"
	AccountAccessFrozen    AccountAccessState = "frozen"
	AccountAccessFinalized AccountAccessState = "finalized"
)

// ErrAccountAccessUnavailable is deliberately generic so callers cannot use
// lifecycle operations to enumerate accounts or infer their prior state.
var ErrAccountAccessUnavailable = errors.New("account access is unavailable")

// AccountAccessStore serializes lifecycle changes with authentication writes.
type AccountAccessStore interface {
	FreezeAccountAccess(context.Context, string, time.Time) error
	RestoreAccountAccess(context.Context, string, time.Time) error
	FinalizeAccountAccess(context.Context, string, time.Time) error
}

// AccountAccessBoundary is the exported adapter-services boundary used by F12.
// Restore never recreates sessions, OAuth attempts, credentials, or tokens.
type AccountAccessBoundary struct {
	store AccountAccessStore
	now   func() time.Time
}

func NewAccountAccessBoundary(
	store AccountAccessStore,
	now func() time.Time,
) (*AccountAccessBoundary, error) {
	if store == nil {
		return nil, errors.New("account access store is required")
	}
	if now == nil {
		now = time.Now
	}
	return &AccountAccessBoundary{store: store, now: now}, nil
}

// Freeze revokes every session and invalidates pending OAuth/link attempts.
// Repeated calls are safe.
func (boundary *AccountAccessBoundary) Freeze(
	ctx context.Context,
	accountID string,
) error {
	if accountID == "" {
		return ErrAccountAccessUnavailable
	}
	return boundary.store.FreezeAccountAccess(ctx, accountID, boundary.now().UTC())
}

// Restore re-enables authentication during the grace period. It is idempotent
// and does not restore any artifact invalidated by Freeze.
func (boundary *AccountAccessBoundary) Restore(
	ctx context.Context,
	accountID string,
) error {
	if accountID == "" {
		return ErrAccountAccessUnavailable
	}
	return boundary.store.RestoreAccountAccess(ctx, accountID, boundary.now().UTC())
}

// Finalize irreversibly disables access and removes authentication PII and
// reusable credentials. Repeated calls are safe.
func (boundary *AccountAccessBoundary) Finalize(
	ctx context.Context,
	accountID string,
) error {
	if accountID == "" {
		return ErrAccountAccessUnavailable
	}
	return boundary.store.FinalizeAccountAccess(ctx, accountID, boundary.now().UTC())
}

func accountAccessAllowed(account Account) bool {
	return account.AccessState == "" || account.AccessState == AccountAccessActive
}
