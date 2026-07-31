package scheduling

import "errors"

var (
	ErrInvalidArgument       = errors.New("invalid scheduling argument")
	ErrUnauthenticated       = errors.New("authentication required")
	ErrForbidden             = errors.New("operation forbidden")
	ErrNotFound              = errors.New("scheduled post not found")
	ErrConflict              = errors.New("scheduled post revision conflict")
	ErrImmutable             = errors.New("scheduled post can no longer be changed")
	ErrDependencyUnavailable = errors.New("scheduling dependency unavailable")
)

type FieldError struct {
	Field   string
	Rule    string
	Code    string
	Message string
}

func (fieldError *FieldError) Error() string {
	return fieldError.Message
}

func (fieldError *FieldError) Unwrap() error {
	return ErrInvalidArgument
}

func invalidField(field, rule, code, message string) error {
	return &FieldError{
		Field:   field,
		Rule:    rule,
		Code:    code,
		Message: message,
	}
}
