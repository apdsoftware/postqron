package statusnotifications

import "errors"

var (
	ErrInvalidArgument = errors.New("invalid argument")
	ErrForbidden       = errors.New("forbidden")
	ErrNotFound        = errors.New("not found")
	ErrConflict        = errors.New("conflict")
	ErrLeaseLost       = errors.New("work item lease was lost")
)
