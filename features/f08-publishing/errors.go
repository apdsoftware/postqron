package publishing

import "errors"

var (
	ErrInvalidArgument     = errors.New("invalid publishing argument")
	ErrNotFound            = errors.New("publishing resource not found")
	ErrConflict            = errors.New("publishing state conflict")
	ErrForbidden           = errors.New("publishing action forbidden")
	ErrProviderUnavailable = errors.New("publishing provider unavailable")
	ErrUnsafeAdapter       = errors.New("publishing adapter cannot guarantee safe replay")
)
