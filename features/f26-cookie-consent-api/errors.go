package cookieconsent

import "errors"

var (
	ErrInvalidRequest      = errors.New("invalid cookie preference request")
	ErrPolicyUnavailable   = errors.New("approved cookie policy is unavailable")
	ErrPolicyMismatch      = errors.New("cookie policy version is not current")
	ErrIdempotencyConflict = errors.New("idempotency key was reused with different input")
)
