package smartqueue

import "errors"

var (
	ErrInvalidArgument   = errors.New("invalid smart queue argument")
	ErrForbidden         = errors.New("smart queue access denied")
	ErrNotFound          = errors.New("smart queue resource not found")
	ErrConflict          = errors.New("smart queue conflict")
	ErrSlotUnavailable   = errors.New("smart queue slot is no longer available")
	ErrPreviewExpired    = errors.New("smart queue preview expired")
	ErrPreviewConsumed   = errors.New("smart queue preview already consumed")
	ErrQueueChanged      = errors.New("smart queue changed after preview")
	ErrCapacityExceeded  = errors.New("smart queue plan capacity exceeded")
	ErrFeatureDisabled   = errors.New("smart queue is unavailable on the active plan")
	ErrNoSlotAvailable   = errors.New("no smart queue slot is available within the search limit")
	ErrIdempotencyReplay = errors.New("idempotency key was already used with different input")
)

type FieldError struct {
	Field   string `json:"field"`
	Rule    string `json:"rule"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (fieldError *FieldError) Error() string { return fieldError.Message }
func (fieldError *FieldError) Unwrap() error { return ErrInvalidArgument }

func invalidField(field, rule, code, message string) error {
	return &FieldError{Field: field, Rule: rule, Code: code, Message: message}
}
