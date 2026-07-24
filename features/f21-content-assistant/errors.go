package contentassistant

import "errors"

var (
	ErrInvalidArgument      = errors.New("invalid argument")
	ErrUnauthenticated      = errors.New("authentication required")
	ErrForbidden            = errors.New("operation forbidden")
	ErrNotFound             = errors.New("proposal not found")
	ErrConflict             = errors.New("proposal revision conflict")
	ErrGeneratorUnavailable = errors.New("content generator unavailable")
)

type FieldError struct {
	Field   string `json:"field"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (fieldError *FieldError) Error() string {
	return fieldError.Message
}

func (fieldError *FieldError) Unwrap() error {
	return ErrInvalidArgument
}

type GeneratorError struct {
	Cause error
}

func (generatorError *GeneratorError) Error() string {
	return ErrGeneratorUnavailable.Error()
}

func (generatorError *GeneratorError) Unwrap() error {
	return ErrGeneratorUnavailable
}
