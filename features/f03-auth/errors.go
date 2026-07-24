package auth

import (
	"errors"
	"fmt"
)

const (
	CodeInvalidRequest           = "invalid_request"
	CodeUnsupportedProvider      = "unsupported_provider"
	CodeInvalidState             = "invalid_state"
	CodeFlowExpired              = "flow_expired"
	CodeProviderDenied           = "provider_denied"
	CodeProviderUnavailable      = "provider_unavailable"
	CodeVerifiedEmailRequired    = "verified_email_required"
	CodeLinkingRequired          = "linking_required"
	CodeIdentityConflict         = "identity_conflict"
	CodeUnauthenticated          = "unauthenticated"
	CodeReauthenticationRequired = "reauthentication_required"
	CodeLastProvider             = "last_provider"
	CodeInvalidConsent           = "invalid_consent"
	CodeCountryNotSupported      = "country_not_supported"
	CodeConflict                 = "conflict"
	CodeInternal                 = "internal"
)

type Error struct {
	Code      string
	Message   string
	Retryable bool
	Cause     error
}

func (e *Error) Error() string {
	if e.Message != "" {
		return e.Message
	}
	return e.Code
}

func (e *Error) Unwrap() error {
	return e.Cause
}

func newError(code, message string, retryable bool, cause error) error {
	return &Error{Code: code, Message: message, Retryable: retryable, Cause: cause}
}

func ErrorDetails(err error) (code, message string, retryable bool) {
	var domainError *Error
	if errors.As(err, &domainError) {
		return domainError.Code, domainError.Error(), domainError.Retryable
	}
	return CodeInternal, "Operazione non completata. Riprova.", true
}

func wrapInternal(operation string, err error) error {
	return newError(
		CodeInternal,
		"Operazione non completata. Riprova.",
		true,
		fmt.Errorf("%s: %w", operation, err),
	)
}
