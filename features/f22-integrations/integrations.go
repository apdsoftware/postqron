// Package integrations implements Postqron's scoped public API and reliable,
// signed webhook delivery boundary.
package integrations

import (
	"context"
	"errors"
	"time"
)

const FeatureID = "integrations"

type Scope string

const (
	ScopePostsRead     Scope = "posts:read"
	ScopePostsWrite    Scope = "posts:write"
	ScopeWebhooksRead  Scope = "webhooks:read"
	ScopeWebhooksWrite Scope = "webhooks:write"
)

type Principal struct {
	CredentialID string
	WorkspaceID  string
	Scopes       map[Scope]struct{}
	ExpiresAt    time.Time
}

func (principal Principal) HasScope(required Scope, now time.Time) bool {
	if principal.CredentialID == "" || principal.WorkspaceID == "" {
		return false
	}
	if !principal.ExpiresAt.IsZero() && !now.Before(principal.ExpiresAt) {
		return false
	}
	_, allowed := principal.Scopes[required]
	return allowed
}

// Authenticator adapts F3's server-side credential lookup. Implementations
// must compare a digest of the bearer credential and never persist the raw
// value.
type Authenticator interface {
	Authenticate(ctx context.Context, bearerCredential string) (Principal, error)
}

// Authorizer adapts F4's active membership and RBAC checks. A credential is
// always pinned to one workspace before this finer-grained check runs.
type Authorizer interface {
	Authorize(
		ctx context.Context,
		principal Principal,
		workspaceID string,
		required Scope,
	) error
}

// RateLimiter adapts F15's shared production limiter. Keys are derived from
// authenticated server state rather than request headers.
type RateLimiter interface {
	Allow(ctx context.Context, key string, now time.Time) (allowed bool, retryAfter time.Duration, err error)
}

var (
	ErrUnauthenticated     = errors.New("authentication required")
	ErrForbidden           = errors.New("operation forbidden")
	ErrRateLimited         = errors.New("rate limited")
	ErrInvalidArgument     = errors.New("invalid argument")
	ErrInvalidCursor       = errors.New("invalid pagination cursor")
	ErrIdempotencyConflict = errors.New("idempotency key reused with a different request")
	ErrNotFound            = errors.New("resource not found")
	ErrUnavailable         = errors.New("dependency unavailable")
)

type Clock func() time.Time

func systemClock() time.Time {
	return time.Now().UTC()
}
