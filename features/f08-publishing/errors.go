package publishing

import "errors"

var (
	ErrInvalidArgument = errors.New("invalid publishing argument")
	ErrNotFound        = errors.New("publishing resource not found")
	ErrConflict        = errors.New("publishing state conflict")
	ErrForbidden       = errors.New("publishing action forbidden")
)
