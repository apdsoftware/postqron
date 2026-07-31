package composer

import "errors"

var (
	ErrInvalidArgument       = errors.New("invalid argument")
	ErrUnauthenticated       = errors.New("authentication required")
	ErrForbidden             = errors.New("operation forbidden")
	ErrNotFound              = errors.New("draft not found")
	ErrConflict              = errors.New("draft revision conflict")
	ErrValidation            = errors.New("draft is not ready for scheduling")
	ErrDependencyUnavailable = errors.New("composer dependency unavailable")
	ErrStorageUnavailable    = errors.New("composer object storage is unavailable")
)

type ValidationFailure struct {
	Report ValidationReport
}

func (failure *ValidationFailure) Error() string {
	return ErrValidation.Error()
}

func (failure *ValidationFailure) Unwrap() error {
	return ErrValidation
}

type FieldRuleError struct {
	Field   string
	Rule    string
	Code    string
	Message string
}

func (fieldError *FieldRuleError) Error() string {
	return fieldError.Message
}

func (fieldError *FieldRuleError) Unwrap() error {
	return ErrInvalidArgument
}
