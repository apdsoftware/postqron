package email

import (
	"errors"
	"math"
	"regexp"
	"strings"
	"time"
)

type RetryPolicy struct {
	BaseDelay time.Duration
	MaxDelay  time.Duration
}

func (policy RetryPolicy) Delay(attempt int, providerDelay time.Duration) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	delay := time.Duration(float64(policy.BaseDelay) * math.Pow(2, float64(attempt-1)))
	if delay > policy.MaxDelay || delay < 0 {
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

func (policy RetryPolicy) Validate() error {
	if policy.BaseDelay <= 0 || policy.MaxDelay < policy.BaseDelay {
		return errors.New("invalid retry policy")
	}
	return nil
}

var (
	diagnosticEmail   = regexp.MustCompile(`(?i)\b[A-Z0-9._%+\-]+@[A-Z0-9.\-]+\.[A-Z]{2,}\b`)
	diagnosticBearer  = regexp.MustCompile(`(?i)\bBearer\s+[A-Za-z0-9._~+/=-]+`)
	diagnosticSecret  = regexp.MustCompile(`(?i)(token|secret|password|api[_-]?key)=([^&\s]+)`)
	secretNamePattern = regexp.MustCompile(`^[A-Z][A-Z0-9_]*$`)
)

func SanitizeDiagnostic(value string) string {
	value = diagnosticEmail.ReplaceAllString(value, "[redacted]")
	value = diagnosticBearer.ReplaceAllString(value, "[redacted]")
	value = diagnosticSecret.ReplaceAllString(value, "$1=[redacted]")
	value = strings.Join(strings.Fields(value), " ")
	if len(value) > 500 {
		value = value[:500]
	}
	return value
}

func validSecretName(value string) bool {
	return secretNamePattern.MatchString(value)
}
