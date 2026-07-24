package publishing

import (
	"fmt"
	"math"
	"regexp"
	"strings"
	"time"
)

type RetryPolicy struct {
	BaseDelay   time.Duration
	MaxDelay    time.Duration
	Lease       time.Duration
	MaxAttempts int
}

func (policy RetryPolicy) Validate() error {
	if policy.BaseDelay <= 0 ||
		policy.MaxDelay < policy.BaseDelay ||
		policy.Lease <= 0 ||
		policy.MaxAttempts < 1 {
		return fmt.Errorf("%w: invalid retry policy", ErrInvalidArgument)
	}
	return nil
}

func (policy RetryPolicy) Delay(attempt int, providerDelay time.Duration) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	delay := time.Duration(float64(policy.BaseDelay) * math.Pow(2, float64(attempt-1)))
	if delay < 0 || delay > policy.MaxDelay {
		delay = policy.MaxDelay
	}
	if providerDelay > delay {
		delay = providerDelay
	}
	if delay > policy.MaxDelay {
		delay = policy.MaxDelay
	}
	return delay
}

type ProviderError struct {
	Code       string
	Detail     string
	Retryable  bool
	RetryAfter time.Duration
}

func (err *ProviderError) Error() string {
	if err == nil {
		return ""
	}
	if err.Detail == "" {
		return err.Code
	}
	return err.Code + ": " + err.Detail
}

var (
	diagnosticEmail  = regexp.MustCompile(`(?i)\b[A-Z0-9._%+\-]+@[A-Z0-9.\-]+\.[A-Z]{2,}\b`)
	diagnosticBearer = regexp.MustCompile(`(?i)\bBearer\s+[A-Za-z0-9._~+/=-]+`)
	diagnosticSecret = regexp.MustCompile(`(?i)(token|secret|password|api[_-]?key)=([^&\s]+)`)
	codePattern      = regexp.MustCompile(`^[a-z][a-z0-9_.-]{0,63}$`)
	providerPattern  = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,63}$`)
)

func sanitizeDiagnostic(value string) string {
	value = diagnosticEmail.ReplaceAllString(value, "[redacted]")
	value = diagnosticBearer.ReplaceAllString(value, "[redacted]")
	value = diagnosticSecret.ReplaceAllString(value, "$1=[redacted]")
	value = strings.Join(strings.Fields(value), " ")
	if len(value) > 500 {
		value = value[:500]
	}
	return value
}

func sanitizeCode(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if !codePattern.MatchString(value) {
		return "provider_error"
	}
	return value
}
